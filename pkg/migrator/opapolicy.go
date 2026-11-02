package migrator

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

// opaLegacyBody returns the map holding the legacy OPAPolicy body (conf and/or
// selectors): spec when it carries them, otherwise the document root, which is
// where the Universal form of this resource keeps them.
func opaLegacyBody(obj map[string]interface{}) map[string]interface{} {
	if spec, ok := obj["spec"].(map[string]interface{}); ok {
		if spec["conf"] != nil || spec["selectors"] != nil {
			return spec
		}
	}
	if obj["conf"] != nil || obj["selectors"] != nil {
		return obj
	}
	if spec, ok := obj["spec"].(map[string]interface{}); ok {
		return spec
	}
	return map[string]interface{}{}
}

// TransformOPAPolicy converts a Kong Mesh OPAPolicy resource to the new MeshOPA API.
//
// Structural change (Kong Mesh 2.5+):
//   - kind: OPAPolicy → kind: MeshOPA
//   - spec.conf.policies[].{inlineString,inline,secret} → spec.default.appendPolicies[].rego.<same key>
//   - spec.conf.agentConfig.{inlineString,inline,secret} → spec.default.agentConfig.<same key>
//     (an entry with none of the three keys is reported, never emitted empty;
//     see convertLegacyOPADataSource)
//   - spec.targetRef                   → spec.targetRef (preserved under a v2 target)
//
// Under TargetV3 the targetRef is additionally rewritten: 3.0 removes
// spec.targetRef.{name,namespace,mesh}, and `name` is converted to
// labels["kuma.io/display-name"] so the policy keeps its original scope. See
// fixMeshOPATargetRefForV3.
//
// A resource that already has kind: MeshOPA is returned unchanged under a v2
// target, and gets only the targetRef rewrite under v3.
func TransformOPAPolicy(raw []byte, target TargetVersion) ([][]byte, []string, error) {
	var obj map[string]interface{}
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		return nil, nil, fmt.Errorf("unmarshal OPAPolicy: %w", err)
	}

	// Universal format uses "type" instead of "kind". The legacy OPAPolicy is
	// unusual in that its body (conf/selectors) sits at the document ROOT with no
	// spec wrapper, while MeshOPA — an ordinary policy — requires spec even in
	// Universal ("kumactl apply" rejects it with ".spec in body is required").
	// So the read container and the write container differ for this format.
	//
	// Treating a Universal document as Kubernetes produced a hybrid carrying both
	// type: OPAPolicy and kind: MeshOPA with the payload left unconverted.
	kind, _ := obj["kind"].(string)
	universal := false
	if kind == "" {
		if t, ok := obj["type"].(string); ok && t != "" {
			kind = t
			universal = true
		}
	}
	name := extractNameFromObj(obj)

	if kind == "MeshOPA" {
		// Already converted. Under v3 the targetRef and data sources still need rewriting.
		if !target.IsV3() {
			return [][]byte{raw}, nil, nil
		}
		changedRef, wsRef := fixMeshOPATargetRefForV3(obj, name)
		changedDS, wsDS := fixMeshOPADataSourcesForV3(obj, name)
		ws := append(wsRef, wsDS...)
		if !changedRef && !changedDS {
			return [][]byte{raw}, ws, nil
		}
		out, err := yaml.Marshal(obj)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal MeshOPA %q: %w", name, err)
		}
		return [][]byte{out}, ws, nil
	}

	var warnings []string

	// Rewrite the type/kind discriminator in whichever format this document uses.
	if universal {
		obj["type"] = "MeshOPA"
	} else {
		obj["kind"] = "MeshOPA"
	}

	// src is where the legacy body (conf/selectors) is read from; spec is where the
	// MeshOPA body is written. They are not always the same container:
	//
	//   - Kubernetes OPAPolicy:     body under spec  → src == spec
	//   - Universal OPAPolicy:      body at the root (this resource has no spec
	//                               wrapper) but MeshOPA requires one
	//   - Universal converted to Kubernetes by the extractor: universalToKubernetes
	//     maps type/name/mesh but does not relocate the root-level body, so conf and
	//     selectors are still at the root next to an empty spec
	//
	// Picking src by where conf/selectors actually are covers all three.
	src := opaLegacyBody(obj)
	spec, _ := obj["spec"].(map[string]interface{})
	if spec == nil {
		spec = map[string]interface{}{}
	}

	conf, _ := src["conf"].(map[string]interface{})
	if conf == nil {
		// No conf — nothing to migrate inside spec; just change the kind.
		warnings = append(warnings, fmt.Sprintf(
			"OPAPolicy %q: no spec.conf found — kind changed to MeshOPA but spec.default is empty; review manually.",
			name))
	} else {
		newDefault := map[string]interface{}{}

		// Migrate policies[] → appendPolicies[].
		if policies, ok := conf["policies"].([]interface{}); ok && len(policies) > 0 {
			appendPolicies := make([]interface{}, 0, len(policies))
			for i, p := range policies {
				field := fmt.Sprintf("conf.policies[%d]", i)
				rego, ws := convertLegacyOPADataSource(p, name, field)
				warnings = append(warnings, ws...)
				if rego == nil {
					continue
				}
				appendPolicies = append(appendPolicies, map[string]interface{}{
					"rego": rego,
				})
			}
			if len(appendPolicies) > 0 {
				newDefault["appendPolicies"] = appendPolicies
			}
		}

		// Migrate agentConfig → default.agentConfig.
		if agentConfig, ok := conf["agentConfig"]; ok && agentConfig != nil {
			ds, ws := convertLegacyOPADataSource(agentConfig, name, "conf.agentConfig")
			warnings = append(warnings, ws...)
			if ds != nil {
				newDefault["agentConfig"] = ds
			}
		}

		// Preserve any other conf fields as top-level default fields (best-effort).
		for k, v := range conf {
			if k == "policies" || k == "agentConfig" {
				continue
			}
			newDefault[k] = v
			warnings = append(warnings, fmt.Sprintf(
				"OPAPolicy %q: conf field %q has no direct MeshOPA mapping — placed under spec.default.%s; review manually.",
				name, k, k))
		}

		spec["default"] = newDefault
		delete(src, "conf")
	}

	obj["spec"] = spec

	// The legacy OPAPolicy CRD carries the mesh as a TOP-LEVEL field, not as the
	// standard kuma.io/mesh label. MeshOPA is an ordinary policy CRD and rejects
	// an unknown top-level "mesh" with a strict decoding error, so it has to move.
	//
	// Universal format is the opposite: a top-level `mesh` is the correct and
	// required spelling there, so it is left alone.
	if meshName, ok := obj["mesh"].(string); ok && meshName != "" && !universal {
		meta, _ := obj["metadata"].(map[string]interface{})
		if meta == nil {
			meta = map[string]interface{}{}
			obj["metadata"] = meta
		}
		labels, _ := meta["labels"].(map[string]interface{})
		if labels == nil {
			labels = map[string]interface{}{}
			meta["labels"] = labels
		}
		if _, exists := labels["kuma.io/mesh"]; !exists {
			labels["kuma.io/mesh"] = meshName
		}
		delete(obj, "mesh")
	}

	// Legacy OPAPolicy selects workloads with spec.selectors[].match tag sets.
	// MeshOPA uses a single spec.targetRef, and leaving selectors in place makes
	// the document invalid (unknown field spec.selectors).
	docs, selWarnings, err := applyOPASelectors(obj, src, name, target)
	warnings = append(warnings, selWarnings...)
	if err != nil {
		return nil, nil, err
	}
	return docs, warnings, nil
}

// convertLegacyOPADataSource copies one legacy OPAPolicy DataSource
// (conf.policies[] entry or conf.agentConfig) into the MeshOPA DataSource shape.
//
// The legacy type (kuma.system.v1alpha1.DataSource) is a oneof of secret,
// inline (base64) and inlineString. The 2.x MeshOPA rego/agentConfig fields accept
// the same three keys, so the value is copied unchanged; under a v3 target
// fixMeshOPADataSourcesForV3 then rewrites it to SecureDataSource (decoding inline).
//
// An entry with none of the three keys returns nil and a warning: emitting an
// empty `rego: {}` would drop an authorization rule without a trace. Keys other
// than the three are reported as dropped.
func convertLegacyOPADataSource(v interface{}, name, field string) (map[string]interface{}, []string) {
	ds, ok := v.(map[string]interface{})
	if !ok {
		return nil, []string{fmt.Sprintf(
			"OPAPolicy %q: spec.%s is not a data source object (%T) — NOT migrated; add it to the "+
				"MeshOPA by hand before applying, or the policy runs without it.", name, field, v)}
	}
	out := map[string]interface{}{}
	var unknown []string
	for k, val := range ds {
		switch k {
		case "secret", "inline", "inlineString":
			if s, ok := val.(string); ok && s != "" {
				out[k] = s
			}
		default:
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	var warnings []string
	if len(out) == 0 {
		desc := "is empty"
		if len(unknown) > 0 {
			desc = "has only unrecognised key(s) " + strings.Join(unknown, ", ")
		}
		return nil, []string{fmt.Sprintf(
			"OPAPolicy %q: spec.%s %s (expected one of secret, inline, inlineString) — NOT migrated; "+
				"add it to the MeshOPA by hand before applying, or the policy runs without it.",
			name, field, desc)}
	}
	if len(out) > 1 {
		warnings = append(warnings, fmt.Sprintf(
			"OPAPolicy %q: spec.%s sets more than one of secret, inline, inlineString; they were all "+
				"copied, but a data source takes exactly one — keep the intended one.", name, field))
	}
	if len(unknown) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"OPAPolicy %q: spec.%s key(s) %s have no MeshOPA data source equivalent and were dropped.",
			name, field, strings.Join(unknown, ", ")))
	}
	return out, warnings
}

// applyOPASelectors converts legacy spec.selectors[] into spec.targetRef and
// marshals the resulting document(s).
//
// MeshOPA holds exactly one targetRef, so a policy with several selectors is
// split into one MeshOPA per selector — the same shape transformGenericLegacy
// uses for multiple legacy sources. A selector matching kuma.io/service: '*'
// collapses to kind: Mesh.
func applyOPASelectors(obj, src map[string]interface{}, name string, target TargetVersion) ([][]byte, []string, error) {
	var warnings []string

	rawSelectors, hasSelectors := src["selectors"].([]interface{})
	if !hasSelectors {
		// No legacy selectors. An existing targetRef is preserved as-is.
		specMap, _ := obj["spec"].(map[string]interface{})
		if _, ok := specMap["targetRef"]; !ok {
			warnings = append(warnings, fmt.Sprintf(
				"OPAPolicy %q: no spec.selectors and no spec.targetRef — MeshOPA requires a targetRef; "+
					"add one by hand before applying.", name))
		}
		if target.IsV3() {
			_, ws := fixMeshOPATargetRefForV3(obj, name)
			warnings = append(warnings, ws...)
			_, wsDS := fixMeshOPADataSourcesForV3(obj, name)
			warnings = append(warnings, wsDS...)
		}
		out, err := yaml.Marshal(obj)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal MeshOPA %q: %w", name, err)
		}
		return [][]byte{out}, warnings, nil
	}
	delete(src, "selectors")

	selectors := make([]OldSelector, 0, len(rawSelectors))
	for _, rs := range rawSelectors {
		sm, ok := rs.(map[string]interface{})
		if !ok {
			continue
		}
		match := map[string]string{}
		if mm, ok := sm["match"].(map[string]interface{}); ok {
			for k, v := range mm {
				if vs, ok := v.(string); ok {
					match[k] = vs
				}
			}
		}
		selectors = append(selectors, OldSelector{Match: match})
	}
	if len(selectors) == 0 {
		selectors = []OldSelector{{}} // degenerate: treat as mesh-wide
	}

	// OPAPolicy is cluster-scoped, so there is no policy namespace to scope against.
	const policyNamespace = ""

	var out [][]byte
	for i, sel := range selectors {
		ref, warn := ConvertSelectorToTargetRef(sel, policyNamespace, true)
		if warn != "" {
			warnings = append(warnings, warn)
		}

		doc := deepCopyMap(obj)
		docSpec, _ := doc["spec"].(map[string]interface{})
		if docSpec == nil {
			docSpec = map[string]interface{}{}
			doc["spec"] = docSpec
		}
		docSpec["targetRef"] = targetRefToMap(ref)
		// A legacy Universal document also carries a stray root-level targetRef
		// only if it was partially migrated; keep the spec copy authoritative.
		if _, isUniversal := doc["type"]; isUniversal {
			delete(doc, "targetRef")
		}

		docName := name
		if len(selectors) > 1 {
			docName = fmt.Sprintf("%s-%d", name, i)
			if meta, ok := doc["metadata"].(map[string]interface{}); ok {
				meta["name"] = docName
			}
			warnings = append(warnings, fmt.Sprintf(
				"OPAPolicy %q: selector %d of %d became a separate MeshOPA %q — MeshOPA holds a single "+
					"targetRef. Apply all %d documents.", name, i+1, len(selectors), docName, len(selectors)))
		}

		if target.IsV3() {
			_, ws := fixMeshOPATargetRefForV3(doc, docName)
			warnings = append(warnings, ws...)
			_, wsDS := fixMeshOPADataSourcesForV3(doc, docName)
			warnings = append(warnings, wsDS...)
		}

		b, err := yaml.Marshal(doc)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal MeshOPA %q: %w", docName, err)
		}
		out = append(out, b)
	}
	return out, warnings, nil
}

// targetRefToMap renders a TargetRef as a plain map for embedding in a
// map-based document.
func targetRefToMap(ref TargetRef) map[string]interface{} {
	m := map[string]interface{}{"kind": ref.Kind}
	if ref.Name != nil && *ref.Name != "" {
		m["name"] = *ref.Name
	}
	if ref.Namespace != nil && *ref.Namespace != "" {
		m["namespace"] = *ref.Namespace
	}
	if len(ref.Tags) > 0 {
		tags := map[string]interface{}{}
		for k, v := range ref.Tags {
			tags[k] = v
		}
		m["tags"] = tags
	}
	if len(ref.Labels) > 0 {
		labels := map[string]interface{}{}
		for k, v := range ref.Labels {
			labels[k] = v
		}
		m["labels"] = labels
	}
	return m
}

// deepCopyMap returns a deep copy of a decoded YAML document so per-selector
// documents can diverge without aliasing each other.
func deepCopyMap(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		return deepCopyMap(t)
	case []interface{}:
		s := make([]interface{}, len(t))
		for i, e := range t {
			s[i] = deepCopyValue(e)
		}
		return s
	default:
		return v
	}
}

// fixMeshOPATargetRefForV3 rewrites a MeshOPA targetRef for the 3.0 API, which
// removes spec.targetRef.{name,namespace,mesh} and narrows spec.targetRef.kind
// to Mesh/Dataplane only (MeshService is no longer valid).
//
// `name` is converted to labels["kuma.io/display-name"] rather than dropped.
// That distinction is the whole point: simply deleting `name` is what produces
// 3.0's scope-widening failure, where a policy scoped to one service silently
// starts applying to every service matching `kind` and the rego begins evaluating
// requests it never saw. Carrying the value into the label preserves the original
// scope.
//
// `namespace` and `mesh` are dropped: the API server prunes namespace on the next
// write on Kubernetes, it is ignored on load in Universal, and `mesh` was only
// ever reserved for future use.
//
// `kind: MeshService` is converted to `kind: Dataplane`, per the exact rewrite
// UPGRADE_km.md documents for this case (kind: MeshService + labels:
// {kuma.io/display-name: ...} → kind: Dataplane + labels: {app: ...}) — the
// label key convention changes along with the kind, so it is renamed too, not
// just carried over.
//
// `kind: MeshSubset` is converted to `kind: Dataplane` with its tags as labels.
// The 3.0 MeshOPA CRD enum is [Mesh, Dataplane] and the 3.0 targetRef has no
// `tags` field, so a MeshSubset{tags} — what TransformOPAPolicy produces from a
// legacy selector without kuma.io/service — would be rejected on apply. A
// MeshSubset with no tags selects every proxy and becomes kind: Mesh.
//
// Returns whether the document was modified.
func fixMeshOPATargetRefForV3(obj map[string]interface{}, name string) (bool, []string) {
	spec, _ := obj["spec"].(map[string]interface{})
	if spec == nil {
		return false, nil
	}
	targetRef, _ := spec["targetRef"].(map[string]interface{})
	if targetRef == nil {
		return false, nil
	}

	var warnings []string
	changed := false

	// Convert the kind FIRST, before the name→label move below. Doing it first
	// means that move writes into (and conflict-checks against) the label key
	// that matches the FINAL kind — labels["app"] for Dataplane rather than
	// labels["kuma.io/display-name"] for MeshService — so a later warning about
	// a conflicting label names the key that is actually in the output.
	wasMeshService := false
	displayLabelKey := "kuma.io/display-name" // MeshService selector convention
	if trKind, _ := targetRef["kind"].(string); trKind == "MeshSubset" {
		c, ws := convertMeshOPAMeshSubsetForV3(targetRef, name)
		changed = changed || c
		warnings = append(warnings, ws...)
	}
	if trKind, _ := targetRef["kind"].(string); trKind == "MeshService" {
		wasMeshService = true
		displayLabelKey = "app" // Dataplane selector convention
		targetRef["kind"] = "Dataplane"
		changed = true
		if labels, ok := targetRef["labels"].(map[string]interface{}); ok {
			if dn, ok := labels["kuma.io/display-name"].(string); ok && dn != "" {
				if _, exists := labels["app"]; !exists {
					labels["app"] = dn
					delete(labels, "kuma.io/display-name")
				}
			}
		}
	}

	if tName, ok := targetRef["name"].(string); ok && tName != "" {
		labels, _ := targetRef["labels"].(map[string]interface{})
		if labels == nil {
			labels = map[string]interface{}{}
		}
		if existing, ok := labels[displayLabelKey].(string); ok && existing != "" && existing != tName {
			// Do not silently overwrite a conflicting selector.
			warnings = append(warnings, fmt.Sprintf(
				"MeshOPA %q: spec.targetRef.name=%q conflicts with the existing labels[%q]=%q. 3.0 "+
					"removes targetRef.name; the label was left as-is and name was NOT migrated — "+
					"resolve this by hand.",
				name, tName, displayLabelKey, existing))
		} else {
			labels[displayLabelKey] = tName
			targetRef["labels"] = labels
			delete(targetRef, "name")
			changed = true
			warnings = append(warnings, fmt.Sprintf(
				"MeshOPA %q: spec.targetRef.name=%q was moved to labels[%q] (3.0 removes "+
					"targetRef.name). This preserves the original scope — dropping the field instead "+
					"would widen the policy to every service matching its kind.",
				name, tName, displayLabelKey))
		}
	}

	for _, f := range []string{"namespace", "mesh"} {
		if v, ok := targetRef[f]; ok && v != nil && v != "" {
			delete(targetRef, f)
			changed = true
			warnings = append(warnings, fmt.Sprintf(
				"MeshOPA %q: spec.targetRef.%s was removed (3.0 removes it; it is pruned by the "+
					"API server on Kubernetes and ignored on load in Universal).",
				name, f))
		}
	}

	if wasMeshService {
		warnings = append(warnings, fmt.Sprintf(
			"MeshOPA %q: spec.targetRef.kind MeshService is removed in 3.0 (only Mesh and Dataplane "+
				"remain valid) — converted to kind: Dataplane, and any labels[\"kuma.io/display-name\"] "+
				"selector was renamed to labels[\"app\"] (the Dataplane label convention). \"app\" is a "+
				"common but not guaranteed Kubernetes pod label — verify it actually matches the target "+
				"workload.",
			name))
	}

	return changed, warnings
}

// convertMeshOPAMeshSubsetForV3 rewrites a MeshOPA targetRef of kind MeshSubset
// for 3.0: tags become Dataplane labels, no tags becomes kind: Mesh. An existing
// label with a different value is not overwritten; the conversion is then
// abandoned and reported, leaving the document as it was.
func convertMeshOPAMeshSubsetForV3(targetRef map[string]interface{}, name string) (bool, []string) {
	tags, _ := targetRef["tags"].(map[string]interface{})
	if len(tags) == 0 {
		targetRef["kind"] = "Mesh"
		delete(targetRef, "tags")
		return true, []string{fmt.Sprintf(
			"MeshOPA %q: spec.targetRef kind MeshSubset with no tags was converted to kind: Mesh "+
				"(3.0 accepts only Mesh and Dataplane; an empty MeshSubset already selected every proxy).",
			name)}
	}

	labels, _ := targetRef["labels"].(map[string]interface{})
	for k, v := range tags {
		if existing, ok := labels[k]; ok && existing != v {
			return false, []string{fmt.Sprintf(
				"MeshOPA %q: spec.targetRef kind MeshSubset is rejected in 3.0 (only Mesh and Dataplane). "+
					"Its tag %s=%v conflicts with the existing label %s=%v, so it was NOT converted. "+
					"Rewrite it as kind: Dataplane with labels by hand.",
				name, k, v, k, existing)}
		}
	}
	if labels == nil {
		labels = map[string]interface{}{}
	}
	keys := make([]string, 0, len(tags))
	for k, v := range tags {
		labels[k] = v
		keys = append(keys, k)
	}
	sort.Strings(keys)
	targetRef["kind"] = "Dataplane"
	targetRef["labels"] = labels
	delete(targetRef, "tags")
	return true, []string{fmt.Sprintf(
		"MeshOPA %q: spec.targetRef kind MeshSubset (rejected in 3.0, which accepts only Mesh and "+
			"Dataplane and has no tags field) was converted to kind: Dataplane with labels %s. A "+
			"Dataplane label selector matches the Dataplane's labels, not its inbound tags: check that "+
			"these keys exist as labels on the pods (Kubernetes) or in the Dataplane metadata "+
			"(Universal), or the policy selects nothing.",
		name, strings.Join(keys, ", "))}
}

// legacyOPADataSourceFields are the pre-3.0 flat DataSource variants on
// MeshOPA's agentConfig and appendPolicies[].rego fields.
var legacyOPADataSourceFields = []string{"secret", "inline", "inlineString"}

// hasLegacyOPADataSourceShape reports whether ds still uses the pre-3.0 flat
// DataSource shape (a bare secret/inline/inlineString key) rather than the
// discriminated SecureDataSource shape (a "type" key).
func hasLegacyOPADataSourceShape(ds map[string]interface{}) bool {
	if ds == nil {
		return false
	}
	if _, hasType := ds["type"]; hasType {
		return false
	}
	for _, k := range legacyOPADataSourceFields {
		if v, ok := ds[k]; ok && v != nil && v != "" {
			return true
		}
	}
	return false
}

// convertOPADataSourceToSecure rewrites a legacy flat DataSource map (secret /
// inline / inlineString) to the discriminated SecureDataSource shape 3.0
// requires (type + insecureInline.value / secretRef.{kind,name}). Returns the
// replacement map, or nil with a non-empty note when the field should be left
// as-is (already converted, empty, or malformed).
//
// inline is base64 and is decoded here, unlike the MeshExternalService TLS
// DataSource case (warnMeshExternalServiceDataSource) which leaves inline
// decoding to the operator: that field holds certificate/key material, where
// re-emitting decoded bytes as plain-text YAML is a real exposure decision.
// Rego source and OPA agent config are not credential material — inlineString,
// the plain-text sibling of inline, is already accepted unencrypted in the
// same field today — so decoding here introduces no new exposure.
func convertOPADataSourceToSecure(ds map[string]interface{}) (map[string]interface{}, string) {
	if !hasLegacyOPADataSourceShape(ds) {
		return nil, ""
	}
	if secret, ok := ds["secret"].(string); ok && secret != "" {
		return map[string]interface{}{
			"type":      "Secret",
			"secretRef": map[string]interface{}{"kind": "Secret", "name": secret},
		}, ""
	}
	if inlineStr, ok := ds["inlineString"].(string); ok && inlineStr != "" {
		return map[string]interface{}{
			"type":           "InsecureInline",
			"insecureInline": map[string]interface{}{"value": inlineStr},
		}, ""
	}
	if inlineB64, ok := ds["inline"].(string); ok && inlineB64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(inlineB64)
		if err != nil {
			return nil, fmt.Sprintf("inline value is not valid base64 (%v) — left as-is; fix by hand", err)
		}
		return map[string]interface{}{
			"type":           "InsecureInline",
			"insecureInline": map[string]interface{}{"value": string(decoded)},
		}, ""
	}
	return nil, ""
}

// fixMeshOPADataSourcesForV3 rewrites a MeshOPA's spec.default.agentConfig and
// every spec.default.appendPolicies[].rego from the legacy flat DataSource
// shape to the discriminated SecureDataSource type 3.0 requires. A MeshOPA
// still using the old shape is rejected at write time on 3.0 — the missing
// `type` discriminator is a validation violation (UPGRADE_km.md: "MeshOPA data
// sources use the SecureDataSource shape").
//
// Returns whether the document was modified.
func fixMeshOPADataSourcesForV3(obj map[string]interface{}, name string) (bool, []string) {
	spec, _ := obj["spec"].(map[string]interface{})
	if spec == nil {
		return false, nil
	}
	def, _ := spec["default"].(map[string]interface{})
	if def == nil {
		return false, nil
	}

	var warnings []string
	changed := false

	if agentConfig, ok := def["agentConfig"].(map[string]interface{}); ok {
		secure, errNote := convertOPADataSourceToSecure(agentConfig)
		if secure != nil {
			def["agentConfig"] = secure
			changed = true
			warnings = append(warnings, fmt.Sprintf(
				"MeshOPA %q: spec.default.agentConfig was rewritten from the legacy flat DataSource "+
					"shape to SecureDataSource (3.0 removes the old shape; a MeshOPA still using it is "+
					"rejected at write time) — auto-corrected.",
				name))
		} else if errNote != "" {
			warnings = append(warnings, fmt.Sprintf("MeshOPA %q: spec.default.agentConfig: %s.", name, errNote))
		}
	}

	if policies, ok := def["appendPolicies"].([]interface{}); ok {
		for i, p := range policies {
			pm, ok := p.(map[string]interface{})
			if !ok {
				continue
			}
			rego, ok := pm["rego"].(map[string]interface{})
			if !ok {
				continue
			}
			secure, errNote := convertOPADataSourceToSecure(rego)
			if secure != nil {
				pm["rego"] = secure
				changed = true
				warnings = append(warnings, fmt.Sprintf(
					"MeshOPA %q: spec.default.appendPolicies[%d].rego was rewritten from the legacy flat "+
						"DataSource shape to SecureDataSource (3.0 removes the old shape; a MeshOPA still "+
						"using it is rejected at write time) — auto-corrected.",
					name, i))
			} else if errNote != "" {
				warnings = append(warnings, fmt.Sprintf(
					"MeshOPA %q: spec.default.appendPolicies[%d].rego: %s.", name, i, errNote))
			}
		}
	}

	return changed, warnings
}

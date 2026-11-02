package migrator

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

// Kuma 3.0 policy-shape checks and rewrites that are not tied to a single
// scenario. Every function here is called from ScanForDeprecations or from the
// legacy transforms; the v3-only ones are gated by their callers.

// displayNameLabel and kubeNamespaceLabel live in v3_gateway.go, zoneLabel in v3_mes.go.
const originLabel = "kuma.io/origin"

// controlPlaneOwnedLabels are the labels the Kuma 3.0 control plane computes or
// sets itself, minus kuma.io/mesh (which is how a Kubernetes resource states its
// mesh and must stay). Source: pkg/core/resources/labels/registry.go on kuma
// master (descriptors with Owner: OwnerControlPlane) and UPGRADE.md
// "Control-plane-owned labels are rejected when they differ from the computed value".
//
// A supplied value that differs from the computed one is rejected on create; an
// absent one is always accepted. The controller-only labels (managed-by,
// deletion-grace-period-started-at, service-name, is-headless-service) cannot be
// supplied at all. Stripping them all is therefore always safe.
var controlPlaneOwnedLabels = []string{
	originLabel,
	zoneLabel,
	"kuma.io/env",
	displayNameLabel,
	"kuma.io/policy-role",
	kubeNamespaceLabel,
	"k8s.kuma.io/service-account",
	"kuma.io/listener-zoneingress",
	"kuma.io/listener-zoneegress",
	"kuma.io/managed-by",
	"kuma.io/deletion-grace-period-started-at",
	"k8s.kuma.io/service-name",
	"k8s.kuma.io/is-headless-service",
}

// objLabels returns the resource's own labels: metadata.labels on Kubernetes
// format, the top-level labels map on Universal format. Never a selector.
func objLabels(obj map[string]interface{}) map[string]interface{} {
	if md, ok := obj["metadata"].(map[string]interface{}); ok {
		labels, _ := md["labels"].(map[string]interface{})
		return labels
	}
	labels, _ := obj["labels"].(map[string]interface{})
	return labels
}

// isKumaResource reports whether obj is a Kuma resource: apiVersion kuma.io/* on
// Kubernetes format, or a Universal document (type, no apiVersion). Gateway API
// and core Kubernetes objects are left alone.
func isKumaResource(obj map[string]interface{}) bool {
	if av, ok := obj["apiVersion"].(string); ok {
		return strings.HasPrefix(av, "kuma.io/")
	}
	_, hasType := obj["type"].(string)
	return hasType
}

// stripControlPlaneLabels removes the control-plane-owned labels from a Kuma
// resource's own labels (never from targetRef.labels, which are selectors).
// Called under --to-latest v3 only.
func stripControlPlaneLabels(obj map[string]interface{}, name, kind string) (bool, []string) {
	if !isKumaResource(obj) {
		return false, nil
	}
	labels := objLabels(obj)
	if len(labels) == 0 {
		return false, nil
	}
	var removed []string
	for _, k := range controlPlaneOwnedLabels {
		if _, ok := labels[k]; ok {
			delete(labels, k)
			removed = append(removed, k)
		}
	}
	if len(removed) == 0 {
		return false, nil
	}
	return true, []string{fmt.Sprintf(
		"%s %q: removed control-plane-owned labels %s. Kuma 3.0 computes them itself and rejects a "+
			"supplied value that differs from the computed one, e.g. after a rename or a policy split.",
		kind, name, strings.Join(removed, ", "))}
}

// ---- Labels-only real-resource selection (UPGRADE.md line ~916) ---------------

// labelSelectedKinds are the targetRef/backendRef kinds that 3.0 resolves by
// labels only, with name and namespace removed from the schema.
var labelSelectedKinds = map[string]bool{
	"MeshService":          true,
	"MeshExternalService":  true,
	"MeshMultiZoneService": true,
	"MeshHTTPRoute":        true,
}

// namespacedRefKinds are the selectable kinds that users create in their own
// namespace. For these, a 2.x ref with a name but no namespace resolved in the
// policy's namespace, so the converted labels pin k8s.kuma.io/namespace to it.
// MeshExternalService and MeshMultiZoneService live in the system namespace.
var namespacedRefKinds = map[string]bool{
	"MeshService":   true,
	"MeshHTTPRoute": true,
}

// fixRealResourceTargetRefsForV3 rewrites name/namespace/mesh on every ref in
// spec.targetRef, spec.to[].targetRef and the route backendRefs under spec.to[]
// to labels, as 3.0 requires:
//   - name      → labels["kuma.io/display-name"]
//   - namespace → labels["k8s.kuma.io/namespace"] (the policy's own namespace
//     when only name was set on a MeshService/MeshHTTPRoute ref)
//   - mesh      → labels["kuma.io/mesh"]
//
// sectionName stays. A Dataplane ref with name/namespace is only warned about: a
// Dataplane name is not one of its labels. A ref whose existing labels conflict
// with the derived ones is left untouched and warned about.
func fixRealResourceTargetRefsForV3(obj map[string]interface{}, name, kind string) (bool, []string) {
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return false, nil
	}
	policyNS := ""
	if md, ok := obj["metadata"].(map[string]interface{}); ok {
		policyNS, _ = md["namespace"].(string)
	}
	// A zone-origin policy referred to the zone's own resource by name. Labels also
	// match same-named resources synced from other zones, so pin the zone.
	policyZone := ""
	if own := objLabels(obj); own != nil && own[originLabel] == "zone" {
		policyZone, _ = own[zoneLabel].(string)
	}

	type refAt struct {
		path string
		ref  map[string]interface{}
	}
	var refs []refAt
	if tr, ok := spec["targetRef"].(map[string]interface{}); ok {
		refs = append(refs, refAt{"spec.targetRef", tr})
	}
	to, _ := spec["to"].([]interface{})
	for i, entry := range to {
		em, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		if tr, ok := em["targetRef"].(map[string]interface{}); ok {
			refs = append(refs, refAt{fmt.Sprintf("spec.to[%d].targetRef", i), tr})
		}
	}

	modified := false
	var warnings []string
	for _, r := range refs {
		changed, ws := fixRefToLabels(r.ref, r.path, kind, name, policyNS, policyZone)
		if changed {
			modified = true
		}
		warnings = append(warnings, ws...)
	}
	return modified, warnings
}

// backendRefLabelKinds are the backendRef kinds 3.0 accepts on MeshHTTPRoute and
// MeshTCPRoute; its BackendRef schema has kind/labels/sectionName/weight/port and
// no name or namespace.
var backendRefLabelKinds = map[string]bool{
	"MeshService":          true,
	"MeshExternalService":  true,
	"MeshMultiZoneService": true,
}

// fixRouteBackendRefNamesForV3 rewrites name/namespace/mesh to labels on
// MeshHTTPRoute/MeshTCPRoute spec.to[].rules[].default.backendRefs[] and
// filters[].requestMirror.backendRef entries of kind MeshService,
// MeshExternalService or MeshMultiZoneService, with the same rules as
// fixRealResourceTargetRefsForV3. Other backendRef kinds (MeshServiceSubset) are
// left to fixRouteBackendRefsForV3 in v3_gateway.go.
func fixRouteBackendRefNamesForV3(obj map[string]interface{}, name, kind string) (bool, []string) {
	if kind != "MeshHTTPRoute" && kind != "MeshTCPRoute" {
		return false, nil
	}
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return false, nil
	}
	policyNS := ""
	if md, ok := obj["metadata"].(map[string]interface{}); ok {
		policyNS, _ = md["namespace"].(string)
	}
	policyZone := ""
	if own := objLabels(obj); own != nil && own[originLabel] == "zone" {
		policyZone, _ = own[zoneLabel].(string)
	}

	modified := false
	var warnings []string
	to, _ := spec["to"].([]interface{})
	for i, entry := range to {
		em, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		collectBackendRefs(em["rules"], fmt.Sprintf("spec.to[%d].rules", i), func(p string, br map[string]interface{}) {
			if k, _ := br["kind"].(string); !backendRefLabelKinds[k] {
				return
			}
			changed, ws := fixRefToLabels(br, p, kind, name, policyNS, policyZone)
			if changed {
				modified = true
			}
			warnings = append(warnings, ws...)
		})
	}
	return modified, warnings
}

// collectBackendRefs walks a route's rules[] and calls fn for every
// default.backendRefs[] entry and every filters[].requestMirror.backendRef.
func collectBackendRefs(rules interface{}, path string, fn func(string, map[string]interface{})) {
	list, _ := rules.([]interface{})
	for j, rule := range list {
		rm, ok := rule.(map[string]interface{})
		if !ok {
			continue
		}
		def, ok := rm["default"].(map[string]interface{})
		if !ok {
			continue
		}
		brs, _ := def["backendRefs"].([]interface{})
		for k, br := range brs {
			if bm, ok := br.(map[string]interface{}); ok {
				fn(fmt.Sprintf("%s[%d].default.backendRefs[%d]", path, j, k), bm)
			}
		}
		filters, _ := def["filters"].([]interface{})
		for k, f := range filters {
			fm, ok := f.(map[string]interface{})
			if !ok {
				continue
			}
			mirror, ok := fm["requestMirror"].(map[string]interface{})
			if !ok {
				continue
			}
			if bm, ok := mirror["backendRef"].(map[string]interface{}); ok {
				fn(fmt.Sprintf("%s[%d].default.filters[%d].requestMirror.backendRef", path, j, k), bm)
			}
		}
	}
}

func fixRefToLabels(ref map[string]interface{}, path, kind, name, policyNS, policyZone string) (bool, []string) {
	refKind, _ := ref["kind"].(string)
	refName, hasName := ref["name"].(string)
	refNS, hasNS := ref["namespace"].(string)
	refMesh, hasMesh := ref["mesh"].(string)

	if refKind == "Dataplane" && (hasName || hasNS) {
		return false, []string{fmt.Sprintf(
			"%s %q: %s selects a Dataplane by name/namespace (%s/%s). Kuma 3.0 selects Dataplanes by "+
				"labels only and rejects name and namespace. A Dataplane name is not one of its labels, so "+
				"this is not rewritten: replace it with labels the target proxies carry (e.g. app, "+
				"k8s.kuma.io/namespace).",
			kind, name, path, refNS, refName)}
	}
	if !labelSelectedKinds[refKind] && refKind != "Dataplane" {
		if hasMesh || hasName || hasNS {
			return false, []string{fmt.Sprintf(
				"%s %q: %s (kind %s) sets name/namespace/mesh, which Kuma 3.0 removes from targetRef. "+
					"Remove them.", kind, name, path, refKind)}
		}
		return false, nil
	}
	if !hasName && !hasNS && !hasMesh {
		return false, nil
	}

	want := map[string]string{}
	if hasName && refName != "" && refKind != "Dataplane" {
		want[displayNameLabel] = refName
		if !hasNS && policyNS != "" && namespacedRefKinds[refKind] {
			want[kubeNamespaceLabel] = policyNS
		}
		if policyZone != "" {
			want[zoneLabel] = policyZone
		}
	}
	if hasNS && refNS != "" {
		want[kubeNamespaceLabel] = refNS
	}
	if hasMesh && refMesh != "" {
		want[meshLabel] = refMesh
	}

	existing, _ := ref["labels"].(map[string]interface{})
	var conflicts []string
	for k, v := range want {
		if cur, ok := existing[k]; ok && cur != v {
			conflicts = append(conflicts, fmt.Sprintf("%s=%v (derived %s)", k, cur, v))
		}
	}
	if len(conflicts) > 0 {
		sort.Strings(conflicts)
		return false, []string{fmt.Sprintf(
			"%s %q: %s sets name/namespace/mesh and labels that disagree (%s). Kuma 3.0 selects %s by "+
				"labels only and rejects name/namespace/mesh. Not rewritten: resolve the conflict by hand.",
			kind, name, path, strings.Join(conflicts, ", "), refKind)}
	}

	labels := existing
	if labels == nil {
		labels = map[string]interface{}{}
	}
	for k, v := range want {
		labels[k] = v
	}
	ref["labels"] = labels
	delete(ref, "name")
	delete(ref, "namespace")
	delete(ref, "mesh")

	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k+"="+want[k])
	}
	sort.Strings(keys)
	return true, []string{fmt.Sprintf(
		"%s %q: %s rewritten from name/namespace/mesh to labels {%s} — Kuma 3.0 selects %s by labels "+
			"only. Check that the labels select the same resource; labels match every resource carrying "+
			"them, so verify none is shared by another resource.",
		kind, name, path, strings.Join(keys, ", "), refKind)}
}

// ---- spec.to[].targetRef kinds on 3.0 ------------------------------------------

// v3ToTargetRefKinds lists, per policy, the spec.to[].targetRef kinds the Kuma 3.0
// validators accept. Extracted from validateTo in
// pkg/plugins/policies/*/api/v1alpha1/validator.go on kuma master; also tabulated
// in docs/meshhttproute-3.0.md. A policy listed with an empty set has no to[].
// MeshFaultInjection and MeshRateLimit additionally reject to[] unless the
// top-level targetRef is Mesh (common_api.IncludesGateways).
var v3ToTargetRefKinds = map[string]map[string]bool{
	"MeshAccessLog":             kindSet("Mesh", "MeshService", "MeshExternalService", "MeshMultiZoneService", "MeshHTTPRoute"),
	"MeshCircuitBreaker":        kindSet("Mesh", "MeshService", "MeshExternalService", "MeshMultiZoneService"),
	"MeshFaultInjection":        kindSet("Mesh"),
	"MeshHealthCheck":           kindSet("Mesh", "MeshService", "MeshExternalService", "MeshMultiZoneService"),
	"MeshHTTPRoute":             kindSet("MeshService", "MeshExternalService", "MeshMultiZoneService"),
	"MeshLoadBalancingStrategy": kindSet("Mesh", "MeshService", "MeshExternalService", "MeshMultiZoneService", "MeshHTTPRoute"),
	"MeshMetric":                kindSet(),
	"MeshPassthrough":           kindSet(),
	"MeshProxyPatch":            kindSet(),
	"MeshRateLimit":             kindSet("Mesh"),
	"MeshRetry":                 kindSet("Mesh", "MeshService", "MeshExternalService", "MeshMultiZoneService", "MeshHTTPRoute"),
	"MeshTCPRoute":              kindSet("MeshService", "MeshExternalService", "MeshMultiZoneService"),
	"MeshTimeout":               kindSet("Mesh", "MeshService", "MeshExternalService", "MeshMultiZoneService", "MeshHTTPRoute"),
	"MeshTLS":                   kindSet(),
	"MeshTrace":                 kindSet(),
	"MeshTrafficPermission":     kindSet(),
}

// v3MeshOnlyToKinds are the policies whose 3.0 validator rejects to[] outright
// unless spec.targetRef is kind: Mesh.
var v3MeshOnlyToKinds = map[string]bool{
	"MeshFaultInjection": true,
	"MeshRateLimit":      true,
}

func kindSet(kinds ...string) map[string]bool {
	m := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		m[k] = true
	}
	return m
}

// warnV3ToTargetRefKinds flags every spec.to[].targetRef kind that the policy's
// 3.0 validator rejects. MeshSubset, MeshServiceSubset and MeshGateway are
// rejected by every policy. Called under --to-latest v3 only.
func warnV3ToTargetRefKinds(obj map[string]interface{}, name, kind string) []string {
	allowed, known := v3ToTargetRefKinds[kind]
	if !known {
		return nil
	}
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return nil
	}
	to, ok := spec["to"].([]interface{})
	if !ok || len(to) == 0 {
		return nil
	}
	if len(allowed) == 0 {
		return []string{fmt.Sprintf(
			"%s %q: Kuma 3.0 %s has no spec.to[] and rejects this policy. Move the configuration to "+
				"the field the policy uses on 3.0 (spec.default or spec.rules[]).", kind, name, kind)}
	}
	if v3MeshOnlyToKinds[kind] {
		topKind := "Mesh"
		if tr, ok := spec["targetRef"].(map[string]interface{}); ok {
			if k, _ := tr["kind"].(string); k != "" {
				topKind = k
			}
		}
		if topKind != "Mesh" {
			return []string{fmt.Sprintf(
				"%s %q: Kuma 3.0 rejects spec.to[] on %s unless spec.targetRef is kind: Mesh (here %s). "+
					"Use spec.rules[] to configure the targeted proxies' inbound traffic.",
				kind, name, kind, topKind)}
		}
	}

	var warnings []string
	for i, entry := range to {
		em, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		tr, _ := em["targetRef"].(map[string]interface{})
		k, _ := tr["kind"].(string)
		if k == "" || allowed[k] {
			continue
		}
		warnings = append(warnings, fmt.Sprintf(
			"%s %q: spec.to[%d].targetRef.kind %s is rejected by Kuma 3.0. %s accepts %s in to[]; no policy "+
				"accepts MeshSubset, MeshServiceSubset or MeshGateway there. Point it at the real destination "+
				"selected by labels.",
			kind, name, i, k, kind, strings.Join(sortedKeys(allowed), ", ")))
	}
	return warnings
}

// ---- healthyPanicThreshold: MeshHealthCheck → MeshCircuitBreaker ---------------

// panicThresholdDetectors is the detector set given to a companion
// MeshCircuitBreaker. 3.0 applies healthyPanicThreshold only from an enabled
// outlierDetection, and the validator requires at least one detector there.
// totalFailures.consecutive: 5 is Envoy's own consecutive_5xx default.
var panicThresholdDetectors = map[string]interface{}{
	"totalFailures": map[string]interface{}{"consecutive": 5},
}

const panicThresholdCaveat = "3.0 reads healthyPanicThreshold only from an enabled MeshCircuitBreaker " +
	"outlierDetection, which needs at least one detector, so the companion sets " +
	"detectors.totalFailures.consecutive: 5 (Envoy's default). That turns on outlier ejection for these " +
	"destinations; if a MeshCircuitBreaker already covers them, move the threshold into it instead."

// splitHealthyPanicThreshold moves to[].default.healthyPanicThreshold out of a
// converted MeshHealthCheck into a companion MeshCircuitBreaker with the same
// spec.targetRef and to[].targetRef. Returns nil when the policy carries none.
func splitHealthyPanicThreshold(policy *KubePolicy) (*KubePolicy, []string, error) {
	var mcbTo []PolicyEntry
	for i, entry := range policy.Spec.To {
		if len(entry.Default) == 0 {
			continue
		}
		var def map[string]interface{}
		if err := json.Unmarshal(entry.Default, &def); err != nil {
			return nil, nil, fmt.Errorf("parse MeshHealthCheck %q to[%d].default: %w", policy.Metadata.Name, i, err)
		}
		threshold, ok := def["healthyPanicThreshold"]
		if !ok {
			continue
		}
		delete(def, "healthyPanicThreshold")
		b, err := json.Marshal(def)
		if err != nil {
			return nil, nil, err
		}
		policy.Spec.To[i].Default = b
		mcbDef, err := json.Marshal(map[string]interface{}{
			"outlierDetection": map[string]interface{}{
				"healthyPanicThreshold": threshold,
				"detectors":             panicThresholdDetectors,
			},
		})
		if err != nil {
			return nil, nil, err
		}
		mcbTo = append(mcbTo, PolicyEntry{TargetRef: entry.TargetRef, Default: mcbDef})
	}
	if len(mcbTo) == 0 {
		return nil, nil, nil
	}
	top := TargetRef{Kind: "Mesh"}
	if policy.Spec.TargetRef != nil {
		top = *policy.Spec.TargetRef
	}
	mcbName := policy.Metadata.Name + "-panic-threshold"
	mcb := buildKubePolicy(mcbName, policy.Metadata.Namespace, "MeshCircuitBreaker", top, mcbTo, nil)
	mcb.Metadata.Labels = copyStringMap(policy.Metadata.Labels)
	return &mcb, []string{fmt.Sprintf(
		"MeshHealthCheck %q: healthyPanicThreshold moved to companion MeshCircuitBreaker %q "+
			"(to[].default.outlierDetection.healthyPanicThreshold) — Kuma 3.0 removes the field from "+
			"MeshHealthCheck. %s",
		policy.Metadata.Name, mcbName, panicThresholdCaveat)}, nil
}

// fixHealthCheckPanicThresholdForV3 removes healthyPanicThreshold from a
// hand-written MeshHealthCheck (3.0 rejects the unknown field on the API server
// and prunes it on Kubernetes) and warns with the MeshCircuitBreaker that carries
// it on 3.0. The scan pass emits one document per input, so the companion is
// printed in the warning rather than written.
func fixHealthCheckPanicThresholdForV3(obj map[string]interface{}, name string) (bool, []string) {
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return false, nil
	}
	modified := false
	var warnings []string

	for _, key := range []string{"default", "conf"} {
		if def, ok := spec[key].(map[string]interface{}); ok {
			if _, ok := def["healthyPanicThreshold"]; ok {
				delete(def, "healthyPanicThreshold")
				modified = true
				warnings = append(warnings, fmt.Sprintf(
					"MeshHealthCheck %q: removed spec.%s.healthyPanicThreshold — Kuma 3.0 removes the field. "+
						"Set it on a MeshCircuitBreaker as to[].default.outlierDetection.healthyPanicThreshold.",
					name, key))
			}
		}
	}

	var mcbTo []interface{}
	to, _ := spec["to"].([]interface{})
	for _, entry := range to {
		em, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		def, ok := em["default"].(map[string]interface{})
		if !ok {
			continue
		}
		threshold, ok := def["healthyPanicThreshold"]
		if !ok {
			continue
		}
		delete(def, "healthyPanicThreshold")
		modified = true
		mcbTo = append(mcbTo, map[string]interface{}{
			"targetRef": em["targetRef"],
			"default": map[string]interface{}{
				"outlierDetection": map[string]interface{}{
					"healthyPanicThreshold": threshold,
					"detectors":             panicThresholdDetectors,
				},
			},
		})
	}
	if len(mcbTo) > 0 {
		mcbSpec := map[string]interface{}{"to": mcbTo}
		if tr, ok := spec["targetRef"]; ok {
			mcbSpec["targetRef"] = tr
		}
		md := map[string]interface{}{"name": name + "-panic-threshold"}
		if omd, ok := obj["metadata"].(map[string]interface{}); ok {
			if ns, ok := omd["namespace"]; ok {
				md["namespace"] = ns
			}
		}
		if mesh, ok := objLabels(obj)[meshLabel]; ok {
			md["labels"] = map[string]interface{}{meshLabel: mesh}
		}
		snippet, _ := yaml.Marshal(map[string]interface{}{
			"apiVersion": kumaAPIVersion,
			"kind":       "MeshCircuitBreaker",
			"metadata":   md,
			"spec":       mcbSpec,
		})
		warnings = append(warnings, fmt.Sprintf(
			"MeshHealthCheck %q: removed to[].default.healthyPanicThreshold — Kuma 3.0 removes the field "+
				"(it would be rejected or silently dropped, falling back to Envoy's 50%%). Apply this "+
				"MeshCircuitBreaker to keep the threshold. %s\n%s",
			name, panicThresholdCaveat, strings.TrimRight(string(snippet), "\n")))
	}
	return modified, warnings
}

// ---- MeshFaultInjection from[] (all clients) → rules[] -------------------------

// fixMeshFaultInjectionFromForV3 rewrites a MeshFaultInjection whose from[]
// entries all target kind: Mesh into rules[] entries without matches[], which on
// 3.0 means "every client" — the same scope. Any narrower from[] entry blocks the
// rewrite (its clients need spiffeID/sni matches the manifest cannot supply);
// warnFromDeprecatedForRulesAPI then reports the policy as rejected.
func fixMeshFaultInjectionFromForV3(obj map[string]interface{}, name string) (bool, []string) {
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return false, nil
	}
	from, ok := spec["from"].([]interface{})
	if !ok || len(from) == 0 {
		return false, nil
	}
	for _, k := range fromSourceKinds(from) {
		if k != "Mesh" {
			return false, nil
		}
	}
	rules, _ := spec["rules"].([]interface{})
	for _, entry := range from {
		em, _ := entry.(map[string]interface{})
		rule := map[string]interface{}{}
		if def, ok := em["default"]; ok {
			rule["default"] = def
		}
		rules = append(rules, rule)
	}
	spec["rules"] = rules
	delete(spec, "from")
	return true, []string{fmt.Sprintf(
		"MeshFaultInjection %q: from[] (kind: Mesh) rewritten to rules[] without matches[] — Kuma 3.0 "+
			"removes from[]; a rule with no matches applies to every client, the same scope.", name)}
}

// ---- MeshTrafficPermission empty match entries (UPGRADE.md line ~32) -----------

// warnMTPEmptyMatches flags `{}` entries in rules[].default.{allow,deny,
// allowWithShadowDeny}. 2.14 accepts them; 3.0 rejects them on create and update
// ("must specify at least one of 'spiffeID' or 'sni'").
func warnMTPEmptyMatches(obj map[string]interface{}, name string, target TargetVersion) []string {
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return nil
	}
	rules, _ := spec["rules"].([]interface{})
	var warnings []string
	for i, rule := range rules {
		rm, _ := rule.(map[string]interface{})
		def, _ := rm["default"].(map[string]interface{})
		for _, action := range []string{"allow", "deny", "allowWithShadowDeny"} {
			entries, _ := def[action].([]interface{})
			for j, e := range entries {
				em, isMap := e.(map[string]interface{})
				if !isMap && e != nil {
					continue
				}
				if em["spiffeID"] != nil || em["sni"] != nil {
					continue
				}
				prefix := "Kuma 3.0 will reject it on create and update"
				if target.IsV3() {
					prefix = "Kuma 3.0 rejects it on create and update"
				}
				warnings = append(warnings, fmt.Sprintf(
					"MeshTrafficPermission %q: rules[%d].default.%s[%d] is an empty match. %s: every entry "+
						"needs spiffeID, sni, or both. Remove it or fill it in on 2.14 before upgrading — the "+
						"upgrade does not rewrite stored policies.",
					name, i, action, j, prefix))
			}
		}
	}
	return warnings
}

// ---- MeshProxyPatch cluster circuitBreakers (UPGRADE.md line ~2627) ------------

// warnMeshProxyPatchCircuitBreakers flags a MeshProxyPatch that patches a
// cluster's circuitBreakers. Before 3.0 the patch appended a second DEFAULT
// threshold that Envoy ignored; from 3.0 it merges into the existing one and
// overrides MeshCircuitBreaker for every field it sets.
func warnMeshProxyPatchCircuitBreakers(obj map[string]interface{}, name string, target TargetVersion) []string {
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return nil
	}
	def, _ := spec["default"].(map[string]interface{})
	mods, _ := def["appendModifications"].([]interface{})
	var hits []int
	for i, mod := range mods {
		mm, _ := mod.(map[string]interface{})
		cluster, ok := mm["cluster"].(map[string]interface{})
		if !ok {
			continue
		}
		if op, _ := cluster["operation"].(string); op != "Patch" {
			continue
		}
		patchesCB := false
		if v, ok := cluster["value"].(string); ok &&
			(strings.Contains(v, "circuitBreakers") || strings.Contains(v, "circuit_breakers")) {
			patchesCB = true
		}
		jps, _ := cluster["jsonPatches"].([]interface{})
		for _, jp := range jps {
			jm, _ := jp.(map[string]interface{})
			if p, _ := jm["path"].(string); strings.HasPrefix(p, "/circuitBreakers") || strings.HasPrefix(p, "/circuit_breakers") {
				patchesCB = true
			}
		}
		if patchesCB {
			hits = append(hits, i)
		}
	}
	if len(hits) == 0 {
		return nil
	}
	when := "From Kuma 3.0 the patch"
	if target.IsV3() {
		when = "On Kuma 3.0 the patch"
	}
	return []string{fmt.Sprintf(
		"MeshProxyPatch %q: appendModifications %v patch cluster circuitBreakers. Before 3.0 this appended a "+
			"second DEFAULT-priority threshold that Envoy ignored. %s merges into the existing threshold "+
			"and overrides MeshCircuitBreaker for every field it sets. Remove the patch if you no longer rely "+
			"on it, or confirm the values it sets are the ones you want.",
		name, hits, when)}
}

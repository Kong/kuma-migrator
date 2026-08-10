package migrator

import (
	"fmt"

	"sigs.k8s.io/yaml"
)

// TransformOPAPolicy converts a Kong Mesh OPAPolicy resource to the new MeshOPA API.
//
// Structural change (Kong Mesh 2.5+):
//   - kind: OPAPolicy → kind: MeshOPA
//   - spec.conf.policies[].inlineString → spec.default.appendPolicies[].rego.inlineString
//   - spec.conf.policies[].secret      → spec.default.appendPolicies[].rego.secret
//   - spec.conf.agentConfig            → spec.default.agentConfig (preserved as-is)
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

	kind, _ := obj["kind"].(string)
	name := extractNameFromObj(obj)

	if kind == "MeshOPA" {
		// Already converted. Under v3 the targetRef still needs rewriting.
		if !target.IsV3() {
			return [][]byte{raw}, nil, nil
		}
		changed, ws := fixMeshOPATargetRefForV3(obj, name)
		if !changed {
			return [][]byte{raw}, ws, nil
		}
		out, err := yaml.Marshal(obj)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal MeshOPA %q: %w", name, err)
		}
		return [][]byte{out}, ws, nil
	}

	var warnings []string

	// Rewrite kind.
	obj["kind"] = "MeshOPA"

	// Transform spec.
	spec, _ := obj["spec"].(map[string]interface{})
	if spec == nil {
		spec = map[string]interface{}{}
		obj["spec"] = spec
	}

	conf, _ := spec["conf"].(map[string]interface{})
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
			for _, p := range policies {
				pol, ok := p.(map[string]interface{})
				if !ok {
					continue
				}
				regoEntry := map[string]interface{}{}
				if inlineStr, ok := pol["inlineString"].(string); ok && inlineStr != "" {
					regoEntry["inlineString"] = inlineStr
				}
				if secret, ok := pol["secret"].(string); ok && secret != "" {
					regoEntry["secret"] = secret
				}
				appendPolicies = append(appendPolicies, map[string]interface{}{
					"rego": regoEntry,
				})
			}
			if len(appendPolicies) > 0 {
				newDefault["appendPolicies"] = appendPolicies
			}
		}

		// Migrate agentConfig → default.agentConfig.
		if agentConfig, ok := conf["agentConfig"]; ok {
			newDefault["agentConfig"] = agentConfig
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
		delete(spec, "conf")
	}

	obj["spec"] = spec

	if target.IsV3() {
		_, ws := fixMeshOPATargetRefForV3(obj, name)
		warnings = append(warnings, ws...)
	}

	out, err := yaml.Marshal(obj)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal MeshOPA %q: %w", name, err)
	}
	return [][]byte{out}, warnings, nil
}

// fixMeshOPATargetRefForV3 rewrites a MeshOPA targetRef for the 3.0 API, which
// removes spec.targetRef.{name,namespace,mesh}.
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

	if tName, ok := targetRef["name"].(string); ok && tName != "" {
		labels, _ := targetRef["labels"].(map[string]interface{})
		if labels == nil {
			labels = map[string]interface{}{}
		}
		if existing, ok := labels["kuma.io/display-name"].(string); ok && existing != "" && existing != tName {
			// Do not silently overwrite a conflicting selector.
			warnings = append(warnings, fmt.Sprintf(
				"MeshOPA %q: spec.targetRef.name=%q conflicts with the existing "+
					"labels[\"kuma.io/display-name\"]=%q. 3.0 removes targetRef.name; the label was "+
					"left as-is and name was NOT migrated — resolve this by hand.",
				name, tName, existing))
		} else {
			labels["kuma.io/display-name"] = tName
			targetRef["labels"] = labels
			delete(targetRef, "name")
			changed = true
			warnings = append(warnings, fmt.Sprintf(
				"MeshOPA %q: spec.targetRef.name=%q was moved to "+
					"labels[\"kuma.io/display-name\"] (3.0 removes targetRef.name). This preserves the "+
					"original scope — dropping the field instead would widen the policy to every "+
					"service matching its kind.",
				name, tName))
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

	return changed, warnings
}

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
//   - spec.targetRef                   → spec.targetRef (preserved as-is)
//
// If the resource already has kind: MeshOPA it is returned unchanged.
func TransformOPAPolicy(raw []byte) ([][]byte, []string, error) {
	var obj map[string]interface{}
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		return nil, nil, fmt.Errorf("unmarshal OPAPolicy: %w", err)
	}

	kind, _ := obj["kind"].(string)
	name := extractNameFromObj(obj)

	if kind == "MeshOPA" {
		// Already converted.
		return [][]byte{raw}, nil, nil
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

	out, err := yaml.Marshal(obj)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal MeshOPA %q: %w", name, err)
	}
	return [][]byte{out}, warnings, nil
}

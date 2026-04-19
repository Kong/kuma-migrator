package migrator

import (
	"fmt"

	"sigs.k8s.io/yaml"
)

// rulesAPIMigrationKinds is the set of Mesh* policy kinds where the from[] section
// was deprecated in Kuma 2.10 in favour of the rules[] API.
var rulesAPIMigrationKinds = map[string]bool{
	"MeshAccessLog":     true,
	"MeshCircuitBreaker": true,
	"MeshRateLimit":     true,
	"MeshTimeout":       true,
	"MeshTls":           true,
}

// TransformFromToRules migrates a policy that uses the deprecated from[] structure
// (Kuma ≤ 2.9) to the new rules[] API (Kuma 2.10+).
//
// Each from[i].default becomes rules[i].default.  The source targetRef inside
// each from[] entry is discarded because rules[] has no source discrimination;
// a warning is emitted when multiple entries with distinct source kinds are found.
//
// Uses a map-based round-trip to preserve ALL top-level fields (including
// Universal-format fields like "type", "name", "mesh", "creationTime", etc.).
func TransformFromToRules(raw []byte) ([][]byte, []string, error) {
	var obj map[string]interface{}
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		return nil, nil, fmt.Errorf("unmarshal policy: %w", err)
	}

	// Resolve kind and name for warning messages (handles both formats).
	kind, _ := obj["kind"].(string)
	if kind == "" {
		kind, _ = obj["type"].(string)
	}
	name := extractNameFromObj(obj)

	warnings, modified := applyFromToRulesMap(obj, kind, name)
	if !modified {
		return [][]byte{raw}, warnings, nil
	}

	b, err := yaml.Marshal(obj)
	if err != nil {
		return nil, warnings, fmt.Errorf("marshal policy: %w", err)
	}
	return [][]byte{b}, warnings, nil
}

// applyFromToRulesMap performs the from[] → rules[] migration on a
// map[string]interface{}, preserving all other top-level and spec fields.
// Returns (warnings, modified). Called by TransformFromToRules.
func applyFromToRulesMap(obj map[string]interface{}, kind, name string) (warnings []string, modified bool) {
	spec, _ := obj["spec"].(map[string]interface{})
	if spec == nil {
		return nil, false
	}
	fromRaw, exists := spec["from"]
	if !exists {
		return nil, false
	}
	from, _ := fromRaw.([]interface{})
	if len(from) == 0 {
		return nil, false
	}

	// Warn when multiple from[] entries have different source kinds — flattening
	// them into rules[] loses the per-source configuration intent.
	if len(from) > 1 {
		distinctKinds := map[string]bool{}
		for _, entry := range from {
			if em, ok := entry.(map[string]interface{}); ok {
				if tr, ok := em["targetRef"].(map[string]interface{}); ok {
					if k, ok := tr["kind"].(string); ok {
						distinctKinds[k] = true
					}
				}
			}
		}
		if len(distinctKinds) > 1 {
			warnings = append(warnings, fmt.Sprintf(
				"%s %q: from[] has %d entries with different source kinds (%v) — "+
					"rules[] has no source discrimination; all rules will apply to ALL "+
					"inbound traffic regardless of source. Review and consolidate manually.",
				kind, name, len(from), sortedKeys(distinctKinds)))
		} else {
			warnings = append(warnings, fmt.Sprintf(
				"%s %q: from[] had %d entries — merged into %d rules[] entries. "+
					"Verify that the intended per-source behaviour is preserved.",
				kind, name, len(from), len(from)))
		}
	}

	// Convert: each from[i].default → rules[i].default (source targetRef discarded).
	rules := make([]interface{}, 0, len(from))
	for _, entry := range from {
		em, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		rule := map[string]interface{}{}
		if def, ok := em["default"]; ok {
			rule["default"] = def
		}
		rules = append(rules, rule)
	}

	delete(spec, "from")
	if len(rules) > 0 {
		spec["rules"] = rules
	}

	warnings = append(warnings, fmt.Sprintf(
		"%s %q: from[] migrated to rules[] (Kuma 2.10+). "+
			"Source-based targeting inside from[] is no longer supported — "+
			"policies in rules[] apply to all inbound traffic matching spec.targetRef.",
		kind, name))

	return warnings, true
}

// applyFromToRules converts policy.Spec.From → policy.Spec.Rules in-place and
// returns any warnings.  Called from transformScenarioSubset when a Kubernetes-style
// Scenario-B policy also needs the from→rules migration applied as a second pass.
func applyFromToRules(policy *KubePolicy) []string {
	if len(policy.Spec.From) == 0 {
		return nil
	}

	var warnings []string
	name := policy.Metadata.Name
	kind := policy.Kind

	if len(policy.Spec.From) > 1 {
		distinctKinds := map[string]bool{}
		for _, entry := range policy.Spec.From {
			distinctKinds[entry.TargetRef.Kind] = true
		}
		if len(distinctKinds) > 1 {
			warnings = append(warnings, fmt.Sprintf(
				"%s %q: from[] has %d entries with different source kinds (%v) — "+
					"rules[] has no source discrimination; all rules will apply to ALL "+
					"inbound traffic regardless of source. Review and consolidate manually.",
				kind, name, len(policy.Spec.From), sortedKeys(distinctKinds)))
		} else {
			warnings = append(warnings, fmt.Sprintf(
				"%s %q: from[] had %d entries — merged into %d rules[] entries. "+
					"Verify that the intended per-source behaviour is preserved.",
				kind, name, len(policy.Spec.From), len(policy.Spec.From)))
		}
	}

	rules := make([]RuleEntry, 0, len(policy.Spec.From))
	for _, entry := range policy.Spec.From {
		rules = append(rules, RuleEntry{Default: entry.Default})
	}
	policy.Spec.Rules = rules
	policy.Spec.From = nil

	warnings = append(warnings, fmt.Sprintf(
		"%s %q: from[] migrated to rules[] (Kuma 2.10+). "+
			"Source-based targeting inside from[] is no longer supported — "+
			"policies in rules[] apply to all inbound traffic matching spec.targetRef.",
		kind, name))

	return warnings
}

// sortedKeys returns a sorted slice of the keys of a bool map (for stable warning text).
func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Simple insertion sort — only ever a handful of keys.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

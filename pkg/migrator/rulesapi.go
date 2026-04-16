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
func TransformFromToRules(raw []byte) ([][]byte, []string, error) {
	var policy KubePolicy
	if err := yaml.Unmarshal(raw, &policy); err != nil {
		return nil, nil, fmt.Errorf("unmarshal %s: %w", "policy", err)
	}

	warnings := applyFromToRules(&policy)

	b, err := yaml.Marshal(policy)
	if err != nil {
		return nil, warnings, fmt.Errorf("marshal policy: %w", err)
	}
	return [][]byte{b}, warnings, nil
}

// applyFromToRules converts policy.Spec.From → policy.Spec.Rules in-place and
// returns any warnings.  It is also called from transformScenarioSubset so that
// Scenario-B policies that still use from[] get the Rules API migration applied
// as a second pass.
func applyFromToRules(policy *KubePolicy) []string {
	if len(policy.Spec.From) == 0 {
		return nil
	}

	var warnings []string
	name := policy.Metadata.Name
	kind := policy.Kind

	// Warn when multiple from[] entries have different source kinds — flattening
	// them into rules[] loses the per-source configuration intent.
	if len(policy.Spec.From) > 1 {
		// Collect distinct source kinds to decide whether to warn.
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

	// Convert: each from[i].default → rules[i].default.
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

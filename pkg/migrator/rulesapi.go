package migrator

import (
	"fmt"

	"sigs.k8s.io/yaml"
)

// rulesAPIMigrationKinds is the set of Mesh* policy kinds where the from[] section
// was deprecated in Kuma 2.10 in favour of the rules[] API.
var rulesAPIMigrationKinds = map[string]bool{
	"MeshAccessLog":      true,
	"MeshCircuitBreaker": true,
	"MeshRateLimit":      true,
	"MeshTimeout":        true,
	// Upstream spells this kind MeshTLS (all caps). It was "MeshTls" here, which
	// never matched, so MeshTLS policies carrying the 2.9-era from[] were passed
	// through unconverted.
	"MeshTLS": true,
}

// TransformFromToRules migrates a policy that uses the deprecated from[] structure
// (Kuma ≤ 2.9) to the new rules[] API (Kuma 2.10+).
//
// Each from[i].default becomes rules[i].default.  The source targetRef inside
// each from[] entry is discarded because rules[] has no source discrimination;
// a warning is emitted when multiple entries with distinct source kinds are found.
//
// When the policy also has to[] alongside from[], Kuma 2.10+ forbids having both
// rules[] and to[] in the same spec. In this case the policy is split into two
// documents: the first carries rules[] (inbound) with the original name, the second
// carries to[] (outbound) with "-outbound" appended to the resource name.
//
// Uses a map-based round-trip to preserve ALL top-level fields (including
// Universal-format fields like "type", "name", "mesh", "creationTime", etc.).
func TransformFromToRules(raw []byte) ([][]byte, []string, error) {
	return TransformFromToRulesForTarget(raw, TargetV2)
}

// TransformFromToRulesForTarget is TransformFromToRules aware of the target major
// version. Under TargetV3 it also warns, per from[] entry, when the entry selected
// a subset of clients: 3.0 removes from[] from these policies, so that selector
// cannot be kept, and the rules[] entry now applies to every client.
func TransformFromToRulesForTarget(raw []byte, target TargetVersion) ([][]byte, []string, error) {
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

	var widening []string
	if target.IsV3() {
		if spec, ok := obj["spec"].(map[string]interface{}); ok {
			from, _ := spec["from"].([]interface{})
			widening = fromScopeWideningWarnings(kind, name, fromSourceKinds(from))
		}
	}

	warnings, modified := applyFromToRulesMap(obj, kind, name)
	warnings = append(warnings, widening...)
	if !modified {
		return [][]byte{raw}, warnings, nil
	}

	// After from[]→rules[] conversion: check if to[] is still present.
	// Kuma 2.10+ forbids rules[] and to[] coexisting in the same spec — split.
	spec, _ := obj["spec"].(map[string]interface{})
	if spec == nil {
		spec = obj
	}
	if _, hasTo := spec["to"]; hasTo {
		outboundObj, err := deepCopyObjViaYAML(obj)
		if err != nil {
			return nil, warnings, fmt.Errorf("deep copy for split: %w", err)
		}
		outboundSpec, _ := outboundObj["spec"].(map[string]interface{})
		if outboundSpec == nil {
			outboundSpec = outboundObj
		}
		delete(outboundSpec, "rules")
		outboundName := name + "-outbound"
		setObjName(outboundObj, outboundName)
		// The control plane derives kuma.io/display-name from the resource name, so the
		// copied value names the inbound half (and 3.0 rejects the mismatch on create).
		deleteObjLabel(outboundObj, displayNameLabel)

		delete(spec, "to")

		warnings = append(warnings, splitWarning(kind, name, outboundName))

		b1, err := yaml.Marshal(obj)
		if err != nil {
			return nil, warnings, fmt.Errorf("marshal rules policy: %w", err)
		}
		b2, err := yaml.Marshal(outboundObj)
		if err != nil {
			return nil, warnings, fmt.Errorf("marshal outbound policy: %w", err)
		}
		return [][]byte{b1, b2}, warnings, nil
	}

	b, err := yaml.Marshal(obj)
	if err != nil {
		return nil, warnings, fmt.Errorf("marshal policy: %w", err)
	}
	return [][]byte{b}, warnings, nil
}

// deepCopyObjViaYAML performs a deep copy of a map via YAML marshal/unmarshal.
func deepCopyObjViaYAML(obj map[string]interface{}) (map[string]interface{}, error) {
	b, err := yaml.Marshal(obj)
	if err != nil {
		return nil, err
	}
	var cp map[string]interface{}
	if err := yaml.Unmarshal(b, &cp); err != nil {
		return nil, err
	}
	return cp, nil
}

// setObjName sets the resource name on a Kubernetes (metadata.name) or
// Universal (top-level name) format object.
func setObjName(obj map[string]interface{}, name string) {
	if md, ok := obj["metadata"].(map[string]interface{}); ok {
		md["name"] = name
	} else if _, ok := obj["name"]; ok {
		obj["name"] = name
	}
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
func applyFromToRules(policy *KubePolicy, target TargetVersion) []string {
	if len(policy.Spec.From) == 0 {
		return nil
	}

	var warnings []string
	name := policy.Metadata.Name
	kind := policy.Kind

	var widening []string
	if target.IsV3() {
		kinds := make([]string, 0, len(policy.Spec.From))
		for _, entry := range policy.Spec.From {
			kinds = append(kinds, entry.TargetRef.Kind)
		}
		widening = fromScopeWideningWarnings(kind, name, kinds)
	}

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

	return append(warnings, widening...)
}

// fromSourceKinds returns the targetRef.kind of every from[] entry ("" when absent).
func fromSourceKinds(from []interface{}) []string {
	kinds := make([]string, 0, len(from))
	for _, entry := range from {
		k := ""
		if em, ok := entry.(map[string]interface{}); ok {
			if tr, ok := em["targetRef"].(map[string]interface{}); ok {
				k, _ = tr["kind"].(string)
			}
		}
		kinds = append(kinds, k)
	}
	return kinds
}

// rulesMatchKinds are the rules-API policies whose 3.0 Rule carries matches[]
// (spiffeID/sni). MeshCircuitBreaker and MeshTLS rules have no matches at all, so a
// per-client scope cannot be expressed on them in 3.0.
var rulesMatchKinds = map[string]bool{
	"MeshAccessLog": true,
	"MeshRateLimit": true,
	"MeshTimeout":   true,
}

// fromScopeWideningWarnings returns one warning per from[] entry whose targetRef is
// not kind: Mesh. Kuma 3.0 removes from[] from MeshTimeout, MeshCircuitBreaker,
// MeshAccessLog, MeshRateLimit and MeshTLS; the mechanical rules[] equivalent is
// documented only for kind: Mesh (all clients). Any narrower selector is lost.
func fromScopeWideningWarnings(kind, name string, sourceKinds []string) []string {
	var warnings []string
	for i, k := range sourceKinds {
		if k == "Mesh" {
			continue
		}
		narrow := fmt.Sprintf("%s rules[] has no matches[] field, so a per-client scope cannot be "+
			"expressed on 3.0", kind)
		if rulesMatchKinds[kind] {
			narrow = "to narrow it again, add rules[].matches[] with the clients' spiffeID or sni " +
				"(needs MeshIdentity)"
		}
		warnings = append(warnings, fmt.Sprintf(
			"%s %q: from[%d] selected clients with targetRef kind %q. Kuma 3.0 removes from[], so the "+
				"selector is dropped and the converted rules[] entry applies to every client of the "+
				"targeted proxies — a wider scope than before; %s.",
			kind, name, i, k, narrow))
	}
	return warnings
}

// deleteObjLabel removes a label from a Kubernetes (metadata.labels) or Universal
// (top-level labels) format object.
func deleteObjLabel(obj map[string]interface{}, key string) {
	labels := objLabels(obj)
	if labels == nil {
		return
	}
	delete(labels, key)
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

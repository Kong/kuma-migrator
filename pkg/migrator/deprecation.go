package migrator

import (
	"encoding/json"
	"fmt"

	"sigs.k8s.io/yaml"
)

// ScanForDeprecations inspects a single YAML document for deprecated fields
// that require manual action or in-place transformation.
//
// Returns warnings for each deprecation found. Some deprecations are
// automatically repaired in the returned (possibly modified) bytes; others
// are warn-only.
//
// Covered deprecations:
//   - MeshMetric spec.default.sidecar.regex → sidecar.profiles.exclude (v2.7)
//   - MeshHealthCheck spec.default.healthyPanicThreshold moved to MeshCircuitBreaker (v2.10)
//   - MeshTrust spec.origin deprecated → status.origin (v2.13)
//   - MeshTrafficPermission/MeshFaultInjection from[].targetRef.kind: MeshService (v2.7)
//   - MeshTrafficPermission action: ALLOW/DENY uppercase casing (Kong Mesh 2.1)
//   - MeshLoadBalancingStrategy hashPolicies[].type: SourceIP → Connection (v2.10)
//   - Dataplane transparentProxying.redirectPortInboundV6 removed (v2.9)
//   - Dataplane transparentProxying.reachableServices uses legacy kuma.io/service names (v2.10)
//   - Any Mesh* policy with spec.targetRef.kind: MeshSubset without service-identity tags (v2.10)
//
// Both Kubernetes format (kind/metadata) and Universal format (type/name) are supported.
func ScanForDeprecations(raw []byte) (out []byte, warnings []string) {
	var obj map[string]interface{}
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		return raw, nil
	}

	// Normalise: Universal format uses "type" instead of "kind".
	kind, _ := obj["kind"].(string)
	if kind == "" {
		kind, _ = obj["type"].(string)
	}
	name := extractNameFromObj(obj)

	switch kind {
	case "MeshMetric":
		out, warnings = fixMeshMetricSidecarRegex(obj, raw, name)
	case "MeshHealthCheck":
		out = raw
		warnings = warnHealthCheckPanicThreshold(obj, name)
	case "MeshTrust":
		out = raw
		warnings = warnMeshTrustOrigin(obj, name)
	case "MeshTrafficPermission":
		out = raw
		warnings = append(warnings, warnMeshServiceInFrom(obj, name, kind)...)
		warnings = append(warnings, warnMeshTrafficPermissionActionCasing(obj, name)...)
	case "MeshFaultInjection":
		out = raw
		warnings = warnMeshServiceInFrom(obj, name, kind)
	case "MeshLoadBalancingStrategy":
		out = raw
		warnings = warnSourceIPHashPolicy(obj, name)
	case "Dataplane":
		out = raw
		warnings = append(warnings, warnDataplaneRedirectPortInboundV6(obj, name)...)
		warnings = append(warnings, warnDataplaneReachableServices(obj, name)...)
	default:
		out = raw
		// Generic check: any Mesh* policy with MeshSubset targetRef that has no
		// service-identity tags is already-migrated style but uses a deprecated kind.
		if len(kind) > 4 && kind[:4] == "Mesh" {
			warnings = warnMeshSubsetWithoutServiceTag(obj, name, kind)
		}
	}
	return out, warnings
}

// ---- MeshMetric sidecar.regex → sidecar.profiles.exclude (v2.7) ---------------

func fixMeshMetricSidecarRegex(obj map[string]interface{}, raw []byte, name string) ([]byte, []string) {
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return raw, nil
	}
	def, ok := spec["default"].(map[string]interface{})
	if !ok {
		return raw, nil
	}
	sidecar, ok := def["sidecar"].(map[string]interface{})
	if !ok {
		return raw, nil
	}
	regex, ok := sidecar["regex"].(string)
	if !ok || regex == "" {
		return raw, nil
	}

	// Transform: move regex → profiles.exclude.
	delete(sidecar, "regex")
	sidecar["profiles"] = map[string]interface{}{
		"exclude": []interface{}{
			map[string]interface{}{
				"type":  "Regex",
				"match": regex,
			},
		},
	}
	def["sidecar"] = sidecar
	spec["default"] = def
	obj["spec"] = spec

	fixed, err := yaml.Marshal(obj)
	if err != nil {
		return raw, []string{fmt.Sprintf("MeshMetric %q: sidecar.regex detected but could not be auto-fixed: %v — migrate manually", name, err)}
	}
	return fixed, []string{fmt.Sprintf(
		"MeshMetric %q: sidecar.regex=%q migrated to sidecar.profiles.exclude (Kuma 2.7+). "+
			"Review profiles.appendProfiles and include/exclude rules to ensure the filter set matches your intent.",
		name, regex)}
}

// ---- MeshHealthCheck healthyPanicThreshold (v2.10) ----------------------------

func warnHealthCheckPanicThreshold(obj map[string]interface{}, name string) []string {
	if !hasNestedField(obj, "spec", "default", "healthyPanicThreshold") &&
		!hasNestedField(obj, "spec", "conf", "healthyPanicThreshold") {
		return nil
	}
	return []string{fmt.Sprintf(
		"MeshHealthCheck %q: healthyPanicThreshold has been moved to MeshCircuitBreaker.spec.default.outlierDetection (Kuma 2.10+) — "+
			"create a MeshCircuitBreaker policy targeting the same service with this value.",
		name)}
}

// ---- MeshTrust spec.origin (v2.13) -------------------------------------------

func warnMeshTrustOrigin(obj map[string]interface{}, name string) []string {
	if !hasNestedField(obj, "spec", "origin") {
		return nil
	}
	return []string{fmt.Sprintf(
		"MeshTrust %q: spec.origin is deprecated in Kuma 2.13 — it has moved to status.origin (read-only, managed by Kuma). "+
			"Remove spec.origin from this resource.",
		name)}
}

// ---- MeshTrafficPermission/MeshFaultInjection from[].targetRef.kind: MeshService (v2.7) --

// warnMeshServiceInFrom warns when from[].targetRef.kind is MeshService.
// MeshService in the from[] targetRef was deprecated in Kuma 2.7 in favour of
// Dataplane with labels (which is what ScenarioSubset produces). Resources that were
// already manually migrated but used MeshService in from[] should use Dataplane.
func warnMeshServiceInFrom(obj map[string]interface{}, name, kind string) []string {
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return nil
	}
	from, ok := spec["from"].([]interface{})
	if !ok {
		return nil
	}
	var warnings []string
	for _, entry := range from {
		e, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		tr, ok := e["targetRef"].(map[string]interface{})
		if !ok {
			continue
		}
		if tr["kind"] == "MeshService" {
			warnings = append(warnings, fmt.Sprintf(
				"%s %q: from[].targetRef.kind MeshService is deprecated in Kuma 2.7+ — "+
					"use kind: Dataplane with labels instead.",
				kind, name))
			break
		}
	}
	return warnings
}

// ---- MeshTrafficPermission action casing (Kong Mesh 2.1) ----------------------

var deprecatedActions = map[string]string{
	"ALLOW":                  "Allow",
	"DENY":                   "Deny",
	"ALLOW_WITH_SHADOW_DENY": "AllowWithShadowDeny",
}

func warnMeshTrafficPermissionActionCasing(obj map[string]interface{}, name string) []string {
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return nil
	}
	from, ok := spec["from"].([]interface{})
	if !ok {
		return nil
	}
	var warnings []string
	seen := map[string]bool{}
	for _, entry := range from {
		e, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		def, ok := e["default"].(map[string]interface{})
		if !ok {
			continue
		}
		action, _ := def["action"].(string)
		if newAction, deprecated := deprecatedActions[action]; deprecated && !seen[action] {
			seen[action] = true
			warnings = append(warnings, fmt.Sprintf(
				"MeshTrafficPermission %q: action value %q is deprecated — use %q instead (Kong Mesh 2.1+).",
				name, action, newAction))
		}
	}
	return warnings
}

// ---- MeshLoadBalancingStrategy SourceIP → Connection (v2.10) -----------------

func warnSourceIPHashPolicy(obj map[string]interface{}, name string) []string {
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return nil
	}
	to, ok := spec["to"].([]interface{})
	if !ok {
		return nil
	}
	for _, entry := range to {
		e, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		def, ok := e["default"].(map[string]interface{})
		if !ok {
			continue
		}
		lb, ok := def["loadBalancer"].(map[string]interface{})
		if !ok {
			continue
		}
		hashPolicies, ok := lb["hashPolicies"].([]interface{})
		if !ok {
			continue
		}
		for _, hp := range hashPolicies {
			h, ok := hp.(map[string]interface{})
			if !ok {
				continue
			}
			if h["type"] == "SourceIP" {
				return []string{fmt.Sprintf(
					"MeshLoadBalancingStrategy %q: hashPolicies[].type SourceIP is deprecated in Kuma 2.10+ — "+
						"use Connection instead.",
					name)}
			}
		}
	}
	return nil
}

// ---- Dataplane transparentProxying.redirectPortInboundV6 (v2.9) --------------

// warnDataplaneRedirectPortInboundV6 checks both Universal (networking at top level)
// and Kubernetes (networking under spec) layout.
func warnDataplaneRedirectPortInboundV6(obj map[string]interface{}, name string) []string {
	// Universal format: networking is a top-level field.
	// Kubernetes format: networking is under spec (uncommon — Dataplanes are auto-generated on K8s).
	if !hasNestedField(obj, "networking", "transparentProxying", "redirectPortInboundV6") &&
		!hasNestedField(obj, "spec", "networking", "transparentProxying", "redirectPortInboundV6") {
		return nil
	}
	return []string{fmt.Sprintf(
		"Dataplane %q: transparentProxying.redirectPortInboundV6 was removed in Kuma 2.9 — "+
			"remove this field from the resource.",
		name)}
}

// ---- Dataplane transparentProxying.reachableServices (v2.10) ----------------

// warnDataplaneReachableServices warns when a Dataplane uses reachableServices with
// legacy kuma.io/service names. In Kuma 2.10+ with spec.meshServices.mode: Exclusive,
// service names in reachableServices must be updated to use MeshService display names.
func warnDataplaneReachableServices(obj map[string]interface{}, name string) []string {
	// Universal format: networking at top level; Kubernetes: under spec.
	networking, _ := obj["networking"].(map[string]interface{})
	if networking == nil {
		spec, _ := obj["spec"].(map[string]interface{})
		networking, _ = spec["networking"].(map[string]interface{})
	}
	if networking == nil {
		return nil
	}
	tp, _ := networking["transparentProxying"].(map[string]interface{})
	if tp == nil {
		return nil
	}
	services, _ := tp["reachableServices"].([]interface{})
	if len(services) == 0 {
		return nil
	}
	return []string{fmt.Sprintf(
		"Dataplane %q: transparentProxying.reachableServices uses legacy kuma.io/service names (%v). "+
			"When spec.meshServices.mode is Exclusive (Kuma 2.10+), update these to the corresponding "+
			"MeshService display names (kuma.io/display-name label value).",
		name, services)}
}

// ---- MeshSubset in targetRef without service-identity tags (v2.10) -----------

func warnMeshSubsetWithoutServiceTag(obj map[string]interface{}, name, kind string) []string {
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return nil
	}
	targetRef, ok := spec["targetRef"].(map[string]interface{})
	if !ok {
		return nil
	}
	if targetRef["kind"] != "MeshSubset" {
		return nil
	}
	// Check tags for service-identity keys; if present, ScenarioSubset already handles it.
	tags, _ := targetRef["tags"].(map[string]interface{})
	for k := range tags {
		if k == "kuma.io/service" || k == "k8s.kuma.io/service-name" {
			return nil // ScenarioSubset will rewrite this
		}
	}
	return []string{fmt.Sprintf(
		"%s %q: spec.targetRef.kind MeshSubset is deprecated in Kuma 2.10+ — "+
			"use kind: Dataplane with labels instead.",
		kind, name)}
}

// ---- Helpers -----------------------------------------------------------------

// extractNameFromObj returns the resource name from a generic YAML object.
func extractNameFromObj(obj map[string]interface{}) string {
	if meta, ok := obj["metadata"].(map[string]interface{}); ok {
		if n, ok := meta["name"].(string); ok {
			return n
		}
	}
	if n, ok := obj["name"].(string); ok {
		return n
	}
	return "<unknown>"
}

// hasNestedField checks whether a sequence of keys leads to a non-nil value.
func hasNestedField(obj map[string]interface{}, keys ...string) bool {
	cur := obj
	for i, k := range keys {
		v, ok := cur[k]
		if !ok || v == nil {
			return false
		}
		if i == len(keys)-1 {
			// Check that the final value is not the JSON null.
			if b, err := json.Marshal(v); err == nil && string(b) == "null" {
				return false
			}
			return true
		}
		m, ok := v.(map[string]interface{})
		if !ok {
			return false
		}
		cur = m
	}
	return false
}

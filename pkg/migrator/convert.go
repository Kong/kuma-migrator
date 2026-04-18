package migrator

import (
	"fmt"
	"strings"
)

// legacyTypeMap maps old policy type names to their Mesh* equivalents.
// TrafficRoute is intentionally absent — it is ambiguous (HTTP vs TCP).
var legacyTypeMap = map[string]string{
	"Timeout":           "MeshTimeout",
	"CircuitBreaker":    "MeshCircuitBreaker",
	"Retry":             "MeshRetry",
	"TrafficPermission": "MeshTrafficPermission",
	"FaultInjection":    "MeshFaultInjection",
	"RateLimit":         "MeshRateLimit",
	"HealthCheck":       "MeshHealthCheck",
	"TrafficLog":        "MeshAccessLog",
	"TrafficTrace":      "MeshTrace",
	"ProxyTemplate":     "MeshProxyPatch",
}

// OldTypeToNew converts a legacy policy type name to its Mesh* equivalent.
// Returns an error for ambiguous types (TrafficRoute).
func OldTypeToNew(t string) (string, error) {
	if t == "TrafficRoute" {
		return "", fmt.Errorf("TrafficRoute is ambiguous: migrate manually to MeshHTTPRoute or MeshTCPRoute")
	}
	if newType, ok := legacyTypeMap[t]; ok {
		return newType, nil
	}
	return t, nil // already a Mesh* type or unrecognised — pass through
}

// ParseKumaServiceTag parses a kuma.io/service tag value.
//
// In Kubernetes mode the control-plane generates values with the pattern:
//
//	<service-name>_<namespace>_svc_<port>
//
// e.g. "backend_demo_svc_3001" → name="backend", namespace="demo"
//
// In Universal mode the value is a free-form string — returned as-is for name,
// with an empty namespace.
//
// A wildcard "*" returns ("", "").
func ParseKumaServiceTag(value string) (name, namespace string) {
	if value == "*" || value == "" {
		return "", ""
	}

	const svcMarker = "_svc_"
	idx := strings.LastIndex(value, svcMarker)
	if idx == -1 {
		// Universal mode: raw value is the service name.
		return value, ""
	}

	nameAndNS := value[:idx]
	lastUnderscore := strings.LastIndex(nameAndNS, "_")
	if lastUnderscore == -1 {
		return nameAndNS, ""
	}

	return nameAndNS[:lastUnderscore], nameAndNS[lastUnderscore+1:]
}

// ConvertTargetRef transforms a single TargetRef to use the correct kind for its position.
//
// topLevel=true  → spec.targetRef position: only Mesh and Dataplane are valid in Kuma 2.13.x.
// topLevel=false → to[]/from[] position: MeshService is valid.
//
// Namespace scoping rule: name+namespace may only be used when the policy namespace matches
// the target resource's namespace. When they differ (e.g. policy in kuma-system, target in
// demo), labels must be used instead to avoid ambiguity on Global CP.
//
// Returns the converted TargetRef and an optional warning string (non-empty when a
// Dataplane labels ref is emitted and the "app" label key is a best-guess).
func ConvertTargetRef(ref TargetRef, policyNamespace string, topLevel bool) (TargetRef, string) {
	if ref.Kind != "MeshSubset" && ref.Kind != "MeshServiceSubset" {
		return ref, "" // already correct (Mesh, MeshService, Dataplane, …)
	}

	// Partition the tags into service-identity, namespace hint, zone, and other.
	serviceValue := ""
	serviceTagKey := ""
	namespaceFromTag := ""
	zoneValue := ""
	otherTags := map[string]string{}

	for k, v := range ref.Tags {
		switch k {
		case "kuma.io/service":
			serviceValue = v
			serviceTagKey = k
		case "k8s.kuma.io/service-name":
			serviceValue = v
			serviceTagKey = k
		case "k8s.kuma.io/namespace":
			namespaceFromTag = v
		case "kuma.io/zone":
			zoneValue = v
		default:
			otherTags[k] = v
		}
	}

	// No service-identity tag.
	if serviceTagKey == "" {
		// At top-level (spec.targetRef): MeshSubset with only workload-selector tags is
		// deprecated in Kuma 2.10+. Migrate to Dataplane with labels.
		if topLevel {
			labels := map[string]string{}
			for k, v := range otherTags {
				labels[k] = v
			}
			if namespaceFromTag != "" {
				labels["k8s.kuma.io/namespace"] = namespaceFromTag
			}
			if zoneValue != "" {
				labels["kuma.io/zone"] = zoneValue
			}
			if len(labels) == 0 {
				return ref, "" // empty MeshSubset — nothing to convert
			}
			return TargetRef{Kind: "Dataplane", Labels: labels}, ""
		}
		// In to[]/from[] context: leave unchanged; the deprecation scanner will warn.
		return ref, ""
	}

	// Wildcard → Mesh (applies to all services in the mesh).
	if serviceValue == "*" {
		return TargetRef{Kind: "Mesh"}, ""
	}

	// Resolve service name and namespace.
	var name, ns string
	if serviceTagKey == "k8s.kuma.io/service-name" {
		name = serviceValue
		ns = namespaceFromTag
		if ns == "" {
			ns = policyNamespace
		}
	} else {
		// kuma.io/service: parse the encoded format.
		name, ns = ParseKumaServiceTag(serviceValue)
		if ns == "" {
			ns = namespaceFromTag
		}
		if ns == "" {
			ns = policyNamespace
		}
	}

	// In Kuma 2.13.x: spec.targetRef → Dataplane; to[]/from[] → MeshService.
	outputKind := "MeshService"
	if topLevel {
		outputKind = "Dataplane"
	}

	// When there are extra refinement tags or a zone scope, labels are required.
	if len(otherTags) > 0 || zoneValue != "" {
		labels, warn := buildLabels(name, ns, zoneValue, otherTags, topLevel)
		return TargetRef{Kind: outputKind, Labels: labels}, warn
	}

	// Namespace scoping: use labels when policy namespace differs from target namespace.
	// When policyNamespace is unknown (empty — Scenario A / Universal) keep name/namespace.
	if policyNamespace != "" && policyNamespace != ns {
		labels, warn := buildLabels(name, ns, "", nil, topLevel)
		return TargetRef{Kind: outputKind, Labels: labels}, warn
	}

	// Same namespace (or unknown): use name + optional namespace.
	result := TargetRef{Kind: outputKind}
	if name != "" {
		result.Name = strPtr(name)
	}
	if ns != "" {
		result.Namespace = strPtr(ns)
	}
	return result, ""
}

// buildLabels constructs the labels map for a labels-based targetRef.
//
// For Dataplane (topLevel=true): uses "app" as the display name key and returns a warning
// because "app" is a best-guess Kubernetes pod label that may not match the actual pods.
//
// For MeshService (topLevel=false): uses "kuma.io/display-name".
func buildLabels(name, ns, zone string, extra map[string]string, topLevel bool) (map[string]string, string) {
	labels := map[string]string{}
	var warn string

	if topLevel {
		if name != "" {
			labels["app"] = name
		}
		warn = fmt.Sprintf(
			"spec.targetRef Dataplane labels use 'app: %s' as a best-guess pod label — verify this matches your actual pod labels before applying",
			name,
		)
	} else {
		if name != "" {
			labels["kuma.io/display-name"] = name
		}
	}

	if ns != "" {
		labels["k8s.kuma.io/namespace"] = ns
	}
	if zone != "" {
		labels["kuma.io/zone"] = zone
	}
	for k, v := range extra {
		labels[k] = v
	}
	return labels, warn
}

// ConvertSelectorToTargetRef converts a legacy OldSelector (from sources[]/destinations[])
// into a new TargetRef. topLevel and policyNamespace follow the same semantics as
// ConvertTargetRef. Returns the TargetRef and an optional warning string.
func ConvertSelectorToTargetRef(sel OldSelector, policyNamespace string, topLevel bool) (TargetRef, string) {
	tags := sel.Match
	if len(tags) == 0 {
		return TargetRef{Kind: "Mesh"}, ""
	}

	serviceValue, hasService := tags["kuma.io/service"]
	if hasService && serviceValue == "*" {
		return TargetRef{Kind: "Mesh"}, ""
	}

	if !hasService {
		// Only non-service tags — MeshSubset workload selector, no service identity.
		return TargetRef{Kind: "MeshSubset", Tags: tags}, ""
	}

	// Delegate via a synthetic MeshSubset so the full conversion rules apply.
	synthetic := TargetRef{Kind: "MeshSubset", Tags: tags}
	return ConvertTargetRef(synthetic, policyNamespace, topLevel)
}

func strPtr(s string) *string { return &s }

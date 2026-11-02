package migrator

import (
	"fmt"
	"sort"
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
// Namespace scoping rule (to[]/from[] MeshService only): name+namespace may only be used
// when the policy namespace matches the target resource's namespace. When they differ (e.g.
// policy in kuma-system, target in demo), labels must be used instead to avoid ambiguity on
// Global CP. A top-level Dataplane always uses labels: a Dataplane name is the proxy's own
// name (the pod name on Kubernetes), never the service name decoded from a tag.
//
// Returns the converted TargetRef and an optional warning string (non-empty when a
// Dataplane labels ref is emitted and the "app" label key is a best-guess).
func ConvertTargetRef(ref TargetRef, policyNamespace string, topLevel bool) (TargetRef, string) {
	return convertTargetRef(ref, policyNamespace, topLevel, false)
}

// ConvertTargetRefForTarget is ConvertTargetRef aware of the target major version.
//
// Under TargetV2 it behaves exactly like ConvertTargetRef and never errors.
//
// Under TargetV3 two 3.0 rules apply to every ref it generates:
//   - Real resources (Dataplane, MeshService, …) are selected by labels only
//     (UPGRADE.md "Real-resource policy selection now uses labels only"), so a
//     generated ref never carries name/namespace. MeshService refs use
//     kuma.io/display-name + k8s.kuma.io/namespace; top-level Dataplane refs use the
//     best-guess "app" label and return the usual verification warning.
//   - MeshSubset is gone everywhere. A tag-only MeshSubset becomes Dataplane+labels
//     at the top level; in to[]/from[] it has no equivalent (3.0 accepts no tag
//     selector there), so an error is returned.
//
// Refs that need no conversion (Mesh, an existing MeshService, …) are returned
// unchanged; ScanForDeprecations rewrites leftover name/namespace on them.
func ConvertTargetRefForTarget(ref TargetRef, policyNamespace string, topLevel bool, target TargetVersion) (TargetRef, string, error) {
	if !target.IsV3() {
		out, warn := convertTargetRef(ref, policyNamespace, topLevel, false)
		return out, warn, nil
	}
	out, warn := convertTargetRef(ref, policyNamespace, topLevel, true)
	if out.Kind == "MeshSubset" || out.Kind == "MeshServiceSubset" {
		if topLevel && len(out.Tags) == 0 {
			// A MeshSubset with no tags selects every proxy in the mesh.
			return TargetRef{Kind: "Mesh"}, warn, nil
		}
		return out, warn, fmt.Errorf(
			"targetRef kind %s with tags %v has no Kuma 3.0 equivalent in to[]/from[]: 3.0 accepts no "+
				"tag selector there, only Mesh or a real resource (MeshService, MeshExternalService, "+
				"MeshMultiZoneService, MeshHTTPRoute) selected by labels. Pick the real destination by hand, "+
				"or re-run with --to-latest v2 to keep the MeshSubset", out.Kind, out.Tags)
	}
	return out, warn, nil
}

func convertTargetRef(ref TargetRef, policyNamespace string, topLevel bool, labelsOnly bool) (TargetRef, string) {
	if ref.Kind != "MeshSubset" && ref.Kind != "MeshServiceSubset" {
		// Normalise a MeshService ref that still carries the old Kuma-generated internal
		// name format (e.g. "echo_demo_svc_8000" = service_namespace_svc_port).
		// This happens when a policy was partially migrated: the kind was updated from
		// MeshSubset to MeshService but the CP-generated CRD name was left unchanged.
		// At top-level (spec.targetRef), MeshService is invalid in Kuma 2.13.x — convert
		// to Dataplane. In to[]/from[] position, keep MeshService and decode the name.
		if ref.Kind == "MeshService" && ref.Name != nil {
			if name, ns := ParseKumaServiceTag(*ref.Name); ns != "" {
				// Encoded namespace found — this is the old internal format.
				outputKind := "MeshService"
				if topLevel {
					outputKind = "Dataplane"
				}
				if ns == "" {
					ns = policyNamespace
				}
				// A top-level Dataplane is always selected by labels: a Dataplane's
				// name is the pod/proxy name, never the service name decoded here.
				if labelsOnly || topLevel || (policyNamespace != "" && policyNamespace != ns) {
					labels, warn := buildLabels(name, ns, "", nil, topLevel)
					return TargetRef{Kind: outputKind, Labels: labels, SectionName: ref.SectionName}, warn
				}
				result := TargetRef{Kind: outputKind, Name: strPtr(name), SectionName: ref.SectionName}
				if ns != "" {
					result.Namespace = strPtr(ns)
				}
				return result, ""
			}
		}
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
			labels, dropWarn := selectorLabelsFromTags(otherTags)
			if namespaceFromTag != "" {
				labels["k8s.kuma.io/namespace"] = namespaceFromTag
			}
			if zoneValue != "" {
				labels["kuma.io/zone"] = zoneValue
			}
			if len(labels) == 0 {
				return ref, dropWarn // empty MeshSubset — nothing to convert
			}
			return TargetRef{Kind: "Dataplane", Labels: labels}, dropWarn
		}
		// In to[]/from[] context there is no service to point at, so the ref is left
		// as MeshSubset. 2.14's shared TargetRef schema still lists the kind; under
		// --to-latest v3 ConvertTargetRefForTarget turns this into an error, and
		// ScanForDeprecations flags any MeshSubset left in spec.to[] (warnV3ToTargetRefKinds).
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
	// Kuma 3.0 resolves real resources by labels only, so labelsOnly always takes this path.
	// A top-level Dataplane always takes it too: Dataplane name/namespace selects one
	// proxy by its own name (the pod name on Kubernetes), and the service name decoded
	// from the tag is never that, so the name form would match nothing.
	if labelsOnly || topLevel || (policyNamespace != "" && policyNamespace != ns) {
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
	labels, warn := selectorLabelsFromTags(extra)

	if topLevel {
		if name != "" {
			labels["app"] = name
		}
		guess := fmt.Sprintf(
			"spec.targetRef Dataplane labels use 'app: %s' as a best-guess pod label — verify this matches your actual pod labels before applying",
			name,
		)
		if ns == "" {
			guess += " (on Universal a Dataplane carries only the labels in its own labels field: add 'app: " +
				name + "' there or select a label the Dataplanes already have)"
		}
		warn = joinWarnings(guess, warn)
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
	return labels, warn
}

// selectorLabelTags are the Kuma-reserved tag keys that are also labels on the
// resources a labels selector matches (Dataplane, MeshService). On Kubernetes a
// Dataplane's labels are its pod's labels plus the ones the control plane sets:
// kuma.io/mesh, kuma.io/zone, kuma.io/origin, kuma.io/env and k8s.kuma.io/namespace
// (kuma pkg/core/resources/labels/registry.go; pod_converter.go merges the pod
// labels). The other reserved keys that appear as inbound tags — kuma.io/protocol,
// kuma.io/instance, k8s.kuma.io/service-port, … — are never labels, so a selector
// carrying them matches nothing. kuma.io/display-name is a label, but on a Dataplane
// it holds the proxy's own name, not the service's, so it is never taken from a tag.
var selectorLabelTags = map[string]bool{
	"kuma.io/zone":          true,
	"kuma.io/env":           true,
	"kuma.io/mesh":          true,
	"kuma.io/origin":        true,
	"k8s.kuma.io/namespace": true,
}

// isReservedTagKey reports whether key is in a Kuma-reserved domain (kuma.io or a
// subdomain such as k8s.kuma.io).
func isReservedTagKey(key string) bool {
	i := strings.Index(key, "/")
	if i < 0 {
		return false
	}
	domain := key[:i]
	return domain == "kuma.io" || strings.HasSuffix(domain, ".kuma.io")
}

// selectorLabelsFromTags copies refinement tags into a labels selector, dropping
// the Kuma-reserved tags that are not labels on the selected resources. Returns a
// warning naming the dropped tags, empty when none was dropped.
func selectorLabelsFromTags(tags map[string]string) (map[string]string, string) {
	labels := map[string]string{}
	var dropped []string
	for k, v := range tags {
		if isReservedTagKey(k) && !selectorLabelTags[k] {
			dropped = append(dropped, k+"="+v)
			continue
		}
		labels[k] = v
	}
	if len(dropped) == 0 {
		return labels, ""
	}
	sort.Strings(dropped)
	return labels, fmt.Sprintf(
		"dropped tag(s) %s from the generated labels selector — they are Kuma inbound tags, not labels on "+
			"Dataplanes or MeshServices, so a selector carrying them would match nothing. Check that the "+
			"remaining labels still select the intended proxies or services",
		strings.Join(dropped, ", "))
}

// joinWarnings joins the non-empty warnings into one message.
func joinWarnings(ws ...string) string {
	var out []string
	for _, w := range ws {
		if w != "" {
			out = append(out, w)
		}
	}
	return strings.Join(out, "; ")
}

// ConvertSelectorToTargetRef converts a legacy OldSelector (from sources[]/destinations[])
// into a new TargetRef. topLevel and policyNamespace follow the same semantics as
// ConvertTargetRef. Returns the TargetRef and an optional warning string.
func ConvertSelectorToTargetRef(sel OldSelector, policyNamespace string, topLevel bool) (TargetRef, string) {
	ref, warn, _ := ConvertSelectorToTargetRefForTarget(sel, policyNamespace, topLevel, TargetV2)
	return ref, warn
}

// ConvertSelectorToTargetRefForTarget is ConvertSelectorToTargetRef aware of the
// target major version. Under TargetV3 a selector with no kuma.io/service tag is
// routed through ConvertTargetRefForTarget instead of being emitted as MeshSubset:
// top-level it becomes Dataplane+labels, in to[]/from[] it is an error (see
// ConvertTargetRefForTarget).
func ConvertSelectorToTargetRefForTarget(sel OldSelector, policyNamespace string, topLevel bool, target TargetVersion) (TargetRef, string, error) {
	tags := sel.Match
	if len(tags) == 0 {
		return TargetRef{Kind: "Mesh"}, "", nil
	}

	serviceValue, hasService := tags["kuma.io/service"]
	if hasService && serviceValue == "*" {
		return TargetRef{Kind: "Mesh"}, "", nil
	}

	if !hasService && !target.IsV3() {
		// Only non-service tags — MeshSubset workload selector, no service identity.
		return TargetRef{Kind: "MeshSubset", Tags: tags}, "", nil
	}

	// Delegate via a synthetic MeshSubset so the full conversion rules apply.
	synthetic := TargetRef{Kind: "MeshSubset", Tags: tags}
	return ConvertTargetRefForTarget(synthetic, policyNamespace, topLevel, target)
}

// meshSubsetFromKinds are the policies whose 2.x from[] must select clients with
// MeshSubset tags rather than MeshService. UPGRADE.md "Deprecation of
// 'from[].targetRef.kind: MeshService'" (2.7): only these two kinds ever accepted
// MeshService in from[], and the documented replacement is MeshSubset with the
// kuma.io/service tag. The tag value is the Dataplane's own inbound tag (the
// encoded <name>_<namespace>_svc_<port> on Kubernetes), so it is kept verbatim.
var meshSubsetFromKinds = map[string]bool{
	"MeshTrafficPermission": true,
	"MeshFaultInjection":    true,
}

// fromSubsetRefV2 converts a legacy source selector into the from[] targetRef a
// 2.x MeshTrafficPermission/MeshFaultInjection documents as current: kind: Mesh
// for "every client", otherwise MeshSubset with the selector's tags. A
// kuma.io/service: '*' entry is dropped from the tags (it means "any service").
func fromSubsetRefV2(sel OldSelector) TargetRef {
	tags := map[string]string{}
	for k, v := range sel.Match {
		if k == "kuma.io/service" && v == "*" {
			continue
		}
		tags[k] = v
	}
	if len(tags) == 0 {
		return TargetRef{Kind: "Mesh"}
	}
	return TargetRef{Kind: "MeshSubset", Tags: tags}
}

// fromRefForTarget converts a from[] targetRef of an existing policy (Subset
// scenario). Under v2, MeshTrafficPermission and MeshFaultInjection keep MeshSubset
// (the 2.7+ recommended form) instead of being rewritten to the deprecated
// MeshService; a partially migrated MeshService whose name is the old
// <name>_<namespace>_svc_<port> tag value is turned back into that MeshSubset.
// Every other case is ConvertTargetRefForTarget with topLevel=false.
func fromRefForTarget(ref TargetRef, policyKind, policyNamespace string, target TargetVersion) (TargetRef, string, error) {
	if !target.IsV3() && meshSubsetFromKinds[policyKind] {
		switch ref.Kind {
		case "MeshSubset":
			return ref, "", nil
		case "MeshService":
			if ref.Name != nil {
				if _, ns := ParseKumaServiceTag(*ref.Name); ns != "" {
					return fromSubsetRefV2(OldSelector{Match: map[string]string{"kuma.io/service": *ref.Name}}), "", nil
				}
			}
		}
	}
	return ConvertTargetRefForTarget(ref, policyNamespace, false, target)
}

func strPtr(s string) *string { return &s }

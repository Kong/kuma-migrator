package migrator

import (
	"encoding/json"
	"fmt"
	"strings"

	"sigs.k8s.io/yaml"
)

const gatewayAPIVersionAlpha2 = "gateway.networking.k8s.io/v1alpha2"

// ---- Old MeshHTTPRoute / MeshTCPRoute structs (input) -----------------------

type oldMeshHTTPRoute struct {
	APIVersion string           `json:"apiVersion"`
	Kind       string           `json:"kind"`
	Metadata   KubeMetadata     `json:"metadata"`
	Spec       oldHTTPRouteSpec `json:"spec"`
}

type oldHTTPRouteSpec struct {
	TargetRef oldRouteTargetRef `json:"targetRef"`
	To        []oldHTTPRouteTo  `json:"to"`
}

type oldHTTPRouteTo struct {
	TargetRef oldRouteTargetRef  `json:"targetRef"`
	Rules     []oldHTTPRouteRule `json:"rules"`
}

type oldHTTPRouteRule struct {
	Matches []json.RawMessage `json:"matches,omitempty"`
	Default oldHTTPRuleDefault `json:"default"`
}

type oldHTTPRuleDefault struct {
	BackendRefs []json.RawMessage `json:"backendRefs,omitempty"`
	Filters     []json.RawMessage `json:"filters,omitempty"`
}

// oldMeshTCPRoute reuses the same structure — only backendRefs differ.
type oldMeshTCPRoute struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Metadata   KubeMetadata    `json:"metadata"`
	Spec       oldTCPRouteSpec `json:"spec"`
}

type oldTCPRouteSpec struct {
	TargetRef oldRouteTargetRef `json:"targetRef"`
	To        []oldTCPRouteTo   `json:"to"`
}

type oldTCPRouteTo struct {
	TargetRef oldRouteTargetRef `json:"targetRef"`
	Rules     []oldTCPRouteRule `json:"rules"`
}

type oldTCPRouteRule struct {
	Default oldTCPRuleDefault `json:"default"`
}

type oldTCPRuleDefault struct {
	BackendRefs []json.RawMessage `json:"backendRefs,omitempty"`
}

// oldRouteTargetRef is used for both top-level targetRef and to[].targetRef.
type oldRouteTargetRef struct {
	Kind        string            `json:"kind,omitempty"`
	Name        string            `json:"name,omitempty"`
	Namespace   string            `json:"namespace,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	SectionName string            `json:"sectionName,omitempty"`
}

// ---- MeshHTTPRoute → HTTPRoute ----------------------------------------------

// TransformMeshHTTPRoute converts a MeshHTTPRoute into a Gateway API HTTPRoute.
//
// The MeshHTTPRoute to[] structure is flattened: rules from all to[] entries
// become rules in a single HTTPRoute. backendRefs within each rule are converted
// from MeshService/MeshServiceSubset references to Kubernetes Service references.
func TransformMeshHTTPRoute(raw []byte) ([][]byte, []string, error) {
	var route oldMeshHTTPRoute
	if err := yaml.Unmarshal(raw, &route); err != nil {
		return nil, nil, fmt.Errorf("unmarshal MeshHTTPRoute: %w", err)
	}

	name := route.Metadata.Name
	namespace := route.Metadata.Namespace
	var warnings []string

	parentRef, w := buildRouteParentRef(route.Spec.TargetRef, name)
	warnings = append(warnings, w...)

	// Flatten to[].rules[] into a single rules[] array.
	var rules []interface{}
	for i, toEntry := range route.Spec.To {
		for j, rule := range toEntry.Rules {
			httpRule := map[string]interface{}{}

			// Matches (already use correct field names — just check for unsupported types).
			if len(rule.Matches) > 0 {
				convertedMatches, matchWarns := convertHTTPMatches(rule.Matches, name, i, j)
				warnings = append(warnings, matchWarns...)
				if len(convertedMatches) > 0 {
					httpRule["matches"] = convertedMatches
				}
			}

			// Filters (type names already match HTTPRoute — only patch requestMirror.percentage).
			if len(rule.Default.Filters) > 0 {
				convertedFilters, filterWarns := convertHTTPFilters(rule.Default.Filters, name, i, j)
				warnings = append(warnings, filterWarns...)
				if len(convertedFilters) > 0 {
					httpRule["filters"] = convertedFilters
				}
			}

			// BackendRefs: prefer explicit backendRefs from the rule; fall back to to[].targetRef.
			var backendRefs []interface{}
			if len(rule.Default.BackendRefs) > 0 {
				converted, bwarn := convertHTTPBackendRefs(rule.Default.BackendRefs, name, i, j)
				warnings = append(warnings, bwarn...)
				backendRefs = converted
			} else if toEntry.TargetRef.Name != "" {
				backendRefs = []interface{}{backendRefFromTargetRef(toEntry.TargetRef)}
				warnings = append(warnings, fmt.Sprintf(
					"HTTPRoute %q to[%d].rules[%d]: no explicit backendRefs — derived from to[].targetRef (port unknown; set backendRefs[].port manually)", name, i, j))
			}
			if len(backendRefs) > 0 {
				httpRule["backendRefs"] = backendRefs
			}

			rules = append(rules, httpRule)
		}
	}

	meta := map[string]interface{}{
		"name":      name,
		"namespace": namespace,
	}
	if ann := meshAnnotationFromLabels(route.Metadata.Labels); len(ann) > 0 {
		meta["annotations"] = ann
	}

	spec := map[string]interface{}{
		"parentRefs": []interface{}{parentRef},
	}
	if len(rules) > 0 {
		spec["rules"] = rules
	}

	output := map[string]interface{}{
		"apiVersion": gatewayAPIVersion,
		"kind":       "HTTPRoute",
		"metadata":   meta,
		"spec":       spec,
	}

	b, err := yaml.Marshal(output)
	if err != nil {
		return nil, warnings, fmt.Errorf("marshal HTTPRoute: %w", err)
	}
	return [][]byte{b}, warnings, nil
}

// ---- MeshTCPRoute → TCPRoute ------------------------------------------------

// TransformMeshTCPRoute converts a MeshTCPRoute into a Gateway API TCPRoute.
func TransformMeshTCPRoute(raw []byte) ([][]byte, []string, error) {
	var route oldMeshTCPRoute
	if err := yaml.Unmarshal(raw, &route); err != nil {
		return nil, nil, fmt.Errorf("unmarshal MeshTCPRoute: %w", err)
	}

	name := route.Metadata.Name
	namespace := route.Metadata.Namespace
	var warnings []string

	parentRef, w := buildRouteParentRef(route.Spec.TargetRef, name)
	warnings = append(warnings, w...)

	// Flatten to[].rules[] into a single rules[] array.
	var rules []interface{}
	for i, toEntry := range route.Spec.To {
		for j, rule := range toEntry.Rules {
			tcpRule := map[string]interface{}{}

			var backendRefs []interface{}
			if len(rule.Default.BackendRefs) > 0 {
				converted, bwarn := convertHTTPBackendRefs(rule.Default.BackendRefs, name, i, j)
				warnings = append(warnings, bwarn...)
				backendRefs = converted
			} else if toEntry.TargetRef.Name != "" {
				backendRefs = []interface{}{backendRefFromTargetRef(toEntry.TargetRef)}
				warnings = append(warnings, fmt.Sprintf(
					"TCPRoute %q to[%d].rules[%d]: no explicit backendRefs — derived from to[].targetRef (port unknown; set backendRefs[].port manually)", name, i, j))
			}
			if len(backendRefs) > 0 {
				tcpRule["backendRefs"] = backendRefs
			}

			rules = append(rules, tcpRule)
		}
	}

	meta := map[string]interface{}{
		"name":      name,
		"namespace": namespace,
	}
	if ann := meshAnnotationFromLabels(route.Metadata.Labels); len(ann) > 0 {
		meta["annotations"] = ann
	}

	spec := map[string]interface{}{
		"parentRefs": []interface{}{parentRef},
	}
	if len(rules) > 0 {
		spec["rules"] = rules
	}

	output := map[string]interface{}{
		"apiVersion": gatewayAPIVersionAlpha2,
		"kind":       "TCPRoute",
		"metadata":   meta,
		"spec":       spec,
	}

	b, err := yaml.Marshal(output)
	if err != nil {
		return nil, warnings, fmt.Errorf("marshal TCPRoute: %w", err)
	}
	return [][]byte{b}, warnings, nil
}

// ---- Conversion helpers ------------------------------------------------------

// buildRouteParentRef converts spec.targetRef into a Gateway API parentRef.
func buildRouteParentRef(ref oldRouteTargetRef, routeName string) (map[string]interface{}, []string) {
	var warnings []string
	parentRef := map[string]interface{}{}

	switch ref.Kind {
	case "MeshGateway":
		parentRef["group"] = "gateway.networking.k8s.io"
		parentRef["kind"] = "Gateway"
		if ref.Name != "" {
			parentRef["name"] = ref.Name
		}
		if ref.Namespace != "" {
			parentRef["namespace"] = ref.Namespace
		}
		// Attempt to derive sectionName from listener tags.
		if len(ref.Tags) > 0 {
			if sn := sectionNameFromTags(ref.Tags); sn != "" {
				parentRef["sectionName"] = sn
			} else {
				warnings = append(warnings, fmt.Sprintf(
					"Route %q: spec.targetRef.tags %v could not be mapped to a sectionName — "+
						"set parentRefs[0].sectionName to the listener name of the migrated Gateway (e.g. 'http-8080')", routeName, ref.Tags))
			}
		}
	case "MeshService":
		// GAMMA use case: route attaches to a Service directly.
		parentRef["group"] = ""
		parentRef["kind"] = "Service"
		if ref.Name != "" {
			parentRef["name"] = ref.Name
		}
		if ref.Namespace != "" {
			parentRef["namespace"] = ref.Namespace
		}
		warnings = append(warnings, fmt.Sprintf(
			"Route %q: spec.targetRef.kind=MeshService maps to parentRef.kind=Service (GAMMA). "+
				"Ensure your Kuma version supports Gateway API GAMMA service mesh routing.", routeName))
	case "Mesh", "":
		parentRef["group"] = "gateway.networking.k8s.io"
		parentRef["kind"] = "Gateway"
		parentRef["name"] = "(all-gateways)"
		warnings = append(warnings, fmt.Sprintf(
			"Route %q: spec.targetRef.kind=Mesh has no single Gateway equivalent — "+
				"set parentRefs[0].name to the specific Gateway this route should attach to", routeName))
	default:
		parentRef["group"] = "gateway.networking.k8s.io"
		parentRef["kind"] = "Gateway"
		if ref.Name != "" {
			parentRef["name"] = ref.Name
		}
		warnings = append(warnings, fmt.Sprintf(
			"Route %q: spec.targetRef.kind=%q has no direct Gateway API parentRef mapping — review manually", routeName, ref.Kind))
	}
	return parentRef, warnings
}

// sectionNameFromTags attempts to derive a Gateway listener sectionName from
// MeshGateway listener tags. The conventional tag key is "port" with a value
// like "http-8080" matching the listener name generated by gatewayListenerName.
func sectionNameFromTags(tags map[string]string) string {
	if v, ok := tags["port"]; ok {
		return v
	}
	// If there's exactly one tag and its value looks like a listener name, use it.
	if len(tags) == 1 {
		for _, v := range tags {
			if strings.Contains(v, "-") {
				return v
			}
		}
	}
	return ""
}

// convertHTTPMatches converts MeshHTTPRoute matches, warning about unsupported
// header match types (Present/Absent) that have no HTTPRoute equivalent.
func convertHTTPMatches(rawMatches []json.RawMessage, routeName string, toIdx, ruleIdx int) ([]interface{}, []string) {
	var warnings []string
	var result []interface{}

	for _, rawMatch := range rawMatches {
		var match map[string]interface{}
		if err := json.Unmarshal(rawMatch, &match); err != nil {
			continue
		}

		// Check headers for unsupported match types.
		if headers, ok := match["headers"].([]interface{}); ok {
			var filteredHeaders []interface{}
			for _, h := range headers {
				hdr, ok := h.(map[string]interface{})
				if !ok {
					filteredHeaders = append(filteredHeaders, h)
					continue
				}
				hType, _ := hdr["type"].(string)
				if hType == "Present" || hType == "Absent" {
					warnings = append(warnings, fmt.Sprintf(
						"HTTPRoute %q to[%d].rules[%d]: header match type %q is not supported in Gateway API HTTPRoute — "+
							"this match condition has been removed; implement it via an ExtensionRef filter or custom policy", routeName, toIdx, ruleIdx, hType))
					// Drop this header condition.
					continue
				}
				filteredHeaders = append(filteredHeaders, h)
			}
			if len(filteredHeaders) > 0 {
				match["headers"] = filteredHeaders
			} else {
				delete(match, "headers")
			}
		}

		result = append(result, match)
	}
	return result, warnings
}

// convertHTTPFilters converts MeshHTTPRoute filters to HTTPRoute filters.
// Filter type names already match HTTPRoute — only requestMirror.percentage → percent needs patching.
func convertHTTPFilters(rawFilters []json.RawMessage, routeName string, toIdx, ruleIdx int) ([]interface{}, []string) {
	var warnings []string
	var result []interface{}

	for _, rawFilter := range rawFilters {
		var filter map[string]interface{}
		if err := json.Unmarshal(rawFilter, &filter); err != nil {
			continue
		}

		// Patch requestMirror.percentage → percent.
		if mirror, ok := filter["requestMirror"].(map[string]interface{}); ok {
			if pct, exists := mirror["percentage"]; exists {
				mirror["percent"] = pct
				delete(mirror, "percentage")
				filter["requestMirror"] = mirror
			}
			// Convert backendRef kind inside mirror.
			if bref, ok := mirror["backendRef"].(map[string]interface{}); ok {
				mirror["backendRef"] = convertSingleBackendRef(bref, routeName, toIdx, ruleIdx, &warnings)
				filter["requestMirror"] = mirror
			}
		}

		result = append(result, filter)
	}
	return result, warnings
}

// convertHTTPBackendRefs converts an array of raw MeshHTTPRoute backendRefs to
// Gateway API Service backendRefs.
func convertHTTPBackendRefs(rawRefs []json.RawMessage, routeName string, toIdx, ruleIdx int) ([]interface{}, []string) {
	var warnings []string
	var result []interface{}
	for _, rawRef := range rawRefs {
		var ref map[string]interface{}
		if err := json.Unmarshal(rawRef, &ref); err != nil {
			continue
		}
		result = append(result, convertSingleBackendRef(ref, routeName, toIdx, ruleIdx, &warnings))
	}
	return result, warnings
}

// convertSingleBackendRef maps a MeshService/MeshServiceSubset/MeshExternalService
// backendRef to a K8s Service backendRef.
func convertSingleBackendRef(ref map[string]interface{}, routeName string, toIdx, ruleIdx int, warnings *[]string) map[string]interface{} {
	result := map[string]interface{}{}
	for k, v := range ref {
		switch k {
		case "kind":
			switch v {
			case "MeshService":
				result["kind"] = "Service"
				result["group"] = ""
			case "MeshServiceSubset":
				result["kind"] = "Service"
				result["group"] = ""
				*warnings = append(*warnings, fmt.Sprintf(
					"Route %q to[%d].rules[%d]: backendRef kind=MeshServiceSubset converted to Service — "+
						"subset tags are not supported in Gateway API backendRefs; consider traffic-splitting via weighted Services", routeName, toIdx, ruleIdx))
			case "MeshExternalService":
				result["kind"] = "Service"
				result["group"] = ""
				*warnings = append(*warnings, fmt.Sprintf(
					"Route %q to[%d].rules[%d]: backendRef kind=MeshExternalService has no direct Gateway API equivalent — "+
						"Kuma may support MeshExternalService in backendRefs; verify with your Kuma version and update manually if needed", routeName, toIdx, ruleIdx))
			default:
				result["kind"] = v
			}
		case "tags":
			// Tags are not applicable to Gateway API Service backendRefs — drop them.
			*warnings = append(*warnings, fmt.Sprintf(
				"Route %q to[%d].rules[%d]: backendRef tags %v removed — Gateway API backendRefs target K8s Services, not tagged subsets", routeName, toIdx, ruleIdx, v))
		default:
			result[k] = v
		}
	}
	return result
}

// backendRefFromTargetRef creates a minimal backendRef from a to[].targetRef
// when no explicit backendRefs were specified in the rule.
func backendRefFromTargetRef(ref oldRouteTargetRef) map[string]interface{} {
	result := map[string]interface{}{
		"kind":  "Service",
		"group": "",
		"name":  ref.Name,
	}
	if ref.Namespace != "" {
		result["namespace"] = ref.Namespace
	}
	return result
}

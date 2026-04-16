package migrator

import (
	"fmt"
	"strings"

	"sigs.k8s.io/yaml"
)

// ---- Input structs for MeshGatewayRoute (legacy) ----------------------------

type meshGatewayRoute struct {
	APIVersion string              `json:"apiVersion"`
	Kind       string              `json:"kind"`
	Mesh       string              `json:"mesh,omitempty"` // Universal-style top-level mesh field
	Metadata   gwRouteMetadata     `json:"metadata"`
	Spec       gwRouteSpec         `json:"spec"`
}

type gwRouteMetadata struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

type gwRouteSpec struct {
	Selectors []gwSelector `json:"selectors"`
	Conf      gwRouteConf  `json:"conf"`
}

type gwSelector struct {
	Match map[string]string `json:"match"`
}

type gwRouteConf struct {
	HTTP *gwHTTPConf `json:"http,omitempty"`
	TCP  *gwTCPConf  `json:"tcp,omitempty"`
}

type gwHTTPConf struct {
	Hostnames []string     `json:"hostnames,omitempty"`
	Rules     []gwHTTPRule `json:"rules"`
}

type gwHTTPRule struct {
	Matches  []gwHTTPMatch  `json:"matches"`
	Filters  []gwFilter     `json:"filters,omitempty"`
	Backends []gwBackend    `json:"backends,omitempty"`
}

type gwHTTPMatch struct {
	Path            *gwPathMatch    `json:"path,omitempty"`
	Method          string          `json:"method,omitempty"`
	Headers         []gwHeaderMatch `json:"headers,omitempty"`
	QueryParameters []gwQueryMatch  `json:"query_parameters,omitempty"`
}

type gwPathMatch struct {
	Match string `json:"match"` // EXACT | PREFIX | REGEX
	Value string `json:"value"`
}

type gwHeaderMatch struct {
	Match string `json:"match"` // EXACT | REGEX | PRESENT | ABSENT
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

type gwQueryMatch struct {
	Match string `json:"match"` // EXACT | REGEX
	Name  string `json:"name"`
	Value string `json:"value"`
}

type gwFilter struct {
	RequestHeader  *gwHeaderFilter `json:"request_header,omitempty"`
	ResponseHeader *gwHeaderFilter `json:"response_header,omitempty"`
	Redirect       *gwRedirect     `json:"redirect,omitempty"`
	Rewrite        *gwRewrite      `json:"rewrite,omitempty"`
	Mirror         *gwMirror       `json:"mirror,omitempty"`
}

type gwHeaderFilter struct {
	Set    []gwNameValue `json:"set,omitempty"`
	Add    []gwNameValue `json:"add,omitempty"`
	Remove []string      `json:"remove,omitempty"`
}

type gwNameValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type gwRedirect struct {
	Scheme     string     `json:"scheme,omitempty"`
	Hostname   string     `json:"hostname,omitempty"`
	Port       uint32     `json:"port,omitempty"`
	StatusCode uint32     `json:"status_code,omitempty"`
	Path       *gwRewrite `json:"path,omitempty"`
}

type gwRewrite struct {
	ReplaceFull        string `json:"replace_full,omitempty"`
	ReplacePrefixMatch string `json:"replace_prefix_match,omitempty"`
	HostToBackend      bool   `json:"host_to_backend_hostname,omitempty"`
}

type gwMirror struct {
	Percentage float64    `json:"percentage,omitempty"`
	Backend    *gwBackend `json:"backend,omitempty"`
}

type gwBackend struct {
	Weight      uint32            `json:"weight,omitempty"`
	Destination map[string]string `json:"destination,omitempty"`
}

type gwTCPConf struct {
	Rules []gwTCPRule `json:"rules"`
}

type gwTCPRule struct {
	Backends []gwBackend `json:"backends"`
}

// ---- Main transformer -------------------------------------------------------

// TransformMeshGatewayRoute converts a legacy MeshGatewayRoute into one or
// more Gateway API HTTPRoute or TCPRoute resources.
func TransformMeshGatewayRoute(raw []byte) ([][]byte, []string, error) {
	var route meshGatewayRoute
	if err := yaml.Unmarshal(raw, &route); err != nil {
		return nil, nil, fmt.Errorf("unmarshal MeshGatewayRoute: %w", err)
	}

	if route.Spec.Conf.HTTP != nil {
		return transformGWHTTPRoute(route)
	}
	if route.Spec.Conf.TCP != nil {
		return transformGWTCPRoute(route)
	}
	return nil, []string{fmt.Sprintf("MeshGatewayRoute %q: no conf.http or conf.tcp section found — skipped", route.Metadata.Name)}, nil
}

// ---- HTTP -------------------------------------------------------------------

func transformGWHTTPRoute(route meshGatewayRoute) ([][]byte, []string, error) {
	name := route.Metadata.Name
	namespace := route.Metadata.Namespace
	http := route.Spec.Conf.HTTP
	var warnings []string

	// parentRefs — one per selector entry.
	parentRefs, w := parentRefsFromSelectors(route.Spec.Selectors, name, namespace)
	warnings = append(warnings, w...)

	// rules
	var rules []interface{}
	for rIdx, rule := range http.Rules {
		httpRule := map[string]interface{}{}

		// matches
		if len(rule.Matches) > 0 {
			convertedMatches, mw := convertGWHTTPMatches(rule.Matches, name, rIdx)
			warnings = append(warnings, mw...)
			if len(convertedMatches) > 0 {
				httpRule["matches"] = convertedMatches
			}
		}

		// filters
		if len(rule.Filters) > 0 {
			convertedFilters, fw := convertGWFilters(rule.Filters, name, rIdx)
			warnings = append(warnings, fw...)
			if len(convertedFilters) > 0 {
				httpRule["filters"] = convertedFilters
			}
		}

		// backendRefs
		if len(rule.Backends) > 0 {
			backendRefs, bw := convertGWBackends(rule.Backends, name, rIdx)
			warnings = append(warnings, bw...)
			if len(backendRefs) > 0 {
				httpRule["backendRefs"] = backendRefs
			}
		}

		rules = append(rules, httpRule)
	}

	meta := map[string]interface{}{
		"name": name,
	}
	if namespace != "" {
		meta["namespace"] = namespace
	}
	// Carry mesh annotation forward.
	meshName := meshNameFromGWRoute(route)
	if meshName != "" {
		meta["annotations"] = map[string]interface{}{
			"kuma.io/mesh": meshName,
		}
	}

	spec := map[string]interface{}{
		"parentRefs": parentRefs,
	}
	if len(http.Hostnames) > 0 {
		spec["hostnames"] = toStringSlice(http.Hostnames)
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

// ---- TCP --------------------------------------------------------------------

func transformGWTCPRoute(route meshGatewayRoute) ([][]byte, []string, error) {
	name := route.Metadata.Name
	namespace := route.Metadata.Namespace
	tcp := route.Spec.Conf.TCP
	var warnings []string

	parentRefs, w := parentRefsFromSelectors(route.Spec.Selectors, name, namespace)
	warnings = append(warnings, w...)

	var rules []interface{}
	for rIdx, rule := range tcp.Rules {
		backendRefs, bw := convertGWBackends(rule.Backends, name, rIdx)
		warnings = append(warnings, bw...)
		tcpRule := map[string]interface{}{}
		if len(backendRefs) > 0 {
			tcpRule["backendRefs"] = backendRefs
		}
		rules = append(rules, tcpRule)
	}

	meta := map[string]interface{}{
		"name": name,
	}
	if namespace != "" {
		meta["namespace"] = namespace
	}
	if meshName := meshNameFromGWRoute(route); meshName != "" {
		meta["annotations"] = map[string]interface{}{
			"kuma.io/mesh": meshName,
		}
	}

	spec := map[string]interface{}{
		"parentRefs": parentRefs,
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

// ---- parentRefs -------------------------------------------------------------

// parentRefsFromSelectors converts MeshGatewayRoute selectors into Gateway API parentRefs.
// Each selector becomes one parentRef. The kuma.io/service tag is used to derive the
// Gateway name/namespace; remaining tags are used to derive sectionName.
func parentRefsFromSelectors(selectors []gwSelector, routeName, routeNS string) ([]interface{}, []string) {
	var refs []interface{}
	var warnings []string

	for i, sel := range selectors {
		ref := map[string]interface{}{
			"group": "gateway.networking.k8s.io",
			"kind":  "Gateway",
		}

		svcTag := sel.Match["kuma.io/service"]
		if svcTag == "" {
			warnings = append(warnings, fmt.Sprintf(
				"MeshGatewayRoute %q selectors[%d]: no kuma.io/service tag — cannot derive Gateway name; set parentRefs[%d].name manually", routeName, i, i))
			ref["name"] = "(unknown-gateway)"
		} else {
			gwName, gwNS := parseGatewaySvcTag(svcTag)
			if gwName != "" {
				ref["name"] = gwName
			} else {
				// Universal-mode: use the raw value as name.
				ref["name"] = svcTag
				warnings = append(warnings, fmt.Sprintf(
					"MeshGatewayRoute %q selectors[%d]: kuma.io/service=%q does not follow the K8s _svc_ pattern — "+
						"used as Gateway name directly; verify this is correct", routeName, i, svcTag))
			}
			// Only set namespace when it differs from route namespace (avoids redundancy).
			if gwNS != "" && gwNS != routeNS {
				ref["namespace"] = gwNS
			}
		}

		// Remaining tags (excluding kuma.io/service) → attempt sectionName derivation.
		otherTags := map[string]string{}
		for k, v := range sel.Match {
			if k != "kuma.io/service" {
				otherTags[k] = v
			}
		}
		if len(otherTags) > 0 {
			if sn := sectionNameFromTags(otherTags); sn != "" {
				ref["sectionName"] = sn
			} else {
				warnings = append(warnings, fmt.Sprintf(
					"MeshGatewayRoute %q selectors[%d]: listener tags %v could not be mapped to a sectionName — "+
						"set parentRefs[%d].sectionName to the listener name of the migrated Gateway (e.g. 'http-8080')", routeName, i, otherTags, i))
			}
		}

		refs = append(refs, ref)
	}
	return refs, warnings
}

// parseGatewaySvcTag parses a MeshGateway selector service tag.
// Handles "name_ns_svc_port" (standard) and "name_ns_svc" (no port, common for gateways).
// Returns ("", "") for tags that do not follow either K8s pattern (Universal free-form names).
func parseGatewaySvcTag(tag string) (name, namespace string) {
	const svcPortMarker = "_svc_"
	const svcSuffix = "_svc"

	if strings.Contains(tag, svcPortMarker) {
		// Standard K8s format: name_ns_svc_port
		return ParseKumaServiceTag(tag)
	}
	if strings.HasSuffix(tag, svcSuffix) {
		// Gateway K8s format: name_ns_svc (no port)
		nameAndNS := tag[:len(tag)-len(svcSuffix)]
		lastUnderscore := strings.LastIndex(nameAndNS, "_")
		if lastUnderscore != -1 {
			return nameAndNS[:lastUnderscore], nameAndNS[lastUnderscore+1:]
		}
		return nameAndNS, ""
	}
	// Universal free-form name — caller should handle this.
	return "", ""
}

// ---- Match conversions ------------------------------------------------------

func convertGWHTTPMatches(matches []gwHTTPMatch, routeName string, rIdx int) ([]interface{}, []string) {
	var result []interface{}
	var warnings []string

	for _, m := range matches {
		out := map[string]interface{}{}

		if m.Path != nil {
			out["path"] = map[string]interface{}{
				"type":  convertPathMatchType(m.Path.Match),
				"value": m.Path.Value,
			}
		}
		if m.Method != "" {
			out["method"] = m.Method
		}
		if len(m.Headers) > 0 {
			hdrs, hw := convertGWHeaderMatches(m.Headers, routeName, rIdx)
			warnings = append(warnings, hw...)
			if len(hdrs) > 0 {
				out["headers"] = hdrs
			}
		}
		if len(m.QueryParameters) > 0 {
			// field rename: query_parameters → queryParams
			qps := convertGWQueryMatches(m.QueryParameters)
			if len(qps) > 0 {
				out["queryParams"] = qps
			}
		}

		result = append(result, out)
	}
	return result, warnings
}

func convertPathMatchType(t string) string {
	switch strings.ToUpper(t) {
	case "EXACT":
		return "Exact"
	case "PREFIX":
		return "PathPrefix"
	case "REGEX":
		return "RegularExpression"
	default:
		return t
	}
}

func convertGWHeaderMatches(headers []gwHeaderMatch, routeName string, rIdx int) ([]interface{}, []string) {
	var result []interface{}
	var warnings []string
	for _, h := range headers {
		switch strings.ToUpper(h.Match) {
		case "ABSENT", "PRESENT":
			warnings = append(warnings, fmt.Sprintf(
				"HTTPRoute %q rules[%d]: header match type %q for header %q is not supported in Gateway API HTTPRoute — "+
					"this match condition has been dropped; implement it via an ExtensionRef filter or custom policy",
				routeName, rIdx, h.Match, h.Name))
			continue
		}
		out := map[string]interface{}{
			"type":  convertHeaderMatchType(h.Match),
			"name":  h.Name,
		}
		if h.Value != "" {
			out["value"] = h.Value
		}
		result = append(result, out)
	}
	return result, warnings
}

func convertHeaderMatchType(t string) string {
	switch strings.ToUpper(t) {
	case "EXACT":
		return "Exact"
	case "REGEX":
		return "RegularExpression"
	default:
		return t
	}
}

func convertGWQueryMatches(qps []gwQueryMatch) []interface{} {
	var result []interface{}
	for _, q := range qps {
		result = append(result, map[string]interface{}{
			"type":  convertHeaderMatchType(q.Match), // same enum as headers
			"name":  q.Name,
			"value": q.Value,
		})
	}
	return result
}

// ---- Filter conversions -----------------------------------------------------

func convertGWFilters(filters []gwFilter, routeName string, rIdx int) ([]interface{}, []string) {
	var result []interface{}
	var warnings []string

	for _, f := range filters {
		switch {
		case f.RequestHeader != nil:
			result = append(result, map[string]interface{}{
				"type":                  "RequestHeaderModifier",
				"requestHeaderModifier": convertGWHeaderFilter(f.RequestHeader),
			})

		case f.ResponseHeader != nil:
			result = append(result, map[string]interface{}{
				"type":                   "ResponseHeaderModifier",
				"responseHeaderModifier": convertGWHeaderFilter(f.ResponseHeader),
			})

		case f.Redirect != nil:
			r := f.Redirect
			redirect := map[string]interface{}{}
			if r.Scheme != "" {
				redirect["scheme"] = r.Scheme
			}
			if r.Hostname != "" {
				redirect["hostname"] = r.Hostname
			}
			if r.Port != 0 {
				redirect["port"] = r.Port
			}
			if r.StatusCode != 0 {
				redirect["statusCode"] = r.StatusCode // status_code → statusCode
			}
			if r.Path != nil {
				if pathRewrite := convertGWRewritePath(r.Path); pathRewrite != nil {
					redirect["path"] = pathRewrite
				}
			}
			result = append(result, map[string]interface{}{
				"type":            "RequestRedirect",
				"requestRedirect": redirect,
			})

		case f.Rewrite != nil:
			rw := f.Rewrite
			urlRewrite := map[string]interface{}{}
			if pathRewrite := convertGWRewritePath(rw); pathRewrite != nil {
				urlRewrite["path"] = pathRewrite
			}
			if rw.HostToBackend {
				warnings = append(warnings, fmt.Sprintf(
					"HTTPRoute %q rules[%d]: rewrite.host_to_backend_hostname has no Gateway API equivalent — "+
						"this setting has been dropped; set the host header manually via a RequestHeaderModifier filter if needed",
					routeName, rIdx))
			}
			result = append(result, map[string]interface{}{
				"type":      "URLRewrite",
				"urlRewrite": urlRewrite,
			})

		case f.Mirror != nil:
			m := f.Mirror
			mirror := map[string]interface{}{}
			if m.Percentage != 0 {
				mirror["percent"] = m.Percentage // percentage → percent
			}
			if m.Backend != nil && m.Backend.Destination != nil {
				bref, bw := convertGWSingleBackend(*m.Backend, routeName, rIdx)
				warnings = append(warnings, bw...)
				// Mirror backendRef has no weight.
				delete(bref, "weight")
				mirror["backendRef"] = bref
			}
			result = append(result, map[string]interface{}{
				"type":          "RequestMirror",
				"requestMirror": mirror,
			})
		}
	}
	return result, warnings
}

func convertGWHeaderFilter(f *gwHeaderFilter) map[string]interface{} {
	out := map[string]interface{}{}
	if len(f.Set) > 0 {
		out["set"] = nameValueSlice(f.Set)
	}
	if len(f.Add) > 0 {
		out["add"] = nameValueSlice(f.Add)
	}
	if len(f.Remove) > 0 {
		out["remove"] = f.Remove
	}
	return out
}

func nameValueSlice(nvs []gwNameValue) []interface{} {
	out := make([]interface{}, len(nvs))
	for i, nv := range nvs {
		out[i] = map[string]interface{}{"name": nv.Name, "value": nv.Value}
	}
	return out
}

// convertGWRewritePath converts a gwRewrite into a Gateway API path rewrite spec.
// Returns nil if neither rewrite field is set.
func convertGWRewritePath(rw *gwRewrite) map[string]interface{} {
	if rw.ReplaceFull != "" {
		return map[string]interface{}{
			"type":            "ReplaceFullPath",
			"replaceFullPath": rw.ReplaceFull,
		}
	}
	if rw.ReplacePrefixMatch != "" {
		return map[string]interface{}{
			"type":               "ReplacePrefixMatch",
			"replacePrefixMatch": rw.ReplacePrefixMatch,
		}
	}
	return nil
}

// ---- Backend conversions ----------------------------------------------------

func convertGWBackends(backends []gwBackend, routeName string, rIdx int) ([]interface{}, []string) {
	var result []interface{}
	var warnings []string
	for _, b := range backends {
		ref, bw := convertGWSingleBackend(b, routeName, rIdx)
		warnings = append(warnings, bw...)
		result = append(result, ref)
	}
	return result, warnings
}

func convertGWSingleBackend(b gwBackend, routeName string, rIdx int) (map[string]interface{}, []string) {
	var warnings []string
	ref := map[string]interface{}{
		"kind":  "Service",
		"group": "",
	}

	svcTag := b.Destination["kuma.io/service"]
	if svcTag == "" {
		warnings = append(warnings, fmt.Sprintf(
			"HTTPRoute %q rules[%d]: backend has no kuma.io/service tag — skipped; add backendRefs manually", routeName, rIdx))
		return ref, warnings
	}

	svcName, svcNS := ParseKumaServiceTag(svcTag)
	if svcName == "" {
		// wildcard or unparseable
		warnings = append(warnings, fmt.Sprintf(
			"HTTPRoute %q rules[%d]: backend kuma.io/service=%q could not be parsed — set backendRefs name/namespace manually", routeName, rIdx, svcTag))
		ref["name"] = svcTag
		return ref, warnings
	}

	ref["name"] = svcName
	if svcNS != "" {
		ref["namespace"] = svcNS
	}

	// Extract port from the _svc_PORT segment.
	port := svcPortFromTag(svcTag)
	if port != "" {
		ref["port"] = port
	} else {
		warnings = append(warnings, fmt.Sprintf(
			"HTTPRoute %q rules[%d]: backend kuma.io/service=%q has no port in the svc tag — "+
				"set backendRefs[].port manually", routeName, rIdx, svcTag))
	}

	if b.Weight > 0 {
		ref["weight"] = b.Weight
	}

	return ref, warnings
}

// svcPortFromTag extracts the port string from a K8s-format kuma.io/service tag.
// e.g. "backend_demo_svc_3000" → "3000"; "backend_demo_svc" → "".
func svcPortFromTag(tag string) string {
	const marker = "_svc_"
	idx := strings.LastIndex(tag, marker)
	if idx == -1 {
		return ""
	}
	port := tag[idx+len(marker):]
	if port == "" {
		return ""
	}
	return port
}

// ---- Helpers ----------------------------------------------------------------

// meshNameFromGWRoute extracts the mesh name from a MeshGatewayRoute,
// checking the top-level "mesh" field (Universal) then the kuma.io/mesh label.
func meshNameFromGWRoute(route meshGatewayRoute) string {
	if route.Mesh != "" {
		return route.Mesh
	}
	if v, ok := route.Metadata.Labels["kuma.io/mesh"]; ok {
		return v
	}
	return ""
}

func toStringSlice(ss []string) []interface{} {
	out := make([]interface{}, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

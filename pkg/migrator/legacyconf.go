package migrator

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Legacy policy `conf` bodies are NOT structurally compatible with the `default`
// section of their Mesh* successor. Copying `conf` verbatim produces a document
// that applies cleanly (Kuma tolerates unknown fields on most paths) while the
// setting silently has no effect — e.g. Timeout's `connectTimeout` is
// `connectionTimeout` on MeshTimeout.
//
// This file holds one converter per legacy kind. Every converter is written as an
// explicit list of moves so that anything NOT moved is reported: confMapper tracks
// which leaves of the input were consumed and unmapped() returns the rest, which
// the caller turns into warnings. A field that upstream dropped outright (e.g.
// Retry's retriableStatusCodes) therefore surfaces as a warning instead of
// disappearing.
//
// Field names are the JSON/YAML forms (protojson lowerCamelCases the snake_case
// proto field names). Sources:
//   - legacy: kuma api/mesh/v1alpha1/{timeout,circuit_breaker,retry,health_check,
//     fault_injection,rate_limit,traffic_log,traffic_trace}.proto
//   - new: the frozen 2.14.x CRD schemas under
//     developer.konghq.com/app/assets/mesh/2.14.x/raw/crds/

// legacyConfConverter converts a legacy `conf` body into the `default` section of
// the corresponding Mesh* policy. It returns the converted body (nil when the
// input carried nothing convertible) and warnings for everything it could not map.
type legacyConfConverter func(conf map[string]interface{}, policyName string) (map[string]interface{}, []string)

// legacyConfConverters is keyed by the legacy policy type name. Kinds absent from
// this map either have no conf (TrafficPermission) or need context beyond the
// document itself (TrafficLog/TrafficTrace resolve a backend name against the
// Mesh resource — see meshBackendIndex).
var legacyConfConverters = map[string]legacyConfConverter{
	"Timeout":        convertTimeoutConf,
	"CircuitBreaker": convertCircuitBreakerConf,
	"Retry":          convertRetryConf,
	"HealthCheck":    convertHealthCheckConf,
	"FaultInjection": convertFaultInjectionConf,
	"RateLimit":      convertRateLimitConf,
	"ProxyTemplate":  convertProxyTemplateConf,
}

// ---- confMapper --------------------------------------------------------------

// confMapper moves values between two nested maps by dotted path and remembers
// which parts of the source were consumed.
type confMapper struct {
	src  map[string]interface{}
	dst  map[string]interface{}
	used map[string]bool
}

func newConfMapper(src map[string]interface{}) *confMapper {
	return &confMapper{
		src:  src,
		dst:  map[string]interface{}{},
		used: map[string]bool{},
	}
}

// get returns the value at a dotted path without marking it consumed.
func (m *confMapper) get(path string) (interface{}, bool) {
	cur := interface{}(m.src)
	for _, part := range strings.Split(path, ".") {
		asMap, ok := cur.(map[string]interface{})
		if !ok {
			return nil, false
		}
		cur, ok = asMap[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// markUsed records a source path (and everything under it) as consumed.
func (m *confMapper) markUsed(path string) { m.used[path] = true }

// set writes a value at a dotted path in the destination map.
func (m *confMapper) set(path string, value interface{}) {
	parts := strings.Split(path, ".")
	cur := m.dst
	for _, part := range parts[:len(parts)-1] {
		next, ok := cur[part].(map[string]interface{})
		if !ok {
			next = map[string]interface{}{}
			cur[part] = next
		}
		cur = next
	}
	cur[parts[len(parts)-1]] = value
}

// move copies src[from] to dst[to] when present, marking the source consumed.
func (m *confMapper) move(from, to string) bool {
	v, ok := m.get(from)
	if !ok || v == nil {
		return false
	}
	m.markUsed(from)
	m.set(to, v)
	return true
}

// moveWith copies src[from] to dst[to] after passing it through fn. When fn
// returns nil the value is still marked consumed but nothing is written.
func (m *confMapper) moveWith(from, to string, fn func(interface{}) interface{}) bool {
	v, ok := m.get(from)
	if !ok || v == nil {
		return false
	}
	m.markUsed(from)
	converted := fn(v)
	if converted == nil {
		return false
	}
	m.set(to, converted)
	return true
}

// unmapped returns the leaf paths of the source that no move consumed, sorted.
// Empty containers are not reported — only paths that actually carry a value.
func (m *confMapper) unmapped() []string {
	var out []string
	var walk func(node interface{}, path string)
	walk = func(node interface{}, path string) {
		if path != "" && m.isConsumed(path) {
			return
		}
		switch v := node.(type) {
		case map[string]interface{}:
			if len(v) == 0 {
				return
			}
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				child := k
				if path != "" {
					child = path + "." + k
				}
				walk(v[k], child)
			}
		case nil:
			return
		default:
			out = append(out, path)
		}
	}
	walk(m.src, "")
	sort.Strings(out)
	return out
}

// isConsumed reports whether path, or any ancestor of it, was marked used.
func (m *confMapper) isConsumed(path string) bool {
	for p := path; ; {
		if m.used[p] {
			return true
		}
		idx := strings.LastIndex(p, ".")
		if idx < 0 {
			return false
		}
		p = p[:idx]
	}
}

// result returns the destination map, or nil when nothing was mapped.
func (m *confMapper) result() map[string]interface{} {
	if len(m.dst) == 0 {
		return nil
	}
	return m.dst
}

// warnUnmapped turns leftover source paths into a single warning.
func warnUnmapped(m *confMapper, kind, newKind, name string) []string {
	left := m.unmapped()
	if len(left) == 0 {
		return nil
	}
	return []string{fmt.Sprintf(
		"%s %q: field(s) %s have no %s equivalent and were dropped — "+
			"review this policy manually.",
		kind, name, strings.Join(quoteAll(left), ", "), newKind)}
}

func quoteAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strconv.Quote("conf." + s)
	}
	return out
}

// normalizePercentage renders a legacy float percentage in a form the new
// int-or-string schema accepts: whole numbers stay numbers, fractions become
// strings ("50.5"), which is the documented representation for the anyOf.
func normalizePercentage(v interface{}) interface{} {
	f, ok := v.(float64)
	if !ok {
		return v
	}
	if f == math.Trunc(f) {
		return int(f)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// ---- Timeout → MeshTimeout ---------------------------------------------------

// MeshTimeout has no grpc section: gRPC runs over HTTP/2 and upstream folded the
// two gRPC timeouts into the http ones. There is also no http.idleTimeout — the
// legacy tcp.idleTimeout and http.idleTimeout both map to the single top-level
// idleTimeout, so a policy that set both loses one; that case is warned about.
func convertTimeoutConf(conf map[string]interface{}, name string) (map[string]interface{}, []string) {
	m := newConfMapper(conf)
	var warnings []string

	m.move("connectTimeout", "connectionTimeout")
	m.move("tcp.idleTimeout", "idleTimeout")
	m.move("http.requestTimeout", "http.requestTimeout")
	m.move("http.streamIdleTimeout", "http.streamIdleTimeout")
	m.move("http.maxStreamDuration", "http.maxStreamDuration")

	// http.idleTimeout and tcp.idleTimeout collapse onto the same field.
	if httpIdle, ok := m.get("http.idleTimeout"); ok && httpIdle != nil {
		m.markUsed("http.idleTimeout")
		if tcpIdle, had := m.get("tcp.idleTimeout"); had && tcpIdle != nil && tcpIdle != httpIdle {
			warnings = append(warnings, fmt.Sprintf(
				"Timeout %q: conf.tcp.idleTimeout (%v) and conf.http.idleTimeout (%v) both map to "+
					"MeshTimeout default.idleTimeout — kept the tcp value; set default.idleTimeout manually "+
					"if the http value is the one you need.",
				name, tcpIdle, httpIdle))
		} else {
			m.set("idleTimeout", httpIdle)
		}
	}

	// gRPC is HTTP/2: upstream removed the grpc section from MeshTimeout.
	if v, ok := m.get("grpc.streamIdleTimeout"); ok && v != nil {
		m.markUsed("grpc.streamIdleTimeout")
		if _, exists := m.get("http.streamIdleTimeout"); !exists {
			m.set("http.streamIdleTimeout", v)
		}
	}
	if v, ok := m.get("grpc.maxStreamDuration"); ok && v != nil {
		m.markUsed("grpc.maxStreamDuration")
		if _, exists := m.get("http.maxStreamDuration"); !exists {
			m.set("http.maxStreamDuration", v)
		}
	}

	warnings = append(warnings, warnUnmapped(m, "Timeout", "MeshTimeout", name)...)
	return m.result(), warnings
}

// ---- CircuitBreaker → MeshCircuitBreaker -------------------------------------

// The legacy conf is flat; MeshCircuitBreaker splits it into connectionLimits
// (the old thresholds) and outlierDetection (everything else), and renames every
// detector.
func convertCircuitBreakerConf(conf map[string]interface{}, name string) (map[string]interface{}, []string) {
	m := newConfMapper(conf)

	m.move("interval", "outlierDetection.interval")
	m.move("baseEjectionTime", "outlierDetection.baseEjectionTime")
	m.move("maxEjectionPercent", "outlierDetection.maxEjectionPercent")
	m.move("splitExternalAndLocalErrors", "outlierDetection.splitExternalAndLocalErrors")

	m.move("detectors.totalErrors.consecutive", "outlierDetection.detectors.totalFailures.consecutive")
	m.move("detectors.gatewayErrors.consecutive", "outlierDetection.detectors.gatewayFailures.consecutive")
	m.move("detectors.localErrors.consecutive", "outlierDetection.detectors.localOriginFailures.consecutive")

	m.move("detectors.standardDeviation.requestVolume", "outlierDetection.detectors.successRate.requestVolume")
	m.move("detectors.standardDeviation.minimumHosts", "outlierDetection.detectors.successRate.minimumHosts")
	m.moveWith("detectors.standardDeviation.factor",
		"outlierDetection.detectors.successRate.standardDeviationFactor", normalizePercentage)

	m.move("detectors.failure.requestVolume", "outlierDetection.detectors.failurePercentage.requestVolume")
	m.move("detectors.failure.minimumHosts", "outlierDetection.detectors.failurePercentage.minimumHosts")
	m.move("detectors.failure.threshold", "outlierDetection.detectors.failurePercentage.threshold")

	m.move("thresholds.maxConnections", "connectionLimits.maxConnections")
	m.move("thresholds.maxPendingRequests", "connectionLimits.maxPendingRequests")
	m.move("thresholds.maxRetries", "connectionLimits.maxRetries")
	m.move("thresholds.maxRequests", "connectionLimits.maxRequests")

	return m.result(), warnUnmapped(m, "CircuitBreaker", "MeshCircuitBreaker", name)
}

// ---- Retry → MeshRetry -------------------------------------------------------

// legacyRetryOn maps the legacy HttpRetryOn proto enum values to the MeshRetry
// HTTPRetryOn strings (kuma pkg/plugins/policies/meshretry/api/v1alpha1).
// retriable_status_codes and retriable_headers have no MeshRetry equivalent:
// MeshRetry validates retryOn against a closed set that contains neither.
var legacyRetryOn = map[string]string{
	"all_5xx":                    "5xx",
	"gateway_error":              "GatewayError",
	"reset":                      "Reset",
	"connect_failure":            "ConnectFailure",
	"envoy_ratelimited":          "EnvoyRatelimited",
	"retriable_4xx":              "Retriable4xx",
	"refused_stream":             "RefusedStream",
	"http3_post_connect_failure": "Http3PostConnectFailure",
}

// legacyGrpcRetryOn maps the legacy Retry.Conf.Grpc.RetryOn enum. Note the
// spelling change on the first entry: "cancelled" → "Canceled".
var legacyGrpcRetryOn = map[string]string{
	"cancelled":          "Canceled",
	"deadline_exceeded":  "DeadlineExceeded",
	"internal":           "Internal",
	"resource_exhausted": "ResourceExhausted",
	"unavailable":        "Unavailable",
}

// legacyRetryMethod maps legacy retriableMethods (the shared HttpMethod enum) to
// the HttpMethod* members of MeshRetry's retryOn list.
var legacyRetryMethod = map[string]string{
	"CONNECT": "HttpMethodConnect",
	"DELETE":  "HttpMethodDelete",
	"GET":     "HttpMethodGet",
	"HEAD":    "HttpMethodHead",
	"OPTIONS": "HttpMethodOptions",
	"PATCH":   "HttpMethodPatch",
	"POST":    "HttpMethodPost",
	"PUT":     "HttpMethodPut",
	"TRACE":   "HttpMethodTrace",
}

func convertRetryConf(conf map[string]interface{}, name string) (map[string]interface{}, []string) {
	m := newConfMapper(conf)
	var warnings []string

	// TCP: note the singular field name on MeshRetry.
	m.move("tcp.maxConnectAttempts", "tcp.maxConnectAttempt")

	m.move("http.numRetries", "http.numRetries")
	m.move("http.perTryTimeout", "http.perTryTimeout")
	m.move("http.backOff.baseInterval", "http.backOff.baseInterval")
	m.move("http.backOff.maxInterval", "http.backOff.maxInterval")

	// retryOn + retriableMethods both feed the single MeshRetry retryOn list.
	var retryOn []interface{}
	if v, ok := m.get("http.retryOn"); ok {
		m.markUsed("http.retryOn")
		for _, item := range toSlice(v) {
			s := fmt.Sprintf("%v", item)
			if mapped, ok := legacyRetryOn[strings.ToLower(s)]; ok {
				retryOn = append(retryOn, mapped)
				continue
			}
			// Already in the new form (a manually-edited manifest) — keep it.
			if isNewHTTPRetryOn(s) {
				retryOn = append(retryOn, s)
				continue
			}
			warnings = append(warnings, fmt.Sprintf(
				"Retry %q: conf.http.retryOn value %q has no MeshRetry equivalent and was dropped "+
					"(MeshRetry validates retryOn against a closed set which does not include "+
					"status-code or header based retries).", name, s))
		}
	}
	if v, ok := m.get("http.retriableMethods"); ok {
		m.markUsed("http.retriableMethods")
		for _, item := range toSlice(v) {
			s := fmt.Sprintf("%v", item)
			if mapped, ok := legacyRetryMethod[strings.ToUpper(s)]; ok {
				retryOn = append(retryOn, mapped)
				continue
			}
			warnings = append(warnings, fmt.Sprintf(
				"Retry %q: conf.http.retriableMethods value %q is not a known HTTP method and was dropped.",
				name, s))
		}
	}
	if len(retryOn) > 0 {
		m.set("http.retryOn", retryOn)
	}

	// retriableStatusCodes was removed outright.
	if v, ok := m.get("http.retriableStatusCodes"); ok && v != nil {
		m.markUsed("http.retriableStatusCodes")
		warnings = append(warnings, fmt.Sprintf(
			"Retry %q: conf.http.retriableStatusCodes (%v) has no MeshRetry equivalent and was dropped — "+
				"MeshRetry cannot retry on specific status codes. Use http.retryOn values such as "+
				"5xx or GatewayError, or keep the retry logic in the application.",
			name, v))
	}

	// gRPC.
	m.move("grpc.numRetries", "grpc.numRetries")
	m.move("grpc.perTryTimeout", "grpc.perTryTimeout")
	m.move("grpc.backOff.baseInterval", "grpc.backOff.baseInterval")
	m.move("grpc.backOff.maxInterval", "grpc.backOff.maxInterval")
	if v, ok := m.get("grpc.retryOn"); ok {
		m.markUsed("grpc.retryOn")
		var grpcRetryOn []interface{}
		for _, item := range toSlice(v) {
			s := fmt.Sprintf("%v", item)
			if mapped, ok := legacyGrpcRetryOn[strings.ToLower(s)]; ok {
				grpcRetryOn = append(grpcRetryOn, mapped)
				continue
			}
			grpcRetryOn = append(grpcRetryOn, s)
		}
		if len(grpcRetryOn) > 0 {
			m.set("grpc.retryOn", grpcRetryOn)
		}
	}

	warnings = append(warnings, warnUnmapped(m, "Retry", "MeshRetry", name)...)
	return m.result(), warnings
}

func isNewHTTPRetryOn(s string) bool {
	switch s {
	case "5xx", "GatewayError", "Reset", "Retriable4xx", "ConnectFailure",
		"EnvoyRatelimited", "RefusedStream", "Http3PostConnectFailure":
		return true
	}
	return strings.HasPrefix(s, "HttpMethod")
}

// ---- HealthCheck → MeshHealthCheck -------------------------------------------

// Field names line up almost one-to-one; the exception is http.requestHeadersToAdd,
// which changed from a list of {header:{key,value}, append} to the Gateway-API-style
// {add:[{name,value}], set:[{name,value}]} split. tcp.send/receive stay base64 in
// both APIs, so they copy across unchanged.
func convertHealthCheckConf(conf map[string]interface{}, name string) (map[string]interface{}, []string) {
	m := newConfMapper(conf)
	var warnings []string

	for _, f := range []string{
		"interval", "timeout", "unhealthyThreshold", "healthyThreshold",
		"initialJitter", "intervalJitter", "intervalJitterPercent",
		"failTrafficOnPanic", "eventLogPath", "alwaysLogHealthCheckFailures",
		"noTrafficInterval", "reuseConnection",
	} {
		m.move(f, f)
	}
	m.moveWith("healthyPanicThreshold", "healthyPanicThreshold", normalizePercentage)

	m.move("tcp.send", "tcp.send")
	m.move("tcp.receive", "tcp.receive")

	m.move("http.path", "http.path")
	m.move("http.expectedStatuses", "http.expectedStatuses")

	if v, ok := m.get("http.requestHeadersToAdd"); ok {
		m.markUsed("http.requestHeadersToAdd")
		headers := headerNames{}
		var add, set []interface{}
		for _, item := range toSlice(v) {
			entry, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			header, _ := entry["header"].(map[string]interface{})
			if header == nil {
				continue
			}
			hv := map[string]interface{}{}
			if k, ok := header["key"]; ok {
				hv["name"] = headers.name(k)
			}
			if val, ok := header["value"]; ok {
				hv["value"] = val
			}
			if len(hv) == 0 {
				continue
			}
			// Legacy default for `append` is true (proto BoolValue left unset means
			// Envoy appends), which is the new `add` list. Explicit false is `set`.
			appendVal := true
			if a, ok := entry["append"].(bool); ok {
				appendVal = a
			}
			if appendVal {
				add = append(add, hv)
			} else {
				set = append(set, hv)
			}
		}
		warnings = append(warnings, headers.warnings("HealthCheck", "MeshHealthCheck", name,
			"conf.http.requestHeadersToAdd")...)
		if len(add) > 0 {
			m.set("http.requestHeadersToAdd.add", add)
		}
		if len(set) > 0 {
			m.set("http.requestHeadersToAdd.set", set)
		}
	}

	warnings = append(warnings, warnUnmapped(m, "HealthCheck", "MeshHealthCheck", name)...)
	return m.result(), warnings
}

// ---- FaultInjection → MeshFaultInjection -------------------------------------

// The legacy conf holds a single fault; MeshFaultInjection takes a list under
// default.http[].
func convertFaultInjectionConf(conf map[string]interface{}, name string) (map[string]interface{}, []string) {
	m := newConfMapper(conf)

	fault := newConfMapper(conf)
	fault.moveWith("delay.percentage", "delay.percentage", normalizePercentage)
	fault.move("delay.value", "delay.value")
	fault.moveWith("abort.percentage", "abort.percentage", normalizePercentage)
	fault.move("abort.httpStatus", "abort.httpStatus")
	fault.moveWith("responseBandwidth.percentage", "responseBandwidth.percentage", normalizePercentage)
	var warnings []string
	fault.moveWith("responseBandwidth.limit", "responseBandwidth.limit", func(v interface{}) interface{} {
		limit, w := normalizeBandwidthLimit(v, name)
		warnings = append(warnings, w...)
		return limit
	})

	// Mirror consumption onto m so unmapped() reports against the same source.
	for p := range fault.used {
		m.markUsed(p)
	}

	warnings = append(warnings, warnUnmapped(m, "FaultInjection", "MeshFaultInjection", name)...)
	body := fault.result()
	if body == nil {
		return nil, warnings
	}
	return map[string]interface{}{
		"http": []interface{}{body},
	}, warnings
}

// legacyBandwidth reads the legacy FaultInjection limit. The 2.14 legacy
// validator matched `\d*\s?[gmk]bps` (lower-case units, unanchored); the
// MeshFaultInjection validator (validators.BandwidthRegex, 2.14 and master)
// matches `^(\d*)\s?([GMk]+bps)$` and converts with ConvertBandwidthToKbps, which
// only knows kbps, Mbps and Gbps. So "50 mbps" — valid on FaultInjection — is
// rejected as "must be in kbps/Mbps/Gbps units".
var legacyBandwidth = regexp.MustCompile(`^\s*(\d+)\s*([gGmMkK])bps\s*$`)

var bandwidthUnits = map[string]string{"k": "kbps", "m": "Mbps", "g": "Gbps"}

// normalizeBandwidthLimit rewrites a legacy bandwidth limit as <digits><unit>
// with the unit spelled kbps/Mbps/Gbps. A value it cannot parse is copied
// unchanged with a warning, since the control plane will reject it.
func normalizeBandwidthLimit(v interface{}, policyName string) (interface{}, []string) {
	s, ok := v.(string)
	if !ok {
		return v, []string{fmt.Sprintf(
			"FaultInjection %q: conf.responseBandwidth.limit %v is not a string; MeshFaultInjection needs "+
				"a value such as 50Mbps — copied unchanged, fix it before applying.", policyName, v)}
	}
	match := legacyBandwidth.FindStringSubmatch(s)
	if match == nil {
		return s, []string{fmt.Sprintf(
			"FaultInjection %q: conf.responseBandwidth.limit %q is not <integer><kbps|mbps|gbps> — copied "+
				"unchanged; MeshFaultInjection rejects anything but kbps/Mbps/Gbps units "+
				"(for example 50Mbps), so fix it before applying.", policyName, s)}
	}
	out := match[1] + bandwidthUnits[strings.ToLower(match[2])]
	if out == s {
		return s, nil
	}
	return out, []string{fmt.Sprintf(
		"FaultInjection %q: conf.responseBandwidth.limit %q written as %q — MeshFaultInjection accepts "+
			"only the units kbps, Mbps and Gbps.", policyName, s, out)}
}

// ---- RateLimit → MeshRateLimit -----------------------------------------------

// The legacy conf.http.{requests,interval} pair becomes a requestRate object under
// default.local.http, and onRateLimit headers change from a flat list with an
// `append` flag to the {add,set} split.
func convertRateLimitConf(conf map[string]interface{}, name string) (map[string]interface{}, []string) {
	m := newConfMapper(conf)

	requests, hasRequests := m.get("http.requests")
	interval, hasInterval := m.get("http.interval")
	if hasRequests {
		m.markUsed("http.requests")
	}
	if hasInterval {
		m.markUsed("http.interval")
	}
	if hasRequests || hasInterval {
		rate := map[string]interface{}{}
		if hasRequests {
			rate["num"] = requests
		}
		if hasInterval {
			rate["interval"] = interval
		}
		m.set("local.http.requestRate", rate)
	}

	m.move("http.onRateLimit.status", "local.http.onRateLimit.status")

	var warnings []string
	if v, ok := m.get("http.onRateLimit.headers"); ok {
		m.markUsed("http.onRateLimit.headers")
		headers := headerNames{}
		var add, set []interface{}
		for _, item := range toSlice(v) {
			entry, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			hv := map[string]interface{}{}
			if k, ok := entry["key"]; ok {
				hv["name"] = headers.name(k)
			}
			if val, ok := entry["value"]; ok {
				hv["value"] = val
			}
			if len(hv) == 0 {
				continue
			}
			appendVal := true
			if a, ok := entry["append"].(bool); ok {
				appendVal = a
			}
			if appendVal {
				add = append(add, hv)
			} else {
				set = append(set, hv)
			}
		}
		warnings = append(warnings, headers.warnings("RateLimit", "MeshRateLimit", name,
			"conf.http.onRateLimit.headers")...)
		if len(add) > 0 {
			m.set("local.http.onRateLimit.headers.add", add)
		}
		if len(set) > 0 {
			m.set("local.http.onRateLimit.headers.set", set)
		}
	}

	warnings = append(warnings, warnUnmapped(m, "RateLimit", "MeshRateLimit", name)...)
	return m.result(), warnings
}

// ---- ProxyTemplate → MeshProxyPatch ------------------------------------------

// legacyProxyPatchOperation maps the legacy ProxyTemplate modification
// operations (kuma api/mesh/v1alpha1/proxy_template_operations.go at v2.14:
// add, addFirst, addLast, addBefore, addAfter, remove, patch), keyed in lower
// case, to the PascalCase MeshProxyPatch enum.
var legacyProxyPatchOperation = map[string]string{
	"add":       "Add",
	"remove":    "Remove",
	"patch":     "Patch",
	"addfirst":  "AddFirst",
	"addlast":   "AddLast",
	"addbefore": "AddBefore",
	"addafter":  "AddAfter",
}

// proxyPatchOperations lists the operations each MeshProxyPatch modification
// kind accepts (kubebuilder enums in meshproxypatch/api/v1alpha1 at v2.14 and on
// master). cluster, listener and virtualHost take a plain Add; the two filter
// kinds replace it with the positional AddFirst/AddBefore/AddAfter/AddLast.
// The legacy ProxyTemplate validator enforced the same split, so a valid legacy
// manifest only needs its operation recased.
var proxyPatchOperations = map[string]map[string]bool{
	"cluster":       {"Add": true, "Remove": true, "Patch": true},
	"listener":      {"Add": true, "Remove": true, "Patch": true},
	"virtualHost":   {"Add": true, "Remove": true, "Patch": true},
	"networkFilter": {"Remove": true, "Patch": true, "AddFirst": true, "AddBefore": true, "AddAfter": true, "AddLast": true},
	"httpFilter":    {"Remove": true, "Patch": true, "AddFirst": true, "AddBefore": true, "AddAfter": true, "AddLast": true},
}

func convertProxyTemplateConf(conf map[string]interface{}, name string) (map[string]interface{}, []string) {
	m := newConfMapper(conf)
	var warnings []string

	// `imports` selected a built-in profile; MeshProxyPatch has no equivalent and
	// the control plane now always generates the standard configuration.
	if v, ok := m.get("imports"); ok && v != nil {
		m.markUsed("imports")
		warnings = append(warnings, fmt.Sprintf(
			"ProxyTemplate %q: conf.imports (%v) has no MeshProxyPatch equivalent — profile imports were "+
				"removed with the template indirection. The control plane generates the standard "+
				"configuration and MeshProxyPatch only appends modifications on top.", name, v))
	}

	// `resources` injected whole raw Envoy resources. MeshProxyPatch can only
	// modify what the control plane generates.
	if v, ok := m.get("resources"); ok && v != nil {
		m.markUsed("resources")
		warnings = append(warnings, fmt.Sprintf(
			"ProxyTemplate %q: conf.resources injected raw Envoy resources, which MeshProxyPatch cannot do — "+
				"it only patches generated resources. Re-express these as appendModifications entries "+
				"or drop them.", name))
	}

	if v, ok := m.get("modifications"); ok && v != nil {
		m.markUsed("modifications")
		var mods []interface{}
		for i, item := range toSlice(v) {
			mod, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			converted, w := convertProxyTemplateModification(mod, name, i)
			warnings = append(warnings, w...)
			if converted != nil {
				mods = append(mods, converted)
			}
		}
		if len(mods) > 0 {
			m.set("appendModifications", mods)
		}
	}

	warnings = append(warnings, warnUnmapped(m, "ProxyTemplate", "MeshProxyPatch", name)...)
	return m.result(), warnings
}

// convertProxyTemplateModification converts one legacy modification entry. The
// shape is identical apart from the operation enum casing, so the entry is copied
// and only `operation` is rewritten.
func convertProxyTemplateModification(mod map[string]interface{}, policyName string, idx int) (map[string]interface{}, []string) {
	var warnings []string
	out := map[string]interface{}{}

	for kind, body := range mod {
		bodyMap, ok := body.(map[string]interface{})
		if !ok {
			out[kind] = body
			continue
		}
		copied := map[string]interface{}{}
		for k, v := range bodyMap {
			copied[k] = v
		}
		if op, ok := copied["operation"].(string); ok {
			copied["operation"], warnings = convertProxyPatchOperation(op, kind, policyName, idx, warnings)
		}
		out[kind] = copied
	}

	if len(out) == 0 {
		return nil, warnings
	}
	return out, warnings
}

// convertProxyPatchOperation recases one legacy operation for the MeshProxyPatch
// modification kind it sits under. It returns the operation to write and the
// warnings slice with anything that could not be mapped exactly appended.
func convertProxyPatchOperation(op, kind, policyName string, idx int, warnings []string) (string, []string) {
	mapped, known := legacyProxyPatchOperation[strings.ToLower(op)]
	allowed, knownKind := proxyPatchOperations[kind]
	switch {
	case !known:
		return op, append(warnings, fmt.Sprintf(
			"ProxyTemplate %q: modifications[%d].%s.operation %q is not a ProxyTemplate operation — "+
				"copied unchanged, verify it against the MeshProxyPatch schema.",
			policyName, idx, kind, op))
	case !knownKind || allowed[mapped]:
		return mapped, warnings
	case mapped == "Add":
		// A filter has no plain Add; the positional variants replaced it.
		return "AddLast", append(warnings, fmt.Sprintf(
			"ProxyTemplate %q: modifications[%d].%s used operation \"add\", which MeshProxyPatch "+
				"splits into AddFirst/AddBefore/AddAfter/AddLast. Converted to AddLast — change it "+
				"if this filter has to run earlier in the chain.",
			policyName, idx, kind))
	default:
		return mapped, append(warnings, fmt.Sprintf(
			"ProxyTemplate %q: modifications[%d].%s.operation %q is not accepted on a MeshProxyPatch %s "+
				"modification (allowed: %s) — written as %q, which the control plane rejects; pick an "+
				"allowed operation.",
			policyName, idx, kind, op, kind, strings.Join(sortedKeys(allowed), ", "), mapped))
	}
}

// headerNamePattern is the schema every header name in the new policies must
// match (common_api.HeaderName, api/common/v1alpha1/header.go at v2.14 and on
// master): lower-case RFC 7230 token characters only. The legacy policies took
// any case, and "Accept" or "Content-Type" are what real manifests carry.
var headerNamePattern = regexp.MustCompile("^[a-z0-9!#$%&'*+\\-.^_`|~]+$")

// headerNames lower-cases legacy header names for the new HeaderName schema and
// records what it changed. HTTP header names are case-insensitive (RFC 9110
// §5.1) and Envoy lower-cases them on the wire, so the rename keeps behaviour.
type headerNames struct {
	renamed []string
	invalid []string
}

func (h *headerNames) name(v interface{}) interface{} {
	s, ok := v.(string)
	if !ok {
		return v
	}
	lower := strings.ToLower(s)
	if lower != s {
		h.renamed = append(h.renamed, fmt.Sprintf("%q → %q", s, lower))
	}
	if !headerNamePattern.MatchString(lower) {
		h.invalid = append(h.invalid, strconv.Quote(s))
	}
	return lower
}

func (h *headerNames) warnings(kind, newKind, policyName, path string) []string {
	var out []string
	if len(h.renamed) > 0 {
		out = append(out, fmt.Sprintf(
			"%s %q: %s header name(s) lower-cased (%s) — %s accepts lower-case header names only; "+
				"HTTP header names are case-insensitive, so the behaviour is unchanged.",
			kind, policyName, path, strings.Join(h.renamed, ", "), newKind))
	}
	if len(h.invalid) > 0 {
		out = append(out, fmt.Sprintf(
			"%s %q: %s header name(s) %s contain characters a header name cannot have; %s "+
				"rejects them (pattern %s) — fix the name before applying.",
			kind, policyName, path, strings.Join(h.invalid, ", "), newKind, headerNamePattern.String()))
	}
	return out
}

// ---- helpers -----------------------------------------------------------------

func toSlice(v interface{}) []interface{} {
	if s, ok := v.([]interface{}); ok {
		return s
	}
	return nil
}

// convertLegacyConf runs the converter registered for a legacy kind. When no
// converter exists the conf is returned unchanged (kinds whose bodies genuinely
// carry over, and kinds handled elsewhere).
func convertLegacyConf(legacyKind, policyName string, conf json.RawMessage) (json.RawMessage, []string, error) {
	if len(conf) == 0 {
		return nil, nil, nil
	}
	converter, ok := legacyConfConverters[legacyKind]
	if !ok {
		return conf, nil, nil
	}

	var body map[string]interface{}
	if err := json.Unmarshal(conf, &body); err != nil {
		return nil, nil, fmt.Errorf("parse %s conf: %w", legacyKind, err)
	}
	converted, warnings := converter(body, policyName)
	if converted == nil {
		return nil, warnings, nil
	}
	out, err := json.Marshal(converted)
	if err != nil {
		return nil, warnings, fmt.Errorf("marshal converted %s conf: %w", legacyKind, err)
	}
	return out, warnings, nil
}

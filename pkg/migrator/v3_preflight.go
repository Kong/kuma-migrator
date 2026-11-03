package migrator

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"
)

// Kuma 3.0 checks that the Mesh team's readiness tool (kuma3-preflight) carries
// and that need only the manifest:
//
//   - spec.targetRef.proxyTypes on any policy. 3.0 has no proxyTypes field
//     (api/common/v1alpha1/targetref.go on master); Kubernetes prunes it, the
//     REST API rejects it as an unknown field
//     (pkg/core/resources/model/rest/unmarshaller.go rejectUnknownFields).
//     A stored policy loses the filter and selects every proxy (kumahq/kuma#18857).
//   - appProtocol: kafka on MeshService / MeshMultiZoneService ports and on
//     Dataplane inbounds. 3.0 removed the Kafka protocol
//     (pkg/core/metadata/protocol.go: SupportedProtocols is grpc/http/http2/tcp;
//     kumahq/kuma#17831), and the validators reject anything outside that list.
//   - named entries in Dataplane transparentProxying.directAccessServices and the
//     kuma.io/direct-access-services pod annotation. 3.0 matches only "*"
//     (pkg/xds/generator/direct_access_proxy_generator.go: "Matching by an
//     individual service name relied on a now-removed tag; only the "*" (match
//     everything) entry works").
//   - legacy Retry field names in a hand-written MeshRetry (Kong/kong-mesh#11074).

// ---- proxyTypes in spec.targetRef ---------------------------------------------

// defaultGatewayTimeoutPrefix is the name prefix of the MeshTimeout every 2.x
// control plane creates per mesh (pkg/defaults/mesh/mesh.go at v2.14.7:
// "mesh-gateways-timeout-all" + "-" + mesh), scoped to gateways with
// proxyTypes: [Gateway] and http.streamIdleTimeout: 5s.
const defaultGatewayTimeoutPrefix = "mesh-gateways-timeout-all-"

// proxyTypesInfo describes spec.targetRef.proxyTypes on a policy.
type proxyTypesInfo struct {
	present bool
	values  []string
	// effective is true when the 2.x matcher honoured the field: only for a
	// top-level kind Mesh or MeshSubset (pkg/plugins/policies/core/matchers/
	// dataplane.go at v2.14.7). On any other kind it was ignored.
	effective bool
	refKind   string
}

func (p proxyTypesInfo) has(v string) bool {
	for _, x := range p.values {
		if x == v {
			return true
		}
	}
	return false
}

// gatewayOnly reports a filter that restricted the policy to gateway proxies.
func (p proxyTypesInfo) gatewayOnly() bool {
	return p.present && p.effective && p.has("Gateway") && !p.has("Sidecar")
}

// sidecarOnly reports a filter that kept the policy off gateway proxies.
func (p proxyTypesInfo) sidecarOnly() bool {
	return p.present && p.effective && p.has("Sidecar") && !p.has("Gateway")
}

func policyProxyTypes(obj map[string]interface{}) proxyTypesInfo {
	spec, _ := obj["spec"].(map[string]interface{})
	tr, _ := spec["targetRef"].(map[string]interface{})
	raw, ok := tr["proxyTypes"]
	if !ok {
		return proxyTypesInfo{}
	}
	info := proxyTypesInfo{present: true}
	for _, v := range toSlice(raw) {
		if s, ok := v.(string); ok {
			info.values = append(info.values, s)
		}
	}
	info.refKind, _ = tr["kind"].(string)
	switch info.refKind {
	case "", "Mesh", "MeshSubset":
		info.effective = true
	}
	return info
}

// isDefaultGatewayMeshTimeout reports the 2.x default
// mesh-gateways-timeout-all-<mesh> MeshTimeout (or a document split from it,
// e.g. its "-outbound" half): kind MeshTimeout, the default name prefix and
// proxyTypes: [Gateway].
func isDefaultGatewayMeshTimeout(obj map[string]interface{}) bool {
	kind, _ := obj["kind"].(string)
	if kind == "" {
		kind, _ = obj["type"].(string)
	}
	if kind != "MeshTimeout" {
		return false
	}
	if !strings.HasPrefix(extractNameFromObj(obj), defaultGatewayTimeoutPrefix) {
		return false
	}
	return policyProxyTypes(obj).gatewayOnly()
}

// defaultGatewayTimeoutNote is the advisory for mesh-gateways-timeout-all-<mesh>.
// Dropping proxyTypes from it is wrong: the policy then sets a 5s
// streamIdleTimeout on every sidecar (kumahq/kuma#18857).
func defaultGatewayTimeoutNote(name string, target TargetVersion) string {
	base := fmt.Sprintf(
		"MeshTimeout %q is the default gateway timeout 2.x creates per mesh (proxyTypes: [Gateway], "+
			"http.streamIdleTimeout: 5s). 3.0 removes proxyTypes: the field is pruned, the policy selects "+
			"every proxy, and every sidecar HTTP response that sends no bytes for 5s fails with 504 "+
			"(kumahq/kuma#18857). Delete the policy before upgrading; do not just drop the field. On "+
			"Kubernetes the 2.14 Mesh controller recreates it on the next Mesh reconcile: first add "+
			"MeshTimeout (or *) to the Mesh's spec.skipCreatingInitialPolicies, then delete it. 3.0 ignores "+
			"skipCreatingInitialPolicies, so leaving it set is harmless. On Universal deleting it is enough.",
		name)
	if target.IsV3() {
		return base + fmt.Sprintf(" The original is kept under %s/ and is listed under \"Delete Before "+
			"Upgrading to 3.0\".", ManualDir)
	}
	return base + " On 2.x it still applies to gateways only; delete it right before the 3.0 upgrade."
}

// fixProxyTypes handles spec.targetRef.proxyTypes. Under v3 it removes the field
// when doing so keeps the policy's scope or only adds the proxies 3.0 can no
// longer tell apart from sidecars; a Gateway-only filter is left in place and
// v3RejectionReasons sends the document to ManualDir. Under v2 it only warns.
func fixProxyTypes(obj map[string]interface{}, name, kind string, target TargetVersion) (bool, []string) {
	info := policyProxyTypes(obj)
	if !info.present {
		return false, nil
	}
	if isDefaultGatewayMeshTimeout(obj) {
		return false, []string{defaultGatewayTimeoutNote(name, target)}
	}
	list := "[" + strings.Join(info.values, ", ") + "]"

	if !target.IsV3() {
		switch {
		case info.gatewayOnly():
			return false, []string{fmt.Sprintf(
				"%s %q: spec.targetRef.proxyTypes %s is removed in 3.0. The field is pruned and the policy "+
					"then selects every proxy, sidecars included. 3.0 has no gateway proxy type: plan to select "+
					"the gateway Dataplanes with kind: Dataplane + labels, or delete the policy.",
				kind, name, list)}
		case info.sidecarOnly():
			return false, []string{fmt.Sprintf(
				"%s %q: spec.targetRef.proxyTypes %s is removed in 3.0. The policy then also selects the "+
					"proxies that are gateways on 2.x (a Dataplane with networking.gateway); 3.0 runs them as "+
					"ordinary Dataplanes. --to-latest v3 drops the field.",
				kind, name, list)}
		default:
			return false, []string{fmt.Sprintf(
				"%s %q: spec.targetRef.proxyTypes is removed in 3.0. Here it does not narrow the policy, so "+
					"--to-latest v3 drops it with no change in scope.",
				kind, name)}
		}
	}

	if info.gatewayOnly() {
		return false, []string{fmt.Sprintf(
			"%s %q: spec.targetRef.proxyTypes %s restricts the policy to gateway proxies. 3.0 removes the "+
				"field and has no gateway proxy type, so the policy would select every proxy. Not converted: "+
				"select the gateway Dataplanes with kind: Dataplane + labels, or delete the policy.",
			kind, name, list)}
	}

	spec := obj["spec"].(map[string]interface{})
	tr := spec["targetRef"].(map[string]interface{})
	delete(tr, "proxyTypes")
	var why string
	switch {
	case !info.effective:
		why = fmt.Sprintf("2.x ignores it on a top-level kind %s, so the scope does not change", info.refKind)
	case info.sidecarOnly():
		why = "the policy now also selects the proxies that are gateways on 2.x (a Dataplane with " +
			"networking.gateway), which 3.0 runs as ordinary Dataplanes. If it must not apply to them, " +
			"narrow spec.targetRef to kind: Dataplane + labels"
	default:
		why = "it listed every proxy type, so the scope does not change"
	}
	return true, []string{fmt.Sprintf(
		"%s %q: spec.targetRef.proxyTypes %s is removed in 3.0 (pruned on Kubernetes, rejected as an unknown "+
			"field on Universal). Auto-removed: %s.",
		kind, name, list, why)}
}

// proxyTypesRejectionReason is the v3RejectionReasons entry for a Gateway-only
// proxyTypes filter, or "" when there is none. The default gateway timeout is
// excluded: processFile sends it to ManualDir for deletion instead.
func proxyTypesRejectionReason(obj map[string]interface{}) string {
	info := policyProxyTypes(obj)
	if !info.gatewayOnly() || isDefaultGatewayMeshTimeout(obj) {
		return ""
	}
	return "spec.targetRef.proxyTypes [Gateway] is removed in 3.0 (pruned on Kubernetes, \"unknown field\" " +
		"on Universal) and the policy would select every proxy; select the gateway Dataplanes with " +
		"kind: Dataplane + labels, or delete the policy"
}

// ---- appProtocol: kafka -------------------------------------------------------

// isKafka reports the kafka protocol value (case-insensitive, as ParseProtocol).
func isKafka(v interface{}) bool {
	s, ok := v.(string)
	return ok && strings.EqualFold(s, "kafka")
}

const kafkaTCPNote = "On 2.x kafka is a TCP proxy plus Envoy's kafka_broker filter, which only " +
	"emits Kafka stats; tcp keeps the traffic handling and loses those stats."

// fixKafkaAppProtocol rewrites spec.ports[].appProtocol: kafka to tcp on a
// MeshService or MeshMultiZoneService under v3. 3.0 rejects kafka
// (validator.go: "appProtocol must be one of grpc, http, http2, tcp"). Under v2
// it only warns: 2.14 accepts kafka and its stats filter still works there.
func fixKafkaAppProtocol(obj map[string]interface{}, name, kind string, target TargetVersion) (bool, []string) {
	spec, _ := obj["spec"].(map[string]interface{})
	ports, _ := spec["ports"].([]interface{})
	var hits []string
	for i, p := range ports {
		pm, _ := p.(map[string]interface{})
		if !isKafka(pm["appProtocol"]) {
			continue
		}
		label := fmt.Sprintf("ports[%d]", i)
		if n, ok := pm["name"].(string); ok && n != "" {
			label = fmt.Sprintf("ports[%d] (%s)", i, n)
		} else if port, ok := intValue(pm["port"]); ok {
			label = fmt.Sprintf("ports[%d] (port %d)", i, port)
		}
		hits = append(hits, label)
		if target.IsV3() {
			pm["appProtocol"] = "tcp"
		}
	}
	if len(hits) == 0 {
		return false, nil
	}
	if !target.IsV3() {
		return false, []string{fmt.Sprintf(
			"%s %q: appProtocol kafka on %s is rejected by 3.0, which accepts only tcp, http, http2 and grpc "+
				"(kumahq/kuma#17831). --to-latest v3 rewrites it to tcp. %s",
			kind, name, strings.Join(hits, ", "), kafkaTCPNote)}
	}
	return true, []string{fmt.Sprintf(
		"%s %q: appProtocol kafka is rejected by 3.0 (kumahq/kuma#17831). Auto-rewritten to tcp on %s. %s",
		kind, name, strings.Join(hits, ", "), kafkaTCPNote)}
}

// fixDataplaneInboundKafka rewrites networking.inbound[].protocol: kafka to tcp
// under v3 (dataplane_validator.go rejects a protocol ParseProtocol does not
// know, and 3.0 no longer knows kafka). It runs after
// fixDataplaneInboundProtocolForV3, which copies the kuma.io/protocol tag into
// the field. Under v2 it warns on the field or the tag.
func fixDataplaneInboundKafka(obj map[string]interface{}, name string, target TargetVersion) (bool, []string) {
	net := dataplaneNetworking(obj)
	if net == nil {
		return false, nil
	}
	var hits []string
	for i, in := range inboundList(net) {
		tags, _ := in["tags"].(map[string]interface{})
		field := isKafka(in["protocol"])
		tag := isKafka(tags["kuma.io/protocol"])
		if !field && !(tag && !target.IsV3()) {
			continue
		}
		hits = append(hits, inboundLabel(in, i))
		if target.IsV3() {
			in["protocol"] = "tcp"
		}
	}
	if len(hits) == 0 {
		return false, nil
	}
	if !target.IsV3() {
		return false, []string{fmt.Sprintf(
			"Dataplane %q: inbound protocol kafka on %s is rejected by 3.0, which accepts only tcp, http, http2 "+
				"and grpc (kumahq/kuma#17831). --to-latest v3 sets protocol: tcp. %s",
			name, strings.Join(hits, ", "), kafkaTCPNote)}
	}
	return true, []string{fmt.Sprintf(
		"Dataplane %q: inbound protocol kafka is rejected by 3.0 (kumahq/kuma#17831). Auto-set protocol: tcp "+
			"on %s. %s",
		name, strings.Join(hits, ", "), kafkaTCPNote)}
}

// ---- directAccessServices -------------------------------------------------------

const annotationDirectAccessServices = "kuma.io/direct-access-services"

// splitDirectAccess separates "*" from named entries.
func splitDirectAccess(entries []string) (wildcard bool, named []string) {
	for _, e := range entries {
		e = strings.TrimSpace(e)
		switch e {
		case "":
		case "*":
			wildcard = true
		default:
			named = append(named, e)
		}
	}
	return wildcard, named
}

// directAccessNote builds the warning for named direct-access entries at where.
// changed reports that v3 already reduced the list to "*".
func directAccessNote(kind, name, where string, wildcard bool, named []string, target TargetVersion) string {
	list := strings.Join(named, ", ")
	if wildcard {
		if target.IsV3() {
			return fmt.Sprintf(
				"%s %q: %s lists \"*\" and named services (%s). 3.0 honours only \"*\", which already covers "+
					"every service on 2.x too. Auto-reduced to \"*\"; the scope does not change.",
				kind, name, where, list)
		}
		return fmt.Sprintf(
			"%s %q: %s lists \"*\" and named services (%s). 3.0 honours only \"*\"; the names are redundant "+
				"and --to-latest v3 drops them.",
			kind, name, where, list)
	}
	msg := fmt.Sprintf(
		"%s %q: %s names services (%s). 3.0 honours only \"*\" and ignores named entries silently, so this "+
			"proxy loses direct access to them and its traffic goes through the normal outbounds. There is "+
			"no per-service equivalent: use \"*\" (direct access to every endpoint in the mesh) or drop "+
			"direct access.",
		kind, name, where, list)
	if !target.IsV3() {
		msg += " Not converted by --to-latest v3."
	}
	return msg
}

// fixDataplaneDirectAccess handles networking.transparentProxying.directAccessServices.
func fixDataplaneDirectAccess(obj map[string]interface{}, name string, target TargetVersion) (bool, []string) {
	tp := dataplaneTransparentProxying(obj)
	if tp == nil {
		return false, nil
	}
	raw, ok := tp["directAccessServices"]
	if !ok {
		return false, nil
	}
	var entries []string
	for _, v := range toSlice(raw) {
		if s, ok := v.(string); ok {
			entries = append(entries, s)
		}
	}
	wildcard, named := splitDirectAccess(entries)
	if len(named) == 0 {
		return false, nil
	}
	where := "transparentProxying.directAccessServices"
	note := directAccessNote("Dataplane", name, where, wildcard, named, target)
	if target.IsV3() && wildcard {
		tp["directAccessServices"] = []interface{}{"*"}
		return true, []string{note}
	}
	return false, []string{note}
}

// fixDirectAccessAnnotations handles the kuma.io/direct-access-services
// annotation on a Pod or a workload's pod template.
func fixDirectAccessAnnotations(obj map[string]interface{}, name, kind string, target TargetVersion) (bool, []string) {
	changed := false
	var warnings []string
	for _, site := range podAnnotationSites(obj) {
		raw, ok := site.annotations[annotationDirectAccessServices].(string)
		if !ok {
			continue
		}
		wildcard, named := splitDirectAccess(strings.Split(raw, ","))
		if len(named) == 0 {
			continue
		}
		where := fmt.Sprintf("the %s annotation (%s)", annotationDirectAccessServices, site.path)
		warnings = append(warnings, directAccessNote(kind, name, where, wildcard, named, target))
		if target.IsV3() && wildcard {
			site.annotations[annotationDirectAccessServices] = "*"
			changed = true
		}
	}
	return changed, warnings
}

// ---- MeshRetry with legacy Retry field names --------------------------------

// statusCodeRetryOn returns v as a MeshRetry retryOn status-code item ("503"),
// or false when it is not an HTTP status code. MeshRetry's validator accepts any
// code net/http.StatusText knows (meshretry validator.go validateHTTPRetryOn, the
// same on v2.14.7 and master); the CRD types retryOn items as strings.
func statusCodeRetryOn(v interface{}) (string, bool) {
	var code int
	switch n := v.(type) {
	case float64:
		if n != float64(int(n)) {
			return "", false
		}
		code = int(n)
	case int:
		code = n
	case int64:
		code = int(n)
	case string:
		c, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return "", false
		}
		code = c
	default:
		return "", false
	}
	if http.StatusText(code) == "" {
		return "", false
	}
	return strconv.Itoa(code), true
}

// appendRetryOnUnique appends items to the retryOn list, skipping duplicates.
func appendRetryOnUnique(list []interface{}, items ...string) []interface{} {
	seen := map[string]bool{}
	for _, v := range list {
		seen[fmt.Sprint(v)] = true
	}
	for _, it := range items {
		if !seen[it] {
			list = append(list, it)
			seen[it] = true
		}
	}
	return list
}

// fixMeshRetryLegacyFields rewrites legacy Retry field names found in a MeshRetry
// to[].default, on both targets:
//   - http.retriableStatusCodes → numeric http.retryOn items ("503")
//   - http.retriableMethods → http.retryOn HttpMethod<Method> items
//   - tcp.maxConnectAttempts → tcp.maxConnectAttempt
//
// MeshRetry never had these fields. 2.x drops them silently, so the settings never
// applied. 3.0 rejects the write as an unknown field on the API server and kumactl
// apply, and kubectl apply prunes it (Kong/kong-mesh#11074).
// After the rewrite they take effect, on 2.x as well.
func fixMeshRetryLegacyFields(obj map[string]interface{}, name string) (bool, []string) {
	spec, _ := obj["spec"].(map[string]interface{})
	to, _ := spec["to"].([]interface{})
	changed := false
	var moved, dropped []string
	for i, entry := range to {
		em, _ := entry.(map[string]interface{})
		def, _ := em["default"].(map[string]interface{})
		if def == nil {
			continue
		}
		path := fmt.Sprintf("to[%d].default", i)

		if httpConf, ok := def["http"].(map[string]interface{}); ok {
			retryOn := toSlice(httpConf["retryOn"])
			add := []string{}
			if v, ok := httpConf["retriableStatusCodes"]; ok {
				var codes []string
				for _, c := range toSlice(v) {
					if s, ok := statusCodeRetryOn(c); ok {
						codes = append(codes, s)
					} else {
						dropped = append(dropped, fmt.Sprintf("%s.http.retriableStatusCodes value %v (not an HTTP status code)", path, c))
					}
				}
				add = append(add, codes...)
				delete(httpConf, "retriableStatusCodes")
				changed = true
				if len(codes) > 0 {
					moved = append(moved, fmt.Sprintf("%s.http.retriableStatusCodes → retryOn %v", path, codes))
				}
			}
			if v, ok := httpConf["retriableMethods"]; ok {
				var methods []string
				for _, m := range toSlice(v) {
					s := fmt.Sprint(m)
					if mapped, ok := legacyRetryMethod[strings.ToUpper(s)]; ok {
						methods = append(methods, mapped)
					} else if strings.HasPrefix(s, "HttpMethod") {
						methods = append(methods, s)
					} else {
						dropped = append(dropped, fmt.Sprintf("%s.http.retriableMethods value %q (not an HTTP method)", path, s))
					}
				}
				add = append(add, methods...)
				delete(httpConf, "retriableMethods")
				changed = true
				if len(methods) > 0 {
					moved = append(moved, fmt.Sprintf("%s.http.retriableMethods → retryOn %v", path, methods))
				}
			}
			if len(add) > 0 {
				httpConf["retryOn"] = appendRetryOnUnique(retryOn, add...)
			}
		}

		if tcp, ok := def["tcp"].(map[string]interface{}); ok {
			if v, ok := tcp["maxConnectAttempts"]; ok {
				delete(tcp, "maxConnectAttempts")
				changed = true
				if existing, has := tcp["maxConnectAttempt"]; has {
					dropped = append(dropped, fmt.Sprintf(
						"%s.tcp.maxConnectAttempts (%v; maxConnectAttempt %v already set and kept)", path, v, existing))
				} else {
					tcp["maxConnectAttempt"] = v
					moved = append(moved, fmt.Sprintf("%s.tcp.maxConnectAttempts → maxConnectAttempt", path))
				}
			}
		}
	}
	if !changed {
		return false, nil
	}
	sort.Strings(dropped)
	msg := fmt.Sprintf(
		"MeshRetry %q: legacy Retry field names that MeshRetry never had. 2.x drops them silently, so these "+
			"settings never applied. 3.0 rejects the write as an unknown field on the API server and kumactl "+
			"apply; kubectl apply prunes it with a warning (UPGRADE.md \"Resources with fields that are not in "+
			"the schema are rejected\").",
		name)
	if len(moved) > 0 {
		msg += " Auto-rewritten: " + strings.Join(moved, "; ") + ". These retries now take effect on 2.x and " +
			"3.0: check they are what you want before applying."
	}
	if len(dropped) > 0 {
		msg += " Dropped: " + strings.Join(dropped, "; ") + "."
	}
	return true, []string{msg}
}

// isDefaultGatewayMeshTimeoutDoc is isDefaultGatewayMeshTimeout on a raw document.
func isDefaultGatewayMeshTimeoutDoc(raw []byte) bool {
	var obj map[string]interface{}
	if err := yaml.Unmarshal(raw, &obj); err != nil || obj == nil {
		return false
	}
	return isDefaultGatewayMeshTimeout(obj)
}

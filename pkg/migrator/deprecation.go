package migrator

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"sigs.k8s.io/yaml"
)

// ScanForDeprecations inspects a single YAML document for deprecated fields
// that require manual action or in-place transformation.
//
// Returns warnings for each deprecation found. Some deprecations are
// automatically repaired in the returned (possibly modified) bytes; others
// are warn-only. When any auto-fix mutates the document, the object is
// re-marshalled once at the end; otherwise the original bytes are returned
// verbatim.
//
// Covered deprecations:
//   - MeshMetric spec.default.sidecar.regex → sidecar.profiles.exclude (v2.7, auto-fix)
//   - MeshHealthCheck spec.default.healthyPanicThreshold moved to MeshCircuitBreaker (v2.10)
//   - MeshTrust spec.origin deprecated → status.origin (v2.13)
//   - MeshTrafficPermission/MeshFaultInjection from[].targetRef.kind: MeshService (v2.7)
//   - MeshTrafficPermission action: ALLOW/DENY uppercase casing (Kong Mesh 2.1)
//   - MeshTrafficPermission spec.*.spiffeId → spiffeID casing (v2.12, auto-fix)
//   - MeshTrafficPermission/MeshFaultInjection from[] deprecated → rules[] API (v2.13/2.14)
//   - MeshLoadBalancingStrategy hashPolicies[].type: SourceIP → Connection + connection.sourceIP
//     (auto-fix on both targets; deprecated v2.10, removed 3.0)
//   - MeshLoadBalancingStrategy to[].default.loadBalancer.{ringHash,maglev}.hashPolicies
//     → to[].default.hashPolicies (v2.12, auto-fix)
//   - MeshService spec.ports[].protocol → appProtocol (v2.8, auto-fix)
//   - MeshMetric/MeshTrace/MeshAccessLog inline openTelemetry.endpoint → MeshOpenTelemetryBackend + backendRef (v2.14, removed 3.0)
//   - MeshAccessLog openTelemetry.attributes[].key validation tightened (v2.14)
//   - Mesh spec.routing.defaultForbidMeshExternalServiceAccess removed (3.0)
//   - Mesh spec.mtls → MeshIdentity + MeshTrust, required before 3.0 (removed in 3.0, guided)
//   - Mesh routing.zoneEgress / routing.localityAwareLoadBalancing / skipCreatingInitialPolicies / constraints /
//     PERMISSIVE mTLS backend → removed in 3.0 (warnMeshFieldsRemovedIn30, v3_mesh.go)
//   - MeshMetric prometheus.tls.mode: ActiveMTLSBackend requires a client cert on 3.0 (v3 only)
//   - Dataplane transparentProxying.redirectPortInboundV6 removed (v2.9)
//   - Dataplane transparentProxying.reachableServices uses legacy kuma.io/service names (v2.10)
//   - Any Mesh* policy with a deprecated top-level spec.targetRef.kind: MeshSubset (without
//     service-identity tags) / MeshService / MeshServiceSubset → Dataplane, or MeshHTTPRoute
//     → spec.to[].targetRef (v2.10/2.11)
//   - Mesh/MeshService/MeshExternalService/MeshMultiZoneService names that violate RFC 1035 /
//     exceed 63 chars (warning in 2.14, hard error in 3.0)
//   - Dataplane 3.0 removals (v3 rewrites in v3_dataplane.go): networking.gateway of any type,
//     inbound tags → labels, kuma.io/protocol tag → inbound protocol, tag-based outbounds →
//     backendRef, reachableServices → reachableBackends, reachableBackends name/namespace →
//     labels; warn-only advertisedAddress, redirect ports, ipFamilyMode, probes, no reachableBackends
//   - MeshService dataplaneTags → dataplaneLabels, ServiceTag identities (3.0, v3_scan.go)
//   - MeshPassthrough Domain without port, filter-chain collisions (3.0, v3_scan.go)
//   - ZoneIngress/ZoneEgress removed, MeshZoneAddress required (3.0, v3_scan.go)
//   - ContainerPatch sidecar probes on admin port 9901 (3.0, v3_scan.go)
//   - MeshLoadBalancingStrategy to[].default.localityAwareness.crossZone restricted to
//     MeshMultiZoneService targets (3.0, kuma#18210)
//   - Kong Mesh MeshOPA spec.targetRef.kind: MeshService, no longer valid in 3.0 (auto-fix under
//     v3: kind → Dataplane, labels["kuma.io/display-name"] → labels["app"])
//   - Kong Mesh MeshOPA spec.default.agentConfig / appendPolicies[].rego legacy flat DataSource
//     shape → SecureDataSource (auto-fix under v3)
//   - spec.targetRef.proxyTypes removed in 3.0 (v3: dropped when safe, Gateway-only rejected,
//     default mesh-gateways-timeout-all-<mesh> to delete), appProtocol kafka (v3: → tcp),
//     named directAccessServices (warn; v3 reduces "*"+names to "*"), MeshRetry legacy Retry
//     field names (both targets: → retryOn / maxConnectAttempt). See v3_preflight.go.
//
// Both Kubernetes format (kind/metadata) and Universal format (type/name) are supported.
func ScanForDeprecations(raw []byte, target TargetVersion) (out []byte, warnings []string) {
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

	modified := false
	fix := func(changed bool, ws []string) {
		if changed {
			modified = true
		}
		warnings = append(warnings, ws...)
	}

	switch kind {
	case "MeshMetric":
		fix(fixMeshMetricSidecarRegex(obj, name))
		warnings = append(warnings, warnInlineOtelEndpoint(obj, name, kind, target)...)
		warnings = append(warnings, warnMeshMetricActiveMTLSBackend(obj, name, target)...)
	case "MeshTrace":
		warnings = append(warnings, warnInlineOtelEndpoint(obj, name, kind, target)...)
	case "MeshAccessLog":
		warnings = append(warnings, warnInlineOtelEndpoint(obj, name, kind, target)...)
		warnings = append(warnings, warnMeshAccessLogOtelAttributeKeys(obj, name)...)
	case "MeshHealthCheck":
		if target.IsV3() {
			fix(fixHealthCheckPanicThresholdForV3(obj, name))
		} else {
			warnings = warnHealthCheckPanicThreshold(obj, name)
		}
	case "MeshTrust":
		warnings = warnMeshTrustOrigin(obj, name)
	case "MeshTrafficPermission":
		warnings = append(warnings, warnMeshServiceInFrom(obj, name, kind)...)
		warnings = append(warnings, warnMeshTrafficPermissionActionCasing(obj, name)...)
		warnings = append(warnings, warnFromDeprecatedForRulesAPI(obj, name, kind, target)...)
		fix(fixSpiffeIDCasing(obj, name))
		warnings = append(warnings, warnMTPEmptyMatches(obj, name, target)...)
	case "MeshFaultInjection":
		if target.IsV3() {
			fix(fixMeshFaultInjectionFromForV3(obj, name))
		}
		warnings = append(warnings, warnMeshServiceInFrom(obj, name, kind)...)
		warnings = append(warnings, warnFromDeprecatedForRulesAPI(obj, name, kind, target)...)
	case "MeshProxyPatch":
		warnings = append(warnings, warnMeshProxyPatchCircuitBreakers(obj, name, target)...)
	case "MeshLoadBalancingStrategy":
		fix(fixHashPoliciesPath(obj, name))
		fix(fixSourceIPHashPolicy(obj, name))
		warnings = append(warnings, warnMeshLoadBalancingStrategyCrossZoneTarget(obj, name, target)...)
	case "MeshService":
		fix(fixMeshServicePortProtocol(obj, name))
		fix(fixMeshService(obj, name, target))
		fix(fixKafkaAppProtocol(obj, name, kind, target))
	case "MeshMultiZoneService":
		fix(fixKafkaAppProtocol(obj, name, kind, target))
	case "MeshRetry":
		fix(fixMeshRetryLegacyFields(obj, name))
	case "Mesh":
		warnings = append(warnings, warnMeshForbidExternalServiceAccess(obj, name)...)
		warnings = append(warnings, warnMeshMtlsBackends(obj, name, target)...)
		warnings = append(warnings, warnMeshFieldsRemovedIn30(obj, name)...)
	case "Dataplane":
		// Rewrites first (v3 only), so the warn-only checks below report what is left.
		fix(fixDataplane(obj, name, target))
		// After fixDataplane: under v3 the kuma.io/protocol tag is already in inbound[].protocol.
		fix(fixDataplaneInboundKafka(obj, name, target))
		fix(fixDataplaneDirectAccess(obj, name, target))
		warnings = append(warnings, warnDataplaneRedirectPortInboundV6(obj, name)...)
		warnings = append(warnings, warnDataplaneReachableServices(obj, name, target)...)
		warnings = append(warnings, warnDataplaneInboundTags(obj, name, target)...)
		warnings = append(warnings, warnDataplaneV3Removals(obj, name, target)...)
	case "MeshPassthrough":
		warnings = append(warnings, warnMeshPassthroughDomains(obj, name)...)
		warnings = append(warnings, warnMeshPassthroughV3(obj, name, target)...)
	case "ZoneIngress", "ZoneEgress":
		warnings = append(warnings, warnZoneProxyRemoved(obj, name, kind, target)...)
	case "ContainerPatch":
		warnings = append(warnings, warnContainerPatchAdminProbePort(obj, name, target)...)
	case "HostnameGenerator":
		warnings = append(warnings, warnHostnameGeneratorTemplate(obj, name)...)
	case "MeshExternalService":
		if target.IsV3() {
			fix(fixMESDataSourcesForV3(obj, name))
		}
		warnings = append(warnings, warnMeshExternalServiceDataSource(obj, name, target)...)
		warnings = append(warnings, warnMESZoneLabel(obj, name, target)...)
	case "MeshOPA":
		if target.IsV3() {
			fix(fixMeshOPATargetRefForV3(obj, name))
			fix(fixMeshOPADataSourcesForV3(obj, name))
		}
		warnings = append(warnings, warnMeshOPATargetRefFields(obj, name, target)...)
		warnings = append(warnings, warnMeshOPALegacyDataSource(obj, name, target)...)
	case "MeshGlobalRateLimit":
		warnings = append(warnings, warnMeshGlobalRateLimitRemoved(obj, name, target)...)
	case "AccessRole":
		fix(scanAccessRole(obj, name, target))
	case "AccessAudit":
		fix(scanAccessAudit(obj, name, target))
	case "MeshHTTPRoute":
		warnings = append(warnings, warnMeshHTTPRouteNoCatchAll(obj, name, target)...)
		fix(fixRouteBackendRefsForV3(obj, name, kind, target))
	case "MeshTCPRoute":
		fix(fixRouteBackendRefsForV3(obj, name, kind, target))
	}

	// Generic checks applied to every Mesh* policy regardless of kind.
	if len(kind) > 4 && kind[:4] == "Mesh" {
		// Deprecated top-level spec.targetRef kinds (MeshSubset/MeshService/MeshServiceSubset
		// → Dataplane; MeshHTTPRoute → spec.to[].targetRef).
		warnings = append(warnings, warnDeprecatedTopLevelTargetRef(obj, name, kind, target)...)
		// spec.targetRef.proxyTypes is removed in 3.0 (dropped under v3 when safe).
		fix(fixProxyTypes(obj, name, kind, target))
		if target.IsV3() {
			// MeshOPA's targetRef is rewritten by fixMeshOPATargetRefForV3 above.
			if kind != "MeshOPA" {
				fix(fixRealResourceTargetRefsForV3(obj, name, kind))
			}
			fix(fixRouteBackendRefNamesForV3(obj, name, kind))
			warnings = append(warnings, warnV3ToTargetRefKinds(obj, name, kind)...)
		}
	}

	// Last: the targetRef rewrite above reads the policy's own kuma.io/zone label.
	if target.IsV3() {
		fix(stripControlPlaneLabels(obj, name, kind))
	}

	// Pods and workload pod templates: reachable-services/backends annotation rewrites.
	if knownWorkloadKinds[kind] {
		fix(fixWorkloadReachableAnnotations(obj, name, kind, target))
		fix(fixDirectAccessAnnotations(obj, name, kind, target))
	}

	// Annotation deprecations apply to any resource carrying metadata.annotations,
	// and to the pod template of a workload.
	if kind != "" {
		warnings = append(warnings, warnDeprecatedAnnotations(obj, name, kind, target)...)
	}

	// Name-format validation for kinds with strict RFC 1035 requirements. These
	// are warnings today (Kuma 2.14) and become hard errors in 3.0.
	if rfc1035Kinds[kind] && name != "" && name != "<unknown>" {
		if w := ValidateResourceName(name, kind); w != "" {
			warnings = append(warnings, w+" — becomes a hard error in Kuma 3.0.")
		}
	}

	if modified {
		if fixed, err := yaml.Marshal(obj); err == nil {
			return fixed, warnings
		}
	}
	return raw, warnings
}

// ---- MeshMetric sidecar.regex → sidecar.profiles.exclude (v2.7) ---------------

// fixMeshMetricSidecarRegex mutates obj in place, moving spec.default.sidecar.regex
// to sidecar.profiles.exclude. Returns whether the document was modified.
func fixMeshMetricSidecarRegex(obj map[string]interface{}, name string) (bool, []string) {
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return false, nil
	}
	def, ok := spec["default"].(map[string]interface{})
	if !ok {
		return false, nil
	}
	sidecar, ok := def["sidecar"].(map[string]interface{})
	if !ok {
		return false, nil
	}
	regex, ok := sidecar["regex"].(string)
	if !ok || regex == "" {
		return false, nil
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

	return true, []string{fmt.Sprintf(
		"MeshMetric %q: sidecar.regex=%q migrated to sidecar.profiles.exclude (Kuma 2.7+). "+
			"Review profiles.appendProfiles and include/exclude rules to ensure the filter set matches your intent.",
		name, regex)}
}

// ---- MeshHealthCheck healthyPanicThreshold (v2.10) ----------------------------

// warnHealthCheckPanicThreshold flags healthyPanicThreshold on a MeshHealthCheck.
// The real field lives in to[].default (that is where the legacy HealthCheck
// conversion writes it too); spec.default/spec.conf are checked for hand-written
// variants. Under --to-latest v3 fixHealthCheckPanicThresholdForV3 runs instead.
func warnHealthCheckPanicThreshold(obj map[string]interface{}, name string) []string {
	found := hasNestedField(obj, "spec", "default", "healthyPanicThreshold") ||
		hasNestedField(obj, "spec", "conf", "healthyPanicThreshold")
	if spec, ok := obj["spec"].(map[string]interface{}); ok && !found {
		to, _ := spec["to"].([]interface{})
		for _, entry := range to {
			if em, ok := entry.(map[string]interface{}); ok && hasNestedField(em, "default", "healthyPanicThreshold") {
				found = true
				break
			}
		}
	}
	if !found {
		return nil
	}
	return []string{fmt.Sprintf(
		"MeshHealthCheck %q: healthyPanicThreshold is deprecated (Kuma 2.10+) and removed in 3.0 — "+
			"set it on a MeshCircuitBreaker with the same targetRef and to[].targetRef, as "+
			"to[].default.outlierDetection.healthyPanicThreshold.",
		name)}
}

// ---- MeshTrust spec.origin (v2.13) -------------------------------------------

func warnMeshTrustOrigin(obj map[string]interface{}, name string) []string {
	if !hasNestedField(obj, "spec", "origin") {
		return nil
	}
	return []string{fmt.Sprintf(
		"MeshTrust %q: spec.origin was REMOVED in Kuma 2.13 — from the API and from the "+
			"Kubernetes CRD schema, so a manifest still setting it can be rejected as "+
			"unknown-field input (strict validation / server-side apply). The value is now "+
			"published read-only at status.origin.kri and managed by Kuma. Remove spec.origin "+
			"from this resource. Note the Kuma website still documents spec.origin with no "+
			"deprecation marker; UPGRADE.md is authoritative here.",
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
					"use kind: MeshSubset with the kuma.io/service tag instead (UPGRADE.md 2.7).",
				kind, name))
			break
		}
	}
	return warnings
}

// ---- MeshTrafficPermission/MeshFaultInjection from[] → rules[] API (v2.13/2.14) --

// warnFromDeprecatedForRulesAPI warns when an MTP/MFI uses the from[] field, which was
// deprecated in favour of the rules[] API (MeshFaultInjection in 2.13, MeshTrafficPermission
// in 2.14 — kumahq/kuma#16182). The conversion is intentionally NOT automated: the rules[]
// API matches clients by SPIFFE identity / SNI, while from[] uses tag/label selectors. The
// SPIFFE trust-domain and identity strings are not present in the source manifest (they depend
// on MeshIdentity / cluster identity config), so a mechanical rewrite would either fail or —
// worse, for MeshTrafficPermission — silently widen access. The warning gives the manual steps.
func warnFromDeprecatedForRulesAPI(obj map[string]interface{}, name, kind string, target TargetVersion) []string {
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return nil
	}
	if from, ok := spec["from"].([]interface{}); !ok || len(from) == 0 {
		return nil
	}
	switch kind {
	case "MeshTrafficPermission":
		rejected := ""
		if target.IsV3() {
			rejected = " Kuma 3.0 rejects this policy as it stands: spec.from is not in the schema " +
				"(\"spec.from: unknown field\"), and an MTP without rules[] fails with \"policy must define rules\"."
		}
		return []string{fmt.Sprintf(
			"MeshTrafficPermission %q: the from[] field is deprecated in favour of the rules[] API "+
				"(Kuma 2.14, removed in 3.0) and is NOT auto-converted. rules[] requires MeshIdentity, "+
				"matches clients by SPIFFE identity under default.{allow,deny,allowWithShadowDeny}, and is "+
				"default-deny. Manually translate each from[] source selector to a spiffeID matcher and place "+
				"it under allow / deny / allowWithShadowDeny per its Allow/Deny/AllowWithShadowDeny value. The "+
				"SPIFFE trust-domain and identity values cannot be derived from this manifest.%s",
			name, rejected)}
	case "MeshFaultInjection":
		rejected := ""
		if target.IsV3() {
			rejected = " Kuma 3.0 rejects this policy as it stands: spec.from is not in the schema " +
				"(\"spec.from: unknown field\"). Only from[] entries that all target kind: Mesh are rewritten " +
				"automatically."
		}
		return []string{fmt.Sprintf(
			"MeshFaultInjection %q: the from[] field is deprecated in favour of the rules[] API "+
				"(Kuma 2.13, removed in 3.0) and is NOT auto-converted. In rules[], each entry has matches[] "+
				"(spiffeID/sni) plus a default fault config; re-express each from[] source as a matches[] clause. "+
				"Omitting matches[] applies the fault to all inbound traffic, which widens the original scope.%s",
			name, rejected)}
	}
	return nil
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

// ---- MeshTrafficPermission spiffeId → spiffeID casing (v2.12) ------------------

// fixSpiffeIDCasing renames any spiffeId key to spiffeID anywhere in the document.
// The field was renamed for Go/JSON consistency in Kuma 2.12. The rename is
// unambiguous so it is applied wherever it appears in the spec.
func fixSpiffeIDCasing(obj map[string]interface{}, name string) (bool, []string) {
	if !renameKeyDeep(obj, "spiffeId", "spiffeID") {
		return false, nil
	}
	return true, []string{fmt.Sprintf(
		"MeshTrafficPermission %q: field spiffeId was renamed to spiffeID in Kuma 2.12 — auto-corrected.",
		name)}
}

// ---- MeshLoadBalancingStrategy SourceIP → Connection (v2.10) -----------------

// fixSourceIPHashPolicy rewrites every hashPolicies[] entry of type SourceIP to
// type Connection with connection.sourceIP: true, on both targets:
//   - 3.0 drops SourceIP from the enum, so the policy is rejected
//     ("Unsupported value: \"SourceIP\"").
//   - 2.14 still accepts it (deprecated since 2.10) but its xds hashPolicy builder
//     only handles Connection, so a SourceIP entry produces no Envoy hash policy
//     at all. The rewrite makes the source-IP hashing the manifest asks for take
//     effect, which is a behaviour change worth stating.
//
// Both validators require connection to be set for Connection (and SourceIP);
// the rewrite sets connection.sourceIP: true whatever the input carried there.
// Covers spec.to[].default.hashPolicies and the deprecated
// loadBalancer.{ringHash,maglev}.hashPolicies location.
func fixSourceIPHashPolicy(obj map[string]interface{}, name string) (bool, []string) {
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return false, nil
	}
	to, _ := spec["to"].([]interface{})
	var fixed []string
	for i, entry := range to {
		e, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		def, ok := e["default"].(map[string]interface{})
		if !ok {
			continue
		}
		lists := map[string]interface{}{
			fmt.Sprintf("spec.to[%d].default.hashPolicies", i): def["hashPolicies"],
		}
		if lb, ok := def["loadBalancer"].(map[string]interface{}); ok {
			for _, algo := range []string{"ringHash", "maglev"} {
				if a, ok := lb[algo].(map[string]interface{}); ok {
					lists[fmt.Sprintf("spec.to[%d].default.loadBalancer.%s.hashPolicies", i, algo)] = a["hashPolicies"]
				}
			}
		}
		for _, path := range sortedMapKeys(lists) {
			hps, _ := lists[path].([]interface{})
			for j, hp := range hps {
				h, ok := hp.(map[string]interface{})
				if !ok || h["type"] != "SourceIP" {
					continue
				}
				h["type"] = "Connection"
				conn, _ := h["connection"].(map[string]interface{})
				if conn == nil {
					conn = map[string]interface{}{}
					h["connection"] = conn
				}
				conn["sourceIP"] = true
				fixed = append(fixed, fmt.Sprintf("%s[%d]", path, j))
			}
		}
	}
	if len(fixed) == 0 {
		return false, nil
	}
	return true, []string{fmt.Sprintf(
		"MeshLoadBalancingStrategy %q: %s type SourceIP rewritten as type: Connection with "+
			"connection.sourceIP: true — SourceIP is deprecated since Kuma 2.10 and removed in 3.0 (rejected). "+
			"Kuma 2.14 accepts SourceIP but builds no Envoy hash policy from it, so source-IP hashing starts "+
			"to take effect once this is applied.",
		name, strings.Join(fixed, ", "))}
}

// ---- MeshLoadBalancingStrategy hashPolicies path move (v2.12) -----------------

// fixHashPoliciesPath moves spec.to[].default.loadBalancer.{ringHash,maglev}.hashPolicies
// up to spec.to[].default.hashPolicies (kumahq/kuma deprecation, v2.12). The nested
// location under the algorithm block is deprecated.
func fixHashPoliciesPath(obj map[string]interface{}, name string) (bool, []string) {
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return false, nil
	}
	to, ok := spec["to"].([]interface{})
	if !ok {
		return false, nil
	}
	changed := false
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
		for _, algo := range []string{"ringHash", "maglev"} {
			a, ok := lb[algo].(map[string]interface{})
			if !ok {
				continue
			}
			hp, ok := a["hashPolicies"].([]interface{})
			if !ok || len(hp) == 0 {
				continue
			}
			if _, exists := def["hashPolicies"]; exists {
				continue // don't clobber a value already at the new location
			}
			def["hashPolicies"] = hp
			delete(a, "hashPolicies")
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	return true, []string{fmt.Sprintf(
		"MeshLoadBalancingStrategy %q: hashPolicies moved out of loadBalancer.{ringHash,maglev} to "+
			"spec.to[].default.hashPolicies (Kuma 2.12+) — auto-corrected; verify the result.",
		name)}
}

// mlbLoadBalancers returns the loadBalancer maps from each spec.to[].default entry.
func mlbLoadBalancers(obj map[string]interface{}) []map[string]interface{} {
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return nil
	}
	to, ok := spec["to"].([]interface{})
	if !ok {
		return nil
	}
	var out []map[string]interface{}
	for _, entry := range to {
		e, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		def, ok := e["default"].(map[string]interface{})
		if !ok {
			continue
		}
		if lb, ok := def["loadBalancer"].(map[string]interface{}); ok {
			out = append(out, lb)
		}
	}
	return out
}

// ---- MeshService spec.ports[].protocol → appProtocol (v2.8) -------------------

// fixMeshServicePortProtocol renames the legacy spec.ports[].protocol field to
// appProtocol. MeshService Port only carries appProtocol in current Kuma; the old
// protocol name is silently dropped on apply, so the rename preserves intent.
func fixMeshServicePortProtocol(obj map[string]interface{}, name string) (bool, []string) {
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return false, nil
	}
	ports, ok := spec["ports"].([]interface{})
	if !ok {
		return false, nil
	}
	changed := false
	for _, p := range ports {
		pm, ok := p.(map[string]interface{})
		if !ok {
			continue
		}
		prot, ok := pm["protocol"]
		if !ok {
			continue
		}
		if _, exists := pm["appProtocol"]; exists {
			continue
		}
		pm["appProtocol"] = prot
		delete(pm, "protocol")
		changed = true
	}
	if !changed {
		return false, nil
	}
	return true, []string{fmt.Sprintf(
		"MeshService %q: spec.ports[].protocol was renamed to appProtocol (Kuma 2.8+) — auto-corrected.",
		name)}
}

// ---- Inline OpenTelemetry endpoint → MeshOpenTelemetryBackend (v2.14) ----------

// warnInlineOtelEndpoint warns when an observability policy (MeshMetric, MeshTrace,
// MeshAccessLog) configures an OpenTelemetry backend with an inline endpoint string.
// As of Kuma 2.14 the inline endpoint is deprecated in favour of a standalone
// MeshOpenTelemetryBackend resource referenced via backendRef; it is removed in 3.0.
func warnInlineOtelEndpoint(obj map[string]interface{}, name, kind string, target TargetVersion) []string {
	if !hasOtelInlineEndpoint(obj) {
		return nil
	}

	// Constraints on the replacement that are easy to get wrong when hand-writing
	// the backendRef, and that all reject on apply.
	const constraints = " When you write the backendRef: it selects the backend by " +
		"`labels` only (`name` is not supported), it is mutually exclusive with the inline " +
		"`endpoint`, and the MeshOpenTelemetryBackend itself must live in the system namespace " +
		"(kuma-system). With protocol grpc, endpoint.path must be empty."

	if target.IsV3() {
		return []string{fmt.Sprintf(
			"%s %q: an inline openTelemetry.endpoint is REMOVED in 3.0 — a policy that still sets it "+
				"fails validation. Define a MeshOpenTelemetryBackend resource and reference it via "+
				"backendRef.%s",
			kind, name, constraints)}
	}

	// Under a 2.x target the rewrite is optional and carries a real hazard if the
	// fleet is not fully on 2.14 yet, so say so rather than pushing the change.
	return []string{fmt.Sprintf(
		"%s %q: an inline openTelemetry.endpoint is deprecated in Kuma 2.14 and removed in 3.0 — "+
			"define a MeshOpenTelemetryBackend resource and reference it via backendRef.%s "+
			"Do NOT make this change while any control plane or data plane is still below 2.14: "+
			"MeshOpenTelemetryBackend does not exist before 2.14, and against an older data plane "+
			"the CP silently skips the OTel route with nothing logged. The inline endpoint keeps "+
			"working through the upgrade, so migrate it after the fleet is on 2.14.",
		kind, name, constraints)}
}

// hasOtelInlineEndpoint reports whether the document contains an openTelemetry map
// with a non-empty endpoint string at any depth.
func hasOtelInlineEndpoint(v interface{}) bool {
	switch t := v.(type) {
	case map[string]interface{}:
		if ot, ok := t["openTelemetry"].(map[string]interface{}); ok {
			if ep, ok := ot["endpoint"].(string); ok && ep != "" {
				return true
			}
		}
		for _, child := range t {
			if hasOtelInlineEndpoint(child) {
				return true
			}
		}
	case []interface{}:
		for _, child := range t {
			if hasOtelInlineEndpoint(child) {
				return true
			}
		}
	}
	return false
}

// ---- MeshAccessLog OpenTelemetry attribute key validation (v2.14) -------------

// otelAttributeKeyRe matches a valid OpenTelemetry attribute key under Kuma 2.14's
// tightened validation. Mirrors upstream otelAttributeNameRegex verbatim
// (kuma/pkg/core/validators/common_validators.go): must start with a lowercase
// letter, may contain [a-z0-9] and single '.' or '_' delimiters, and must end
// alphanumeric.
//
// Note two things upstream rejects that look innocuous: a '-' delimiter
// (`service-name`) and a leading digit (`5xx.count`). An earlier version of this
// pattern allowed both, so those keys passed the scan and were then rejected on
// apply — the exact failure this check exists to prevent.
var otelAttributeKeyRe = regexp.MustCompile(`^[a-z](?:[a-z0-9]|[._][a-z0-9])*$`)

// warnMeshAccessLogOtelAttributeKeys warns when an openTelemetry backend's
// attributes[].key would be rejected by Kuma 2.14's stricter validation (reserved
// "otel." prefix, uppercase, %placeholders%, or bad delimiters) — these break on reapply.
func warnMeshAccessLogOtelAttributeKeys(obj map[string]interface{}, name string) []string {
	var warnings []string
	collectOtelAttributeKeys(obj, func(key string) {
		reason := ""
		switch {
		case len(key) >= 5 && key[:5] == "otel.":
			reason = `the "otel." prefix is reserved`
		case !otelAttributeKeyRe.MatchString(key):
			reason = "it must start with a lowercase letter, use only [a-z0-9] with single " +
				`'.' or '_' delimiters (no '-'), and end alphanumeric; %placeholders% are ` +
				"rejected in keys (move the placeholder into the attribute value)"
		}
		if reason != "" {
			warnings = append(warnings, fmt.Sprintf(
				"MeshAccessLog %q: openTelemetry attribute key %q is rejected by Kuma 2.14's stricter validation (%s) — rename it.",
				name, key, reason))
		}
	})
	return warnings
}

// collectOtelAttributeKeys walks the document and invokes fn for every
// openTelemetry.attributes[].key string it finds.
func collectOtelAttributeKeys(v interface{}, fn func(string)) {
	switch t := v.(type) {
	case map[string]interface{}:
		if ot, ok := t["openTelemetry"].(map[string]interface{}); ok {
			if attrs, ok := ot["attributes"].([]interface{}); ok {
				for _, a := range attrs {
					if am, ok := a.(map[string]interface{}); ok {
						if k, ok := am["key"].(string); ok {
							fn(k)
						}
					}
				}
			}
		}
		for _, child := range t {
			collectOtelAttributeKeys(child, fn)
		}
	case []interface{}:
		for _, child := range t {
			collectOtelAttributeKeys(child, fn)
		}
	}
}

// ---- Mesh routing.defaultForbidMeshExternalServiceAccess (removed 3.0) --------

func warnMeshForbidExternalServiceAccess(obj map[string]interface{}, name string) []string {
	var v interface{}
	if spec, ok := obj["spec"].(map[string]interface{}); ok {
		if routing, ok := spec["routing"].(map[string]interface{}); ok {
			v = routing["defaultForbidMeshExternalServiceAccess"]
		}
	}
	if v == nil {
		if routing, ok := obj["routing"].(map[string]interface{}); ok {
			v = routing["defaultForbidMeshExternalServiceAccess"]
		}
	}
	if v == nil {
		return nil
	}
	if b, _ := v.(bool); !b {
		return []string{fmt.Sprintf(
			"Mesh %q: spec.routing.defaultForbidMeshExternalServiceAccess is removed in Kuma 3.0 — "+
				"it is set to false, so remove it with no replacement.",
			name)}
	}
	return []string{fmt.Sprintf(
		"Mesh %q: spec.routing.defaultForbidMeshExternalServiceAccess is removed in Kuma 3.0 — "+
			"control MeshExternalService access with MeshTrafficPermission instead: write an explicit "+
			"MeshTrafficPermission on each MeshExternalService that only the intended clients may reach.",
		name)}
}

// ---- Mesh spec.mtls backends → MeshIdentity + MeshTrust (advisory) ------------

// warnMeshMtlsBackends warns when a Mesh carries a legacy spec.mtls block
// (enabledBackend or backends). Kuma 3.0 removes Mesh.mtls: the field is dropped
// silently, it no longer issues certificates, and a mesh whose only identity source
// is mtls serves and accepts plaintext, so MeshTLS and MeshTrafficPermission stop
// applying to its proxies (UPGRADE.md "Mesh.mtls is removed from the API" and the
// three entries after it). The successor is MeshIdentity + MeshTrust.
//
// This is NOT auto-converted into a MeshIdentity: the cutover is a guided CA
// migration (Kuma MADR-074) whose inputs — trust domain (runtime/zone-derived),
// per-workload SPIFFE paths, and the location of the CA (CP-generated Secrets for
// builtin, operator DataSources for provided, an external system for the Kong Mesh
// types) — are not present in the Mesh manifest. The text is built per backend type.
// Under v3 TransformMeshForTarget deletes the block and calls this for the text.
func warnMeshMtlsBackends(obj map[string]interface{}, name string, target TargetVersion) []string {
	// Kubernetes format: spec.mtls; Universal format: top-level mtls.
	mtls, _ := obj["mtls"].(map[string]interface{})
	if mtls == nil {
		if spec, ok := obj["spec"].(map[string]interface{}); ok {
			mtls, _ = spec["mtls"].(map[string]interface{})
		}
	}
	if mtls == nil {
		return nil
	}
	backends, _ := mtls["backends"].([]interface{})
	enabled, _ := mtls["enabledBackend"].(string)
	if len(backends) == 0 && enabled == "" {
		return nil
	}

	// The cutover text depends on where each backend keeps its CA: builtin
	// generates it into CP-owned Secrets, provided reads it from operator-owned
	// DataSources, and the Kong Mesh enterprise types keep it in an external
	// system. Text that is true for one is wrong for the others.
	classes := classifyMtlsBackends(backends)

	msg := fmt.Sprintf("Mesh %q: ", name)
	if target.IsV3() {
		msg += "spec.mtls is REMOVED in 3.0 and has been dropped from the Mesh output. BLOCKING: " +
			"before upgrading, create a MeshIdentity (and MeshTrust) that selects every proxy in this " +
			"mesh, unless one already does. On 3.0 spec.mtls issues no certificates: a mesh whose only " +
			"identity source is spec.mtls serves and accepts plaintext, and MeshTLS and " +
			"MeshTrafficPermission stop applying to its proxies. Finish the MeshIdentity migration " +
			"first: after the upgrade, proxies still holding a legacy mesh certificate are not trusted " +
			"by MeshIdentity peers and there is no rolling path back."
	} else {
		msg += "spec.mtls is the legacy mTLS/identity model and is removed in Kuma 3.0. Migrating " +
			"to MeshIdentity (workload identity) + MeshTrust (trust domains) is required before " +
			"upgrading to 3.0: on 3.0 spec.mtls is dropped silently and issues no certificates, so a " +
			"mesh whose only identity source is spec.mtls serves plaintext and MeshTLS / " +
			"MeshTrafficPermission stop applying."
	}
	msg += " This is a guided CA cutover (see Kuma MADR-074), not a manifest rewrite: the trust " +
		"domain and the per-workload SPIFFE paths are not in this manifest."

	if len(classes.builtin) > 0 {
		msg += fmt.Sprintf(
			" Backend(s) %s are type builtin: the control plane generated the CA and stores it in the "+
				"<mesh>.ca-builtin-cert-<backend> / <mesh>.ca-builtin-key-<backend> Secrets. A MeshIdentity "+
				"with spec.provider.type: Bundled and autogenerate mints a new CA; to keep the current one, "+
				"reference those Secrets from spec.provider.bundled.ca.certificate / privateKey.",
			strings.Join(classes.builtin, ", "))
		if target.IsV3() {
			msg += " The upgrade does not delete the ca-builtin Secrets; delete them yourself once no " +
				"tooling needs the CA material."
		}
	}
	if len(classes.provided) > 0 {
		msg += fmt.Sprintf(
			" Backend(s) %s are type provided: the CA certificate and key are the DataSources in "+
				"conf.cert / conf.key, which you own. To keep that CA, reference the same material from "+
				"spec.provider.bundled.ca.certificate / privateKey of a Bundled MeshIdentity "+
				"(SecureDataSource shape: type Secret with secretRef, or type InsecureInline).",
			strings.Join(classes.provided, ", "))
	}
	if len(classes.other) > 0 {
		msg += fmt.Sprintf(
			" Backend(s) %s have a type the migrator does not recognise: find where that CA's "+
				"material lives before choosing a MeshIdentity provider.",
			strings.Join(classes.other, ", "))
	}

	// Kong Mesh 2.14 added the MeshIdentity `Extension` provider, which is the
	// native successor for the enterprise CA backends. Name the mapping when the
	// Mesh actually uses one of them — it is not on the docs site, so it is easy
	// to conclude that only Bundled and Spire exist.
	if ext := kongMeshCABackendExtensions(backends); len(ext.types) > 0 {
		if target.IsV3() {
			msg += fmt.Sprintf(
				" This Mesh uses the Kong Mesh CA backend type(s) %s, which are removed in Kong Mesh 3.0. "+
					"Replace them with a MeshIdentity using spec.provider.type: Extension with "+
					"spec.provider.extension.name set to %s.",
				strings.Join(ext.types, ", "), strings.Join(ext.names, "/"))
		} else {
			msg += fmt.Sprintf(
				" This Mesh uses the Kong Mesh CA backend type(s) %s. Kong Mesh 2.14 added a matching "+
					"MeshIdentity provider: spec.provider.type: Extension with spec.provider.extension.name "+
					"set to %s.",
				strings.Join(ext.types, ", "), strings.Join(ext.names, "/"))
		}
		msg += " The CA stays in the external system and the Extension provider issues workload " +
			"certificates from it, so the control plane generates no new CA and stores no CA key. " +
			"Note the Kuma docs still list only Bundled and Spire as providers — the " +
			"Extension provider and its acmpca/vault/certmanager configs are undocumented. " +
			"certmanager is Kubernetes-only; on Universal the CP does not register it."
	}
	if !target.IsV3() {
		msg += " The field still works on 2.x, so this output keeps it."
	}

	return []string{msg}
}

// mtlsBackendClasses groups Mesh mTLS backend names by where their CA lives.
// The Kong Mesh enterprise types are reported through kongMeshCABackendExtensions.
type mtlsBackendClasses struct {
	builtin, provided, other []string
}

// classifyMtlsBackends sorts mtls.backends[] by type into builtin, provided and
// unrecognised. Types in caBackendExtensionMap are skipped here.
func classifyMtlsBackends(backends []interface{}) mtlsBackendClasses {
	var out mtlsBackendClasses
	for _, b := range backends {
		bm, ok := b.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := bm["name"].(string)
		typ, _ := bm["type"].(string)
		if name == "" {
			name = "(unnamed)"
		}
		switch {
		case typ == "builtin":
			out.builtin = append(out.builtin, name)
		case typ == "provided":
			out.provided = append(out.provided, name)
		case caBackendExtensionMap[typ] != "":
			// reported with its Extension provider mapping
		default:
			out.other = append(out.other, name)
		}
	}
	return out
}

// caBackendExtensionMap maps a legacy Mesh mTLS backend type to the Kong Mesh
// MeshIdentity extension provider name that replaces it in 2.14.
var caBackendExtensionMap = map[string]string{
	"vault":        "vault",
	"acm":          "acmpca",
	"acmpca":       "acmpca",
	"cert-manager": "certmanager",
	"certmanager":  "certmanager",
}

type caBackendExtensions struct {
	types []string
	names []string
}

// kongMeshCABackendExtensions returns the enterprise CA backend types present in
// the Mesh together with their MeshIdentity extension provider names.
func kongMeshCABackendExtensions(backends []interface{}) caBackendExtensions {
	var out caBackendExtensions
	seen := map[string]bool{}
	for _, b := range backends {
		bm, ok := b.(map[string]interface{})
		if !ok {
			continue
		}
		typ, _ := bm["type"].(string)
		ext, ok := caBackendExtensionMap[typ]
		if !ok || seen[typ] {
			continue
		}
		seen[typ] = true
		out.types = append(out.types, typ)
		out.names = append(out.names, ext)
	}
	return out
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

// ---- Dataplane transparentProxying.reachableServices (v2.10, removed 3.0) ----

// warnDataplaneReachableServices warns when a Dataplane uses reachableServices with
// legacy kuma.io/service names. In Kuma 2.10+ with spec.meshServices.mode: Exclusive,
// service names in reachableServices must be updated to use MeshService display names.
// Under v3 the field is removed outright; fixDataplaneReachableServicesForV3 has
// already converted the decodable entries, so this reports what is left.
func warnDataplaneReachableServices(obj map[string]interface{}, name string, target TargetVersion) []string {
	tp := dataplaneTransparentProxying(obj)
	services, _ := tp["reachableServices"].([]interface{})
	if len(services) == 0 {
		return nil
	}
	if target.IsV3() {
		return []string{fmt.Sprintf(
			"Dataplane %q: transparentProxying.reachableServices is removed in 3.0 (with the "+
				"kuma.io/service-based mechanism) and these entries could not be converted: %v. Replace each "+
				"with a reachableBackends ref, e.g. `reachableBackends: {refs: [{kind: MeshService, labels: "+
				"{kuma.io/display-name: <name>}, port: <port>}]}` (add k8s.kuma.io/namespace for a "+
				"Kubernetes service).",
			name, services)}
	}
	return []string{fmt.Sprintf(
		"Dataplane %q: transparentProxying.reachableServices uses legacy kuma.io/service names (%v). "+
			"When spec.meshServices.mode is Exclusive (opt-in since Kuma 2.10; the default for meshes "+
			"without a meshServices block in Kuma 3.0), update these to the corresponding MeshService "+
			"display names (kuma.io/display-name label value), or migrate to the structured "+
			"reachableBackends.refs[] form. 3.0 removes reachableServices.",
		name, services)}
}

// ---- MeshHTTPRoute catch-all rule (3.0) --------------------------------------

// warnMeshHTTPRouteNoCatchAll warns when a MeshHTTPRoute has no catch-all rule.
//
// kuma#18268 (2026-09-01) changed an unmatched request from falling through to the
// destination to being answered with a 404, because "This is what the Gateway API requires
// of an HTTPRoute, and it is what the GAMMA conformance suite asserts" (UPGRADE.md). When
// to[].targetRef names a MeshService with no sectionName the rules cover *every* HTTP port
// of that destination, including ports no rule mentions; on a gRPC destination the client
// sees UNIMPLEMENTED. The case upstream calls out is a route written only to anchor another
// policy (MeshTimeout/MeshRetry/MeshAccessLog) via a narrow match — that pattern still
// validates on 3.0, so nothing else signals the change.
//
// v3 only: on the 2.x line unmatched requests still fall through.
func warnMeshHTTPRouteNoCatchAll(obj map[string]interface{}, name string, target TargetVersion) []string {
	if !target.IsV3() {
		return nil
	}
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return nil
	}
	to, ok := spec["to"].([]interface{})
	if !ok || len(to) == 0 {
		return nil
	}

	sawRule := false
	for _, rawTo := range to {
		toEntry, ok := rawTo.(map[string]interface{})
		if !ok {
			continue
		}
		rules, ok := toEntry["rules"].([]interface{})
		if !ok {
			continue
		}
		for _, rawRule := range rules {
			rule, ok := rawRule.(map[string]interface{})
			if !ok {
				continue
			}
			sawRule = true
			matches, _ := rule["matches"].([]interface{})
			if httpMatchesAreCatchAll(matches) {
				return nil
			}
		}
	}
	if !sawRule {
		return nil
	}

	return []string{fmt.Sprintf(
		"MeshHTTPRoute %q: none of its rules is a catch-all (empty matches[], or an unconditional "+
			"PathPrefix \"/\" match) — on Kuma 3.0 a request matching no rule is answered with a 404 "+
			"instead of falling through to the destination (kuma#18268), across every HTTP port of that "+
			"destination when to[].targetRef names a MeshService with no sectionName. On a gRPC "+
			"destination the client sees UNIMPLEMENTED. This is easy to hit by accident when the route "+
			"exists only to anchor a MeshTimeout/MeshRetry/MeshAccessLog via a narrow match. Add a "+
			"catch-all rule if traffic outside the listed matches should still reach the backend — on "+
			"the route itself when the anchored policies should cover it too, or on a second "+
			"MeshHTTPRoute when they should not.",
		name)}
}

// httpMatchesAreCatchAll reports whether a rule's matches[] lets everything through:
// either no matches at all, or an unconditional PathPrefix "/" entry (one carrying no
// other narrowing field such as method, headers or queryParams).
func httpMatchesAreCatchAll(matches []interface{}) bool {
	if len(matches) == 0 {
		return true
	}
	for _, rawMatch := range matches {
		m, ok := rawMatch.(map[string]interface{})
		if !ok {
			continue
		}
		path, ok := m["path"].(map[string]interface{})
		if !ok || path["type"] != "PathPrefix" || path["value"] != "/" {
			continue
		}
		narrowed := false
		for k := range m {
			if k != "path" {
				narrowed = true
				break
			}
		}
		if !narrowed {
			return true
		}
	}
	return false
}

// ---- Deprecated top-level spec.targetRef kinds (v2.10/2.11) -------------------

// warnDeprecatedTopLevelTargetRef warns when a policy's top-level spec.targetRef uses a
// kind that Kuma deprecated for that position. Mirrors the upstream
// validators.TopLevelTargetRefDeprecations rule (kind-agnostic, applies to every policy):
//   - MeshSubset / MeshService / MeshServiceSubset → use Dataplane with labels
//   - MeshHTTPRoute → reference it in spec.to[].targetRef instead
//
// Under --to-latest v3 the rule is stronger and uniform: every policy's validateTop on
// kuma master accepts Mesh and Dataplane only — verified across all of
// pkg/plugins/policies/*/api/v1alpha1/valid*.go plus Kong Mesh's MeshOPA, with no
// per-policy exception and no system-role exception (the SystemPolicyRole branch that used
// to allow MeshGateway now lists the same two kinds). So anything else is rejected outright,
// including MeshGateway — valid at 2.14 for several policies, but removed with the rest of
// the built-in gateway API — and MeshHTTPRoute, which was a valid top-level kind for
// MeshTimeout, MeshRetry and MeshRateLimit at 2.14. See
// site/content/topics/policy-targeting.md ("Kinds accepted per policy").
//
// These are warn-only, not auto-converted: a MeshService/MeshServiceSubset selector cannot
// be mechanically expanded to the equivalent Dataplane label set from the manifest alone
// (only the legacy Kuma-internal `_svc_` names carry enough info, and those are already
// rewritten to Dataplane by ScenarioSubset before this post-pass runs). For MeshSubset the
// tagged case is likewise handled by ScenarioSubset, so under v2 it is only flagged when it
// carries no service-identity tags; under v3 it is flagged either way, since reaching this
// post-pass with the kind intact means nothing rewrote it.
func warnDeprecatedTopLevelTargetRef(obj map[string]interface{}, name, kind string, target TargetVersion) []string {
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return nil
	}
	targetRef, ok := spec["targetRef"].(map[string]interface{})
	if !ok {
		return nil
	}
	trKind, _ := targetRef["kind"].(string)
	if trKind == "" || trKind == "Mesh" || trKind == "Dataplane" {
		return nil
	}

	// On 3.0 every non-Mesh/Dataplane kind is rejected, so the advisory always fires and
	// carries the uniform rule. On 2.x only the kinds Kuma actually deprecated are flagged.
	v3 := target.IsV3()
	suffix := ""
	if v3 {
		suffix = " Kuma 3.0 rejects it outright: Mesh and Dataplane are the only top-level " +
			"targetRef kinds any policy accepts."
	}

	switch trKind {
	case "MeshSubset":
		// Tagged MeshSubset is rewritten to Dataplane by ScenarioSubset; under v2 only warn
		// when no service-identity tags are present.
		if !v3 {
			tags, _ := targetRef["tags"].(map[string]interface{})
			for k := range tags {
				if k == "kuma.io/service" || k == "k8s.kuma.io/service-name" {
					return nil
				}
			}
		}
		return []string{fmt.Sprintf(
			"%s %q: spec.targetRef.kind MeshSubset is deprecated in Kuma 2.10+ — "+
				"use kind: Dataplane with labels instead.%s",
			kind, name, suffix)}
	case "MeshService", "MeshServiceSubset":
		return []string{fmt.Sprintf(
			"%s %q: spec.targetRef.kind %s is deprecated as a top-level target in Kuma 2.10+ — "+
				"use kind: Dataplane with labels instead.%s",
			kind, name, trKind, suffix)}
	case "MeshHTTPRoute":
		return []string{fmt.Sprintf(
			"%s %q: spec.targetRef.kind MeshHTTPRoute is deprecated as a top-level target — "+
				"reference the MeshHTTPRoute in spec.to[].targetRef instead.%s",
			kind, name, suffix)}
	case "MeshGateway":
		// Valid at 2.14 (system-role policies), removed in 3.0 with the built-in gateway API.
		if !v3 {
			return nil
		}
		return []string{fmt.Sprintf(
			"%s %q: spec.targetRef.kind MeshGateway is removed in Kuma 3.0 — the built-in "+
				"gateway API (MeshGateway, MeshGatewayRoute, MeshGatewayInstance, "+
				"MeshGatewayConfig) is deleted, so no policy can target one. Re-target the "+
				"policy at the delegated gateway's own proxies (kind: Dataplane with labels).%s",
			kind, name, suffix)}
	}

	if !v3 {
		return nil
	}
	return []string{fmt.Sprintf(
		"%s %q: spec.targetRef.kind %s is not a valid top-level target.%s",
		kind, name, trKind, suffix)}
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

// renameKeyDeep renames every occurrence of oldKey to newKey in the nested
// structure rooted at v (maps and slices). Returns whether anything changed.
// An existing newKey is never overwritten.
func renameKeyDeep(v interface{}, oldKey, newKey string) bool {
	changed := false
	switch t := v.(type) {
	case map[string]interface{}:
		if val, ok := t[oldKey]; ok {
			if _, exists := t[newKey]; !exists {
				t[newKey] = val
				delete(t, oldKey)
				changed = true
			}
		}
		for _, child := range t {
			if renameKeyDeep(child, oldKey, newKey) {
				changed = true
			}
		}
	case []interface{}:
		for _, child := range t {
			if renameKeyDeep(child, oldKey, newKey) {
				changed = true
			}
		}
	}
	return changed
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

// ---- HostnameGenerator spec.template rendered-value validation (v2.14) --------

// hostnameTemplateExprRe matches a Go-template expression inside a
// HostnameGenerator template, e.g. `{{ .DisplayName }}`.
var hostnameTemplateExprRe = regexp.MustCompile(`\{\{[^}]*\}\}`)

// rfc1123SubdomainRe matches a valid DNS subdomain, which is what a rendered
// HostnameGenerator template must produce.
var rfc1123SubdomainRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)

// warnHostnameGeneratorTemplate warns when spec.template would not render to a
// valid RFC 1123 DNS subdomain. Kuma 2.14 validates the rendered template at
// creation time; before that, a bad template silently produced a broken hostname.
//
// The check substitutes each {{ ... }} expression with a single valid label
// character before validating, so it flags only defects in the literal skeleton
// of the template (leading dot, consecutive dots, uppercase, trailing dot) and
// never guesses at what an expression will expand to.
func warnHostnameGeneratorTemplate(obj map[string]interface{}, name string) []string {
	tmpl, _ := obj["template"].(string)
	if tmpl == "" {
		if spec, ok := obj["spec"].(map[string]interface{}); ok {
			tmpl, _ = spec["template"].(string)
		}
	}
	if tmpl == "" {
		return nil
	}

	skeleton := hostnameTemplateExprRe.ReplaceAllString(tmpl, "x")
	if rfc1123SubdomainRe.MatchString(skeleton) {
		return nil
	}

	reason := "it does not render to a valid RFC 1123 DNS subdomain"
	switch {
	case strings.HasPrefix(skeleton, "."):
		reason = "it renders with a leading dot"
	case strings.HasSuffix(skeleton, "."):
		reason = "it renders with a trailing dot"
	case strings.Contains(skeleton, ".."):
		reason = "it renders with consecutive dots"
	case strings.ToLower(skeleton) != skeleton:
		reason = "it renders with uppercase characters"
	}

	return []string{fmt.Sprintf(
		"HostnameGenerator %q: spec.template %q is rejected by Kuma 2.14 — %s. Earlier versions "+
			"accepted it and silently produced a broken hostname, so this may already be "+
			"misbehaving. Fix the template before upgrading.",
		name, tmpl, reason)}
}

// ---- MeshPassthrough domain match validation (v2.14) -------------------------

// partialWildcardRe matches an unsupported partial wildcard such as `*foo.com`
// (a `*` not immediately followed by a dot). Mirrors upstream
// wildcardPartialPrefixPattern in the MeshPassthrough validator.
var partialWildcardRe = regexp.MustCompile(`^\*[^.]+`)

// l7ProtocolsNeedingPort are the protocols for which a wildcard domain match
// requires an explicit port upstream.
var l7ProtocolsNeedingPort = map[string]bool{"grpc": true, "http": true, "http2": true}

// warnMeshPassthroughDomains warns about appendMatch entries that Kuma 2.14's
// tightened MeshPassthrough validation rejects: partial wildcards, wildcard
// domains on an L7 protocol with no port, and Domain matches on tcp/mysql.
func warnMeshPassthroughDomains(obj map[string]interface{}, name string) []string {
	var warnings []string
	forEachPassthroughMatch(obj, func(m map[string]interface{}) {
		typ, _ := m["type"].(string)
		if typ != "Domain" {
			return
		}
		value, _ := m["value"].(string)
		protocol, _ := m["protocol"].(string)
		_, hasPort := m["port"]

		if partialWildcardRe.MatchString(value) {
			warnings = append(warnings, fmt.Sprintf(
				"MeshPassthrough %q: appendMatch value %q uses a partial wildcard, which Kuma 2.14 "+
					"rejects (only a full `*.` prefix is supported) — rewrite it as %q.",
				name, value, "*."+strings.TrimLeft(value, "*")))
		}
		if protocol == "tcp" || protocol == "mysql" {
			warnings = append(warnings, fmt.Sprintf(
				"MeshPassthrough %q: appendMatch value %q has type Domain with protocol %q, which is "+
					"not supported for a domain — use type IP/CIDR, or an L7 protocol.",
				name, value, protocol))
		}
		if !hasPort && strings.HasPrefix(value, "*") && l7ProtocolsNeedingPort[protocol] {
			warnings = append(warnings, fmt.Sprintf(
				"MeshPassthrough %q: appendMatch value %q is a wildcard domain on protocol %q with no "+
					"port — Kuma 2.14 rejects this because wildcard domains do not work across all "+
					"ports for layer 7 protocols. Set an explicit port.",
				name, value, protocol))
		}
	})
	return warnings
}

// forEachPassthroughMatch invokes fn for every appendMatch entry in the document,
// handling both the Kubernetes (spec.default) and Universal (top-level) layouts.
func forEachPassthroughMatch(obj map[string]interface{}, fn func(map[string]interface{})) {
	visit := func(container map[string]interface{}) {
		def, _ := container["default"].(map[string]interface{})
		if def == nil {
			return
		}
		matches, _ := def["appendMatch"].([]interface{})
		for _, m := range matches {
			if mm, ok := m.(map[string]interface{}); ok {
				fn(mm)
			}
		}
	}
	if spec, ok := obj["spec"].(map[string]interface{}); ok {
		visit(spec)
	}
	visit(obj)
}

// ---- Deprecated / removed Kubernetes annotations (2.13, 2.14, 3.0) -----------

// podAnnotationDeprecation holds the message for each target. An empty v2 means the
// annotation is still valid on 2.x and is flagged only under --to-latest v3. An
// empty v3 means the v2 message applies to 3.0 as well.
type podAnnotationDeprecation struct {
	v2 string
	v3 string
}

func (d podAnnotationDeprecation) message(target TargetVersion) string {
	if target.IsV3() && d.v3 != "" {
		return d.v3
	}
	return d.v2
}

const (
	reasonEbpfRemoved = "is removed in 3.0 with the eBPF transparent proxy and silently ignored. Migrate to the " +
		"iptables transparent proxy (the default) and run `kumactl uninstall ebpf` with the pre-upgrade " +
		"version before upgrading, or the pinned BPF programs keep intercepting traffic"
	reasonPrometheus = "is deprecated in favour of the MeshMetric policy — move the scrape configuration into a " +
		"MeshMetric resource targeting this workload"
	reasonIgnored = "Because it is ignored rather than rejected, a Pod still carrying it is silently running " +
		"with the default rather than the value set here"
)

// deprecatedPodAnnotations lists annotations deprecated on 2.14 or removed in 3.0.
//
// v2 entries mirror PodAnnotationDeprecations in
// kuma/pkg/plugins/runtime/k8s/metadata/annotations.go on release-2.14. v3-only
// entries come from UPGRADE.md "Upgrade to 3.0.0" and were checked against the 3.0
// annotations.go, where none of them is defined any more.
//
// The list is deliberately exact-match rather than prefix-match. Only
// prometheus.metrics.kuma.io/port and /path are deprecated; the
// aggregate-<name>-(port|path|enabled|address) family is NOT, so a prefix match
// would produce false positives on a supported configuration.
var deprecatedPodAnnotations = map[string]podAnnotationDeprecation{
	"kuma.io/builtindns": {v2: "is no longer supported and is IGNORED — use \"kuma.io/builtin-dns\" instead. " +
		reasonIgnored},
	"kuma.io/builtindnsport": {v2: "is no longer supported and is IGNORED — use \"kuma.io/builtin-dns-port\" " +
		"instead. " + reasonIgnored},
	"kuma.io/virtual-probes": {
		v2: "is deprecated and will be removed in a future release. The default " +
			"flipped to disabled in Kuma 2.13; the replacement is the Application Probe Proxy. Confirm " +
			"probes still work after upgrading",
		v3: "is removed in 3.0 with Virtual Probes. The injector no longer rewrites probes to a virtual probes " +
			"listener; Application Probe Proxy (kuma.io/application-probe-proxy-port) is the only probe-rewriting " +
			"path and is on by default. Remove the annotation and re-inject the pod after upgrading",
	},
	"kuma.io/virtual-probes-port": {
		v2: "is being replaced by \"kuma.io/application-probe-proxy-port\"",
		v3: "is removed in 3.0 with Virtual Probes — use \"kuma.io/application-probe-proxy-port\" and " +
			"re-inject the pod after upgrading",
	},
	"kuma.io/sidecar-injection": {v2: "is not supported as an annotation — it must be set as a LABEL. " +
		"As an annotation it has no effect, so injection is following whatever the namespace default is"},
	"prometheus.metrics.kuma.io/port": {v2: reasonPrometheus},
	"prometheus.metrics.kuma.io/path": {v2: reasonPrometheus},

	// 3.0 removals.
	"kuma.io/ingress": {v3: "is removed in 3.0 and ignored: the Pod is no longer turned into a ZoneIngress. It " +
		"is reconciled as a regular Dataplane if it has a sidecar, otherwise ignored. Replace it with the chart's " +
		"zone proxy Deployment (meshes[].ingress.enabled) or label its Service k8s.kuma.io/zone-proxy-type"},
	"kuma.io/egress": {v3: "is removed in 3.0 and ignored: the Pod is no longer turned into a ZoneEgress. It " +
		"is reconciled as a regular Dataplane if it has a sidecar, otherwise ignored. Replace it with the chart's " +
		"zone proxy Deployment (meshes[].egress.enabled) or label its Service k8s.kuma.io/zone-proxy-type"},
	"kuma.io/ingress-public-address": {v3: "is removed in 3.0 — the zone's public address comes from " +
		"MeshZoneAddress (created automatically on Kubernetes for each mesh-scoped zone proxy Service)"},
	"kuma.io/ingress-public-port": {v3: "is removed in 3.0 — the zone's public port comes from " +
		"MeshZoneAddress (created automatically on Kubernetes for each mesh-scoped zone proxy Service)"},
	"kuma.io/mesh": {v3: "is no longer honored as an annotation in 3.0 — only the kuma.io/mesh LABEL (on the " +
		"resource, or on its Namespace) assigns the mesh. A resource with only the annotation lands in the " +
		"default mesh. Switch it to a label before upgrading"},
	annotationReachableServices: {v3: "is removed in 3.0 with the kuma.io/service-based reachable services. " +
		"Use kuma.io/reachable-backends with `refs: [{kind: MeshService, labels: {kuma.io/display-name: <name>, " +
		"k8s.kuma.io/namespace: <ns>}, port: <port>}]`"},
	"kuma.io/xds-transport-protocol-variant": {v3: "is removed in 3.0 — Delta xDS is the only protocol. Remove " +
		"it; for a zero-downtime upgrade set KUMA_EXPERIMENTAL_DELTA_XDS=true on the old control plane and " +
		"restart the proxies first"},
	"kuma.io/builtin-dns-logging": {v3: "is removed in 3.0 with CoreDNS (the embedded DNS proxy is always used)"},
	"kuma.io/gateway": {v3: "is removed in 3.0 and ignored: the pod is injected like any other workload and its " +
		"inbound traffic is redirected through Envoy. Replace it with traffic.kuma.io/exclude-inbound-ports " +
		"listing every port the gateway listens on (comma-separated, no all-ports value), and annotate the " +
		"fronting Service kuma.io/ignore: \"true\""},
	"kuma.io/transparent-proxying-ebpf":                          {v3: reasonEbpfRemoved},
	"kuma.io/transparent-proxying-ebpf-bpf-fs-path":              {v3: reasonEbpfRemoved},
	"kuma.io/transparent-proxying-ebpf-cgroup-path":              {v3: reasonEbpfRemoved},
	"kuma.io/transparent-proxying-ebpf-tc-attach-iface":          {v3: reasonEbpfRemoved},
	"kuma.io/transparent-proxying-ebpf-instance-ip-env-var-name": {v3: reasonEbpfRemoved},
	"kuma.io/transparent-proxying-ebpf-programs-source-path":     {v3: reasonEbpfRemoved},
}

// warnDeprecatedAnnotations warns about annotations deprecated on 2.13/2.14 or
// removed in 3.0, on the resource's own metadata.annotations and on a workload's
// pod template (spec.template / spec.jobTemplate.spec.template). Unlike
// ScanKumaAnnotations (which reports the yes/no boolean spelling), these are
// deprecated wholesale in favour of a policy resource, a renamed annotation, or a
// label.
//
// Several of these are ignored rather than rejected by the control plane, so the
// only signal an operator gets is a CP log line at Pod admission — which is
// easily missed. That is why they are surfaced here.
func warnDeprecatedAnnotations(obj map[string]interface{}, name, kind string, target TargetVersion) []string {
	var warnings []string
	for _, site := range podAnnotationSites(obj) {
		where := ""
		if site.path != "metadata.annotations" {
			where = " (" + site.path + ")"
		}
		// Sort for deterministic warning order across runs.
		for _, k := range sortedMapKeys(site.annotations) {
			if d, ok := deprecatedPodAnnotations[k]; ok {
				if reason := d.message(target); reason != "" {
					warnings = append(warnings, fmt.Sprintf("%s %q: annotation %q%s %s.", kind, name, k, where, reason))
				}
			}
		}
		if target.IsV3() {
			if v, ok := site.annotations["kuma.io/application-probe-proxy-port"]; ok && fmt.Sprint(v) == "0" {
				warnings = append(warnings, fmt.Sprintf(
					"%s %q: annotation \"kuma.io/application-probe-proxy-port\": \"0\"%s no longer falls back to "+
						"virtual probes in 3.0 — it leaves the pod's original probes untouched, so they hit the "+
						"application through the mesh. Remove the annotation to keep Application Probe Proxy, or "+
						"exclude the probe ports from inbound redirection.",
					kind, name, where))
			}
		}
	}
	return warnings
}

// ---- Dataplane networking.inbound[].tags (removed 3.0) ----------------------

// warnDataplaneInboundTags warns when a Dataplane declares inbound tags.
//
// In 3.0 these are dropped SILENTLY rather than rejected: the proto field is
// `reserved` and the CP loads with AllowUnknownFields, so a manifest that still
// carries them applies cleanly and simply loses the tags. Anything that selected
// on those tags (policy targetRefs, MeshService generation) then stops matching
// with no error anywhere. That silence is why this is worth flagging.
//
// Fires only under TargetV3. Inbound tags are mandatory in the 2.x line —
// kuma.io/service is present on every Universal Dataplane — so a forward-looking
// advisory under TargetV2 would fire on every Dataplane in the input and say
// nothing the operator can act on yet.
func warnDataplaneInboundTags(obj map[string]interface{}, name string, target TargetVersion) []string {
	if !target.IsV3() {
		return nil
	}
	networking, _ := obj["networking"].(map[string]interface{})
	if networking == nil {
		if spec, ok := obj["spec"].(map[string]interface{}); ok {
			networking, _ = spec["networking"].(map[string]interface{})
		}
	}
	if networking == nil {
		return nil
	}
	inbounds, _ := networking["inbound"].([]interface{})
	tagged := 0
	for _, in := range inbounds {
		im, ok := in.(map[string]interface{})
		if !ok {
			continue
		}
		if tags, ok := im["tags"].(map[string]interface{}); ok && len(tags) > 0 {
			tagged++
		}
	}
	if tagged == 0 {
		return nil
	}

	return []string{fmt.Sprintf(
		"Dataplane %q: %d inbound(s) declare networking.inbound[].tags, which 3.0 REMOVES — and "+
			"removes silently: the field is reserved in the proto and unknown fields are allowed, "+
			"so this manifest will apply without error and simply lose the tags. Any policy "+
			"targetRef or MeshService generation that selects on them stops matching, with nothing "+
			"logged. Move the tags to Dataplane labels before applying to 3.0.",
		name, tagged)}
}

// ---- MeshLoadBalancingStrategy crossZone target restriction (3.0) -----------

// warnMeshLoadBalancingStrategyCrossZoneTarget warns when a to[] entry sets
// localityAwareness.crossZone but targets something other than
// MeshMultiZoneService. Kuma 3.0 rejects this at create/update (kuma#18210;
// validator message: "crossZone is only supported when targetRef.kind is
// MeshMultiZoneService") — crossZone only makes sense for a service that
// actually spans zones.
//
// Fires only under TargetV3: this restriction landed on 3.0-dev master and is
// not in the 2.14 line, so a 2.x manifest using crossZone on another target
// kind is not (yet) rejected.
func warnMeshLoadBalancingStrategyCrossZoneTarget(obj map[string]interface{}, name string, target TargetVersion) []string {
	if !target.IsV3() {
		return nil
	}
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return nil
	}
	to, ok := spec["to"].([]interface{})
	if !ok {
		return nil
	}

	var warnings []string
	for i, entry := range to {
		e, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		def, ok := e["default"].(map[string]interface{})
		if !ok {
			continue
		}
		la, ok := def["localityAwareness"].(map[string]interface{})
		if !ok {
			continue
		}
		if _, hasCrossZone := la["crossZone"]; !hasCrossZone {
			continue
		}
		tr, _ := e["targetRef"].(map[string]interface{})
		trKind, _ := tr["kind"].(string)
		if trKind == "MeshMultiZoneService" {
			continue
		}
		warnings = append(warnings, fmt.Sprintf(
			"MeshLoadBalancingStrategy %q: to[%d].default.localityAwareness.crossZone is set but "+
				"to[%d].targetRef.kind is %q, not MeshMultiZoneService — Kuma 3.0 rejects this at "+
				"create/update (crossZone is only supported when the target is a MeshMultiZoneService). "+
				"Remove crossZone, or retarget this entry at a MeshMultiZoneService.",
			name, i, i, trKind))
	}
	return warnings
}

// ---- MeshExternalService TLS DataSource → SecureDataSource (3.0) -------------

// legacyDataSourceKeys are the pre-3.0 DataSource variants on a
// MeshExternalService TLS verification field.
var legacyDataSourceKeys = []string{"inline", "inlineString", "secret"}

// warnMeshExternalServiceDataSource warns when spec.tls.verification.{caCert,
// clientCert,clientKey} uses the legacy DataSource shape that 3.0 replaces with
// SecureDataSource.
//
// Under v3, fixMESDataSourcesForV3 runs first and rewrites `inlineString` and
// `secret`, so this only reports what is left: `inline`, which stays warn-only
// because the conversion is not a pure rename — `inline` carried base64 and its
// successor `insecureInline.value` carries plain text, so rewriting means
// decoding credential material and re-emitting it in the clear. That is a
// decision for the operator, not a silent transform.
//
// Under v2 every variant is reported as a forward-looking advisory; 2.14.6 and
// later accept the new shape, so the rewrite can be done on 2.x.
func warnMeshExternalServiceDataSource(obj map[string]interface{}, name string, target TargetVersion) []string {
	verification := mesTLSVerification(obj)
	if verification == nil {
		return nil
	}

	var warnings []string
	for _, field := range mesDataSourceFields {
		ds, _ := verification[field].(map[string]interface{})
		if ds == nil {
			continue
		}
		for _, legacy := range legacyDataSourceKeys {
			if _, ok := ds[legacy]; !ok {
				continue
			}
			replacement := "secretRef"
			extra := ""
			if legacy == "inline" {
				replacement = "type: InsecureInline + insecureInline.value"
				extra = " Note inline was base64-encoded while insecureInline.value is plain text, " +
					"so this is a decode, not a rename."
			} else if legacy == "inlineString" {
				replacement = "type: InsecureInline + insecureInline.value"
			} else {
				replacement = "type: Secret + secretRef"
			}
			if !target.IsV3() {
				extra += " Kuma 2.14.6 and later accept both shapes, so rewrite it on 2.14.6+ before " +
					"upgrading the global control plane to 3.0."
			}
			warnings = append(warnings, fmt.Sprintf(
				"MeshExternalService %q: spec.tls.verification.%s.%s uses the legacy DataSource shape, %s. "+
					"Replace it with %s.%s",
				name, field, legacy,
				target.removalNote("3.0 replaces DataSource with SecureDataSource"),
				replacement, extra))
		}
	}
	return warnings
}

// ---- Kong Mesh MeshOPA targetRef name/namespace/mesh/kind (removed 3.0) ----

// warnMeshOPATargetRefFields warns when a Kong Mesh MeshOPA policy scopes its
// targetRef with name/namespace/mesh (all removed in 3.0), or uses
// kind: MeshService (no longer a valid MeshOPA targetRef kind in 3.0).
//
// Under a v3 target this scanner runs AFTER ScanForDeprecations has already
// called fixMeshOPATargetRefForV3, so on a document that fix touched, every
// check below finds nothing left to report — the fix's own warning already
// describes what changed. This only fires in earnest under v2 (forward-looking
// advisory; the fix does not run) or when the fix declined to touch a
// conflicting label (still present under v3 too).
//
// The failure mode for `name` is scope-widening rather than an error: a MeshOPA
// that used `name` to bind its rego to a single service silently starts
// applying to every service matching `kind`, changing which requests are
// evaluated.
func warnMeshOPATargetRefFields(obj map[string]interface{}, name string, target TargetVersion) []string {
	spec, _ := obj["spec"].(map[string]interface{})
	if spec == nil {
		return nil
	}
	targetRef, _ := spec["targetRef"].(map[string]interface{})
	if targetRef == nil {
		return nil
	}

	var warnings []string

	var present []string
	for _, f := range []string{"name", "namespace", "mesh"} {
		if v, ok := targetRef[f]; ok && v != nil && v != "" {
			present = append(present, f)
		}
	}
	if len(present) > 0 {
		msg := fmt.Sprintf(
			"MeshOPA %q: spec.targetRef.{%s} %s. For kind: MeshService with name: <svc>, the "+
				"documented rewrite is kind: Dataplane with labels: {app: <svc>}; namespace and mesh "+
				"are dropped.",
			name, strings.Join(present, ","),
			target.removalNote("select the proxies through labels instead"))

		// Only `name` carries the scope-widening hazard; namespace/mesh are pruned.
		for _, f := range present {
			if f == "name" {
				msg += " This one is not a clean failure: without `name` the policy widens to every " +
					"service matching `kind`, so the rego starts evaluating requests it never saw before. " +
					"Verify the label selector reproduces the original scope exactly."
				break
			}
		}
		warnings = append(warnings, msg)
	}

	if trKind, _ := targetRef["kind"].(string); trKind == "MeshService" {
		if target.IsV3() {
			warnings = append(warnings, fmt.Sprintf(
				"MeshOPA %q: spec.targetRef.kind MeshService is removed in 3.0 — only Mesh and Dataplane "+
					"remain valid targetRef kinds. Use kind: Dataplane with labels[\"app\"] instead.",
				name))
		} else {
			warnings = append(warnings, fmt.Sprintf(
				"MeshOPA %q: spec.targetRef.kind MeshService works in 2.14, but Kuma 3.0 narrows "+
					"MeshOPA's targetRef to Mesh and Dataplane only. Plan the move to kind: Dataplane "+
					"with labels[\"app\"] before upgrading.",
				name))
		}
	}

	return warnings
}

// ---- Kong Mesh MeshOPA DataSource → SecureDataSource (removed 3.0) --------

// warnMeshOPALegacyDataSource warns when a MeshOPA's spec.default.agentConfig
// or any spec.default.appendPolicies[].rego still uses the legacy flat
// DataSource shape (secret/inline/inlineString), which 3.0 replaces with the
// discriminated SecureDataSource type (UPGRADE_km.md: "MeshOPA data sources
// use the SecureDataSource shape"). A MeshOPA in the old shape is rejected at
// write time on 3.0 — the missing `type` discriminator is a validation
// violation.
//
// Under v3 this runs after ScanForDeprecations has already called
// fixMeshOPADataSourcesForV3, so it only fires in earnest under v2, where the
// rewrite is forward-looking rather than performed.
func warnMeshOPALegacyDataSource(obj map[string]interface{}, name string, target TargetVersion) []string {
	spec, _ := obj["spec"].(map[string]interface{})
	if spec == nil {
		return nil
	}
	def, _ := spec["default"].(map[string]interface{})
	if def == nil {
		return nil
	}

	var fields []string
	if agentConfig, ok := def["agentConfig"].(map[string]interface{}); ok && hasLegacyOPADataSourceShape(agentConfig) {
		fields = append(fields, "agentConfig")
	}
	if policies, ok := def["appendPolicies"].([]interface{}); ok {
		for i, p := range policies {
			pm, ok := p.(map[string]interface{})
			if !ok {
				continue
			}
			if rego, ok := pm["rego"].(map[string]interface{}); ok && hasLegacyOPADataSourceShape(rego) {
				fields = append(fields, fmt.Sprintf("appendPolicies[%d].rego", i))
			}
		}
	}
	if len(fields) == 0 {
		return nil
	}

	if target.IsV3() {
		return []string{fmt.Sprintf(
			"MeshOPA %q: spec.default.%s still use the legacy flat DataSource shape "+
				"(secret/inline/inlineString), which 3.0 removed in favour of the discriminated "+
				"SecureDataSource type (type + insecureInline.value / secretRef). A MeshOPA in the old "+
				"shape is rejected at write time.",
			name, strings.Join(fields, ", spec.default."))}
	}
	return []string{fmt.Sprintf(
		"MeshOPA %q: spec.default.%s use the legacy flat DataSource shape (secret/inline/inlineString), "+
			"which works in 2.14 but is removed in 3.0 in favour of the discriminated SecureDataSource "+
			"type (type + insecureInline.value / secretRef). Rewrite before upgrading to 3.0.",
		name, strings.Join(fields, ", spec.default."))}
}

// ---- Kong Mesh MeshGlobalRateLimit (removed 3.0) ----------------------------

// warnMeshGlobalRateLimitRemoved warns that the enterprise MeshGlobalRateLimit
// policy is removed in 3.0. Leftover objects go inert rather than being rejected,
// and Helm does not delete the CRD, so the resource can linger and look healthy
// while enforcing nothing.
//
// Under v3 the pipeline keeps the document out of the apply tree (it goes to
// ManualDir) and the report lists it under "Delete Before Upgrading to 3.0".
func warnMeshGlobalRateLimitRemoved(_ map[string]interface{}, name string, target TargetVersion) []string {
	if target.IsV3() {
		return []string{fmt.Sprintf(
			"MeshGlobalRateLimit %q: removed in 3.0 with no in-mesh replacement, together with its CP "+
				"support, the rate-limit service in the Helm chart (ratelimit.*, global.ratelimit.*), "+
				"the KMESH_GLOBAL_RATE_LIMIT_* env vars and the kmesh.globalRateLimit CP config. Delete "+
				"it before upgrading and remove the ratelimit.* / global.ratelimit.* Helm overrides. It "+
				"is not part of any apply step: the copy is under %s/ and the report lists it under "+
				"\"Delete Before Upgrading to 3.0\". A leftover object is not rejected, it just enforces "+
				"nothing. Helm does not delete the CRD; remove it after upgrading with "+
				"kubectl delete crd meshglobalratelimits.kuma.io. Plan rate limiting at the gateway or "+
				"with MeshRateLimit.",
			name, ManualDir)}
	}
	return []string{fmt.Sprintf(
		"MeshGlobalRateLimit %q: works in 2.14, removed in 3.0 with no in-mesh replacement. Delete it "+
			"before upgrading to 3.0: a leftover object is not rejected, it just enforces nothing. Plan "+
			"rate limiting at the gateway or with MeshRateLimit.",
		name)}
}

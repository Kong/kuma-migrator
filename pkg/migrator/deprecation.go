package migrator

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
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
//   - MeshLoadBalancingStrategy hashPolicies[].type: SourceIP → Connection (v2.10)
//   - MeshLoadBalancingStrategy to[].default.loadBalancer.{ringHash,maglev}.hashPolicies
//     → to[].default.hashPolicies (v2.12, auto-fix)
//   - MeshService spec.ports[].protocol → appProtocol (v2.8, auto-fix)
//   - MeshMetric/MeshTrace/MeshAccessLog inline openTelemetry.endpoint → MeshOpenTelemetryBackend + backendRef (v2.14, removed 3.0)
//   - MeshAccessLog openTelemetry.attributes[].key validation tightened (v2.14)
//   - Mesh spec.routing.defaultForbidMeshExternalServiceAccess removed (3.0)
//   - Mesh spec.mtls.backends → advisory: MeshIdentity + MeshTrust successor model (2.12+, guided)
//   - Mesh without spec.meshServices → advisory: 3.0 defaults the mode to Exclusive (kuma#17102)
//   - Dataplane transparentProxying.redirectPortInboundV6 removed (v2.9)
//   - Dataplane transparentProxying.reachableServices uses legacy kuma.io/service names (v2.10)
//   - Any Mesh* policy with a deprecated top-level spec.targetRef.kind: MeshSubset (without
//     service-identity tags) / MeshService / MeshServiceSubset → Dataplane, or MeshHTTPRoute
//     → spec.to[].targetRef (v2.10/2.11)
//   - Mesh/MeshService/MeshExternalService/MeshMultiZoneService names that violate RFC 1035 /
//     exceed 63 chars (warning in 2.14, hard error in 3.0)
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
	case "MeshTrace":
		warnings = append(warnings, warnInlineOtelEndpoint(obj, name, kind, target)...)
	case "MeshAccessLog":
		warnings = append(warnings, warnInlineOtelEndpoint(obj, name, kind, target)...)
		warnings = append(warnings, warnMeshAccessLogOtelAttributeKeys(obj, name)...)
	case "MeshHealthCheck":
		warnings = warnHealthCheckPanicThreshold(obj, name)
	case "MeshTrust":
		warnings = warnMeshTrustOrigin(obj, name)
	case "MeshTrafficPermission":
		warnings = append(warnings, warnMeshServiceInFrom(obj, name, kind)...)
		warnings = append(warnings, warnMeshTrafficPermissionActionCasing(obj, name)...)
		warnings = append(warnings, warnFromDeprecatedForRulesAPI(obj, name, kind)...)
		fix(fixSpiffeIDCasing(obj, name))
	case "MeshFaultInjection":
		warnings = append(warnings, warnMeshServiceInFrom(obj, name, kind)...)
		warnings = append(warnings, warnFromDeprecatedForRulesAPI(obj, name, kind)...)
	case "MeshLoadBalancingStrategy":
		warnings = append(warnings, warnSourceIPHashPolicy(obj, name)...)
		fix(fixHashPoliciesPath(obj, name))
	case "MeshService":
		fix(fixMeshServicePortProtocol(obj, name))
	case "Mesh":
		warnings = append(warnings, warnMeshForbidExternalServiceAccess(obj, name)...)
		warnings = append(warnings, warnMeshMtlsBackends(obj, name)...)
		warnings = append(warnings, warnMeshServicesDefaultFlip(obj, name)...)
	case "Dataplane":
		warnings = append(warnings, warnDataplaneRedirectPortInboundV6(obj, name)...)
		warnings = append(warnings, warnDataplaneReachableServices(obj, name)...)
		warnings = append(warnings, warnDataplaneInboundTags(obj, name, target)...)
	case "MeshPassthrough":
		warnings = append(warnings, warnMeshPassthroughDomains(obj, name)...)
	case "HostnameGenerator":
		warnings = append(warnings, warnHostnameGeneratorTemplate(obj, name)...)
	case "MeshExternalService":
		warnings = append(warnings, warnMeshExternalServiceDataSource(obj, name, target)...)
	case "MeshOPA":
		warnings = append(warnings, warnMeshOPATargetRefFields(obj, name, target)...)
	case "MeshGlobalRateLimit":
		warnings = append(warnings, warnMeshGlobalRateLimitRemoved(obj, name, target)...)
	}

	// Generic checks applied to every Mesh* policy regardless of kind.
	if len(kind) > 4 && kind[:4] == "Mesh" {
		// Deprecated top-level spec.targetRef kinds (MeshSubset/MeshService/MeshServiceSubset
		// → Dataplane; MeshHTTPRoute → spec.to[].targetRef).
		warnings = append(warnings, warnDeprecatedTopLevelTargetRef(obj, name, kind)...)
	}

	// Annotation deprecations apply to any resource carrying metadata.annotations.
	if kind != "" {
		warnings = append(warnings, warnDeprecatedAnnotations(obj, name, kind)...)
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
					"use kind: Dataplane with labels instead.",
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
func warnFromDeprecatedForRulesAPI(obj map[string]interface{}, name, kind string) []string {
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return nil
	}
	if from, ok := spec["from"].([]interface{}); !ok || len(from) == 0 {
		return nil
	}
	switch kind {
	case "MeshTrafficPermission":
		return []string{fmt.Sprintf(
			"MeshTrafficPermission %q: the from[] field is deprecated in favour of the rules[] API "+
				"(Kuma 2.14, removed in 3.0) and is NOT auto-converted. rules[] requires MeshIdentity, "+
				"matches clients by SPIFFE identity under default.{allow,deny,allowWithShadowDeny}, and is "+
				"default-deny. Manually translate each from[] source selector to a spiffeID matcher and place "+
				"it under allow / deny / allowWithShadowDeny per its Allow/Deny/AllowWithShadowDeny value. The "+
				"SPIFFE trust-domain and identity values cannot be derived from this manifest.",
			name)}
	case "MeshFaultInjection":
		return []string{fmt.Sprintf(
			"MeshFaultInjection %q: the from[] field is deprecated in favour of the rules[] API "+
				"(Kuma 2.13, removed in 3.0) and is NOT auto-converted. In rules[], each entry has matches[] "+
				"(spiffeID/sni) plus a default fault config; re-express each from[] source as a matches[] clause. "+
				"Omitting matches[] applies the fault to all inbound traffic, which widens the original scope.",
			name)}
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

func warnSourceIPHashPolicy(obj map[string]interface{}, name string) []string {
	for _, lb := range mlbLoadBalancers(obj) {
		for _, key := range []string{"hashPolicies", ""} {
			var hashPolicies []interface{}
			if key == "" {
				// also check nested ringHash/maglev hashPolicies
				for _, algo := range []string{"ringHash", "maglev"} {
					if a, ok := lb[algo].(map[string]interface{}); ok {
						if hp, ok := a["hashPolicies"].([]interface{}); ok {
							hashPolicies = append(hashPolicies, hp...)
						}
					}
				}
			} else if hp, ok := lb[key].([]interface{}); ok {
				hashPolicies = hp
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
	}
	return nil
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
	if !hasNestedField(obj, "spec", "routing", "defaultForbidMeshExternalServiceAccess") &&
		!hasNestedField(obj, "routing", "defaultForbidMeshExternalServiceAccess") {
		return nil
	}
	return []string{fmt.Sprintf(
		"Mesh %q: spec.routing.defaultForbidMeshExternalServiceAccess is removed in Kuma 3.0 — "+
			"control MeshExternalService access with MeshTrafficPermission instead.",
		name)}
}

// ---- Mesh spec.mtls backends → MeshIdentity + MeshTrust (advisory) ------------

// warnMeshMtlsBackends emits a forward-looking advisory when a Mesh defines legacy mTLS
// backends (spec.mtls.backends, builtin/provided/vault). Kuma 2.12+ introduces MeshIdentity
// (workload identity) + MeshTrust (trust domains) as the successor model, and the experimental
// SPIFFE rules[] API for MeshTrafficPermission requires MeshIdentity. This is intentionally
// NOT auto-converted: the cutover is a guided multi-step CA migration (Kuma MADR-074) whose
// inputs — trust domain (runtime/zone-derived), per-workload SPIFFE paths, and CA key material
// (CP-generated Secret / DataSource / Vault) — are not present in the Mesh manifest, and the
// builtin path mints a brand-new CA (a trust-root change). spec.mtls is not deprecated, so
// doing nothing is currently safe.
func warnMeshMtlsBackends(obj map[string]interface{}, name string) []string {
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
	backends, ok := mtls["backends"].([]interface{})
	if !ok || len(backends) == 0 {
		return nil
	}

	msg := fmt.Sprintf(
		"Mesh %q: spec.mtls.backends is the legacy mTLS/identity model. Kuma 2.12+ introduces "+
			"MeshIdentity (workload identity) + MeshTrust (trust domains) as the successor, and the "+
			"experimental SPIFFE rules[] MeshTrafficPermission API requires MeshIdentity. This is a "+
			"guided CA cutover (see Kuma MADR-074), not a manifest rewrite — the trust domain, "+
			"per-workload SPIFFE paths, and CA key material are not in this manifest, and the builtin "+
			"backend mints a new CA. spec.mtls is NOT deprecated, so no action is required today; plan "+
			"the migration when adopting MeshIdentity / the rules[] MeshTrafficPermission API.",
		name)

	// Kong Mesh 2.14 added the MeshIdentity `Extension` provider, which is the
	// native successor for the enterprise CA backends. Name the mapping when the
	// Mesh actually uses one of them — it is not on the docs site, so it is easy
	// to conclude that only Bundled and Spire exist.
	if ext := kongMeshCABackendExtensions(backends); len(ext.types) > 0 {
		msg += fmt.Sprintf(
			" This Mesh uses the Kong Mesh CA backend type(s) %s. Kong Mesh 2.14 added a matching "+
				"MeshIdentity provider: spec.provider.type: Extension with spec.provider.extension.name "+
				"set to %s. Note the Kuma docs still list only Bundled and Spire as providers — the "+
				"Extension provider and its acmpca/vault/certmanager configs are undocumented. "+
				"certmanager is Kubernetes-only; on Universal the CP does not register it.",
			strings.Join(ext.types, ", "), strings.Join(ext.names, "/"))
	}

	return []string{msg}
}

// caBackendExtensionMap maps a legacy Mesh mTLS backend type to the Kong Mesh
// MeshIdentity extension provider name that replaces it in 2.14.
var caBackendExtensionMap = map[string]string{
	"vault":        "vault",
	"acm":          "acmpca",
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

// ---- Mesh meshServices default flip to Exclusive (advisory, 3.0) --------------

// warnMeshServicesDefaultFlip advises when a Mesh has no spec.meshServices block. Kuma 3.0
// changes the default for such meshes from permissive to meshServices.mode: Exclusive
// (kumahq/kuma#17102), which restricts outbound connectivity to explicitly-reachable services
// and requires reachableServices/reachableBackends to name MeshService display names. A mesh
// that already sets meshServices (any mode) is left alone — the flip only affects the nil block.
func warnMeshServicesDefaultFlip(obj map[string]interface{}, name string) []string {
	// Kubernetes format: spec.meshServices; Universal format: top-level meshServices.
	if v, ok := obj["meshServices"]; ok && v != nil {
		return nil
	}
	if spec, ok := obj["spec"].(map[string]interface{}); ok {
		if v, ok := spec["meshServices"]; ok && v != nil {
			return nil
		}
	}
	return []string{fmt.Sprintf(
		"Mesh %q: no spec.meshServices block is set. Kuma 3.0 removes the meshServices field and its "+
			"mode enum from the Mesh schema entirely and behaves as the old Exclusive mode "+
			"unconditionally (kumahq/kuma#17102 flipped the nil default first; the field was then "+
			"removed). Outbound connectivity is restricted to explicitly-reachable services, and "+
			"reachableBackends must name MeshService resources — reachableServices and the "+
			"kuma.io/transparent-proxying-reachable-services annotation are removed in 3.0 too. Set "+
			"spec.meshServices.mode explicitly now (Everywhere preserves current 2.x behaviour) and "+
			"declare reachable backends before upgrading, to avoid breaking connectivity. A Mesh that "+
			"still sets meshServices applies successfully on 3.0; the field is silently ignored.",
		name)}
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
			"When spec.meshServices.mode is Exclusive (opt-in since Kuma 2.10; the default for meshes "+
			"without a meshServices block in Kuma 3.0), update these to the corresponding MeshService "+
			"display names (kuma.io/display-name label value), or migrate to the structured "+
			"reachableBackends.refs[] form.",
		name, services)}
}

// ---- Deprecated top-level spec.targetRef kinds (v2.10/2.11) -------------------

// warnDeprecatedTopLevelTargetRef warns when a policy's top-level spec.targetRef uses a
// kind that Kuma deprecated for that position. Mirrors the upstream
// validators.TopLevelTargetRefDeprecations rule (kind-agnostic, applies to every policy):
//   - MeshSubset / MeshService / MeshServiceSubset → use Dataplane with labels
//   - MeshHTTPRoute → reference it in spec.to[].targetRef instead
//
// These are warn-only, not auto-converted: a MeshService/MeshServiceSubset selector cannot
// be mechanically expanded to the equivalent Dataplane label set from the manifest alone
// (only the legacy Kuma-internal `_svc_` names carry enough info, and those are already
// rewritten to Dataplane by ScenarioSubset before this post-pass runs). For MeshSubset the
// tagged case is likewise handled by ScenarioSubset, so it is only flagged when it carries
// no service-identity tags.
func warnDeprecatedTopLevelTargetRef(obj map[string]interface{}, name, kind string) []string {
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return nil
	}
	targetRef, ok := spec["targetRef"].(map[string]interface{})
	if !ok {
		return nil
	}
	trKind, _ := targetRef["kind"].(string)
	switch trKind {
	case "MeshSubset":
		// Tagged MeshSubset is rewritten to Dataplane by ScenarioSubset; only warn when no
		// service-identity tags are present.
		tags, _ := targetRef["tags"].(map[string]interface{})
		for k := range tags {
			if k == "kuma.io/service" || k == "k8s.kuma.io/service-name" {
				return nil
			}
		}
		return []string{fmt.Sprintf(
			"%s %q: spec.targetRef.kind MeshSubset is deprecated in Kuma 2.10+ — "+
				"use kind: Dataplane with labels instead.",
			kind, name)}
	case "MeshService", "MeshServiceSubset":
		return []string{fmt.Sprintf(
			"%s %q: spec.targetRef.kind %s is deprecated as a top-level target in Kuma 2.10+ — "+
				"use kind: Dataplane with labels instead.",
			kind, name, trKind)}
	case "MeshHTTPRoute":
		return []string{fmt.Sprintf(
			"%s %q: spec.targetRef.kind MeshHTTPRoute is deprecated as a top-level target — "+
				"reference the MeshHTTPRoute in spec.to[].targetRef instead.",
			kind, name)}
	}
	return nil
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

// ---- Deprecated Kubernetes annotations (v2.13 / v2.14) -----------------------

// deprecatedPodAnnotations mirrors PodAnnotationDeprecations in
// kuma/pkg/plugins/runtime/k8s/metadata/annotations.go on release-2.14.
//
// The list is deliberately exact-match rather than prefix-match. Only
// prometheus.metrics.kuma.io/port and /path are deprecated; the
// aggregate-<name>-(port|path|enabled|address) family is NOT, so a prefix match
// would produce false positives on a supported configuration.
var deprecatedPodAnnotations = map[string]string{
	"kuma.io/builtindns": "is no longer supported and is IGNORED — use \"kuma.io/builtin-dns\" instead. " +
		"Because it is ignored rather than rejected, a Pod still carrying it is silently running " +
		"with the default rather than the value set here",
	"kuma.io/builtindnsport": "is no longer supported and is IGNORED — use \"kuma.io/builtin-dns-port\" " +
		"instead. Because it is ignored rather than rejected, a Pod still carrying it is silently " +
		"running with the default rather than the value set here",
	"kuma.io/virtual-probes": "is deprecated and will be removed in a future release. The default " +
		"flipped to disabled in Kuma 2.13; the replacement is the Application Probe Proxy. Confirm " +
		"probes still work after upgrading",
	"kuma.io/virtual-probes-port": "is being replaced by \"kuma.io/application-probe-proxy-port\"",
	"kuma.io/sidecar-injection": "is not supported as an annotation — it must be set as a LABEL. " +
		"As an annotation it has no effect, so injection is following whatever the namespace default is",
	"prometheus.metrics.kuma.io/port": "is deprecated in favour of the MeshMetric policy — move the " +
		"scrape configuration into a MeshMetric resource targeting this workload",
	"prometheus.metrics.kuma.io/path": "is deprecated in favour of the MeshMetric policy — move the " +
		"scrape configuration into a MeshMetric resource targeting this workload",
}

// warnDeprecatedAnnotations warns about metadata annotations deprecated in the
// 2.13/2.14 line. Unlike ScanKumaAnnotations (which repairs the yes/no boolean
// spelling), these are deprecated wholesale in favour of a policy resource, a
// renamed annotation, or a label.
//
// Several of these are ignored rather than rejected by the control plane, so the
// only signal an operator gets is a CP log line at Pod admission — which is
// easily missed. That is why they are surfaced here.
func warnDeprecatedAnnotations(obj map[string]interface{}, name, kind string) []string {
	meta, _ := obj["metadata"].(map[string]interface{})
	if meta == nil {
		return nil
	}
	anns, _ := meta["annotations"].(map[string]interface{})
	if len(anns) == 0 {
		return nil
	}

	// Sort for deterministic warning order across runs.
	keys := make([]string, 0, len(anns))
	for k := range anns {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var warnings []string
	for _, k := range keys {
		reason, ok := deprecatedPodAnnotations[k]
		if !ok {
			continue
		}
		warnings = append(warnings, fmt.Sprintf("%s %q: annotation %q %s.", kind, name, k, reason))
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

// ---- MeshExternalService TLS DataSource → SecureDataSource (3.0) -------------

// legacyDataSourceKeys are the pre-3.0 DataSource variants on a
// MeshExternalService TLS verification field.
var legacyDataSourceKeys = []string{"inline", "inlineString", "secret"}

// warnMeshExternalServiceDataSource warns when spec.tls.verification.{caCert,
// clientCert,clientKey} uses the legacy DataSource shape that 3.0 replaces with
// SecureDataSource.
//
// This is warn-only rather than auto-converted even under v3 because the
// conversion is not a pure rename: `inline` carried base64 and its successor
// `insecureInline.value` carries plain text, so rewriting means decoding
// credential material and re-emitting it in the clear. That is a decision for
// the operator, not a silent transform.
func warnMeshExternalServiceDataSource(obj map[string]interface{}, name string, target TargetVersion) []string {
	tls, _ := obj["tls"].(map[string]interface{})
	if tls == nil {
		if spec, ok := obj["spec"].(map[string]interface{}); ok {
			tls, _ = spec["tls"].(map[string]interface{})
		}
	}
	if tls == nil {
		return nil
	}
	verification, _ := tls["verification"].(map[string]interface{})
	if verification == nil {
		return nil
	}

	var warnings []string
	for _, field := range []string{"caCert", "clientCert", "clientKey"} {
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

// ---- Kong Mesh MeshOPA targetRef name/namespace/mesh (removed 3.0) ----------

// warnMeshOPATargetRefFields warns when a Kong Mesh MeshOPA policy scopes its
// targetRef with name/namespace/mesh, all of which 3.0 removes.
//
// The failure mode is scope-widening rather than an error: a MeshOPA that used
// `name` to bind its rego to a single service silently starts applying to every
// service matching `kind`, changing which requests are evaluated.
func warnMeshOPATargetRefFields(obj map[string]interface{}, name string, target TargetVersion) []string {
	spec, _ := obj["spec"].(map[string]interface{})
	if spec == nil {
		return nil
	}
	targetRef, _ := spec["targetRef"].(map[string]interface{})
	if targetRef == nil {
		return nil
	}

	var present []string
	for _, f := range []string{"name", "namespace", "mesh"} {
		if v, ok := targetRef[f]; ok && v != nil && v != "" {
			present = append(present, f)
		}
	}
	if len(present) == 0 {
		return nil
	}

	msg := fmt.Sprintf(
		"MeshOPA %q: spec.targetRef.{%s} %s use spec.targetRef.labels[\"kuma.io/display-name\"] instead.",
		name, strings.Join(present, ","),
		target.removalNote("3.0 removes these targetRef fields —"))

	// Only `name` carries the scope-widening hazard; namespace/mesh are pruned.
	for _, f := range present {
		if f == "name" {
			msg += " This one is not a clean failure: without `name` the policy widens to every " +
				"service matching `kind`, so the rego starts evaluating requests it never saw before. " +
				"Verify the label selector reproduces the original scope exactly."
			break
		}
	}
	return []string{msg}
}

// ---- Kong Mesh MeshGlobalRateLimit (removed 3.0) ----------------------------

// warnMeshGlobalRateLimitRemoved warns that the enterprise MeshGlobalRateLimit
// policy is removed in 3.0. Leftover objects go inert rather than being rejected,
// and Helm does not delete the CRD, so the resource can linger and look healthy
// while enforcing nothing.
func warnMeshGlobalRateLimitRemoved(_ map[string]interface{}, name string, target TargetVersion) []string {
	if target.IsV3() {
		return []string{fmt.Sprintf(
			"MeshGlobalRateLimit %q: this Kong Mesh policy is REMOVED in 3.0, along with its CP "+
				"support, the rate-limit service in the Helm chart (ratelimit.*, global.ratelimit.*), "+
				"the KMESH_GLOBAL_RATE_LIMIT_* env vars and the kmesh.globalRateLimit CP config. "+
				"Leftover objects become inert rather than rejected, and Helm does not delete the "+
				"CRD — the policy will still be listed while enforcing nothing. Remove it and drop "+
				"the CRD manually (kubectl delete crd meshglobalratelimits.kuma.io). There is no "+
				"in-mesh replacement; plan rate limiting at the gateway or with MeshRateLimit.",
			name)}
	}
	return []string{fmt.Sprintf(
		"MeshGlobalRateLimit %q: this Kong Mesh policy works in 2.14 but is removed in 3.0, with no "+
			"in-mesh replacement. Leftover objects go inert rather than being rejected, so plan the "+
			"replacement before upgrading.",
		name)}
}

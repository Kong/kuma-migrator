package migrator

import (
	"encoding/json"
	"fmt"
	"regexp"

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
//   - Dataplane transparentProxying.redirectPortInboundV6 removed (v2.9)
//   - Dataplane transparentProxying.reachableServices uses legacy kuma.io/service names (v2.10)
//   - Any Mesh* policy with a deprecated top-level spec.targetRef.kind: MeshSubset (without
//     service-identity tags) / MeshService / MeshServiceSubset → Dataplane, or MeshHTTPRoute
//     → spec.to[].targetRef (v2.10/2.11)
//   - Mesh/MeshService/MeshExternalService/MeshMultiZoneService names that violate RFC 1035 /
//     exceed 63 chars (warning in 2.14, hard error in 3.0)
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
		warnings = append(warnings, warnInlineOtelEndpoint(obj, name, kind)...)
	case "MeshTrace":
		warnings = append(warnings, warnInlineOtelEndpoint(obj, name, kind)...)
	case "MeshAccessLog":
		warnings = append(warnings, warnInlineOtelEndpoint(obj, name, kind)...)
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
	case "Dataplane":
		warnings = append(warnings, warnDataplaneRedirectPortInboundV6(obj, name)...)
		warnings = append(warnings, warnDataplaneReachableServices(obj, name)...)
	}

	// Generic checks applied to every Mesh* policy regardless of kind.
	if len(kind) > 4 && kind[:4] == "Mesh" {
		// Deprecated top-level spec.targetRef kinds (MeshSubset/MeshService/MeshServiceSubset
		// → Dataplane; MeshHTTPRoute → spec.to[].targetRef).
		warnings = append(warnings, warnDeprecatedTopLevelTargetRef(obj, name, kind)...)
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
func warnInlineOtelEndpoint(obj map[string]interface{}, name, kind string) []string {
	if !hasOtelInlineEndpoint(obj) {
		return nil
	}
	return []string{fmt.Sprintf(
		"%s %q: an inline openTelemetry.endpoint is deprecated in Kuma 2.14 and removed in 3.0 — "+
			"define a MeshOpenTelemetryBackend resource and reference it via backendRef instead.",
		kind, name)}
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
// tightened validation: lowercase alphanumerics with single '.', '_' or '-' delimiters,
// no leading/trailing/consecutive delimiters.
var otelAttributeKeyRe = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*$`)

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
			reason = "it is not lowercase / has placeholder or invalid delimiter characters"
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
			"MeshService display names (kuma.io/display-name label value), or migrate to the structured "+
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

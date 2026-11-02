package migrator

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"
)

// Kuma 3.0 changes to the Mesh resource and to the observability policies that
// used to inherit settings from it. Sources: kuma/UPGRADE.md "Upgrade to 3.0.0"
// and kong-mesh/UPGRADE_km.md.
//
// Under a v2 target everything here is advisory. Under v3 the Mesh fields 3.0
// removes are deleted from the output, and the companion policies needed to keep
// their behaviour (MeshTLS Permissive, MeshPassthrough All, MeshOpenTelemetryBackend)
// are generated.

// ---- meshServices --------------------------------------------------------------

// meshServicesV3Note returns the extra sentence the meshServices warning carries
// under a 3.0 target. 3.0 keeps the field as deprecated only so stored values keep
// syncing to 2.x zones, which read a missing field as Disabled.
func meshServicesV3Note(target TargetVersion) string {
	if !target.IsV3() {
		return ""
	}
	return " Under 3.0 the field has no effect on 3.0 zones, but it is required during the " +
		"mixed-version window: set it before upgrading the Global CP, because a 2.x zone reads a " +
		"missing field as Disabled and then deletes every generated MeshService. Drop " +
		"spec.meshServices once every zone runs 3.0. Any mode other than Exclusive is rejected by 3.0."
}

// ---- networking.outbound.passthrough --------------------------------------------

// passthroughTrueCompanion handles Mesh networking.outbound.passthrough: true.
// Under 3.0 a transparent-proxy data plane that no MeshPassthrough selects behaves
// as passthroughMode: None (UPGRADE.md "Outbound passthrough defaults to None"), so
// the old allow-all default has to be written down as a policy.
func passthroughTrueCompanion(cm companionMeta, target TargetVersion) ([][]byte, []string) {
	meshName := cm.Mesh
	alternatives := "Narrower alternatives: a MeshPassthrough with passthroughMode: Matched and " +
		"appendMatch listing the allowed destinations, a MeshExternalService per external " +
		"destination, or the CP setting defaults.restrictOutbound=false (KUMA_DEFAULTS_RESTRICT_OUTBOUND)."
	if !target.IsV3() {
		return nil, []string{fmt.Sprintf(
			"Mesh %q: networking.outbound.passthrough=true has been removed from the Mesh output (it is "+
				"the 2.x default). In 3.0 a transparent-proxy data plane that no MeshPassthrough selects "+
				"drops traffic to destinations outside the mesh. Before upgrading to 3.0, add a "+
				"MeshPassthrough with targetRef.kind: Mesh and default.passthroughMode: All to keep the "+
				"current behaviour. %s", meshName, alternatives)}
	}
	crd, warns, err := buildMeshPassthroughMode(cm, "All")
	if err != nil {
		return nil, append(warns, fmt.Sprintf("Mesh %q: MeshPassthrough marshal error: %v", meshName, err))
	}
	return [][]byte{crd}, append(warns, fmt.Sprintf(
		"Mesh %q: networking.outbound.passthrough=true has been extracted to a MeshPassthrough %q "+
			"with passthroughMode: All. In 3.0 a transparent-proxy data plane that no MeshPassthrough "+
			"selects drops traffic to destinations outside the mesh, so this policy keeps the current "+
			"behaviour. Apply it before upgrading. %s", meshName, meshName+"-passthrough", alternatives))
}

// passthroughUnsetV3Warning is emitted for a Mesh that does not set
// networking.outbound.passthrough. 2.x treated that as true. The migrator does not
// generate a passthroughMode: All policy here, because that would widen egress for
// meshes whose operators may already rely on a MeshPassthrough elsewhere.
func passthroughUnsetV3Warning(meshName string) string {
	return fmt.Sprintf(
		"Mesh %q: networking.outbound.passthrough is not set, which 2.x treats as true. In 3.0 a "+
			"transparent-proxy data plane that no MeshPassthrough selects drops traffic to destinations "+
			"outside the mesh (as if passthroughMode: None). Before upgrading, check that a "+
			"MeshPassthrough covers the proxies that reach external destinations: passthroughMode: "+
			"Matched with appendMatch, or passthroughMode: All to keep the 2.x behaviour. Alternatives: "+
			"a MeshExternalService per destination, or the CP setting defaults.restrictOutbound=false.",
		meshName)
}

// ---- 3.0 Mesh field removals ----------------------------------------------------

// applyMeshRemovalsForV3 deletes the Mesh fields that 3.0 removes and returns the
// companion documents and warnings that replace them. obj is the whole document;
// spec is obj["spec"] on Kubernetes and obj itself on Universal.
func applyMeshRemovalsForV3(obj, spec map[string]interface{}, cm companionMeta) ([][]byte, []string) {
	meshName := cm.Mesh
	var companions [][]byte
	var warnings []string
	var dropped []string

	// ── mtls ────────────────────────────────────────────────────────────────
	if mtls, ok := spec["mtls"].(map[string]interface{}); ok || spec["mtls"] != nil {
		warnings = append(warnings, warnMeshMtlsBackends(obj, meshName, TargetV3)...)
		if mtls != nil && enabledBackendMode(mtls) == "PERMISSIVE" {
			crd, w := buildPermissiveMeshTLS(cm)
			if crd != nil {
				companions = append(companions, crd)
			}
			warnings = append(warnings, w...)
		}
		delete(spec, "mtls")
		dropped = append(dropped, "mtls")
	}

	// ── routing.* ───────────────────────────────────────────────────────────
	if routing, ok := spec["routing"].(map[string]interface{}); ok {
		if v, ok := routing["zoneEgress"]; ok {
			if b, _ := v.(bool); b {
				warnings = append(warnings, fmt.Sprintf(
					"Mesh %q: routing.zoneEgress=true is removed in 3.0. Egress use is now implicit: "+
						"cross-zone and MeshExternalService traffic goes through a zone egress based on the "+
						"zone egress topology plus mTLS, with no mesh-level toggle. Check that each zone that "+
						"needs egress runs a zone proxy with egress listeners.", meshName))
			}
			delete(routing, "zoneEgress")
			dropped = append(dropped, "routing.zoneEgress")
		}
		if _, ok := routing["defaultForbidMeshExternalServiceAccess"]; ok {
			warnings = append(warnings, warnMeshForbidExternalServiceAccess(obj, meshName)...)
			delete(routing, "defaultForbidMeshExternalServiceAccess")
			dropped = append(dropped, "routing.defaultForbidMeshExternalServiceAccess")
		}
		if v, ok := routing["localityAwareLoadBalancing"]; ok {
			if b, _ := v.(bool); b {
				warnings = append(warnings, localityAwareAdvisory(meshName))
			}
			delete(routing, "localityAwareLoadBalancing")
			dropped = append(dropped, "routing.localityAwareLoadBalancing")
		}
		if len(routing) == 0 {
			delete(spec, "routing")
		}
	}

	// ── skipCreatingInitialPolicies ─────────────────────────────────────────
	if _, ok := spec["skipCreatingInitialPolicies"]; ok {
		warnings = append(warnings, skipInitialPoliciesAdvisory(meshName))
		delete(spec, "skipCreatingInitialPolicies")
		dropped = append(dropped, "skipCreatingInitialPolicies")
	}

	if len(dropped) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"Mesh %q: removed fields that 3.0 no longer has in the Mesh schema: %s.",
			meshName, strings.Join(dropped, ", ")))
	}
	return companions, warnings
}

func localityAwareAdvisory(meshName string) string {
	return fmt.Sprintf(
		"Mesh %q: routing.localityAwareLoadBalancing=true is removed in 3.0. Locality awareness "+
			"within a zone is on by default. Cross-zone failover needs a MeshLoadBalancingStrategy with "+
			"to[].default.localityAwareness.crossZone, and 3.0 accepts crossZone only on a to[] entry "+
			"whose targetRef.kind is MeshMultiZoneService. The migrator does not generate that policy.",
		meshName)
}

func skipInitialPoliciesAdvisory(meshName string) string {
	return fmt.Sprintf(
		"Mesh %q: skipCreatingInitialPolicies is removed in 3.0 and ignored by the CP. 3.0 creates no "+
			"default policies for a new mesh; existing default policies are kept. Retries are the one "+
			"behaviour that changes: a mesh without a MeshRetry does not retry, so add a MeshRetry to "+
			"any new mesh that needs retries.", meshName)
}

// warnMeshFieldsRemovedIn30 emits the 2.x-target advisories for Mesh fields 3.0
// removes. Under v3 TransformMeshForTarget deletes these fields before the scan
// runs, so in the pipeline this only fires for a v2 target.
// routing.defaultForbidMeshExternalServiceAccess is covered by
// warnMeshForbidExternalServiceAccess and spec.mtls by warnMeshMtlsBackends.
func warnMeshFieldsRemovedIn30(obj map[string]interface{}, name string) []string {
	spec, _ := obj["spec"].(map[string]interface{})
	if spec == nil {
		spec = obj
	}
	var warnings []string
	if routing, ok := spec["routing"].(map[string]interface{}); ok {
		if v, ok := routing["zoneEgress"]; ok {
			msg := fmt.Sprintf("Mesh %q: routing.zoneEgress is removed in 3.0. Remove it before upgrading.", name)
			if b, _ := v.(bool); b {
				msg += " On 3.0 egress use is implicit: cross-zone and MeshExternalService traffic goes " +
					"through a zone egress based on the zone egress topology plus mTLS."
			}
			warnings = append(warnings, msg)
		}
		if v, ok := routing["localityAwareLoadBalancing"]; ok {
			if b, _ := v.(bool); b {
				warnings = append(warnings, localityAwareAdvisory(name)+" Remove the field before upgrading.")
			} else {
				warnings = append(warnings, fmt.Sprintf(
					"Mesh %q: routing.localityAwareLoadBalancing is removed in 3.0. Remove it before upgrading.", name))
			}
		}
	}
	if _, ok := spec["skipCreatingInitialPolicies"]; ok {
		warnings = append(warnings, skipInitialPoliciesAdvisory(name))
	}
	if mtls, ok := spec["mtls"].(map[string]interface{}); ok && enabledBackendMode(mtls) == "PERMISSIVE" {
		warnings = append(warnings, fmt.Sprintf(
			"Mesh %q: the enabled mTLS backend uses mode: PERMISSIVE. In 3.0 only MeshTLS decides whether "+
				"an inbound accepts plaintext, so without a MeshTLS policy the inbounds become Strict and "+
				"plaintext traffic is rejected. Before upgrading, apply a MeshTLS with targetRef.kind: Mesh "+
				"and rules[].default.mode: Permissive for this mesh.", name))
	}
	return warnings
}

// enabledBackendMode returns the upper-cased mode of the backend named by
// mtls.enabledBackend, or "" when mTLS is not enabled.
func enabledBackendMode(mtls map[string]interface{}) string {
	enabled, _ := mtls["enabledBackend"].(string)
	if enabled == "" {
		return ""
	}
	backends, _ := mtls["backends"].([]interface{})
	for _, b := range backends {
		bm, ok := b.(map[string]interface{})
		if !ok {
			continue
		}
		if n, _ := bm["name"].(string); n == enabled {
			mode, _ := bm["mode"].(string)
			return strings.ToUpper(mode)
		}
	}
	return ""
}

// buildPermissiveMeshTLS emits the MeshTLS that replaces mtls.backends[].mode:
// PERMISSIVE, using the shape from UPGRADE.md "mtls.backends[].mode: PERMISSIVE
// no longer makes inbounds permissive".
func buildPermissiveMeshTLS(cm companionMeta) ([]byte, []string) {
	meshName := cm.Mesh
	outName := meshName + "-tls-permissive"
	var warnings []string
	if w := ValidateResourceName(outName, "MeshTLS"); w != "" {
		warnings = append(warnings, w)
	}
	crd := buildCompanion("MeshTLS", outName, cm, map[string]interface{}{
		"targetRef": map[string]interface{}{"kind": "Mesh"},
		"rules": []interface{}{
			map[string]interface{}{
				"default": map[string]interface{}{"mode": "Permissive"},
			},
		},
	})
	b, err := yaml.Marshal(crd)
	if err != nil {
		return nil, append(warnings, fmt.Sprintf("MeshTLS marshal error: %v", err))
	}
	return b, append(warnings, fmt.Sprintf(
		"Mesh %q: the enabled mTLS backend used mode: PERMISSIVE. In 3.0 only MeshTLS decides whether "+
			"an inbound accepts plaintext, so a MeshTLS %q with rules[].default.mode: Permissive has been "+
			"generated. Apply it before upgrading, or the inbounds become Strict and plaintext traffic is "+
			"rejected. Skip it if this mesh already has a MeshTLS policy.", meshName, outName))
}

// transformPassthroughMeshForV3 routes a Mesh that DetectScenario classified as
// passthrough (already Exclusive, nothing to extract) through
// TransformMeshForTarget under a v3 target, so the 3.0 removals apply to every
// Mesh. Returns ok=false for any other document or target.
func transformPassthroughMeshForV3(raw []byte, opts TransformOptions) ([][]byte, []string, bool) {
	if !opts.Target.IsV3() {
		return nil, nil, false
	}
	kind, _ := probeKindName(raw)
	if kind != "Mesh" {
		return nil, nil, false
	}
	docs, warnings, err := TransformMeshWithOptions(raw, opts)
	if err != nil {
		return nil, nil, false
	}
	return docs, warnings, true
}

// ---- MeshMetric ActiveMTLSBackend -----------------------------------------------

// warnMeshMetricActiveMTLSBackend warns under v3 when a MeshMetric Prometheus
// backend uses tls.mode: ActiveMTLSBackend. On 2.x that mode produced a plaintext
// endpoint; on 3.0 it secures the endpoint with the data plane's workload identity.
func warnMeshMetricActiveMTLSBackend(obj map[string]interface{}, name string, target TargetVersion) []string {
	if !target.IsV3() {
		return nil
	}
	spec, _ := obj["spec"].(map[string]interface{})
	def, _ := spec["default"].(map[string]interface{})
	backends, _ := def["backends"].([]interface{})
	for _, b := range backends {
		bm, _ := b.(map[string]interface{})
		prom, _ := bm["prometheus"].(map[string]interface{})
		tls, _ := prom["tls"].(map[string]interface{})
		if mode, _ := tls["mode"].(string); mode == "ActiveMTLSBackend" {
			return []string{fmt.Sprintf(
				"MeshMetric %q: prometheus.tls.mode: ActiveMTLSBackend changes meaning in 3.0. The endpoint "+
					"is secured with the data plane's workload identity, so a scraper connecting from "+
					"another address must present a client certificate the mesh trusts (SPIFFE ID, chained "+
					"to the trust domain's CA bundle). Scrapes from the data plane address or loopback stay "+
					"plaintext. Before upgrading, give each remote scraper such a certificate, or set the "+
					"mode to Disabled to keep a plaintext endpoint.", name)}
		}
	}
	return nil
}

// ---- Inline OpenTelemetry endpoint → MeshOpenTelemetryBackend -------------------

// otelPolicyKinds are the policies whose openTelemetry backend lost the inline
// endpoint in 3.0.
var otelPolicyKinds = map[string]bool{"MeshMetric": true, "MeshTrace": true, "MeshAccessLog": true}

// motbNameInvalidChars matches characters not allowed in an RFC 1035 label.
var motbNameInvalidChars = regexp.MustCompile(`[^a-z0-9-]+`)

// convertInlineOtelEndpointsForV3 rewrites every inline openTelemetry.endpoint in
// MeshMetric/MeshTrace/MeshAccessLog documents into a generated
// MeshOpenTelemetryBackend plus a labels-only backendRef. It is meant to run on a
// transform's output documents before ScanForDeprecations, under a v3 target only.
//
// The 2.x inline endpoint is an OTLP gRPC "host:port" string, so the generated
// backend uses protocol: grpc and no path. An endpoint that does not parse as
// host:port (a URL with a scheme or path, or a missing port) is left in place;
// ScanForDeprecations then reports it for manual migration.
func convertInlineOtelEndpointsForV3(docs [][]byte, opts TransformOptions) ([][]byte, []string) {
	systemNS := resolveSystemNamespace(opts, "")
	var out [][]byte
	var warnings []string
	for _, doc := range docs {
		var obj map[string]interface{}
		if err := yaml.Unmarshal(doc, &obj); err != nil || obj == nil {
			out = append(out, doc)
			continue
		}
		kind, _ := obj["kind"].(string)
		universal := false
		if kind == "" {
			kind, _ = obj["type"].(string)
			universal = true
		}
		if !otelPolicyKinds[kind] || !hasOtelInlineEndpoint(obj) {
			out = append(out, doc)
			continue
		}
		policyName := extractNameFromObj(obj)
		mesh := policyMeshName(obj)

		// One backend per distinct endpoint within this policy.
		backendFor := map[string]string{}
		var order []string
		var unparsed []string
		walkOtelMaps(obj, func(ot map[string]interface{}) {
			ep, _ := ot["endpoint"].(string)
			if ep == "" {
				return
			}
			if _, hasRef := ot["backendRef"]; hasRef {
				unparsed = append(unparsed, ep+" (backendRef is also set)")
				return
			}
			if _, _, ok := parseOtelHostPort(ep); !ok {
				unparsed = append(unparsed, ep)
				return
			}
			bn, ok := backendFor[ep]
			if !ok {
				bn = motbName(policyName, len(order))
				backendFor[ep] = bn
				order = append(order, ep)
			}
			delete(ot, "endpoint")
			ot["backendRef"] = map[string]interface{}{
				"kind":   "MeshOpenTelemetryBackend",
				"labels": map[string]interface{}{"kuma.io/display-name": bn},
			}
		})

		if len(order) == 0 {
			out = append(out, doc)
			continue
		}
		fixed, err := yaml.Marshal(obj)
		if err != nil {
			out = append(out, doc)
			continue
		}
		out = append(out, fixed)

		var created []string
		for _, ep := range order {
			host, port, _ := parseOtelHostPort(ep)
			bn := backendFor[ep]
			cm := companionMeta{Mesh: mesh, Namespace: systemNS, Universal: universal}
			b, err := yaml.Marshal(buildMOTB(bn, host, port, cm))
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("MeshOpenTelemetryBackend marshal error: %v", err))
				continue
			}
			out = append(out, b)
			if w := ValidateResourceName(bn, "MeshOpenTelemetryBackend"); w != "" {
				warnings = append(warnings, w)
			}
			created = append(created, fmt.Sprintf("%s (%s)", bn, ep))
		}
		warnings = append(warnings, fmt.Sprintf(
			"%s %q: inline openTelemetry.endpoint is removed in 3.0 and has been replaced by a "+
				"backendRef selecting a generated MeshOpenTelemetryBackend by its kuma.io/display-name "+
				"label: %s. The backend uses protocol grpc, which is what the inline endpoint used. Apply "+
				"the MeshOpenTelemetryBackend before the policy. On Kubernetes it must stay in the system "+
				"namespace; it was placed in %s (set systemNamespace in the kuma-migrator config file if "+
				"your control plane uses another one). If several policies point at the same collector, "+
				"you can keep one backend and point every backendRef at it.",
			kind, policyName, strings.Join(created, ", "), systemNS))
		if len(unparsed) > 0 {
			sort.Strings(unparsed)
			warnings = append(warnings, fmt.Sprintf(
				"%s %q: these inline openTelemetry endpoints were not converted and must be migrated by "+
					"hand: %s.", kind, policyName, strings.Join(unparsed, ", ")))
		}
	}
	return out, warnings
}

// walkOtelMaps invokes fn for every openTelemetry map in the document.
func walkOtelMaps(v interface{}, fn func(map[string]interface{})) {
	switch t := v.(type) {
	case map[string]interface{}:
		if ot, ok := t["openTelemetry"].(map[string]interface{}); ok {
			fn(ot)
		}
		for k, child := range t {
			if k == "openTelemetry" {
				continue
			}
			walkOtelMaps(child, fn)
		}
	case []interface{}:
		for _, child := range t {
			walkOtelMaps(child, fn)
		}
	}
}

// parseOtelHostPort parses a 2.x inline endpoint ("host:port").
func parseOtelHostPort(ep string) (string, int, bool) {
	if strings.Contains(ep, "://") || strings.Contains(ep, "/") {
		return "", 0, false
	}
	host, portStr, err := net.SplitHostPort(ep)
	if err != nil || host == "" {
		return "", 0, false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, false
	}
	return host, port, true
}

// motbName derives a deterministic RFC 1035 name for the i-th backend generated
// for a policy: "<policy>-otel" for the first, "<policy>-otel-<i+1>" after that.
func motbName(policyName string, i int) string {
	base := motbNameInvalidChars.ReplaceAllString(strings.ToLower(policyName), "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "policy"
	}
	suffix := "-otel"
	if i > 0 {
		suffix = fmt.Sprintf("-otel-%d", i+1)
	}
	if len(base)+len(suffix) > 63 {
		base = strings.TrimRight(base[:63-len(suffix)], "-")
	}
	return base + suffix
}

// policyMeshName returns the mesh a policy belongs to (kuma.io/mesh label on
// Kubernetes, top-level mesh on Universal), or "" when it is not set.
func policyMeshName(obj map[string]interface{}) string {
	if m, ok := obj["mesh"].(string); ok && m != "" {
		return m
	}
	if meta, ok := obj["metadata"].(map[string]interface{}); ok {
		if labels, ok := meta["labels"].(map[string]interface{}); ok {
			if m, ok := labels[meshLabel].(string); ok {
				return m
			}
		}
	}
	return ""
}

// buildMOTB builds a MeshOpenTelemetryBackend in the same format as the policy it
// was extracted from. On Kubernetes it goes in the system namespace (cm.Namespace),
// where the backend must live. No kuma.io/display-name label is set on it, because
// the CP computes that label from the name and rejects a supplied value that differs.
func buildMOTB(name, host string, port int, cm companionMeta) map[string]interface{} {
	return buildCompanion("MeshOpenTelemetryBackend", name, cm, map[string]interface{}{
		"endpoint": map[string]interface{}{
			"address": host,
			"port":    port,
		},
		"protocol": "grpc",
	})
}

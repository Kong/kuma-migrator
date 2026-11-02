package migrator

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// ---- helpers --------------------------------------------------------------------

func mustTransform(t *testing.T, input string, target TargetVersion) ([]map[string]interface{}, []string, Scenario) {
	t.Helper()
	docs, warnings, scenario, err := TransformDocument([]byte(input), target)
	if err != nil {
		t.Fatalf("TransformDocument: %v", err)
	}
	var out []map[string]interface{}
	for _, d := range docs {
		var m map[string]interface{}
		if err := yaml.Unmarshal(d, &m); err != nil {
			t.Fatalf("unmarshal output: %v", err)
		}
		out = append(out, m)
	}
	return out, warnings, scenario
}

func docsOfKind(docs []map[string]interface{}, kind string) []map[string]interface{} {
	var out []map[string]interface{}
	for _, d := range docs {
		k, _ := d["kind"].(string)
		if k == "" {
			k, _ = d["type"].(string)
		}
		if k == kind {
			out = append(out, d)
		}
	}
	return out
}

func warningContaining(warnings []string, subs ...string) string {
	for _, w := range warnings {
		ok := true
		for _, s := range subs {
			if !strings.Contains(w, s) {
				ok = false
				break
			}
		}
		if ok {
			return w
		}
	}
	return ""
}

func dig(m map[string]interface{}, keys ...string) interface{} {
	var cur interface{} = m
	for _, k := range keys {
		mm, ok := cur.(map[string]interface{})
		if !ok {
			return nil
		}
		cur = mm[k]
	}
	return cur
}

const exclusiveMeshWithMtls = `apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
  meshServices:
    mode: Exclusive
  mtls:
    enabledBackend: ca-1
    backends:
      - name: ca-1
        type: builtin
`

// ---- Mesh.mtls --------------------------------------------------------------------

func TestV3Mesh_MtlsRemoved_PassthroughMesh(t *testing.T) {
	// A Mesh that is already Exclusive and has nothing to extract is detected as
	// passthrough; under v3 it must still lose spec.mtls.
	docs, warnings, scenario := mustTransform(t, exclusiveMeshWithMtls, TargetV3)
	if scenario != ScenarioPassthrough {
		t.Errorf("scenario = %s, want Passthrough", scenario)
	}
	if dig(docs[0], "spec", "mtls") != nil {
		t.Errorf("spec.mtls must be removed under v3, got %v", docs[0]["spec"])
	}
	if dig(docs[0], "spec", "meshServices", "mode") != "Exclusive" {
		t.Errorf("meshServices.mode must be kept")
	}
	if warningContaining(warnings, "BLOCKING", "MeshIdentity", "MeshTrust", "plaintext", "ca-builtin-cert") == "" {
		t.Errorf("expected blocking MeshIdentity warning, got: %v", warnings)
	}
	if len(docsOfKind(docs, "MeshTLS")) != 0 {
		t.Error("no MeshTLS expected for a non-PERMISSIVE backend")
	}
}

func TestV3Mesh_MtlsKept_V2(t *testing.T) {
	docs, warnings, _ := mustTransform(t, exclusiveMeshWithMtls, TargetV2)
	if dig(docs[0], "spec", "mtls") == nil {
		t.Error("spec.mtls must be kept under v2")
	}
	if warningContaining(warnings, "required before", "3.0") == "" {
		t.Errorf("expected v2 required-before-3.0 advisory, got: %v", warnings)
	}
	if warningContaining(warnings, "no action is required") != "" {
		t.Errorf("old 'no action is required' wording must be gone: %v", warnings)
	}
}

func TestV3Mesh_MtlsRemoved_Universal(t *testing.T) {
	input := `type: Mesh
name: default
meshServices:
  mode: Exclusive
mtls:
  enabledBackend: ca-1
  backends:
    - name: ca-1
      type: provided
`
	docs, warnings, _ := mustTransform(t, input, TargetV3)
	if _, ok := docs[0]["mtls"]; ok {
		t.Error("top-level mtls must be removed under v3")
	}
	if docs[0]["type"] != "Mesh" || docs[0]["name"] != "default" {
		t.Errorf("Universal fields must be preserved, got %v", docs[0])
	}
	if warningContaining(warnings, "BLOCKING") == "" {
		t.Errorf("expected blocking warning, got: %v", warnings)
	}
}

func TestWarnMeshMtlsBackends_EnabledBackendOnly(t *testing.T) {
	obj := map[string]interface{}{"spec": map[string]interface{}{
		"mtls": map[string]interface{}{"enabledBackend": "ca-1"},
	}}
	if len(warnMeshMtlsBackends(obj, "m", TargetV2)) != 1 {
		t.Error("an mtls block with only enabledBackend must warn")
	}
	empty := map[string]interface{}{"spec": map[string]interface{}{"mtls": map[string]interface{}{}}}
	if len(warnMeshMtlsBackends(empty, "m", TargetV2)) != 0 {
		t.Error("an empty mtls block must not warn")
	}
}

func TestWarnMeshMtlsBackends_KongMeshExtensionByTarget(t *testing.T) {
	obj := map[string]interface{}{"spec": map[string]interface{}{
		"mtls": map[string]interface{}{
			"enabledBackend": "ca-1",
			"backends": []interface{}{
				map[string]interface{}{"name": "ca-1", "type": "acmpca"},
			},
		},
	}}
	v2 := strings.Join(warnMeshMtlsBackends(obj, "m", TargetV2), "\n")
	if !strings.Contains(v2, "acmpca") || !strings.Contains(v2, "spec.provider.type: Extension") {
		t.Errorf("v2: expected acmpca Extension mapping, got: %s", v2)
	}
	v3 := strings.Join(warnMeshMtlsBackends(obj, "m", TargetV3), "\n")
	if !strings.Contains(v3, "removed in Kong Mesh 3.0") || !strings.Contains(v3, "spec.provider.extension.name set to acmpca") {
		t.Errorf("v3: expected removal wording with Extension name, got: %s", v3)
	}
}

func TestCABackendExtensionMap_Acmpca(t *testing.T) {
	for typ, want := range map[string]string{"acmpca": "acmpca", "acm": "acmpca", "vault": "vault", "cert-manager": "certmanager"} {
		if got := caBackendExtensionMap[typ]; got != want {
			t.Errorf("caBackendExtensionMap[%q] = %q, want %q", typ, got, want)
		}
	}
}

func mtlsMeshObj(typ string) map[string]interface{} {
	return map[string]interface{}{"spec": map[string]interface{}{
		"mtls": map[string]interface{}{
			"enabledBackend": "ca-1",
			"backends": []interface{}{
				map[string]interface{}{"name": "ca-1", "type": typ},
			},
		},
	}}
}

// The cutover text is backend-specific: builtin-only facts (a new CA is minted,
// ca-builtin-* Secrets) must not appear for a provided or Kong Mesh backend.
func TestWarnMeshMtlsBackends_TextPerBackendType(t *testing.T) {
	for _, target := range []TargetVersion{TargetV2, TargetV3} {
		for _, typ := range []string{"vault", "acmpca", "certmanager", "provided"} {
			msg := strings.Join(warnMeshMtlsBackends(mtlsMeshObj(typ), "m", target), "\n")
			for _, banned := range []string{"ca-builtin", "mints a new CA", "type builtin"} {
				if strings.Contains(msg, banned) {
					t.Errorf("%s/%s: builtin-only text %q present: %s", target, typ, banned, msg)
				}
			}
		}

		builtin := strings.Join(warnMeshMtlsBackends(mtlsMeshObj("builtin"), "m", target), "\n")
		if !strings.Contains(builtin, "ca-builtin-cert") || !strings.Contains(builtin, "mints a new CA") ||
			!strings.Contains(builtin, "spec.provider.bundled.ca.certificate") {
			t.Errorf("%s/builtin: expected ca-builtin Secrets and Bundled CA reuse, got: %s", target, builtin)
		}
		if strings.Contains(builtin, "Extension") {
			t.Errorf("%s/builtin: Extension provider must not be mentioned: %s", target, builtin)
		}
		if got := strings.Contains(builtin, "does not delete the ca-builtin Secrets"); got != target.IsV3() {
			t.Errorf("%s/builtin: Secret-cleanup sentence present=%v, want %v", target, got, target.IsV3())
		}

		provided := strings.Join(warnMeshMtlsBackends(mtlsMeshObj("provided"), "m", target), "\n")
		if !strings.Contains(provided, "conf.cert / conf.key") {
			t.Errorf("%s/provided: expected provided-specific text, got: %s", target, provided)
		}

		vault := strings.Join(warnMeshMtlsBackends(mtlsMeshObj("vault"), "m", target), "\n")
		if !strings.Contains(vault, "spec.provider.extension.name") || !strings.Contains(vault, "CA stays in the external system") {
			t.Errorf("%s/vault: expected Extension mapping, got: %s", target, vault)
		}

		other := strings.Join(warnMeshMtlsBackends(mtlsMeshObj("custom-ca"), "m", target), "\n")
		if !strings.Contains(other, "does not recognise") {
			t.Errorf("%s/unknown type: expected unrecognised-type text, got: %s", target, other)
		}
	}
}

// ---- mtls PERMISSIVE → MeshTLS ----------------------------------------------------

const permissiveMesh = `apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
  meshServices:
    mode: Exclusive
  mtls:
    enabledBackend: ca-1
    backends:
      - name: ca-1
        type: builtin
        mode: PERMISSIVE
`

func TestV3Mesh_PermissiveEmitsMeshTLS(t *testing.T) {
	docs, warnings, _ := mustTransform(t, permissiveMesh, TargetV3)
	tls := docsOfKind(docs, "MeshTLS")
	if len(tls) != 1 {
		t.Fatalf("expected one MeshTLS companion, got %d", len(tls))
	}
	if dig(tls[0], "metadata", "name") != "default-tls-permissive" {
		t.Errorf("unexpected MeshTLS name: %v", dig(tls[0], "metadata", "name"))
	}
	if dig(tls[0], "metadata", "labels", "kuma.io/mesh") != "default" {
		t.Error("MeshTLS must carry the kuma.io/mesh label")
	}
	if dig(tls[0], "spec", "targetRef", "kind") != "Mesh" {
		t.Error("MeshTLS must target the Mesh")
	}
	rules, _ := dig(tls[0], "spec", "rules").([]interface{})
	if len(rules) != 1 || dig(rules[0].(map[string]interface{}), "default", "mode") != "Permissive" {
		t.Errorf("expected rules[0].default.mode: Permissive, got %v", rules)
	}
	if warningContaining(warnings, "PERMISSIVE", "default-tls-permissive") == "" {
		t.Errorf("expected MeshTLS warning, got: %v", warnings)
	}
}

func TestV3Mesh_PermissiveV2_AdvisoryOnly(t *testing.T) {
	docs, warnings, _ := mustTransform(t, permissiveMesh, TargetV2)
	if len(docsOfKind(docs, "MeshTLS")) != 0 {
		t.Error("v2 must not generate a MeshTLS")
	}
	if warningContaining(warnings, "PERMISSIVE", "MeshTLS", "Permissive") == "" {
		t.Errorf("expected v2 PERMISSIVE advisory, got: %v", warnings)
	}
}

func TestV3Mesh_PermissiveOnDisabledBackend_NoMeshTLS(t *testing.T) {
	// Mode PERMISSIVE on a backend that is not the enabled one has no effect.
	input := strings.Replace(permissiveMesh, "enabledBackend: ca-1", "enabledBackend: ca-2", 1)
	docs, _, _ := mustTransform(t, input, TargetV3)
	if len(docsOfKind(docs, "MeshTLS")) != 0 {
		t.Error("PERMISSIVE on a non-enabled backend must not generate a MeshTLS")
	}
}

// ---- meshServices -----------------------------------------------------------------

func TestV3Mesh_MeshServicesNote(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec: {}
`
	docs, warnings, _ := mustTransform(t, input, TargetV3)
	if dig(docs[0], "spec", "meshServices", "mode") != "Exclusive" {
		t.Error("Exclusive must still be injected under v3")
	}
	if warningContaining(warnings, "set to Exclusive", "Kuma 2.9+", "mixed-version window", "once every zone runs 3.0", "rejected by 3.0") == "" {
		t.Errorf("expected v3 meshServices note on the injection warning, got: %v", warnings)
	}

	_, v2, _ := mustTransform(t, input, TargetV2)
	if warningContaining(v2, "mixed-version window") != "" {
		t.Errorf("v2 warning must not carry the 3.0 note: %v", v2)
	}

	// Already Exclusive under v3: still told to keep it during the mixed window.
	_, v3Excl, _ := mustTransform(t, exclusiveMeshWithMtls, TargetV3)
	if warningContaining(v3Excl, "keep spec.meshServices.mode: Exclusive", "mixed-version window") == "" {
		t.Errorf("expected keep-Exclusive note, got: %v", v3Excl)
	}
}

// ---- routing.* / skipCreatingInitialPolicies -------------------------------------

const meshWithRemovedFields = `apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
  meshServices:
    mode: Exclusive
  skipCreatingInitialPolicies:
    - "*"
  routing:
    zoneEgress: true
    defaultForbidMeshExternalServiceAccess: true
    localityAwareLoadBalancing: true
`

func TestV3Mesh_RemovedFieldsDeleted(t *testing.T) {
	docs, warnings, _ := mustTransform(t, meshWithRemovedFields, TargetV3)
	spec := docs[0]["spec"].(map[string]interface{})
	if _, ok := spec["routing"]; ok {
		t.Errorf("empty routing must be removed, got %v", spec["routing"])
	}
	if _, ok := spec["skipCreatingInitialPolicies"]; ok {
		t.Error("skipCreatingInitialPolicies must be removed")
	}
	for _, want := range [][]string{
		{"routing.zoneEgress=true", "implicit"},
		{"defaultForbidMeshExternalServiceAccess", "MeshTrafficPermission"},
		{"localityAwareLoadBalancing=true", "crossZone", "MeshMultiZoneService"},
		{"skipCreatingInitialPolicies", "MeshRetry"},
		{"removed fields", "routing.zoneEgress", "skipCreatingInitialPolicies"},
	} {
		if warningContaining(warnings, want...) == "" {
			t.Errorf("missing warning with %v; got: %v", want, warnings)
		}
	}
	if len(docsOfKind(docs, "MeshLoadBalancingStrategy")) != 0 {
		t.Error("no MeshLoadBalancingStrategy must be generated")
	}
}

func TestV3Mesh_RemovedFieldsKeepOtherRoutingKeys_Universal(t *testing.T) {
	input := `type: Mesh
name: default
meshServices:
  mode: Exclusive
routing:
  zoneEgress: false
  somethingElse: true
`
	docs, warnings, _ := mustTransform(t, input, TargetV3)
	routing, _ := docs[0]["routing"].(map[string]interface{})
	if routing == nil || routing["somethingElse"] != true {
		t.Fatalf("unrelated routing keys must be kept, got %v", docs[0])
	}
	if _, ok := routing["zoneEgress"]; ok {
		t.Error("zoneEgress must be removed")
	}
	if warningContaining(warnings, "zoneEgress=true") != "" {
		t.Error("zoneEgress=false must not produce the implicit-egress advisory")
	}
}

func TestV3Mesh_RemovedFieldsV2_AdvisoryOnly(t *testing.T) {
	docs, warnings, _ := mustTransform(t, meshWithRemovedFields, TargetV2)
	if dig(docs[0], "spec", "routing", "zoneEgress") != true {
		t.Error("v2 must keep routing.zoneEgress")
	}
	if dig(docs[0], "spec", "skipCreatingInitialPolicies") == nil {
		t.Error("v2 must keep skipCreatingInitialPolicies")
	}
	for _, want := range [][]string{
		{"routing.zoneEgress is removed in 3.0"},
		{"localityAwareLoadBalancing", "crossZone"},
		{"skipCreatingInitialPolicies", "MeshRetry"},
		{"defaultForbidMeshExternalServiceAccess", "MeshTrafficPermission"},
	} {
		if warningContaining(warnings, want...) == "" {
			t.Errorf("missing v2 advisory with %v; got: %v", want, warnings)
		}
	}
}

func TestWarnMeshForbidExternalServiceAccess_False(t *testing.T) {
	obj := map[string]interface{}{"routing": map[string]interface{}{"defaultForbidMeshExternalServiceAccess": false}}
	w := strings.Join(warnMeshForbidExternalServiceAccess(obj, "m"), "\n")
	if !strings.Contains(w, "no replacement") {
		t.Errorf("false value should say no replacement is needed, got: %s", w)
	}
}

// ---- networking.outbound.passthrough ----------------------------------------------

func meshWithPassthrough(v string) string {
	return `apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
  meshServices:
    mode: Exclusive
  networking:
    outbound:
      passthrough: ` + v + "\n"
}

func TestV3Mesh_PassthroughTrue_EmitsAll(t *testing.T) {
	docs, warnings, _ := mustTransform(t, meshWithPassthrough("true"), TargetV3)
	mp := docsOfKind(docs, "MeshPassthrough")
	if len(mp) != 1 {
		t.Fatalf("expected one MeshPassthrough, got %d", len(mp))
	}
	if dig(mp[0], "spec", "default", "passthroughMode") != "All" {
		t.Errorf("expected passthroughMode All, got %v", dig(mp[0], "spec", "default"))
	}
	if dig(mp[0], "spec", "targetRef", "kind") != "Mesh" {
		t.Error("MeshPassthrough must target the Mesh")
	}
	if warningContaining(warnings, "passthroughMode: All", "Matched", "MeshExternalService", "restrictOutbound") == "" {
		t.Errorf("expected warning naming the alternatives, got: %v", warnings)
	}
	if dig(docs[0], "spec", "networking") != nil {
		t.Error("networking.outbound.passthrough must be removed")
	}
}

func TestV3Mesh_PassthroughTrue_V2NoCompanion(t *testing.T) {
	docs, warnings, _ := mustTransform(t, meshWithPassthrough("true"), TargetV2)
	if len(docsOfKind(docs, "MeshPassthrough")) != 0 {
		t.Error("v2 must not generate a MeshPassthrough for passthrough: true")
	}
	if warningContaining(warnings, "Before upgrading to 3.0", "passthroughMode: All") == "" {
		t.Errorf("expected v2 advisory, got: %v", warnings)
	}
}

func TestV3Mesh_PassthroughFalse_StillNone(t *testing.T) {
	docs, warnings, _ := mustTransform(t, meshWithPassthrough("false"), TargetV3)
	mp := docsOfKind(docs, "MeshPassthrough")
	if len(mp) != 1 || dig(mp[0], "spec", "default", "passthroughMode") != "None" {
		t.Errorf("passthrough: false must still produce passthroughMode None, got %v", mp)
	}
	if warningContaining(warnings, "is not set") != "" {
		t.Error("the unset warning must not fire when the key was set")
	}
}

func TestV3Mesh_PassthroughUnset_WarnOnly(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
  meshServices:
    mode: Exclusive
`
	docs, warnings, _ := mustTransform(t, input, TargetV3)
	if len(docsOfKind(docs, "MeshPassthrough")) != 0 {
		t.Error("an unset passthrough must not generate a MeshPassthrough")
	}
	if warningContaining(warnings, "networking.outbound.passthrough is not set", "passthroughMode: None") == "" {
		t.Errorf("expected unset-passthrough warning, got: %v", warnings)
	}
	_, v2, _ := mustTransform(t, input, TargetV2)
	if warningContaining(v2, "is not set") != "" {
		t.Errorf("v2 must not carry the unset warning: %v", v2)
	}
}

// ---- MeshMetric ActiveMTLSBackend -------------------------------------------------

func TestWarnMeshMetricActiveMTLSBackend(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: MeshMetric
metadata:
  name: metrics
spec:
  targetRef:
    kind: Mesh
  default:
    backends:
      - type: Prometheus
        prometheus:
          port: 5670
          tls:
            mode: ActiveMTLSBackend
`
	_, v3 := ScanForDeprecations([]byte(input), TargetV3)
	if warningContaining(v3, "ActiveMTLSBackend", "client certificate", "Disabled") == "" {
		t.Errorf("expected v3 ActiveMTLSBackend warning, got: %v", v3)
	}
	_, v2 := ScanForDeprecations([]byte(input), TargetV2)
	if warningContaining(v2, "ActiveMTLSBackend") != "" {
		t.Errorf("v2 must be silent, got: %v", v2)
	}
	other := strings.Replace(input, "ActiveMTLSBackend", "ProvidedTLS", 1)
	_, v3Other := ScanForDeprecations([]byte(other), TargetV3)
	if warningContaining(v3Other, "ActiveMTLSBackend") != "" {
		t.Errorf("ProvidedTLS must not warn, got: %v", v3Other)
	}
}

// ---- Inline OTel endpoint → MeshOpenTelemetryBackend ------------------------------

const meshTraceInlineOtel = `apiVersion: kuma.io/v1alpha1
kind: MeshTrace
metadata:
  name: tracing
  namespace: kuma-system
  labels:
    kuma.io/mesh: default
spec:
  targetRef:
    kind: Mesh
  default:
    backends:
      - type: OpenTelemetry
        openTelemetry:
          endpoint: otel-collector.observability:4317
`

func TestV3Otel_MeshTraceConverted(t *testing.T) {
	docs, warnings, _ := mustTransform(t, meshTraceInlineOtel, TargetV3)
	if len(docs) != 2 {
		t.Fatalf("expected policy + MeshOpenTelemetryBackend, got %d docs", len(docs))
	}
	backends := dig(docs[0], "spec", "default", "backends").([]interface{})
	ot := backends[0].(map[string]interface{})["openTelemetry"].(map[string]interface{})
	if _, ok := ot["endpoint"]; ok {
		t.Error("inline endpoint must be removed")
	}
	if dig(ot, "backendRef", "kind") != "MeshOpenTelemetryBackend" ||
		dig(ot, "backendRef", "labels", "kuma.io/display-name") != "tracing-otel" {
		t.Errorf("unexpected backendRef: %v", ot["backendRef"])
	}
	if _, ok := dig(ot, "backendRef").(map[string]interface{})["name"]; ok {
		t.Error("backendRef must select by labels only")
	}

	motb := docsOfKind(docs, "MeshOpenTelemetryBackend")
	if len(motb) != 1 {
		t.Fatalf("expected one MOTB, got %d", len(motb))
	}
	m := motb[0]
	if dig(m, "metadata", "name") != "tracing-otel" || dig(m, "metadata", "namespace") != "kuma-system" {
		t.Errorf("unexpected MOTB metadata: %v", m["metadata"])
	}
	if dig(m, "metadata", "labels", "kuma.io/mesh") != "default" {
		t.Error("MOTB must carry the policy's mesh label")
	}
	if dig(m, "metadata", "labels", "kuma.io/display-name") != nil {
		t.Error("MOTB must not set the CP-owned display-name label")
	}
	if dig(m, "spec", "endpoint", "address") != "otel-collector.observability" ||
		dig(m, "spec", "endpoint", "port") != float64(4317) ||
		dig(m, "spec", "protocol") != "grpc" {
		t.Errorf("unexpected MOTB spec: %v", m["spec"])
	}
	if dig(m, "spec", "endpoint", "path") != nil {
		t.Error("grpc backend must have no path")
	}
	if warningContaining(warnings, "REMOVED in 3.0") != "" {
		t.Errorf("converted policy must not carry the manual-migration warning: %v", warnings)
	}
	if warningContaining(warnings, "generated MeshOpenTelemetryBackend", "tracing-otel") == "" {
		t.Errorf("expected conversion warning, got: %v", warnings)
	}
}

func TestV3Otel_V2Unchanged(t *testing.T) {
	docs, warnings, _ := mustTransform(t, meshTraceInlineOtel, TargetV2)
	if len(docs) != 1 {
		t.Fatalf("v2 must not emit a MOTB, got %d docs", len(docs))
	}
	if warningContaining(warnings, "Do NOT make this change") == "" {
		t.Errorf("v2 must keep the advisory, got: %v", warnings)
	}
}

func TestV3Otel_UniversalAccessLog_DedupesEndpoint(t *testing.T) {
	input := `type: MeshAccessLog
name: logs
mesh: prod
spec:
  targetRef:
    kind: Mesh
  rules:
    - default:
        backends:
          - type: OpenTelemetry
            openTelemetry:
              endpoint: otel:4317
          - type: OpenTelemetry
            openTelemetry:
              endpoint: otel:4317
              body: "%START_TIME%"
`
	docs, _, _ := mustTransform(t, input, TargetV3)
	motb := docsOfKind(docs, "MeshOpenTelemetryBackend")
	if len(motb) != 1 {
		t.Fatalf("one endpoint must yield one MOTB, got %d", len(motb))
	}
	if motb[0]["type"] != "MeshOpenTelemetryBackend" || motb[0]["name"] != "logs-otel" || motb[0]["mesh"] != "prod" {
		t.Errorf("expected Universal MOTB, got %v", motb[0])
	}
	if _, ok := motb[0]["metadata"]; ok {
		t.Error("Universal MOTB must not have metadata")
	}
	rules := docs[0]["spec"].(map[string]interface{})["rules"].([]interface{})
	backends := dig(rules[0].(map[string]interface{}), "default", "backends").([]interface{})
	second := backends[1].(map[string]interface{})["openTelemetry"].(map[string]interface{})
	if second["body"] != "%START_TIME%" {
		t.Error("signal-specific fields must stay on the policy")
	}
	if dig(second, "backendRef", "labels", "kuma.io/display-name") != "logs-otel" {
		t.Errorf("both entries must reference the same backend, got %v", second)
	}
}

func TestV3Otel_UnparseableEndpointLeftInPlace(t *testing.T) {
	input := strings.Replace(meshTraceInlineOtel, "otel-collector.observability:4317", "http://otel:4318/v1/traces", 1)
	docs, warnings, _ := mustTransform(t, input, TargetV3)
	if len(docs) != 1 {
		t.Fatalf("an unparseable endpoint must not produce a MOTB, got %d docs", len(docs))
	}
	if warningContaining(warnings, "REMOVED in 3.0") == "" {
		t.Errorf("the scan warning must still fire, got: %v", warnings)
	}
}

func TestParseOtelHostPort(t *testing.T) {
	cases := []struct {
		in   string
		host string
		port int
		ok   bool
	}{
		{"otel:4317", "otel", 4317, true},
		{"10.0.0.1:4317", "10.0.0.1", 4317, true},
		{"[::1]:4317", "::1", 4317, true},
		{"otel", "", 0, false},
		{"http://otel:4317", "", 0, false},
		{"otel:4317/v1", "", 0, false},
		{"otel:0", "", 0, false},
	}
	for _, c := range cases {
		h, p, ok := parseOtelHostPort(c.in)
		if h != c.host || p != c.port || ok != c.ok {
			t.Errorf("parseOtelHostPort(%q) = %q,%d,%v want %q,%d,%v", c.in, h, p, ok, c.host, c.port, c.ok)
		}
	}
}

func TestMotbName(t *testing.T) {
	if got := motbName("tracing", 0); got != "tracing-otel" {
		t.Errorf("got %q", got)
	}
	if got := motbName("tracing", 1); got != "tracing-otel-2" {
		t.Errorf("got %q", got)
	}
	long := strings.Repeat("a", 70)
	if got := motbName(long, 0); len(got) > 63 || !strings.HasSuffix(got, "-otel") {
		t.Errorf("long name not truncated correctly: %q", got)
	}
	if got := motbName("My_Policy", 0); got != "my-policy-otel" {
		t.Errorf("got %q", got)
	}
}

package migrator

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestParseTargetVersion(t *testing.T) {
	cases := []struct {
		in      string
		want    TargetVersion
		wantErr bool
	}{
		{"", TargetV2, false},
		{"v2", TargetV2, false},
		{"V2", TargetV2, false},
		{"2", TargetV2, false},
		{"v3", TargetV3, false},
		{"V3", TargetV3, false},
		{"3", TargetV3, false},
		{"v4", TargetV2, true},
		{"latest", TargetV2, true},
		{"2.14", TargetV2, true},
	}
	for _, c := range cases {
		got, err := ParseTargetVersion(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseTargetVersion(%q): err=%v, wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("ParseTargetVersion(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestTargetVersion_StringAndIsV3(t *testing.T) {
	if TargetV2.String() != "v2" || TargetV3.String() != "v3" {
		t.Errorf("unexpected String(): %q / %q", TargetV2.String(), TargetV3.String())
	}
	if TargetV2.IsV3() || !TargetV3.IsV3() {
		t.Error("IsV3() is wrong")
	}
	if !strings.Contains(TargetV3.Describe(), "3.0") {
		t.Errorf("Describe() should mention 3.0, got %q", TargetV3.Describe())
	}
}

// TestScanForDeprecations_HostnameGeneratorTemplate covers the 2.14 rendered-template
// validation. Templates are checked by substituting each {{ ... }} expression with a
// valid label character, so a valid template that merely starts with an expression
// must not be flagged.
func TestScanForDeprecations_HostnameGeneratorTemplate(t *testing.T) {
	cases := []struct {
		template string
		wantWarn bool
	}{
		{"{{ .DisplayName }}.mesh", false},
		{"{{ .DisplayName }}.{{ .Namespace }}.mesh", false},
		{"svc.mesh", false},
		{".{{ .DisplayName }}.mesh", true}, // leading dot
		{"{{ .DisplayName }}..mesh", true}, // consecutive dots
		{"{{ .DisplayName }}.MESH", true},  // uppercase
		{"{{ .DisplayName }}.mesh.", true}, // trailing dot
	}
	for _, c := range cases {
		input := "apiVersion: kuma.io/v1alpha1\nkind: HostnameGenerator\nmetadata:\n  name: hg\nspec:\n  template: \"" + c.template + "\"\n"
		_, warnings := ScanForDeprecations([]byte(input), TargetV2)
		found := false
		for _, w := range warnings {
			if strings.Contains(w, "spec.template") {
				found = true
			}
		}
		if found != c.wantWarn {
			t.Errorf("template %q: got warn=%v want %v; warnings: %v", c.template, found, c.wantWarn, warnings)
		}
	}
}

func TestScanForDeprecations_MeshPassthroughDomains(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshPassthrough
metadata:
  name: mp
spec:
  targetRef:
    kind: Mesh
  default:
    appendMatch:
      - type: Domain
        value: "*example.com"
        protocol: http
        port: 443
      - type: Domain
        value: "api.example.com"
        protocol: tcp
      - type: Domain
        value: "*.wild.com"
        protocol: http
      - type: Domain
        value: "ok.example.com"
        protocol: http
        port: 443
`
	_, warnings := ScanForDeprecations([]byte(input), TargetV2)
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"partial wildcard", "not supported for a domain", "no port"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected a warning containing %q, got: %v", want, warnings)
		}
	}
	if strings.Contains(joined, "ok.example.com") {
		t.Errorf("valid match should not be flagged, got: %v", warnings)
	}
}

// TestScanForDeprecations_DeprecatedAnnotations mirrors PodAnnotationDeprecations
// in kuma/pkg/plugins/runtime/k8s/metadata/annotations.go on release-2.14.
//
// The negative case matters most: only prometheus.metrics.kuma.io/port and /path
// are deprecated. The aggregate-<name>-(port|path|enabled|address) family is a
// supported configuration, so a prefix match would flag working manifests.
func TestScanForDeprecations_DeprecatedAnnotations(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: Dataplane
metadata:
  name: dp
  annotations:
    prometheus.metrics.kuma.io/port: "1234"
    prometheus.metrics.kuma.io/path: /metrics
    prometheus.metrics.kuma.io/aggregate-app-port: "80"
    prometheus.metrics.kuma.io/aggregate-app-enabled: "true"
    kuma.io/virtual-probes: enabled
    kuma.io/virtual-probes-port: "19001"
    kuma.io/builtindns: "true"
    kuma.io/builtindnsport: "15053"
    kuma.io/sidecar-injection: enabled
    kuma.io/mesh: default
spec: {}
`
	_, warnings := ScanForDeprecations([]byte(input), TargetV2)
	joined := strings.Join(warnings, "\n")

	mustMention := map[string]string{
		"prometheus.metrics.kuma.io/port": "MeshMetric",
		"prometheus.metrics.kuma.io/path": "MeshMetric",
		"kuma.io/virtual-probes":          "Application Probe Proxy",
		"kuma.io/virtual-probes-port":     "kuma.io/application-probe-proxy-port",
		"kuma.io/builtindns":              "kuma.io/builtin-dns",
		"kuma.io/builtindnsport":          "kuma.io/builtin-dns-port",
		"kuma.io/sidecar-injection":       "LABEL",
	}
	for key, want := range mustMention {
		if !strings.Contains(joined, key) {
			t.Errorf("expected a warning for %q, got: %v", key, warnings)
			continue
		}
		if !strings.Contains(joined, want) {
			t.Errorf("warning for %q should mention %q, got: %v", key, want, warnings)
		}
	}

	// Supported annotations must not be flagged.
	for _, notFlagged := range []string{"aggregate-app-port", "aggregate-app-enabled", "kuma.io/mesh"} {
		if strings.Contains(joined, notFlagged) {
			t.Errorf("%q is supported and must not be flagged, got: %v", notFlagged, warnings)
		}
	}

	// builtindns/builtindnsport are ignored rather than rejected — the warning has
	// to say so, because otherwise there is no signal at all.
	if !strings.Contains(joined, "IGNORED") {
		t.Errorf("expected the ignored-not-rejected caveat on builtindns, got: %v", warnings)
	}

	if len(warnings) != len(mustMention) {
		t.Errorf("expected exactly %d warnings, got %d: %v", len(mustMention), len(warnings), warnings)
	}
}

func TestScanForDeprecations_DataplaneInboundTags_V3Only(t *testing.T) {
	input := `type: Dataplane
mesh: default
name: web-01
networking:
  address: 192.168.0.1
  inbound:
    - port: 8080
      tags:
        kuma.io/service: web
`
	_, v2Warnings := ScanForDeprecations([]byte(input), TargetV2)
	for _, w := range v2Warnings {
		if strings.Contains(w, "inbound[].tags") {
			t.Errorf("inbound tags must not warn under a v2 target (they are mandatory in 2.x): %v", v2Warnings)
		}
	}

	_, v3Warnings := ScanForDeprecations([]byte(input), TargetV3)
	found := false
	for _, w := range v3Warnings {
		if strings.Contains(w, "inbound[].tags") && strings.Contains(w, "silently") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a v3 inbound-tags warning mentioning the silent drop, got: %v", v3Warnings)
	}
}

func TestScanForDeprecations_MeshGlobalRateLimit(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGlobalRateLimit
metadata:
  name: mgrl
spec:
  targetRef:
    kind: Mesh
`
	for _, tc := range []struct {
		target TargetVersion
		want   string
	}{
		{TargetV2, "removed in 3.0"},
		{TargetV3, "REMOVED in 3.0"},
	} {
		_, warnings := ScanForDeprecations([]byte(input), tc.target)
		if len(warnings) == 0 || !strings.Contains(strings.Join(warnings, "\n"), tc.want) {
			t.Errorf("target %v: expected warning containing %q, got: %v", tc.target, tc.want, warnings)
		}
	}
}

func TestScanForDeprecations_MeshExternalServiceDataSource(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshExternalService
metadata:
  name: mes
spec:
  match:
    type: HostnameGenerator
  tls:
    enabled: true
    verification:
      caCert:
        inline: Zm9v
      clientKey:
        secret: my-secret
`
	_, warnings := ScanForDeprecations([]byte(input), TargetV3)
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "caCert.inline") {
		t.Errorf("expected caCert.inline warning, got: %v", warnings)
	}
	if !strings.Contains(joined, "base64") {
		t.Errorf("expected the base64-vs-plaintext caveat on inline, got: %v", warnings)
	}
	if !strings.Contains(joined, "clientKey.secret") {
		t.Errorf("expected clientKey.secret warning, got: %v", warnings)
	}
}

// TestScanForDeprecations_MeshOPATargetRefFields covers the v2-target MeshOPA
// advisory. Under v3 the transform rewrites the targetRef so this warning does
// not fire on the output; under v2 the fields are preserved and the advisory is
// the only thing telling the operator about the 3.0 scope-widening hazard.
func TestScanForDeprecations_MeshOPATargetRefFields(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshOPA
metadata:
  name: my-opa
spec:
  targetRef:
    kind: MeshService
    name: backend
    namespace: demo
    mesh: default
  default:
    appendPolicies: []
`
	_, warnings := ScanForDeprecations([]byte(input), TargetV2)
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"name", "namespace", "mesh", "kuma.io/display-name", "widen"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected the v2 MeshOPA advisory to mention %q, got: %v", want, warnings)
		}
	}

	// A MeshOPA already using the label selector must not be flagged.
	clean := `
apiVersion: kuma.io/v1alpha1
kind: MeshOPA
metadata:
  name: my-opa
spec:
  targetRef:
    kind: MeshService
    labels:
      kuma.io/display-name: backend
  default:
    appendPolicies: []
`
	_, cleanWarnings := ScanForDeprecations([]byte(clean), TargetV2)
	for _, w := range cleanWarnings {
		if strings.Contains(w, "display-name") {
			t.Errorf("label-selector MeshOPA should not be flagged, got: %v", cleanWarnings)
		}
	}
}

// TestScanForDeprecations_MeshMtlsKongMeshCABackends pins the Kong Mesh 2.14
// MeshIdentity Extension provider mapping. This mapping is undocumented on the
// Kuma docs site (which still lists only Bundled and Spire), so the test is the
// record of where the names come from.
func TestScanForDeprecations_MeshMtlsKongMeshCABackends(t *testing.T) {
	cases := []struct {
		backendType string
		wantExtName string
	}{
		{"vault", "vault"},
		{"acm", "acmpca"},
		{"cert-manager", "certmanager"},
	}
	for _, c := range cases {
		input := `
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
  mtls:
    enabledBackend: ca-1
    backends:
      - name: ca-1
        type: ` + c.backendType + `
`
		_, warnings := ScanForDeprecations([]byte(input), TargetV2)
		joined := strings.Join(warnings, "\n")
		if !strings.Contains(joined, "type: Extension") {
			t.Errorf("backend %q: expected the Extension provider to be named, got: %v", c.backendType, warnings)
		}
		if !strings.Contains(joined, c.wantExtName) {
			t.Errorf("backend %q: expected extension name %q, got: %v", c.backendType, c.wantExtName, warnings)
		}
	}

	// The builtin backend has no Extension equivalent — advisory only, no mapping.
	builtin := `
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
  mtls:
    enabledBackend: ca-1
    backends:
      - name: ca-1
        type: builtin
`
	_, warnings := ScanForDeprecations([]byte(builtin), TargetV2)
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "MeshIdentity") {
		t.Errorf("builtin backend should still get the mtls advisory, got: %v", warnings)
	}
	if strings.Contains(joined, "type: Extension") {
		t.Errorf("builtin backend must not be mapped to an Extension provider, got: %v", warnings)
	}
}

// TestScanForDeprecations_DataplaneInboundTags_KubernetesFormat covers the
// spec.networking path; the other inbound-tags test uses Universal layout.
func TestScanForDeprecations_DataplaneInboundTags_KubernetesFormat(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: Dataplane
metadata:
  name: web-01
spec:
  networking:
    address: 192.168.0.1
    inbound:
      - port: 8080
        tags:
          kuma.io/service: web
`
	_, warnings := ScanForDeprecations([]byte(input), TargetV3)
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "inbound[].tags") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected inbound-tags warning for Kubernetes-format Dataplane, got: %v", warnings)
	}
}

// TestScanForDeprecations_InlineOtelEndpoint_ByTarget pins the two different
// messages. The v2 wording carries the do-not-migrate-yet caveat, which is the
// operationally important half: MeshOpenTelemetryBackend does not exist before
// 2.14 and the CP silently skips the OTel route against an older data plane.
func TestScanForDeprecations_InlineOtelEndpoint_ByTarget(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshTrace
metadata:
  name: tracing
spec:
  targetRef:
    kind: Mesh
  default:
    backends:
      - type: OpenTelemetry
        openTelemetry:
          endpoint: otel-collector:4317
`
	_, v2 := ScanForDeprecations([]byte(input), TargetV2)
	joinedV2 := strings.Join(v2, "\n")
	if !strings.Contains(joinedV2, "Do NOT make this change") {
		t.Errorf("v2 message should carry the pre-2.14 caveat, got: %v", v2)
	}
	if !strings.Contains(joinedV2, "labels") || !strings.Contains(joinedV2, "kuma-system") {
		t.Errorf("v2 message should state the backendRef constraints, got: %v", v2)
	}

	_, v3 := ScanForDeprecations([]byte(input), TargetV3)
	joinedV3 := strings.Join(v3, "\n")
	if !strings.Contains(joinedV3, "REMOVED in 3.0") {
		t.Errorf("v3 message should state the removal, got: %v", v3)
	}
	if strings.Contains(joinedV3, "Do NOT make this change") {
		t.Errorf("v3 message should not carry the 2.x caveat, got: %v", v3)
	}
}

// TestScanForDeprecations_MeshTrustOriginIsRemovalNotDeprecation pins the
// escalated wording: spec.origin was removed from the CRD schema in 2.13, so a
// manifest still setting it can be rejected outright rather than merely warned.
func TestScanForDeprecations_MeshTrustOriginIsRemovalNotDeprecation(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshTrust
metadata:
  name: my-trust
spec:
  origin: Zone
  targetRef:
    kind: Mesh
`
	_, warnings := ScanForDeprecations([]byte(input), TargetV2)
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"REMOVED", "rejected", "status.origin.kri"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected MeshTrust warning to mention %q, got: %v", want, warnings)
		}
	}
}

// TestScanForDeprecations_MESDataSource_InlineStringAndTargets covers the
// inlineString variant and both target wordings.
func TestScanForDeprecations_MESDataSource_InlineStringAndTargets(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshExternalService
metadata:
  name: mes
spec:
  tls:
    enabled: true
    verification:
      caCert:
        inlineString: |
          -----BEGIN CERTIFICATE-----
`
	_, v3 := ScanForDeprecations([]byte(input), TargetV3)
	joinedV3 := strings.Join(v3, "\n")
	if !strings.Contains(joinedV3, "caCert.inlineString") {
		t.Errorf("expected inlineString to be flagged, got: %v", v3)
	}
	if !strings.Contains(joinedV3, "removed in 3.0") {
		t.Errorf("v3 wording should state the removal is active, got: %v", v3)
	}
	// inlineString is already plain text, so the base64 caveat must NOT appear.
	if strings.Contains(joinedV3, "base64") {
		t.Errorf("base64 caveat applies to inline only, not inlineString: %v", v3)
	}

	_, v2 := ScanForDeprecations([]byte(input), TargetV2)
	if !strings.Contains(strings.Join(v2, "\n"), "still accepted in 2.14") {
		t.Errorf("v2 wording should mark the removal as forward-looking, got: %v", v2)
	}
}

// TestScanForDeprecations_MeshTrustWithoutOrigin is the negative case: a
// MeshTrust that already relies on status.origin must be silent.
func TestScanForDeprecations_MeshTrustWithoutOrigin(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshTrust
metadata:
  name: my-trust
spec:
  trustDomain: mesh.local
  caBundles:
    - type: Pem
      pem:
        value: "-----BEGIN CERTIFICATE-----"
`
	_, warnings := ScanForDeprecations([]byte(input), TargetV2)
	for _, w := range warnings {
		if strings.Contains(w, "spec.origin") {
			t.Errorf("MeshTrust without spec.origin must not be flagged, got: %v", warnings)
		}
	}
}

// TestScanForDeprecations_MeshOPATargetRefKindMeshService covers the new
// kind: MeshService narrowing (only Mesh/Dataplane remain valid in 3.0),
// distinct from the pre-existing name/namespace/mesh field checks.
func TestScanForDeprecations_MeshOPATargetRefKindMeshService(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshOPA
metadata:
  name: my-opa
spec:
  targetRef:
    kind: MeshService
    labels:
      kuma.io/display-name: backend
  default:
    appendPolicies: []
`
	// v2: forward-looking advisory naming the Dataplane/app replacement — the
	// fix does not run, so the scanner is what tells the operator anything.
	_, v2 := ScanForDeprecations([]byte(input), TargetV2)
	joined := strings.Join(v2, "\n")
	if !strings.Contains(joined, "MeshService") || !strings.Contains(joined, `labels["app"]`) {
		t.Errorf("expected a v2 advisory naming MeshService and labels[\"app\"], got: %v", v2)
	}

	// v3: fixMeshOPATargetRefForV3 runs first inside ScanForDeprecations and
	// converts kind + relabels before the scanner sees the document, so the
	// output has no MeshService left and the fix's own warning describes it.
	out, v3 := ScanForDeprecations([]byte(input), TargetV3)
	var parsed map[string]interface{}
	if err := yaml.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	targetRef := parsed["spec"].(map[string]interface{})["targetRef"].(map[string]interface{})
	if targetRef["kind"] != "Dataplane" {
		t.Errorf("expected targetRef.kind converted to Dataplane under v3, got: %s", out)
	}
	labels := targetRef["labels"].(map[string]interface{})
	if labels["app"] != "backend" {
		t.Errorf("expected labels[\"app\"] carrying the display name under v3, got: %s", out)
	}
	if _, still := labels["kuma.io/display-name"]; still {
		t.Errorf("expected kuma.io/display-name renamed away under v3, got: %s", out)
	}
	if !strings.Contains(strings.Join(v3, "\n"), "converted to kind: Dataplane") {
		t.Errorf("expected the fix's own auto-corrected warning under v3, got: %v", v3)
	}
}

// TestScanForDeprecations_MeshOPALegacyDataSource covers the MeshOPA
// agentConfig/rego DataSource → SecureDataSource rewrite. Under v3 this fires
// through the ScanForDeprecations post-pass even for a document that arrives
// already as kind: MeshOPA (never routed through TransformOPAPolicy) — the
// passthrough case, distinct from the OPAPolicy conversion tests in
// opapolicy_test.go which exercise the same fix via TransformOPAPolicy.
func TestScanForDeprecations_MeshOPALegacyDataSource(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshOPA
metadata:
  name: my-opa
spec:
  targetRef:
    kind: Mesh
  default:
    agentConfig:
      inlineString: "bootstrap: true"
    appendPolicies:
      - rego:
          inlineString: "package envoy.authz"
      - rego:
          secret: my-rego-secret
`
	// v2: forward-looking advisory only, old shape preserved.
	out2, v2 := ScanForDeprecations([]byte(input), TargetV2)
	if !strings.Contains(string(out2), "inlineString:") {
		t.Errorf("v2 target must not rewrite the DataSource shape, got:\n%s", out2)
	}
	joined2 := strings.Join(v2, "\n")
	for _, want := range []string{"agentConfig", "appendPolicies[0].rego", "appendPolicies[1].rego", "SecureDataSource"} {
		if !strings.Contains(joined2, want) {
			t.Errorf("expected v2 advisory to mention %q, got: %v", want, v2)
		}
	}

	// v3: auto-converted to the discriminated shape.
	out3, v3 := ScanForDeprecations([]byte(input), TargetV3)
	var parsed map[string]interface{}
	if err := yaml.Unmarshal(out3, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	def := parsed["spec"].(map[string]interface{})["default"].(map[string]interface{})

	agentConfig := def["agentConfig"].(map[string]interface{})
	if agentConfig["type"] != "InsecureInline" {
		t.Errorf("expected agentConfig converted to InsecureInline, got: %s", out3)
	}
	if agentConfig["insecureInline"].(map[string]interface{})["value"] != "bootstrap: true" {
		t.Errorf("expected agentConfig value preserved, got: %s", out3)
	}

	policies := def["appendPolicies"].([]interface{})
	rego0 := policies[0].(map[string]interface{})["rego"].(map[string]interface{})
	if rego0["type"] != "InsecureInline" || rego0["insecureInline"].(map[string]interface{})["value"] != "package envoy.authz" {
		t.Errorf("expected appendPolicies[0].rego converted to InsecureInline, got: %s", out3)
	}
	rego1 := policies[1].(map[string]interface{})["rego"].(map[string]interface{})
	if rego1["type"] != "Secret" {
		t.Errorf("expected appendPolicies[1].rego converted to Secret, got: %s", out3)
	}
	secretRef := rego1["secretRef"].(map[string]interface{})
	if secretRef["kind"] != "Secret" || secretRef["name"] != "my-rego-secret" {
		t.Errorf("expected secretRef {kind: Secret, name: my-rego-secret}, got: %s", out3)
	}

	joined3 := strings.Join(v3, "\n")
	if !strings.Contains(joined3, "auto-corrected") {
		t.Errorf("expected auto-corrected warnings under v3, got: %v", v3)
	}
	// The forward-looking advisory must not ALSO fire once the fix has run.
	if strings.Contains(joined3, "SecureDataSource type (type + insecureInline.value / secretRef). Rewrite before upgrading") {
		t.Errorf("v2-style advisory must not fire on an already-fixed v3 document, got: %v", v3)
	}
}

// TestScanForDeprecations_DataplaneGatewayBuiltinType covers the removal of
// the BUILTIN gateway type (kuma#18058) — reserved in the proto on 3.0, so a
// Dataplane still using it is rejected at parse time.
func TestScanForDeprecations_DataplaneGatewayBuiltinType(t *testing.T) {
	input := `type: Dataplane
mesh: default
name: gw-01
networking:
  address: 192.168.0.1
  gateway:
    type: BUILTIN
    tags:
      kuma.io/service: edge-gateway
`
	_, v2 := ScanForDeprecations([]byte(input), TargetV2)
	for _, w := range v2 {
		if strings.Contains(w, "BUILTIN") {
			t.Errorf("BUILTIN gateway type must not warn under a v2 target (still valid in 2.x): %v", v2)
		}
	}

	_, v3 := ScanForDeprecations([]byte(input), TargetV3)
	joined := strings.Join(v3, "\n")
	if !strings.Contains(joined, "BUILTIN") || !strings.Contains(joined, "reserved") {
		t.Errorf("expected a v3 BUILTIN gateway warning mentioning the reserved proto ordinal, got: %v", v3)
	}

	// A DELEGATED gateway (the only remaining type) must never be flagged.
	delegated := `type: Dataplane
mesh: default
name: gw-01
networking:
  address: 192.168.0.1
  gateway:
    type: DELEGATED
    tags:
      kuma.io/service: edge-gateway
`
	_, delegatedWarnings := ScanForDeprecations([]byte(delegated), TargetV3)
	for _, w := range delegatedWarnings {
		if strings.Contains(w, "BUILTIN") {
			t.Errorf("DELEGATED gateway must not be flagged, got: %v", delegatedWarnings)
		}
	}
}

// TestScanForDeprecations_MeshLoadBalancingStrategyCrossZoneTarget covers the
// 3.0-dev validator restriction (kuma#18210) limiting crossZone to
// MeshMultiZoneService targets.
func TestScanForDeprecations_MeshLoadBalancingStrategyCrossZoneTarget(t *testing.T) {
	wrongTarget := `
apiVersion: kuma.io/v1alpha1
kind: MeshLoadBalancingStrategy
metadata:
  name: mlbs-1
spec:
  to:
    - targetRef:
        kind: MeshService
        name: backend
      default:
        localityAwareness:
          crossZone: {}
`
	_, v2 := ScanForDeprecations([]byte(wrongTarget), TargetV2)
	for _, w := range v2 {
		if strings.Contains(w, "crossZone") {
			t.Errorf("crossZone-on-wrong-target must not warn under v2 (not yet in the 2.14 line): %v", v2)
		}
	}

	_, v3 := ScanForDeprecations([]byte(wrongTarget), TargetV3)
	joined := strings.Join(v3, "\n")
	if !strings.Contains(joined, "crossZone") || !strings.Contains(joined, "MeshMultiZoneService") {
		t.Errorf("expected a v3 crossZone/MeshMultiZoneService warning, got: %v", v3)
	}

	rightTarget := `
apiVersion: kuma.io/v1alpha1
kind: MeshLoadBalancingStrategy
metadata:
  name: mlbs-1
spec:
  to:
    - targetRef:
        kind: MeshMultiZoneService
        name: backend
      default:
        localityAwareness:
          crossZone: {}
`
	_, clean := ScanForDeprecations([]byte(rightTarget), TargetV3)
	for _, w := range clean {
		if strings.Contains(w, "crossZone") {
			t.Errorf("crossZone on a MeshMultiZoneService target must not be flagged, got: %v", clean)
		}
	}
}

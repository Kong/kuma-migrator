package migrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

const mesWithLegacyTLS = `
apiVersion: kuma.io/v1alpha1
kind: MeshExternalService
metadata:
  name: httpbin
  namespace: kuma-system
  labels:
    kuma.io/mesh: default
spec:
  match:
    type: HostnameGenerator
    port: 443
    protocol: http
  endpoints:
    - address: httpbin.org
      port: 443
  tls:
    enabled: true
    verification:
      caCert:
        inlineString: CA-PEM
      clientCert:
        secret: client-cert
      clientKey:
        inline: Zm9v
`

func mesVerification(t *testing.T, raw []byte) map[string]interface{} {
	t.Helper()
	var obj map[string]interface{}
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	v := mesTLSVerification(obj)
	if v == nil {
		t.Fatalf("no spec.tls.verification in:\n%s", raw)
	}
	return v
}

func TestFixMESDataSourcesForV3_RewritesInlineStringAndSecret(t *testing.T) {
	out, warnings := ScanForDeprecations([]byte(mesWithLegacyTLS), TargetV3)
	v := mesVerification(t, out)

	ca := v["caCert"].(map[string]interface{})
	if ca["type"] != "InsecureInline" || ca["insecureInline"].(map[string]interface{})["value"] != "CA-PEM" {
		t.Errorf("caCert not rewritten to InsecureInline: %v", ca)
	}
	if _, ok := ca["inlineString"]; ok {
		t.Errorf("caCert still carries inlineString: %v", ca)
	}

	cc := v["clientCert"].(map[string]interface{})
	ref, _ := cc["secretRef"].(map[string]interface{})
	if cc["type"] != "Secret" || ref["kind"] != "Secret" || ref["name"] != "client-cert" {
		t.Errorf("clientCert not rewritten to Secret/secretRef: %v", cc)
	}

	// inline (base64) is credential material: left as-is and warned about.
	ck := v["clientKey"].(map[string]interface{})
	if ck["inline"] != "Zm9v" || ck["type"] != nil {
		t.Errorf("clientKey.inline must be left untouched, got %v", ck)
	}

	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"caCert.inlineString rewritten", "clientCert.secret rewritten", "2.14.6", "clientKey.inline", "base64"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected warnings to mention %q, got: %v", want, warnings)
		}
	}
	if strings.Contains(joined, "caCert.inlineString uses the legacy") {
		t.Errorf("a rewritten field must not also get the legacy-shape warning: %v", warnings)
	}
}

func TestFixMESDataSourcesForV3_V2LeavesShapeAndMentions2146(t *testing.T) {
	out, warnings := ScanForDeprecations([]byte(mesWithLegacyTLS), TargetV2)
	if string(out) != mesWithLegacyTLS {
		t.Errorf("v2 must not rewrite the document, got:\n%s", out)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "2.14.6 and later accept both shapes") {
		t.Errorf("v2 advisory should say 2.14.6+ accepts the new shape, got: %v", warnings)
	}
	if strings.Count(joined, "uses the legacy DataSource shape") != 3 {
		t.Errorf("expected one advisory per legacy field, got: %v", warnings)
	}
}

func TestFixMESDataSourcesForV3_AlreadyTypedIsUntouched(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshExternalService
metadata:
  name: ok
spec:
  tls:
    verification:
      caCert:
        type: Secret
        secretRef:
          kind: Secret
          name: ca
`
	out, warnings := ScanForDeprecations([]byte(input), TargetV3)
	if string(out) != input {
		t.Errorf("typed SecureDataSource must pass through unchanged, got:\n%s", out)
	}
	for _, w := range warnings {
		if strings.Contains(w, "DataSource") {
			t.Errorf("unexpected DataSource warning: %s", w)
		}
	}
}

func TestTransformExternalService_V3ConvertsTLSDataSource(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: ExternalService
mesh: default
metadata:
  name: secure-api
spec:
  tags:
    kuma.io/service: secure-api
    kuma.io/protocol: http
  networking:
    address: api.example.com:443
    tls:
      enabled: true
      caCert:
        secret: api-ca
`
	docs, _, _, err := TransformDocumentWithOptions([]byte(input), TransformOptions{Target: TargetV3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ca := mesVerification(t, docs[0])["caCert"].(map[string]interface{})
	if ca["type"] != "Secret" {
		t.Errorf("converted ExternalService caCert should be SecureDataSource under v3, got %v", ca)
	}

	docs, _, _, err = TransformDocumentWithOptions([]byte(input), TransformOptions{Target: TargetV2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ca = mesVerification(t, docs[0])["caCert"].(map[string]interface{})
	if ca["secret"] != "api-ca" || ca["type"] != nil {
		t.Errorf("v2 output must keep the 2.x DataSource shape, got %v", ca)
	}
}

func TestWarnMESZoneLabel(t *testing.T) {
	k8s := `
apiVersion: kuma.io/v1alpha1
kind: MeshExternalService
metadata:
  name: db
  labels:
    kuma.io/mesh: default
    kuma.io/zone: zone-east
spec: {}
`
	universal := `
type: MeshExternalService
name: db
mesh: default
labels:
  kuma.io/zone: zone-east
spec: {}
`
	for name, input := range map[string]string{"kubernetes": k8s, "universal": universal} {
		t.Run(name, func(t *testing.T) {
			_, v3 := ScanForDeprecations([]byte(input), TargetV3)
			if !strings.Contains(strings.Join(v3, "\n"), "kuma.io/zone=zone-east") {
				t.Errorf("expected per-zone routing warning under v3, got: %v", v3)
			}
			_, v2 := ScanForDeprecations([]byte(input), TargetV2)
			if strings.Contains(strings.Join(v2, "\n"), "kuma.io/zone") {
				t.Errorf("per-zone routing still works on 2.x — no v2 warning expected, got: %v", v2)
			}
		})
	}
}

const plainMES = `
apiVersion: kuma.io/v1alpha1
kind: MeshExternalService
metadata:
  name: httpbin
  labels:
    kuma.io/mesh: payments
spec: {}
`

func TestWarnMESRequiresMeshIdentity(t *testing.T) {
	cases := []struct {
		name   string
		opts   TransformOptions
		expect bool
	}{
		{"v3 no index", TransformOptions{Target: TargetV3}, true},
		{"v3 identity in another mesh", TransformOptions{Target: TargetV3, MeshIdentities: MeshIdentityIndex{"default": true}}, true},
		{"v3 identity in same mesh", TransformOptions{Target: TargetV3, MeshIdentities: MeshIdentityIndex{"payments": true}}, false},
		{"v2", TransformOptions{Target: TargetV2}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, warnings, _, err := TransformDocumentWithOptions([]byte(plainMES), tc.opts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := strings.Contains(strings.Join(warnings, "\n"), "cluster_not_found")
			if got != tc.expect {
				t.Errorf("MeshIdentity warning = %v, want %v; warnings: %v", got, tc.expect, warnings)
			}
		})
	}
}

func TestWarnMESRequiresMeshIdentity_ConvertedExternalService(t *testing.T) {
	input := `
type: ExternalService
mesh: default
name: httpbin
tags:
  kuma.io/service: httpbin
networking:
  address: httpbin.org:80
`
	_, warnings, _, err := TransformDocumentWithOptions([]byte(input), TransformOptions{Target: TargetV3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "no MeshIdentity for mesh default") {
		t.Errorf("expected MeshIdentity warning on converted ExternalService, got: %v", warnings)
	}
}

func TestBuildTransformOptions_IndexesMeshIdentity(t *testing.T) {
	dir := t.TempDir()
	identity := `
apiVersion: kuma.io/v1alpha1
kind: MeshIdentity
metadata:
  name: identity
  namespace: kuma-system
  labels:
    kuma.io/mesh: payments
spec:
  selector:
    dataplane:
      matchLabels: {}
---
type: MeshIdentity
name: universal-identity
mesh: orders
spec: {}
`
	if err := os.WriteFile(filepath.Join(dir, "identity.yaml"), []byte(identity), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := BuildTransformOptions(dir, TargetV3)
	if !opts.MeshIdentities["payments"] || !opts.MeshIdentities["orders"] {
		t.Fatalf("expected payments and orders indexed, got %v", opts.MeshIdentities)
	}
	if opts.MeshIdentities["default"] {
		t.Errorf("default mesh must not be indexed: %v", opts.MeshIdentities)
	}
}

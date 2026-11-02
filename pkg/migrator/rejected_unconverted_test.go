package migrator

import (
	"strings"
	"testing"
)

// Documents the v3 rewrites leave holding a field 3.0 removes, because the
// manifest lacks what the replacement needs. Each stayed in the apply tree and
// was rejected by Kong Mesh 3.0.1 (test/e2e/results/v3-*.md); the server error
// is quoted above each case. They must come back as RejectedByTargetError so the
// pipeline writes them to the manual tree.

func TestRejectedByTarget_UnconvertedRemovedFields(t *testing.T) {
	cases := []struct {
		name, input, kind, reason string
	}{
		{
			// networking.outbound[0].backendRef: must be defined
			name: "Universal Dataplane with a tag-based outbound",
			input: `type: Dataplane
name: backend-1
mesh: default
labels:
  app: backend
networking:
  address: 10.0.0.11
  inbound:
  - port: 3001
    servicePort: 3000
    tags:
      kuma.io/service: backend
      kuma.io/protocol: http
  outbound:
  - port: 33033
    tags:
      kuma.io/service: redis
`,
			kind:   "Dataplane",
			reason: "networking.outbound[0].backendRef: must be defined",
		},
		{
			// spec.selector.dataplaneTags: unknown field
			name: "Universal MeshService with dataplaneTags",
			input: `type: MeshService
name: legacy-db
mesh: default
spec:
  selector:
    dataplaneTags:
      kuma.io/service: legacy-db
  ports:
  - port: 5432
    targetPort: 5432
    appProtocol: tcp
`,
			kind:   "MeshService",
			reason: "spec.selector.dataplaneTags: unknown field",
		},
		{
			// strict decoding error: unknown field "spec.selector.dataplaneTags"
			name: "Kubernetes MeshService with dataplaneTags",
			input: `apiVersion: kuma.io/v1alpha1
kind: MeshService
metadata:
  name: legacy-db
  namespace: kuma-demo
  labels:
    kuma.io/mesh: default
spec:
  selector:
    dataplaneTags:
      kuma.io/service: legacy-db
  ports:
  - port: 5432
    targetPort: 5432
    appProtocol: tcp
`,
			kind:   "MeshService",
			reason: "spec.selector.dataplaneTags: unknown field",
		},
		{
			// spec.tls.verification.caCert.inline: unknown field
			name: "MeshExternalService with a base64 inline CA",
			input: `type: MeshExternalService
name: example-api
mesh: default
spec:
  match:
    type: HostnameGenerator
    port: 443
    protocol: http
  endpoints:
  - address: api.example.com
    port: 443
  tls:
    enabled: true
    verification:
      mode: Secured
      serverName: api.example.com
      caCert:
        inline: TFMwdExTMUNSVWRKVGlCRFJWSlVTVVpKUTBGVVJTMHRMUzB0Q2c9PQ==
`,
			kind:   "MeshExternalService",
			reason: "spec.tls.verification.caCert.inline: unknown field",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			docs, _, _, err := TransformDocument([]byte(c.input), TargetV3)
			rejected, ok := AsRejectedByTarget(err)
			if !ok {
				t.Fatalf("want RejectedByTargetError, got %v", err)
			}
			if len(docs) != 1 || !rejected.IsRejected(0) {
				t.Fatalf("want the single output document rejected, got %d docs, %+v", len(docs), rejected.Rejected)
			}
			if r := rejected.Rejected[0]; r.Kind != c.kind || !strings.Contains(strings.Join(r.Reasons, " "), c.reason) {
				t.Errorf("rejected = %+v, want kind %s with a reason quoting %q", r, c.kind, c.reason)
			}

			// v2 output goes back to a 2.x control plane, which accepts all of these.
			if _, _, _, err := TransformDocument([]byte(c.input), TargetV2); err != nil {
				t.Errorf("v2: unexpected error %v", err)
			}
		})
	}
}

// The convertible forms stay in the apply tree.
func TestRejectedByTarget_ConvertedRemovedFieldsAccepted(t *testing.T) {
	for name, input := range map[string]string{
		"Dataplane outbound with a decodable service tag": `type: Dataplane
name: web-1
mesh: default
labels:
  app: web
networking:
  address: 10.0.0.12
  inbound:
  - port: 8080
    tags:
      kuma.io/service: web
  outbound:
  - port: 33033
    tags:
      kuma.io/service: redis_kuma-demo_svc_6379
`,
		"MeshService dataplaneTags without reserved keys": `type: MeshService
name: db
mesh: default
spec:
  selector:
    dataplaneTags:
      app: db
  ports:
  - port: 5432
    targetPort: 5432
`,
		"MeshExternalService inlineString CA": `type: MeshExternalService
name: api
mesh: default
spec:
  match:
    type: HostnameGenerator
    port: 443
    protocol: http
  endpoints:
  - address: api.example.com
    port: 443
  tls:
    enabled: true
    verification:
      caCert:
        inlineString: "-----BEGIN CERTIFICATE-----"
`,
	} {
		t.Run(name, func(t *testing.T) {
			_, _, _, err := TransformDocument([]byte(input), TargetV3)
			if r, ok := AsRejectedByTarget(err); ok {
				t.Errorf("unexpected rejection: %+v", r.Rejected)
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// The warning no longer claims the field is dropped silently: 3.0 rejects it.
func TestMeshServiceDataplaneTags_WarningSaysRejected(t *testing.T) {
	in := `type: MeshService
name: legacy-db
mesh: default
spec:
  selector:
    dataplaneTags:
      kuma.io/service: legacy-db
`
	for _, target := range []TargetVersion{TargetV2, TargetV3} {
		_, warnings := ScanForDeprecations([]byte(in), target)
		if hasWarning(warnings, "dropped silently") {
			t.Errorf("%s: warning still says dropped silently: %v", target, warnings)
		}
		if !hasWarning(warnings, "rejects") {
			t.Errorf("%s: expected the warning to say 3.0 rejects it, got: %v", target, warnings)
		}
	}
}

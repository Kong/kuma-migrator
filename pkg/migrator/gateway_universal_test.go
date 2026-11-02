package migrator

import (
	"strings"
	"testing"
)

// e2e (Kong Mesh 2.14.7 and 3.0.1, Universal): a Universal MeshGateway was
// converted to a Gateway API Gateway and listed for kumactl apply, which failed with
//
//	resource[0]: failed to parse resource: invalid resource type ""
//
// Universal has no Gateway API.

const uniGatewaySrc = `type: MeshGateway
name: edge-gateway
mesh: default
selectors:
- match:
    kuma.io/service: edge-gateway
conf:
  listeners:
  - port: 8080
    protocol: HTTP
    hostname: '*'
`

const uniGatewayRouteSrc = `type: MeshGatewayRoute
name: edge-http
mesh: default
selectors:
- match:
    kuma.io/service: edge-gateway
conf:
  http:
    rules:
    - matches:
      - path:
          match: PREFIX
          value: /
      backends:
      - destination:
          kuma.io/service: frontend
`

func TestUniversalGateway_V2KeepsSource(t *testing.T) {
	for _, tc := range []struct{ kind, input string }{
		{"MeshGateway", uniGatewaySrc},
		{"MeshGatewayRoute", uniGatewayRouteSrc},
	} {
		docs, warnings, scenario, err := TransformDocument([]byte(tc.input), TargetV2)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.kind, err)
		}
		if scenario != ScenarioPassthrough {
			t.Errorf("%s: scenario = %s, want passthrough", tc.kind, scenario)
		}
		if len(docs) != 1 || string(docs[0]) != tc.input {
			t.Errorf("%s: want the input unchanged, got:\n%s", tc.kind, docs)
		}
		for _, d := range docs {
			if strings.Contains(string(d), "gateway.networking.k8s.io") {
				t.Errorf("%s: Universal output must not contain Gateway API resources:\n%s", tc.kind, d)
			}
		}
		if !hasWarning(warnings, "Universal has no Gateway API") || !hasWarning(warnings, "Kuma 3.0 removes") {
			t.Errorf("%s: expected the v2 advisory, got: %v", tc.kind, warnings)
		}
	}
}

func TestUniversalGateway_V3RejectedByTarget(t *testing.T) {
	for _, tc := range []struct{ kind, name, input string }{
		{"MeshGateway", "edge-gateway", uniGatewaySrc},
		{"MeshGatewayRoute", "edge-http", uniGatewayRouteSrc},
	} {
		docs, warnings, _, err := TransformDocument([]byte(tc.input), TargetV3)
		rejected, ok := AsRejectedByTarget(err)
		if !ok {
			t.Fatalf("%s: want RejectedByTargetError, got %v", tc.kind, err)
		}
		if len(docs) != 1 || string(docs[0]) != tc.input || !rejected.IsRejected(0) {
			t.Errorf("%s: want the source returned as the rejected document, got %d docs", tc.kind, len(docs))
		}
		r := rejected.Rejected[0]
		if r.Kind != tc.kind || r.Name != tc.name {
			t.Errorf("%s: rejected doc = %s %q", tc.kind, r.Kind, r.Name)
		}
		if !strings.Contains(err.Error(), "delegated gateway") || !strings.Contains(err.Error(), "--exclude-inbound-ports") {
			t.Errorf("%s: error should carry the delegated-gateway guidance: %v", tc.kind, err)
		}
		if !hasWarning(warnings, "Delete the MeshGateway/MeshGatewayRoute objects before upgrading") {
			t.Errorf("%s: expected the guidance as a warning too, got: %v", tc.kind, warnings)
		}
	}
}

// Kubernetes input is unaffected: it still becomes Gateway API.
func TestUniversalGateway_KubernetesStillConverted(t *testing.T) {
	docs, _, scenario, err := TransformDocument([]byte(`apiVersion: kuma.io/v1alpha1
kind: MeshGateway
metadata:
  name: edge-gateway
  labels:
    kuma.io/mesh: default
spec:
  selectors:
  - match:
      kuma.io/service: edge-gateway_kuma-demo_svc
  conf:
    listeners:
    - port: 8080
      protocol: HTTP
`), TargetV2)
	if err != nil {
		t.Fatal(err)
	}
	if scenario != ScenarioGateway || !strings.Contains(string(docs[0]), "kind: Gateway") {
		t.Errorf("Kubernetes MeshGateway should still convert to a Gateway, got %s:\n%s", scenario, docs[0])
	}
}

package migrator

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// TestTransformMeshGatewayRoute_CrossNamespaceReferenceGrant: a backend in another
// namespace than the route needs a ReferenceGrant in the backend namespace.
func TestTransformMeshGatewayRoute_CrossNamespaceReferenceGrant(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayRoute
metadata:
  name: edge
  namespace: gateways
  labels:
    kuma.io/mesh: prod
spec:
  selectors:
    - match:
        kuma.io/service: edge_gateways_svc
  conf:
    http:
      rules:
        - matches:
            - path: {match: PREFIX, value: /}
          filters:
            - mirror:
                percentage: 10
                backend:
                  destination:
                    kuma.io/service: shadow_apps_svc_9090
          backends:
            - weight: 1
              destination:
                kuma.io/service: api_apps_svc_8080
            - weight: 1
              destination:
                kuma.io/service: local_gateways_svc_80
`
	docs, warnings, err := TransformMeshGatewayRoute([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected HTTPRoute + 1 ReferenceGrant, got %d docs", len(docs))
	}
	route := mustUnmarshalGR(t, docs[0])
	if route["metadata"].(map[string]interface{})["labels"].(map[string]interface{})["kuma.io/mesh"] != "prod" {
		t.Errorf("expected kuma.io/mesh label on HTTPRoute: %v", route["metadata"])
	}

	grant := mustUnmarshalGR(t, docs[1])
	if grant["kind"] != "ReferenceGrant" || grant["apiVersion"] != "gateway.networking.k8s.io/v1beta1" {
		t.Fatalf("expected ReferenceGrant v1beta1, got %v %v", grant["apiVersion"], grant["kind"])
	}
	meta := grant["metadata"].(map[string]interface{})
	if meta["namespace"] != "apps" {
		t.Errorf("grant must live in the backend namespace, got %v", meta["namespace"])
	}
	spec := grant["spec"].(map[string]interface{})
	from := spec["from"].([]interface{})[0].(map[string]interface{})
	if from["kind"] != "HTTPRoute" || from["namespace"] != "gateways" || from["group"] != "gateway.networking.k8s.io" {
		t.Errorf("unexpected from: %v", from)
	}
	to := spec["to"].([]interface{})
	if len(to) != 2 {
		t.Fatalf("expected api + shadow (mirror) in to[], got %v", to)
	}
	if to[0].(map[string]interface{})["name"] != "api" || to[1].(map[string]interface{})["name"] != "shadow" {
		t.Errorf("unexpected to[]: %v", to)
	}
	if to[0].(map[string]interface{})["kind"] != "Service" {
		t.Errorf("to[].kind must be Service: %v", to[0])
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "Generated ReferenceGrant apps/gateways-edge") {
		t.Errorf("expected ReferenceGrant warning, got: %v", warnings)
	}
}

// TestTransformMeshGatewayRoute_NoNamespaceWarnsInsteadOfGrant: MeshGatewayRoute is
// cluster-scoped, so the route namespace is usually unknown — warn, don't guess.
func TestTransformMeshGatewayRoute_NoNamespaceWarnsInsteadOfGrant(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayRoute
metadata:
  name: edge
spec:
  selectors:
    - match:
        kuma.io/service: edge_gateways_svc
  conf:
    tcp:
      rules:
        - backends:
            - destination:
                kuma.io/service: redis_cache_svc_6379
`
	docs, warnings, err := TransformMeshGatewayRoute([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected no ReferenceGrant without a route namespace, got %d docs", len(docs))
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, `TCPRoute "edge" has no namespace`) || !strings.Contains(joined, "cache") {
		t.Errorf("expected unset-namespace ReferenceGrant warning, got: %v", warnings)
	}
}

// TestConvertGWSingleBackend_PortIsInteger: Gateway API types backendRefs[].port as
// int32; a string fails schema validation.
func TestConvertGWSingleBackend_PortIsInteger(t *testing.T) {
	ref, w := convertGWSingleBackend(gwBackend{Destination: map[string]string{"kuma.io/service": "api_apps_svc_8080"}}, "r", 0)
	if p, ok := ref["port"].(int); !ok || p != 8080 {
		t.Errorf("port = %#v, want int 8080", ref["port"])
	}
	if len(w) != 0 {
		t.Errorf("unexpected warnings: %v", w)
	}

	ref, w = convertGWSingleBackend(gwBackend{Destination: map[string]string{"kuma.io/service": "api_apps_svc_http"}}, "r", 0)
	if _, ok := ref["port"]; ok {
		t.Errorf("non-numeric port must not be emitted, got %v", ref["port"])
	}
	if !strings.Contains(strings.Join(w, "\n"), "not a valid port number") {
		t.Errorf("expected invalid-port warning, got: %v", w)
	}
}

// TestTransformMeshGatewayInstance_GatewayClassHasNoMeshMarking: GatewayClass is
// cluster-scoped and Kuma never reads a mesh from it.
func TestTransformMeshGatewayInstance_GatewayClassHasNoMeshMarking(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayInstance
metadata:
  name: edge
  namespace: apps
  labels:
    kuma.io/mesh: prod
spec:
  tags:
    kuma.io/service: edge_apps_svc
`
	docs, _, err := TransformMeshGatewayInstance([]byte(input), TargetV2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var gc map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &gc); err != nil {
		t.Fatal(err)
	}
	meta := gc["metadata"].(map[string]interface{})
	if meta["annotations"] != nil || meta["labels"] != nil {
		t.Errorf("GatewayClass must carry no kuma.io/mesh marking, got %v", meta)
	}
}

func TestRemovedGatewaySourceNote_RouteMentionsGatewayParentIgnored(t *testing.T) {
	note := removedGatewaySourceNote(ScenarioGatewayRoute)
	if !strings.Contains(note, "ignores HTTPRoutes whose parentRefs target a Gateway") {
		t.Errorf("v3 route note should say Kuma ignores Gateway-parented HTTPRoutes: %s", note)
	}
	if strings.Contains(removedGatewaySourceNote(ScenarioGateway), "ignores HTTPRoutes") {
		t.Errorf("the Gateway note must not carry the route-specific sentence")
	}
}

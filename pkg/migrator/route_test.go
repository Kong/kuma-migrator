package migrator

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestTransformMeshHTTPRoute_Basic(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshHTTPRoute
metadata:
  name: my-route
  namespace: kuma-demo
  labels:
    kuma.io/mesh: default
spec:
  targetRef:
    kind: MeshGateway
    name: edge-gateway
    namespace: kuma-demo
  to:
    - targetRef:
        kind: MeshService
        name: backend
      rules:
        - matches:
            - path:
                type: PathPrefix
                value: /api
          default:
            backendRefs:
              - kind: MeshService
                name: backend
                port: 8080
                weight: 100
`
	docs, warnings, err := TransformMeshHTTPRoute([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}

	var route map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &route); err != nil {
		t.Fatalf("unmarshal HTTPRoute: %v", err)
	}

	if route["apiVersion"] != "gateway.networking.k8s.io/v1" {
		t.Errorf("expected apiVersion=gateway.networking.k8s.io/v1, got %v", route["apiVersion"])
	}
	if route["kind"] != "HTTPRoute" {
		t.Errorf("expected kind=HTTPRoute, got %v", route["kind"])
	}

	spec := route["spec"].(map[string]interface{})

	// parentRef should be Gateway.
	parentRefs := spec["parentRefs"].([]interface{})
	pr := parentRefs[0].(map[string]interface{})
	if pr["kind"] != "Gateway" {
		t.Errorf("expected parentRef.kind=Gateway, got %v", pr["kind"])
	}
	if pr["name"] != "edge-gateway" {
		t.Errorf("expected parentRef.name=edge-gateway, got %v", pr["name"])
	}

	// rules[0].backendRefs[0] should be Service kind.
	rules := spec["rules"].([]interface{})
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	rule := rules[0].(map[string]interface{})
	backendRefs := rule["backendRefs"].([]interface{})
	br := backendRefs[0].(map[string]interface{})
	if br["kind"] != "Service" {
		t.Errorf("expected backendRef.kind=Service, got %v", br["kind"])
	}
	if br["name"] != "backend" {
		t.Errorf("expected backendRef.name=backend, got %v", br["name"])
	}
	if br["group"] != "" {
		t.Errorf("expected backendRef.group='', got %v", br["group"])
	}

	_ = warnings
}

func TestTransformMeshHTTPRoute_TrafficSplitting(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshHTTPRoute
metadata:
  name: split-route
  namespace: apps
spec:
  targetRef:
    kind: MeshGateway
    name: gw
  to:
    - targetRef:
        kind: MeshService
        name: backend
      rules:
        - default:
            backendRefs:
              - kind: MeshService
                name: backend-v1
                port: 8080
                weight: 90
              - kind: MeshServiceSubset
                name: backend
                tags:
                  version: v2
                port: 8080
                weight: 10
`
	docs, warnings, err := TransformMeshHTTPRoute([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var route map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &route); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	spec := route["spec"].(map[string]interface{})
	rules := spec["rules"].([]interface{})
	rule := rules[0].(map[string]interface{})
	backendRefs := rule["backendRefs"].([]interface{})
	if len(backendRefs) != 2 {
		t.Fatalf("expected 2 backendRefs, got %d", len(backendRefs))
	}

	// Both should be kind: Service.
	for i, raw := range backendRefs {
		br := raw.(map[string]interface{})
		if br["kind"] != "Service" {
			t.Errorf("backendRefs[%d].kind should be Service, got %v", i, br["kind"])
		}
		// tags should be dropped.
		if _, hasTags := br["tags"]; hasTags {
			t.Errorf("backendRefs[%d].tags should be removed", i)
		}
	}

	// Expect warnings about MeshServiceSubset and dropped tags.
	hasSubsetWarn := false
	hasTagsWarn := false
	for _, w := range warnings {
		if strings.Contains(w, "MeshServiceSubset") {
			hasSubsetWarn = true
		}
		if strings.Contains(w, "tags") {
			hasTagsWarn = true
		}
	}
	if !hasSubsetWarn {
		t.Error("expected warning about MeshServiceSubset")
	}
	if !hasTagsWarn {
		t.Error("expected warning about dropped tags")
	}
}

func TestTransformMeshHTTPRoute_HeaderFilters(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshHTTPRoute
metadata:
  name: header-route
  namespace: apps
spec:
  targetRef:
    kind: MeshGateway
    name: gw
  to:
    - targetRef:
        kind: MeshService
        name: backend
      rules:
        - default:
            filters:
              - type: RequestHeaderModifier
                requestHeaderModifier:
                  set:
                    - name: X-Version
                      value: v2
                  remove:
                    - X-Old-Header
            backendRefs:
              - kind: MeshService
                name: backend
                port: 8080
`
	docs, _, err := TransformMeshHTTPRoute([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var route map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &route); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	spec := route["spec"].(map[string]interface{})
	rules := spec["rules"].([]interface{})
	rule := rules[0].(map[string]interface{})
	filters := rule["filters"].([]interface{})
	if len(filters) != 1 {
		t.Fatalf("expected 1 filter, got %d", len(filters))
	}
	filter := filters[0].(map[string]interface{})
	if filter["type"] != "RequestHeaderModifier" {
		t.Errorf("expected filter type=RequestHeaderModifier, got %v", filter["type"])
	}
}

func TestTransformMeshHTTPRoute_RequestMirrorPercentage(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshHTTPRoute
metadata:
  name: mirror-route
  namespace: apps
spec:
  targetRef:
    kind: MeshGateway
    name: gw
  to:
    - targetRef:
        kind: MeshService
        name: backend
      rules:
        - default:
            filters:
              - type: RequestMirror
                requestMirror:
                  backendRef:
                    kind: MeshService
                    name: mirror
                    port: 8080
                  percentage: 10
            backendRefs:
              - kind: MeshService
                name: backend
                port: 8080
`
	docs, _, err := TransformMeshHTTPRoute([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var route map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &route); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	spec := route["spec"].(map[string]interface{})
	rules := spec["rules"].([]interface{})
	rule := rules[0].(map[string]interface{})
	filters := rule["filters"].([]interface{})
	filter := filters[0].(map[string]interface{})
	mirror := filter["requestMirror"].(map[string]interface{})

	// percentage → percent.
	if _, hasOld := mirror["percentage"]; hasOld {
		t.Error("mirror should not have 'percentage' field after conversion")
	}
	if mirror["percent"] != float64(10) {
		t.Errorf("expected mirror.percent=10, got %v", mirror["percent"])
	}

	// backendRef kind should be converted to Service.
	bref := mirror["backendRef"].(map[string]interface{})
	if bref["kind"] != "Service" {
		t.Errorf("expected mirror backendRef.kind=Service, got %v", bref["kind"])
	}
}

func TestTransformMeshHTTPRoute_HeaderMatchPresent(t *testing.T) {
	// Present/Absent header match types should be removed with a warning.
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshHTTPRoute
metadata:
  name: header-match-route
  namespace: apps
spec:
  targetRef:
    kind: MeshGateway
    name: gw
  to:
    - targetRef:
        kind: MeshService
        name: backend
      rules:
        - matches:
            - headers:
                - name: X-Feature
                  type: Present
          default:
            backendRefs:
              - kind: MeshService
                name: backend
                port: 8080
`
	docs, warnings, err := TransformMeshHTTPRoute([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Warning expected.
	hasPresentWarn := false
	for _, w := range warnings {
		if strings.Contains(w, "Present") {
			hasPresentWarn = true
		}
	}
	if !hasPresentWarn {
		t.Error("expected warning about Present header match type")
	}

	// The match should either be removed or have no headers.
	var route map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &route); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	spec := route["spec"].(map[string]interface{})
	rules := spec["rules"].([]interface{})
	rule := rules[0].(map[string]interface{})
	if matches, ok := rule["matches"].([]interface{}); ok {
		for _, m := range matches {
			match := m.(map[string]interface{})
			if _, hasHeaders := match["headers"]; hasHeaders {
				t.Error("match should not have headers after Present type removal")
			}
		}
	}
}

func TestTransformMeshHTTPRoute_ListenerTagsSectionName(t *testing.T) {
	// targetRef with tags that contain a 'port' key → sectionName.
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshHTTPRoute
metadata:
  name: tagged-route
  namespace: apps
spec:
  targetRef:
    kind: MeshGateway
    name: edge-gw
    tags:
      port: http-8080
  to:
    - targetRef:
        kind: MeshService
        name: backend
      rules:
        - default:
            backendRefs:
              - kind: MeshService
                name: backend
                port: 8080
`
	docs, warnings, err := TransformMeshHTTPRoute([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var route map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &route); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	spec := route["spec"].(map[string]interface{})
	parentRefs := spec["parentRefs"].([]interface{})
	pr := parentRefs[0].(map[string]interface{})
	if pr["sectionName"] != "http-8080" {
		t.Errorf("expected sectionName=http-8080 from tags, got %v", pr["sectionName"])
	}

	_ = warnings
}

func TestTransformMeshHTTPRoute_MeshServiceParent(t *testing.T) {
	// targetRef kind=MeshService → GAMMA parentRef kind=Service.
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshHTTPRoute
metadata:
  name: gamma-route
  namespace: apps
spec:
  targetRef:
    kind: MeshService
    name: frontend
    namespace: apps
  to:
    - targetRef:
        kind: MeshService
        name: backend
      rules:
        - default:
            backendRefs:
              - kind: MeshService
                name: backend
                port: 8080
`
	docs, warnings, err := TransformMeshHTTPRoute([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var route map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &route); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	spec := route["spec"].(map[string]interface{})
	parentRefs := spec["parentRefs"].([]interface{})
	pr := parentRefs[0].(map[string]interface{})
	if pr["kind"] != "Service" {
		t.Errorf("expected parentRef.kind=Service for GAMMA, got %v", pr["kind"])
	}
	if pr["group"] != "" {
		t.Errorf("expected parentRef.group='' for Service, got %v", pr["group"])
	}

	// GAMMA warning expected.
	hasGAMMAWarn := false
	for _, w := range warnings {
		if strings.Contains(w, "GAMMA") {
			hasGAMMAWarn = true
		}
	}
	if !hasGAMMAWarn {
		t.Error("expected GAMMA warning for MeshService parent")
	}
}

func TestTransformMeshTCPRoute_Basic(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshTCPRoute
metadata:
  name: tcp-route
  namespace: kuma-demo
  labels:
    kuma.io/mesh: default
spec:
  targetRef:
    kind: MeshGateway
    name: tcp-gateway
  to:
    - targetRef:
        kind: MeshService
        name: db
      rules:
        - default:
            backendRefs:
              - kind: MeshService
                name: db-v1
                port: 5432
                weight: 80
              - kind: MeshService
                name: db-v2
                port: 5432
                weight: 20
`
	docs, _, err := TransformMeshTCPRoute([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}

	var route map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &route); err != nil {
		t.Fatalf("unmarshal TCPRoute: %v", err)
	}

	if route["apiVersion"] != "gateway.networking.k8s.io/v1alpha2" {
		t.Errorf("expected apiVersion=gateway.networking.k8s.io/v1alpha2, got %v", route["apiVersion"])
	}
	if route["kind"] != "TCPRoute" {
		t.Errorf("expected kind=TCPRoute, got %v", route["kind"])
	}

	spec := route["spec"].(map[string]interface{})
	parentRefs := spec["parentRefs"].([]interface{})
	pr := parentRefs[0].(map[string]interface{})
	if pr["kind"] != "Gateway" {
		t.Errorf("expected parentRef.kind=Gateway, got %v", pr["kind"])
	}
	if pr["name"] != "tcp-gateway" {
		t.Errorf("expected parentRef.name=tcp-gateway, got %v", pr["name"])
	}

	rules := spec["rules"].([]interface{})
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	rule := rules[0].(map[string]interface{})
	backendRefs := rule["backendRefs"].([]interface{})
	if len(backendRefs) != 2 {
		t.Fatalf("expected 2 backendRefs, got %d", len(backendRefs))
	}
	for i, raw := range backendRefs {
		br := raw.(map[string]interface{})
		if br["kind"] != "Service" {
			t.Errorf("backendRefs[%d].kind should be Service, got %v", i, br["kind"])
		}
	}
}

func TestTransformMeshTCPRoute_MultipleToEntries(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshTCPRoute
metadata:
  name: multi-route
  namespace: apps
spec:
  targetRef:
    kind: MeshGateway
    name: gw
  to:
    - targetRef:
        kind: MeshService
        name: svc-a
      rules:
        - default:
            backendRefs:
              - kind: MeshService
                name: svc-a
                port: 8001
    - targetRef:
        kind: MeshService
        name: svc-b
      rules:
        - default:
            backendRefs:
              - kind: MeshService
                name: svc-b
                port: 8002
`
	docs, _, err := TransformMeshTCPRoute([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var route map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &route); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	spec := route["spec"].(map[string]interface{})
	rules := spec["rules"].([]interface{})
	// Two to[] entries with one rule each → two rules.
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules from 2 to[] entries, got %d", len(rules))
	}
}

package migrator

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// helper to unmarshal a YAML doc and drill into nested maps.
func mustUnmarshalGR(t *testing.T, raw []byte) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := yaml.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// TestTransformMeshGatewayRoute_BasicHTTP covers the canonical example from the
// migration guide: single selector, single rule with a prefix path match, one backend.
func TestTransformMeshGatewayRoute_BasicHTTP(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayRoute
mesh: default
metadata:
  name: demo-app-gateway
  namespace: kuma-demo
spec:
  selectors:
    - match:
        kuma.io/service: demo-app-gateway_kuma-demo_svc
  conf:
    http:
      hostnames:
        - example.com
      rules:
        - matches:
            - path:
                match: PREFIX
                value: /
          backends:
            - weight: 1
              destination:
                kuma.io/service: demo-app_kuma-demo_svc_5000
`
	docs, warnings, err := TransformMeshGatewayRoute([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}

	out := mustUnmarshalGR(t, docs[0])

	if out["kind"] != "HTTPRoute" {
		t.Errorf("expected kind=HTTPRoute, got %v", out["kind"])
	}
	if out["apiVersion"] != gatewayAPIVersion {
		t.Errorf("expected apiVersion=%s, got %v", gatewayAPIVersion, out["apiVersion"])
	}

	// metadata
	meta := out["metadata"].(map[string]interface{})
	if meta["name"] != "demo-app-gateway" {
		t.Errorf("expected name=demo-app-gateway, got %v", meta["name"])
	}
	ann := meta["annotations"].(map[string]interface{})
	if ann["kuma.io/mesh"] != "default" {
		t.Errorf("expected mesh annotation=default, got %v", ann["kuma.io/mesh"])
	}

	spec := out["spec"].(map[string]interface{})

	// hostnames
	hostnames := spec["hostnames"].([]interface{})
	if len(hostnames) != 1 || hostnames[0] != "example.com" {
		t.Errorf("expected hostnames=[example.com], got %v", hostnames)
	}

	// parentRefs — derived from selector
	parentRefs := spec["parentRefs"].([]interface{})
	if len(parentRefs) != 1 {
		t.Fatalf("expected 1 parentRef, got %d", len(parentRefs))
	}
	pr := parentRefs[0].(map[string]interface{})
	if pr["kind"] != "Gateway" {
		t.Errorf("expected parentRef.kind=Gateway, got %v", pr["kind"])
	}
	if pr["name"] != "demo-app-gateway" {
		t.Errorf("expected parentRef.name=demo-app-gateway, got %v", pr["name"])
	}

	// rules
	rules := spec["rules"].([]interface{})
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	rule := rules[0].(map[string]interface{})

	// match — PREFIX → PathPrefix
	matches := rule["matches"].([]interface{})
	match := matches[0].(map[string]interface{})
	path := match["path"].(map[string]interface{})
	if path["type"] != "PathPrefix" {
		t.Errorf("expected path.type=PathPrefix, got %v", path["type"])
	}
	if path["value"] != "/" {
		t.Errorf("expected path.value=/, got %v", path["value"])
	}

	// backendRefs — parsed from svc tag
	backendRefs := rule["backendRefs"].([]interface{})
	if len(backendRefs) != 1 {
		t.Fatalf("expected 1 backendRef, got %d", len(backendRefs))
	}
	br := backendRefs[0].(map[string]interface{})
	if br["name"] != "demo-app" {
		t.Errorf("expected backendRef.name=demo-app, got %v", br["name"])
	}
	if br["namespace"] != "kuma-demo" {
		t.Errorf("expected backendRef.namespace=kuma-demo, got %v", br["namespace"])
	}
	if br["port"] != "5000" {
		t.Errorf("expected backendRef.port=5000, got %v", br["port"])
	}
	if br["kind"] != "Service" {
		t.Errorf("expected backendRef.kind=Service, got %v", br["kind"])
	}

	// No warnings expected for this clean input
	for _, w := range warnings {
		t.Logf("warning: %s", w)
	}

	_ = warnings
}

func TestTransformMeshGatewayRoute_PathMatchTypes(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"EXACT", "Exact"},
		{"PREFIX", "PathPrefix"},
		{"REGEX", "RegularExpression"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayRoute
mesh: default
metadata:
  name: my-route
spec:
  selectors:
    - match:
        kuma.io/service: gw_demo_svc
  conf:
    http:
      rules:
        - matches:
            - path:
                match: ` + tc.input + `
                value: /foo
          backends:
            - weight: 1
              destination:
                kuma.io/service: backend_demo_svc_3000
`
			docs, _, err := TransformMeshGatewayRoute([]byte(input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			out := mustUnmarshalGR(t, docs[0])
			spec := out["spec"].(map[string]interface{})
			rules := spec["rules"].([]interface{})
			rule := rules[0].(map[string]interface{})
			matches := rule["matches"].([]interface{})
			match := matches[0].(map[string]interface{})
			path := match["path"].(map[string]interface{})
			if path["type"] != tc.expected {
				t.Errorf("expected type=%s, got %v", tc.expected, path["type"])
			}
		})
	}
}

func TestTransformMeshGatewayRoute_HeaderMatchAbsentPresent(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayRoute
mesh: default
metadata:
  name: my-route
spec:
  selectors:
    - match:
        kuma.io/service: gw_demo_svc
  conf:
    http:
      rules:
        - matches:
            - path:
                match: PREFIX
                value: /
              headers:
                - match: ABSENT
                  name: x-baz
                - match: PRESENT
                  name: x-bar
                - match: EXACT
                  name: x-foo
                  value: my-value
          backends:
            - weight: 1
              destination:
                kuma.io/service: backend_demo_svc_3000
`
	docs, warnings, err := TransformMeshGatewayRoute([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := mustUnmarshalGR(t, docs[0])
	spec := out["spec"].(map[string]interface{})
	rules := spec["rules"].([]interface{})
	rule := rules[0].(map[string]interface{})
	matches := rule["matches"].([]interface{})
	match := matches[0].(map[string]interface{})

	// ABSENT and PRESENT should have been dropped; only EXACT should remain.
	headers := match["headers"].([]interface{})
	if len(headers) != 1 {
		t.Errorf("expected 1 header (ABSENT/PRESENT dropped), got %d", len(headers))
	}
	hdr := headers[0].(map[string]interface{})
	if hdr["type"] != "Exact" {
		t.Errorf("expected Exact, got %v", hdr["type"])
	}

	// Should have 2 warnings for ABSENT and PRESENT.
	absentPresentWarns := 0
	for _, w := range warnings {
		if strings.Contains(w, "ABSENT") || strings.Contains(w, "PRESENT") {
			absentPresentWarns++
		}
	}
	if absentPresentWarns != 2 {
		t.Errorf("expected 2 ABSENT/PRESENT warnings, got %d: %v", absentPresentWarns, warnings)
	}
}

func TestTransformMeshGatewayRoute_QueryParams(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayRoute
mesh: default
metadata:
  name: my-route
spec:
  selectors:
    - match:
        kuma.io/service: gw_demo_svc
  conf:
    http:
      rules:
        - matches:
            - path:
                match: PREFIX
                value: /
              query_parameters:
                - match: EXACT
                  name: customer
                  value: kong
          backends:
            - weight: 1
              destination:
                kuma.io/service: backend_demo_svc_3000
`
	docs, _, err := TransformMeshGatewayRoute([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := mustUnmarshalGR(t, docs[0])
	spec := out["spec"].(map[string]interface{})
	rules := spec["rules"].([]interface{})
	rule := rules[0].(map[string]interface{})
	matches := rule["matches"].([]interface{})
	match := matches[0].(map[string]interface{})

	// query_parameters → queryParams
	qps, ok := match["queryParams"].([]interface{})
	if !ok || len(qps) != 1 {
		t.Fatalf("expected 1 queryParam, got %v", match["queryParams"])
	}
	qp := qps[0].(map[string]interface{})
	if qp["name"] != "customer" || qp["value"] != "kong" || qp["type"] != "Exact" {
		t.Errorf("unexpected queryParam: %v", qp)
	}
}

func TestTransformMeshGatewayRoute_Filters(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayRoute
mesh: default
metadata:
  name: my-route
spec:
  selectors:
    - match:
        kuma.io/service: gw_demo_svc
  conf:
    http:
      rules:
        - matches:
            - path:
                match: PREFIX
                value: /
          filters:
            - request_header:
                set:
                  - name: x-custom
                    value: foo
                remove:
                  - x-old
            - redirect:
                scheme: https
                hostname: example.com
                port: 443
                status_code: 301
            - rewrite:
                replace_prefix_match: /new
            - mirror:
                percentage: 10.0
                backend:
                  weight: 1
                  destination:
                    kuma.io/service: mirror_demo_svc_8080
          backends:
            - weight: 1
              destination:
                kuma.io/service: backend_demo_svc_3000
`
	docs, warnings, err := TransformMeshGatewayRoute([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := mustUnmarshalGR(t, docs[0])
	spec := out["spec"].(map[string]interface{})
	rules := spec["rules"].([]interface{})
	rule := rules[0].(map[string]interface{})
	filters := rule["filters"].([]interface{})

	if len(filters) != 4 {
		t.Fatalf("expected 4 filters, got %d", len(filters))
	}

	// RequestHeaderModifier
	f0 := filters[0].(map[string]interface{})
	if f0["type"] != "RequestHeaderModifier" {
		t.Errorf("filter[0] type: expected RequestHeaderModifier, got %v", f0["type"])
	}
	rhm := f0["requestHeaderModifier"].(map[string]interface{})
	if rhm["set"] == nil {
		t.Error("expected requestHeaderModifier.set")
	}

	// RequestRedirect
	f1 := filters[1].(map[string]interface{})
	if f1["type"] != "RequestRedirect" {
		t.Errorf("filter[1] type: expected RequestRedirect, got %v", f1["type"])
	}
	rr := f1["requestRedirect"].(map[string]interface{})
	if rr["statusCode"] == nil {
		t.Error("expected requestRedirect.statusCode (status_code → statusCode)")
	}

	// URLRewrite
	f2 := filters[2].(map[string]interface{})
	if f2["type"] != "URLRewrite" {
		t.Errorf("filter[2] type: expected URLRewrite, got %v", f2["type"])
	}
	uw := f2["urlRewrite"].(map[string]interface{})
	path := uw["path"].(map[string]interface{})
	if path["type"] != "ReplacePrefixMatch" {
		t.Errorf("expected ReplacePrefixMatch, got %v", path["type"])
	}

	// RequestMirror
	f3 := filters[3].(map[string]interface{})
	if f3["type"] != "RequestMirror" {
		t.Errorf("filter[3] type: expected RequestMirror, got %v", f3["type"])
	}
	rm := f3["requestMirror"].(map[string]interface{})
	if rm["percent"] == nil {
		t.Error("expected requestMirror.percent (percentage → percent)")
	}
	bref := rm["backendRef"].(map[string]interface{})
	if bref["name"] != "mirror" {
		t.Errorf("expected backendRef.name=mirror, got %v", bref["name"])
	}

	_ = warnings
}

func TestTransformMeshGatewayRoute_HostToBackendWarning(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayRoute
mesh: default
metadata:
  name: my-route
spec:
  selectors:
    - match:
        kuma.io/service: gw_demo_svc
  conf:
    http:
      rules:
        - matches:
            - path:
                match: PREFIX
                value: /
          filters:
            - rewrite:
                host_to_backend_hostname: true
          backends:
            - weight: 1
              destination:
                kuma.io/service: backend_demo_svc_3000
`
	_, warnings, err := TransformMeshGatewayRoute([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "host_to_backend_hostname") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected host_to_backend_hostname warning, got: %v", warnings)
	}
}

func TestTransformMeshGatewayRoute_SelectorWithListenerTag(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayRoute
mesh: default
metadata:
  name: my-route
spec:
  selectors:
    - match:
        kuma.io/service: demo-app-gateway_kuma-demo_svc
        port: http-80
  conf:
    http:
      rules:
        - matches:
            - path:
                match: PREFIX
                value: /
          backends:
            - weight: 1
              destination:
                kuma.io/service: backend_demo_svc_3000
`
	docs, _, err := TransformMeshGatewayRoute([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := mustUnmarshalGR(t, docs[0])
	spec := out["spec"].(map[string]interface{})
	parentRefs := spec["parentRefs"].([]interface{})
	pr := parentRefs[0].(map[string]interface{})

	if pr["sectionName"] != "http-80" {
		t.Errorf("expected sectionName=http-80, got %v", pr["sectionName"])
	}
}

func TestTransformMeshGatewayRoute_TCPRoute(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayRoute
mesh: default
metadata:
  name: tcp-route
spec:
  selectors:
    - match:
        kuma.io/service: gw_demo_svc
  conf:
    tcp:
      rules:
        - backends:
            - weight: 1
              destination:
                kuma.io/service: backend_demo_svc_3000
`
	docs, _, err := TransformMeshGatewayRoute([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := mustUnmarshalGR(t, docs[0])
	if out["kind"] != "TCPRoute" {
		t.Errorf("expected kind=TCPRoute, got %v", out["kind"])
	}
	if out["apiVersion"] != gatewayAPIVersionAlpha2 {
		t.Errorf("expected apiVersion=%s, got %v", gatewayAPIVersionAlpha2, out["apiVersion"])
	}
}

func TestTransformMeshGatewayRoute_UniversalSvcTag(t *testing.T) {
	// Universal-mode: kuma.io/service has no _svc_ marker — used as Gateway name directly.
	input := `
kind: MeshGatewayRoute
mesh: default
metadata:
  name: my-route
spec:
  selectors:
    - match:
        kuma.io/service: my-gateway
  conf:
    http:
      rules:
        - matches:
            - path:
                match: EXACT
                value: /health
          backends:
            - weight: 1
              destination:
                kuma.io/service: backend-svc
`
	docs, warnings, err := TransformMeshGatewayRoute([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := mustUnmarshalGR(t, docs[0])
	spec := out["spec"].(map[string]interface{})
	parentRefs := spec["parentRefs"].([]interface{})
	pr := parentRefs[0].(map[string]interface{})

	// Should use the raw value as the Gateway name.
	if pr["name"] != "my-gateway" {
		t.Errorf("expected name=my-gateway, got %v", pr["name"])
	}

	// Should warn about the non-K8s format.
	hasWarn := false
	for _, w := range warnings {
		if strings.Contains(w, "does not follow the K8s _svc_ pattern") {
			hasWarn = true
		}
	}
	if !hasWarn {
		t.Errorf("expected warning about non-K8s svc tag, got: %v", warnings)
	}
}

func TestDetectScenario_MeshGatewayRoute(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayRoute
mesh: default
metadata:
  name: my-route
spec:
  selectors:
    - match:
        kuma.io/service: gw_demo_svc
  conf:
    http:
      rules:
        - matches:
            - path:
                match: PREFIX
                value: /
          backends:
            - weight: 1
              destination:
                kuma.io/service: backend_demo_svc_3000
`
	scenario, err := DetectScenario([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scenario != ScenarioGatewayRoute {
		t.Errorf("expected ScenarioGatewayRoute, got %s", scenario)
	}
}

package migrator

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestTransformMeshGateway_Basic(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGateway
metadata:
  name: edge-gateway
  namespace: kuma-demo
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
        hostname: example.com
`
	docs, warnings, err := TransformMeshGateway([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}

	var gw map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &gw); err != nil {
		t.Fatalf("unmarshal Gateway: %v", err)
	}

	if gw["apiVersion"] != "gateway.networking.k8s.io/v1" {
		t.Errorf("expected apiVersion=gateway.networking.k8s.io/v1, got %v", gw["apiVersion"])
	}
	if gw["kind"] != "Gateway" {
		t.Errorf("expected kind=Gateway, got %v", gw["kind"])
	}

	meta := gw["metadata"].(map[string]interface{})
	if meta["name"] != "edge-gateway" {
		t.Errorf("expected name=edge-gateway, got %v", meta["name"])
	}
	if meta["namespace"] != "kuma-demo" {
		t.Errorf("expected namespace=kuma-demo, got %v", meta["namespace"])
	}
	// Default mesh — no annotation needed.
	if ann, ok := meta["annotations"]; ok {
		t.Errorf("unexpected annotations for default mesh: %v", ann)
	}

	spec := gw["spec"].(map[string]interface{})
	if spec["gatewayClassName"] != "gateways.kuma.io/controller" {
		t.Errorf("expected gatewayClassName=gateways.kuma.io/controller, got %v", spec["gatewayClassName"])
	}

	listeners := spec["listeners"].([]interface{})
	if len(listeners) != 1 {
		t.Fatalf("expected 1 listener, got %d", len(listeners))
	}
	l := listeners[0].(map[string]interface{})
	if l["name"] != "http-8080" {
		t.Errorf("expected listener name=http-8080, got %v", l["name"])
	}
	if l["port"] != float64(8080) {
		t.Errorf("expected port=8080, got %v", l["port"])
	}
	if l["protocol"] != "HTTP" {
		t.Errorf("expected protocol=HTTP, got %v", l["protocol"])
	}
	if l["hostname"] != "example.com" {
		t.Errorf("expected hostname=example.com, got %v", l["hostname"])
	}

	// selectors warning expected.
	hasSelectorWarn := false
	for _, w := range warnings {
		if strings.Contains(w, "selectors") {
			hasSelectorWarn = true
		}
	}
	if !hasSelectorWarn {
		t.Error("expected warning about selectors")
	}
}

func TestTransformMeshGateway_NonDefaultMesh(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGateway
metadata:
  name: my-gw
  namespace: apps
  labels:
    kuma.io/mesh: prod
spec:
  conf:
    listeners:
      - port: 443
        protocol: HTTPS
        tls:
          mode: TERMINATE
          certificates:
            - secret: my-tls-cert
`
	docs, _, err := TransformMeshGateway([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var gw map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &gw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	meta := gw["metadata"].(map[string]interface{})
	ann, ok := meta["annotations"].(map[string]interface{})
	if !ok {
		t.Fatal("expected annotations for non-default mesh")
	}
	if ann["kuma.io/mesh"] != "prod" {
		t.Errorf("expected annotation kuma.io/mesh=prod, got %v", ann["kuma.io/mesh"])
	}

	spec := gw["spec"].(map[string]interface{})
	listeners := spec["listeners"].([]interface{})
	l := listeners[0].(map[string]interface{})
	if l["name"] != "https-443" {
		t.Errorf("expected listener name=https-443, got %v", l["name"])
	}

	tls := l["tls"].(map[string]interface{})
	if tls["mode"] != "Terminate" {
		t.Errorf("expected TLS mode=Terminate, got %v", tls["mode"])
	}
	certRefs := tls["certificateRefs"].([]interface{})
	if len(certRefs) != 1 {
		t.Fatalf("expected 1 certRef, got %d", len(certRefs))
	}
	cert := certRefs[0].(map[string]interface{})
	if cert["name"] != "my-tls-cert" {
		t.Errorf("expected cert name=my-tls-cert, got %v", cert["name"])
	}
	if cert["kind"] != "Secret" {
		t.Errorf("expected cert kind=Secret, got %v", cert["kind"])
	}
}

func TestTransformMeshGateway_TLSPassthrough(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGateway
metadata:
  name: tls-gw
  namespace: apps
spec:
  conf:
    listeners:
      - port: 443
        protocol: TLS
        tls:
          mode: PASSTHROUGH
`
	docs, _, err := TransformMeshGateway([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var gw map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &gw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	spec := gw["spec"].(map[string]interface{})
	listeners := spec["listeners"].([]interface{})
	l := listeners[0].(map[string]interface{})
	tls := l["tls"].(map[string]interface{})
	if tls["mode"] != "Passthrough" {
		t.Errorf("expected TLS mode=Passthrough, got %v", tls["mode"])
	}
}

func TestTransformMeshGateway_CrossMeshWarning(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGateway
metadata:
  name: cross-gw
  namespace: apps
spec:
  conf:
    listeners:
      - port: 8080
        protocol: HTTP
        crossMesh: true
`
	_, warnings, err := TransformMeshGateway([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "crossMesh") {
			found = true
		}
	}
	if !found {
		t.Error("expected crossMesh warning")
	}
}

func TestTransformMeshGatewayInstance(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayInstance
metadata:
  name: edge-gateway
  namespace: kuma-demo
  labels:
    kuma.io/mesh: default
spec:
  replicas: 3
  serviceType: LoadBalancer
  tags:
    kuma.io/service: edge-gateway_kuma-demo_svc
  resources:
    limits:
      cpu: 1000m
      memory: 1Gi
    requests:
      cpu: 100m
      memory: 128Mi
`
	docs, warnings, err := TransformMeshGatewayInstance([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect 2 docs: GatewayClass + MeshGatewayConfig.
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs, got %d", len(docs))
	}

	var gc map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &gc); err != nil {
		t.Fatalf("unmarshal GatewayClass: %v", err)
	}
	if gc["kind"] != "GatewayClass" {
		t.Errorf("expected kind=GatewayClass, got %v", gc["kind"])
	}
	if gc["apiVersion"] != "gateway.networking.k8s.io/v1" {
		t.Errorf("expected gatewayAPIVersion, got %v", gc["apiVersion"])
	}

	gcMeta := gc["metadata"].(map[string]interface{})
	if gcMeta["name"] != "edge-gateway" {
		t.Errorf("expected GatewayClass name=edge-gateway, got %v", gcMeta["name"])
	}
	// GatewayClass is cluster-scoped — no namespace.
	if ns, ok := gcMeta["namespace"]; ok && ns != "" {
		t.Errorf("GatewayClass should not have namespace, got %v", ns)
	}

	gcSpec := gc["spec"].(map[string]interface{})
	if gcSpec["controllerName"] != "gateways.kuma.io/controller" {
		t.Errorf("unexpected controllerName: %v", gcSpec["controllerName"])
	}

	var mgc map[string]interface{}
	if err := yaml.Unmarshal(docs[1], &mgc); err != nil {
		t.Fatalf("unmarshal MeshGatewayConfig: %v", err)
	}
	if mgc["kind"] != "MeshGatewayConfig" {
		t.Errorf("expected kind=MeshGatewayConfig, got %v", mgc["kind"])
	}

	mgcMeta := mgc["metadata"].(map[string]interface{})
	if mgcMeta["name"] != "edge-gateway" {
		t.Errorf("expected MeshGatewayConfig name=edge-gateway, got %v", mgcMeta["name"])
	}
	if mgcMeta["namespace"] != "kuma-demo" {
		t.Errorf("expected namespace=kuma-demo, got %v", mgcMeta["namespace"])
	}

	// Spec should be preserved from MeshGatewayInstance.
	mgcSpec := mgc["spec"].(map[string]interface{})
	if mgcSpec["serviceType"] != "LoadBalancer" {
		t.Errorf("expected serviceType=LoadBalancer, got %v", mgcSpec["serviceType"])
	}

	// Warnings about migration.
	if len(warnings) < 2 {
		t.Errorf("expected at least 2 warnings, got %d", len(warnings))
	}
}

func TestDetectScenario_GatewayResources(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		scenario Scenario
	}{
		{"MeshGateway", "MeshGateway", ScenarioGateway},
		{"MeshGatewayInstance", "MeshGatewayInstance", ScenarioGatewayInstance},
		{"MeshHTTPRoute", "MeshHTTPRoute", ScenarioHTTPRoute},
		{"MeshTCPRoute", "MeshTCPRoute", ScenarioTCPRoute},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte("apiVersion: kuma.io/v1alpha1\nkind: " + tc.kind + "\nmetadata:\n  name: test\n  namespace: default\nspec: {}\n")
			scenario, err := DetectScenario(raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if scenario != tc.scenario {
				t.Errorf("expected %v, got %v", tc.scenario, scenario)
			}
		})
	}
}

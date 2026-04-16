package migrator

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestTransformExternalService_KubernetesStyle(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: ExternalService
mesh: default
metadata:
  name: httpbin
  namespace: kong-mesh-system
spec:
  tags:
    kuma.io/service: httpbin
    kuma.io/protocol: http
  networking:
    address: httpbin.org:80
`
	docs, warnings, err := TransformExternalService([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}

	var mes map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &mes); err != nil {
		t.Fatalf("unmarshal MeshExternalService: %v", err)
	}

	if mes["kind"] != "MeshExternalService" {
		t.Errorf("expected kind MeshExternalService, got %v", mes["kind"])
	}

	meta := mes["metadata"].(map[string]interface{})
	if meta["name"] != "httpbin" {
		t.Errorf("expected name=httpbin, got %v", meta["name"])
	}
	labels := meta["labels"].(map[string]interface{})
	if labels["kuma.io/mesh"] != "default" {
		t.Errorf("expected kuma.io/mesh=default, got %v", labels["kuma.io/mesh"])
	}

	spec := mes["spec"].(map[string]interface{})

	// spec.match
	match := spec["match"].(map[string]interface{})
	if match["type"] != "HostnameGenerator" {
		t.Errorf("expected match.type=HostnameGenerator, got %v", match["type"])
	}
	if match["protocol"] != "http" {
		t.Errorf("expected match.protocol=http, got %v", match["protocol"])
	}
	if match["port"] != float64(80) {
		t.Errorf("expected match.port=80, got %v", match["port"])
	}

	// spec.endpoints
	endpoints := spec["endpoints"].([]interface{})
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(endpoints))
	}
	ep := endpoints[0].(map[string]interface{})
	if ep["address"] != "httpbin.org" {
		t.Errorf("expected endpoint address=httpbin.org, got %v", ep["address"])
	}
	if ep["port"] != float64(80) {
		t.Errorf("expected endpoint port=80, got %v", ep["port"])
	}

	// Expect warnings about match.port and HostnameGenerator.
	if len(warnings) < 2 {
		t.Errorf("expected at least 2 warnings, got %d: %v", len(warnings), warnings)
	}

	// tags must NOT appear in output spec.
	if _, hasTags := spec["tags"]; hasTags {
		t.Error("spec.tags should not appear in MeshExternalService output")
	}
}

func TestTransformExternalService_UniversalStyle(t *testing.T) {
	input := `
type: ExternalService
mesh: default
name: postgres
tags:
  kuma.io/service: postgres
  kuma.io/protocol: tcp
networking:
  address: db.internal:5432
`
	docs, _, err := TransformExternalService([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}

	var mes map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &mes); err != nil {
		t.Fatalf("unmarshal MeshExternalService: %v", err)
	}
	if mes["kind"] != "MeshExternalService" {
		t.Errorf("expected kind MeshExternalService, got %v", mes["kind"])
	}

	meta := mes["metadata"].(map[string]interface{})
	if meta["name"] != "postgres" {
		t.Errorf("expected name=postgres, got %v", meta["name"])
	}
	// Universal style — no namespace.
	if ns, ok := meta["namespace"]; ok && ns != "" {
		t.Errorf("expected no namespace for Universal-style output, got %v", ns)
	}

	spec := mes["spec"].(map[string]interface{})
	match := spec["match"].(map[string]interface{})
	if match["protocol"] != "tcp" {
		t.Errorf("expected match.protocol=tcp, got %v", match["protocol"])
	}
	if match["port"] != float64(5432) {
		t.Errorf("expected match.port=5432, got %v", match["port"])
	}
}

func TestTransformExternalService_WithTLS(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: ExternalService
mesh: default
metadata:
  name: secure-api
  namespace: kong-mesh-system
spec:
  tags:
    kuma.io/service: secure-api
    kuma.io/protocol: https
  networking:
    address: api.example.com:443
    tls:
      enabled: true
      allowRenegotiation: false
      serverName: api.example.com
      caCert:
        secret: ca-cert
`
	docs, warnings, err := TransformExternalService([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}

	var mes map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &mes); err != nil {
		t.Fatalf("unmarshal MeshExternalService: %v", err)
	}

	spec := mes["spec"].(map[string]interface{})
	tls, ok := spec["tls"].(map[string]interface{})
	if !ok {
		t.Fatal("expected spec.tls to be present")
	}
	if tls["enabled"] != true {
		t.Errorf("expected tls.enabled=true, got %v", tls["enabled"])
	}
	verification, ok := tls["verification"].(map[string]interface{})
	if !ok {
		t.Fatal("expected spec.tls.verification to be present")
	}
	if verification["mode"] != "Secured" {
		t.Errorf("expected verification.mode=Secured, got %v", verification["mode"])
	}
	if verification["serverName"] != "api.example.com" {
		t.Errorf("expected serverName=api.example.com, got %v", verification["serverName"])
	}

	// TLS warning should be present.
	hasTLSWarn := false
	for _, w := range warnings {
		if strings.Contains(w, "TLS") {
			hasTLSWarn = true
		}
	}
	if !hasTLSWarn {
		t.Error("expected TLS migration warning")
	}
}

func TestTransformExternalService_DefaultProtocol(t *testing.T) {
	// No protocol tag → defaults to tcp.
	input := `
apiVersion: kuma.io/v1alpha1
kind: ExternalService
mesh: default
metadata:
  name: db
  namespace: kong-mesh-system
spec:
  tags:
    kuma.io/service: db
  networking:
    address: db.internal:3306
`
	docs, _, err := TransformExternalService([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var mes map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &mes); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	spec := mes["spec"].(map[string]interface{})
	match := spec["match"].(map[string]interface{})
	if match["protocol"] != "tcp" {
		t.Errorf("expected default protocol=tcp, got %v", match["protocol"])
	}
}

func TestDetectScenario_ExternalService(t *testing.T) {
	kubeES := []byte(`
apiVersion: kuma.io/v1alpha1
kind: ExternalService
mesh: default
metadata:
  name: svc
spec:
  tags:
    kuma.io/service: svc
  networking:
    address: svc.example.com:80
`)
	scenario, err := DetectScenario(kubeES)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scenario != ScenarioExternalService {
		t.Errorf("expected ScenarioExternalService, got %v", scenario)
	}

	universalES := []byte(`
type: ExternalService
mesh: default
name: svc
tags:
  kuma.io/service: svc
networking:
  address: svc.example.com:80
`)
	scenario, err = DetectScenario(universalES)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scenario != ScenarioExternalService {
		t.Errorf("expected ScenarioExternalService for Universal style, got %v", scenario)
	}
}

func TestDetectScenario_Mesh(t *testing.T) {
	withMetrics := []byte(`
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
  metrics:
    enabledBackend: prom
    backends:
      - name: prom
        type: prometheus
        conf:
          port: 5050
`)
	scenario, err := DetectScenario(withMetrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scenario != ScenarioMesh {
		t.Errorf("expected ScenarioMesh, got %v", scenario)
	}

	cleanMesh := []byte(`
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
  mtls:
    enabledBackend: ca-1
  meshServices:
    mode: Exclusive
`)
	scenario, err = DetectScenario(cleanMesh)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scenario != ScenarioPassthrough {
		t.Errorf("expected ScenarioPassthrough for clean Mesh, got %v", scenario)
	}
}

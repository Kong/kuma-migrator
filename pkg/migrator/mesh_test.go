package migrator

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestTransformMesh_MetricsOnly(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
  namespace: kong-mesh-system
spec:
  metrics:
    enabledBackend: prometheus-1
    backends:
      - name: prometheus-1
        type: prometheus
        conf:
          port: 5050
          path: /metrics
`
	docs, warnings, err := TransformMesh([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect: cleaned Mesh + MeshMetric.
	if len(docs) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(docs))
	}

	// First doc: cleaned Mesh — metrics section removed.
	var cleanedMesh map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &cleanedMesh); err != nil {
		t.Fatalf("unmarshal cleaned Mesh: %v", err)
	}
	if spec, ok := cleanedMesh["spec"].(map[string]interface{}); ok {
		if _, hasMetrics := spec["metrics"]; hasMetrics {
			t.Error("cleaned Mesh still has spec.metrics")
		}
	}

	// Second doc: MeshMetric companion.
	var mm map[string]interface{}
	if err := yaml.Unmarshal(docs[1], &mm); err != nil {
		t.Fatalf("unmarshal MeshMetric: %v", err)
	}
	if mm["kind"] != "MeshMetric" {
		t.Errorf("expected kind MeshMetric, got %v", mm["kind"])
	}
	meta := mm["metadata"].(map[string]interface{})
	if meta["name"] != "default-metrics" {
		t.Errorf("expected name default-metrics, got %v", meta["name"])
	}
	labels := meta["labels"].(map[string]interface{})
	if labels["kuma.io/mesh"] != "default" {
		t.Errorf("expected label kuma.io/mesh=default, got %v", labels["kuma.io/mesh"])
	}

	// Spec should have targetRef kind: Mesh and backends.
	spec := mm["spec"].(map[string]interface{})
	targetRef := spec["targetRef"].(map[string]interface{})
	if targetRef["kind"] != "Mesh" {
		t.Errorf("expected targetRef.kind=Mesh, got %v", targetRef["kind"])
	}
	def := spec["default"].(map[string]interface{})
	backends, ok := def["backends"].([]interface{})
	if !ok || len(backends) == 0 {
		t.Fatal("expected backends in MeshMetric spec.default")
	}
	b := backends[0].(map[string]interface{})
	if b["type"] != "Prometheus" {
		t.Errorf("expected backend type Prometheus, got %v", b["type"])
	}

	_ = warnings
}

func TestTransformMesh_SkipMTLSWarning(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
  namespace: kong-mesh-system
spec:
  metrics:
    enabledBackend: prom
    backends:
      - name: prom
        type: prometheus
        conf:
          port: 5050
          path: /metrics
          skipMTLS: true
          tags:
            kuma.io/service: dataplane-metrics
`
	_, warnings, err := TransformMesh([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	foundSkipMTLS := false
	foundTags := false
	for _, w := range warnings {
		if strings.Contains(w, "skipMTLS") {
			foundSkipMTLS = true
		}
		if strings.Contains(w, "tags") {
			foundTags = true
		}
	}
	if !foundSkipMTLS {
		t.Error("expected warning about skipMTLS")
	}
	if !foundTags {
		t.Error("expected warning about tags")
	}
}

func TestTransformMesh_TracingZipkin(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
  namespace: kong-mesh-system
spec:
  tracing:
    defaultBackend: jaeger
    backends:
      - name: jaeger
        type: zipkin
        sampling: 75.5
        conf:
          url: http://jaeger-collector:9411/api/v2/spans
          traceId128bit: true
          apiVersion: httpProto
`
	docs, _, err := TransformMesh([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs, got %d", len(docs))
	}

	var mt map[string]interface{}
	if err := yaml.Unmarshal(docs[1], &mt); err != nil {
		t.Fatalf("unmarshal MeshTrace: %v", err)
	}
	if mt["kind"] != "MeshTrace" {
		t.Errorf("expected kind MeshTrace, got %v", mt["kind"])
	}
	spec := mt["spec"].(map[string]interface{})
	def := spec["default"].(map[string]interface{})

	// Check sampling.overall.
	sampling, ok := def["sampling"].(map[string]interface{})
	if !ok {
		t.Fatal("expected sampling in MeshTrace default")
	}
	if overall := sampling["overall"]; overall != float64(75) {
		t.Errorf("expected sampling.overall=75, got %v", overall)
	}

	// Check Zipkin backend.
	backends := def["backends"].([]interface{})
	b := backends[0].(map[string]interface{})
	if b["type"] != "Zipkin" {
		t.Errorf("expected backend type Zipkin, got %v", b["type"])
	}
	zipkin := b["zipkin"].(map[string]interface{})
	if zipkin["url"] != "http://jaeger-collector:9411/api/v2/spans" {
		t.Errorf("unexpected zipkin url: %v", zipkin["url"])
	}
	if zipkin["traceId128bit"] != true {
		t.Errorf("expected traceId128bit=true, got %v", zipkin["traceId128bit"])
	}
}

func TestTransformMesh_LoggingFileTCP(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
  namespace: kong-mesh-system
spec:
  logging:
    defaultBackend: file-log
    backends:
      - name: file-log
        type: file
        conf:
          path: /var/log/kuma/access.log
      - name: tcp-log
        type: tcp
        conf:
          address: logserver.local:5000
`
	docs, _, err := TransformMesh([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs, got %d", len(docs))
	}

	var mal map[string]interface{}
	if err := yaml.Unmarshal(docs[1], &mal); err != nil {
		t.Fatalf("unmarshal MeshAccessLog: %v", err)
	}
	if mal["kind"] != "MeshAccessLog" {
		t.Errorf("expected kind MeshAccessLog, got %v", mal["kind"])
	}
	spec := mal["spec"].(map[string]interface{})
	def := spec["default"].(map[string]interface{})
	backends := def["backends"].([]interface{})
	if len(backends) != 2 {
		t.Fatalf("expected 2 backends, got %d", len(backends))
	}
	file := backends[0].(map[string]interface{})
	if file["type"] != "File" {
		t.Errorf("expected File backend, got %v", file["type"])
	}
	tcp := backends[1].(map[string]interface{})
	if tcp["type"] != "Tcp" {
		t.Errorf("expected Tcp backend, got %v", tcp["type"])
	}
}

func TestTransformMesh_Passthrough(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
  namespace: kong-mesh-system
spec:
  networking:
    outbound:
      passthrough: false
`
	docs, warnings, err := TransformMesh([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs, got %d", len(docs))
	}

	var mpt map[string]interface{}
	if err := yaml.Unmarshal(docs[1], &mpt); err != nil {
		t.Fatalf("unmarshal MeshPassthrough: %v", err)
	}
	if mpt["kind"] != "MeshPassthrough" {
		t.Errorf("expected kind MeshPassthrough, got %v", mpt["kind"])
	}
	spec := mpt["spec"].(map[string]interface{})
	def := spec["default"].(map[string]interface{})
	if def["passthroughMode"] != "None" {
		t.Errorf("expected passthroughMode=None, got %v", def["passthroughMode"])
	}

	// Cleaned Mesh should have no networking.outbound.passthrough.
	var cleanedMesh map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &cleanedMesh); err != nil {
		t.Fatalf("unmarshal cleaned Mesh: %v", err)
	}
	if meshSpec, ok := cleanedMesh["spec"].(map[string]interface{}); ok {
		if _, hasNet := meshSpec["networking"]; hasNet {
			t.Error("expected networking to be removed from cleaned Mesh")
		}
	}

	// Expect a warning about passthrough extraction.
	hasWarn := false
	for _, w := range warnings {
		if strings.Contains(w, "passthrough") {
			hasWarn = true
		}
	}
	if !hasWarn {
		t.Error("expected passthrough warning")
	}
}

func TestTransformMesh_PassthroughTrue_NoCompanion(t *testing.T) {
	// passthrough=true is the default — no MeshPassthrough companion needed.
	input := `
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
  namespace: kong-mesh-system
spec:
  networking:
    outbound:
      passthrough: true
`
	docs, _, err := TransformMesh([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only the cleaned Mesh; passthrough=true is no-op.
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc (no companion), got %d", len(docs))
	}
}

func TestTransformMesh_MultipleCompanions(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: prod
  namespace: kong-mesh-system
spec:
  metrics:
    enabledBackend: prom
    backends:
      - name: prom
        type: prometheus
        conf:
          port: 5050
          path: /metrics
  tracing:
    defaultBackend: zipkin
    backends:
      - name: zipkin
        type: zipkin
        conf:
          url: http://zipkin:9411/api/v2/spans
  logging:
    defaultBackend: file
    backends:
      - name: file
        type: file
        conf:
          path: /var/log/kuma.log
  networking:
    outbound:
      passthrough: false
`
	docs, _, err := TransformMesh([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Cleaned Mesh + MeshMetric + MeshTrace + MeshAccessLog + MeshPassthrough = 5
	if len(docs) != 5 {
		t.Fatalf("expected 5 docs, got %d", len(docs))
	}
}

func TestMeshNeedsMigration(t *testing.T) {
	withMetrics := []byte(`
spec:
  metrics:
    backends: []
`)
	if !meshNeedsMigration(withMetrics) {
		t.Error("expected meshNeedsMigration=true for metrics")
	}

	withPassthrough := []byte(`
spec:
  networking:
    outbound:
      passthrough: false
`)
	if !meshNeedsMigration(withPassthrough) {
		t.Error("expected meshNeedsMigration=true for passthrough")
	}

	// A Mesh with no observability sections but without Exclusive mode still needs migration.
	withoutExclusiveMode := []byte(`
spec:
  mtls:
    enabledBackend: ca
`)
	if !meshNeedsMigration(withoutExclusiveMode) {
		t.Error("expected meshNeedsMigration=true when meshServices.mode is not Exclusive")
	}

	// Only truly clean when meshServices.mode is already Exclusive.
	fullyClean := []byte(`
spec:
  mtls:
    enabledBackend: ca
  meshServices:
    mode: Exclusive
`)
	if meshNeedsMigration(fullyClean) {
		t.Error("expected meshNeedsMigration=false for Mesh with mode: Exclusive and no observability sections")
	}
}

func TestTransformMesh_InjectsMeshServicesMode(t *testing.T) {
	// A Mesh with no observability sections and no meshServices field should get
	// meshServices.mode: Exclusive injected.
	input := `apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
  namespace: kong-mesh-system
spec:
  mtls:
    enabledBackend: ca-1
    backends:
      - name: ca-1
        type: builtin
`
	docs, warnings, err := TransformMesh([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc (only cleaned Mesh), got %d", len(docs))
	}

	var obj map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	spec := obj["spec"].(map[string]interface{})
	meshSvc, ok := spec["meshServices"].(map[string]interface{})
	if !ok {
		t.Fatal("expected spec.meshServices to be present")
	}
	if meshSvc["mode"] != "Exclusive" {
		t.Errorf("expected mode=Exclusive, got %v", meshSvc["mode"])
	}

	// Should warn the user about the mode change.
	hasWarn := false
	for _, w := range warnings {
		if strings.Contains(w, "Exclusive") {
			hasWarn = true
		}
	}
	if !hasWarn {
		t.Errorf("expected warning about meshServices.mode Exclusive, got: %v", warnings)
	}
}

func TestTransformMesh_PreservesExistingExclusiveMode(t *testing.T) {
	// A Mesh that already has mode: Exclusive should not be modified and should
	// emit no warning about the mode.
	input := `apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
  namespace: kong-mesh-system
spec:
  meshServices:
    mode: Exclusive
  metrics:
    enabledBackend: prom
    backends:
      - name: prom
        type: prometheus
        conf:
          port: 5050
          path: /metrics
`
	docs, warnings, err := TransformMesh([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Cleaned Mesh + MeshMetric companion.
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs, got %d", len(docs))
	}

	var obj map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &obj); err != nil {
		t.Fatalf("unmarshal cleaned Mesh: %v", err)
	}
	spec := obj["spec"].(map[string]interface{})
	meshSvc := spec["meshServices"].(map[string]interface{})
	if meshSvc["mode"] != "Exclusive" {
		t.Errorf("expected mode=Exclusive preserved, got %v", meshSvc["mode"])
	}

	// No Exclusive-mode warning should be emitted (mode was already correct).
	for _, w := range warnings {
		if strings.Contains(w, "meshServices.mode set to Exclusive") {
			t.Errorf("unexpected Exclusive-mode injection warning (mode was already set): %s", w)
		}
	}
}

func TestTransformMesh_MeshServicesMode_ViaTransformDocument(t *testing.T) {
	// End-to-end: DetectScenario should route a Mesh without Exclusive mode
	// through ScenarioMesh, and TransformDocument should inject the field.
	input := `apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: prod
  namespace: kong-mesh-system
spec: {}
`
	docs, _, scenario, err := TransformDocument([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scenario != ScenarioMesh {
		t.Errorf("expected ScenarioMesh for Mesh without Exclusive mode, got %s", scenario)
	}

	var obj map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	spec, _ := obj["spec"].(map[string]interface{})
	if spec == nil {
		t.Fatal("expected non-nil spec")
	}
	meshSvc, _ := spec["meshServices"].(map[string]interface{})
	if meshSvc == nil || meshSvc["mode"] != "Exclusive" {
		t.Errorf("expected meshServices.mode=Exclusive after TransformDocument, got %v", meshSvc)
	}
}

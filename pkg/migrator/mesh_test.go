package migrator

import (
	"fmt"
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
  mtls:
    enabledBackend: ca-1
    backends:
      - name: ca-1
        type: builtin
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
	docs, warnings, err := TransformMesh([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	foundTags := false
	for _, w := range warnings {
		if strings.Contains(w, "client certificate") {
			t.Errorf("skipMTLS: true kept a plaintext endpoint; no TLS warning expected, got %q", w)
		}
		if strings.Contains(w, "tags") {
			foundTags = true
		}
	}
	if !foundTags {
		t.Error("expected warning about tags")
	}
	if prom := meshMetricPrometheus(t, docs); prom["tls"] != nil {
		t.Errorf("skipMTLS: true must not set a TLS mode, got %v", prom["tls"])
	}
}

// meshMetricPrometheus returns default.backends[0].prometheus of the MeshMetric in docs.
func meshMetricPrometheus(t *testing.T, docs [][]byte) map[string]interface{} {
	t.Helper()
	def := meshMetricDefault(t, docs)
	backends, _ := def["backends"].([]interface{})
	if len(backends) == 0 {
		t.Fatal("MeshMetric has no backends")
	}
	b, _ := backends[0].(map[string]interface{})
	prom, _ := b["prometheus"].(map[string]interface{})
	return prom
}

func meshMetricDefault(t *testing.T, docs [][]byte) map[string]interface{} {
	t.Helper()
	for _, d := range docs {
		var obj map[string]interface{}
		if err := yaml.Unmarshal(d, &obj); err != nil {
			t.Fatal(err)
		}
		if obj["kind"] != "MeshMetric" && obj["type"] != "MeshMetric" {
			continue
		}
		spec, _ := obj["spec"].(map[string]interface{})
		def, _ := spec["default"].(map[string]interface{})
		return def
	}
	t.Fatal("no MeshMetric generated")
	return nil
}

func TestTransformMesh_MetricsTLS(t *testing.T) {
	const tmpl = `
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
%s  metrics:
    enabledBackend: prom
    backends:
      - name: prom
        type: prometheus
        conf:
          port: 5670
%s`
	mtls := "  mtls:\n    enabledBackend: ca-1\n    backends:\n      - name: ca-1\n        type: builtin\n"
	cases := []struct {
		name     string
		mtls     string
		conf     string
		target   TargetVersion
		wantMode string
		wantWarn string
	}{
		{"mtls default mode v2: plaintext, warned", mtls, "", TargetV2, "", "No MeshMetric TLS mode keeps that check on 2.x"},
		{"mtls default mode v3: ActiveMTLSBackend", mtls, "", TargetV3, "ActiveMTLSBackend", "requires a client certificate the mesh trusts"},
		{"mtls explicit activeMTLSBackend v3", mtls, "          tls:\n            mode: activeMTLSBackend\n", TargetV3, "ActiveMTLSBackend", "SPIFFE"},
		{"no mtls: plaintext before and after", "", "", TargetV3, "", ""},
		{"skipMTLS", mtls, "          skipMTLS: true\n", TargetV3, "", ""},
		{"disabled", mtls, "          tls:\n            mode: disabled\n", TargetV2, "", ""},
		{"delegated → ProvidedTLS", mtls, "          tls:\n            mode: delegated\n", TargetV2, "ProvidedTLS", "ProvidedTLS"},
		{"unknown mode", mtls, "          tls:\n            mode: bogus\n", TargetV2, "", "is unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := fmt.Sprintf(tmpl, tc.mtls, tc.conf)
			docs, warnings, err := TransformMeshForTarget([]byte(in), tc.target)
			if err != nil {
				t.Fatal(err)
			}
			prom := meshMetricPrometheus(t, docs)
			gotMode := ""
			if tlsm, ok := prom["tls"].(map[string]interface{}); ok {
				gotMode, _ = tlsm["mode"].(string)
			}
			if gotMode != tc.wantMode {
				t.Errorf("tls.mode = %q, want %q", gotMode, tc.wantMode)
			}
			joined := strings.Join(warnings, "\n")
			if tc.wantWarn != "" && !strings.Contains(joined, tc.wantWarn) {
				t.Errorf("warnings lack %q:\n%s", tc.wantWarn, joined)
			}
			if tc.wantWarn == "" && strings.Contains(joined, "client certificate") {
				t.Errorf("unexpected TLS warning:\n%s", joined)
			}
		})
	}
}

func TestTransformMesh_MetricsSidecarAndAggregate(t *testing.T) {
	in := `
type: Mesh
name: default
metrics:
  enabledBackend: prom
  backends:
    - name: prom
      type: prometheus
      conf:
        envoy:
          usedOnly: true
          filterRegex: "cluster\\..*"
        aggregate:
          - name: app
            port: 8080
            path: /stats
          - name: off
            port: 9090
            enabled: false
`
	docs, warnings, err := TransformMesh([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	def := meshMetricDefault(t, docs)
	sidecar, _ := def["sidecar"].(map[string]interface{})
	if sidecar["includeUnused"] != false {
		t.Errorf("usedOnly: true should give includeUnused: false, got %v", sidecar["includeUnused"])
	}
	profiles, _ := sidecar["profiles"].(map[string]interface{})
	ap, _ := profiles["appendProfiles"].([]interface{})
	if len(ap) != 1 || ap[0].(map[string]interface{})["name"] != "All" {
		t.Errorf("appendProfiles = %v, want [All]", ap)
	}
	apps, _ := def["applications"].([]interface{})
	if len(apps) != 1 || apps[0].(map[string]interface{})["name"] != "app" {
		t.Errorf("applications = %v, want only the enabled one", apps)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "filterRegex") {
		t.Error("expected a filterRegex warning")
	}
}

func TestTransformMesh_MetricsDefaultsKeepAllMetrics(t *testing.T) {
	in := `
type: Mesh
name: default
metrics:
  enabledBackend: prom
  backends:
    - name: prom
      type: prometheus
`
	docs, _, err := TransformMesh([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	sidecar, _ := meshMetricDefault(t, docs)["sidecar"].(map[string]interface{})
	if sidecar["includeUnused"] != true {
		t.Errorf("legacy default published unused metrics; includeUnused = %v, want true", sidecar["includeUnused"])
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
	// On 2.x Mesh tracing backends trace nothing until a TrafficTrace selects
	// traffic, so no mesh-wide MeshTrace is generated: it would enable tracing
	// for every proxy. The backend is carried by the MeshTrace converted from
	// each TrafficTrace (TestLegacyConf_TrafficTrace_ResolvesBackend).
	docs, warnings, err := TransformMesh([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected only the cleaned Mesh, got %d docs", len(docs))
	}
	var mesh map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &mesh); err != nil {
		t.Fatalf("unmarshal Mesh: %v", err)
	}
	if dig(mesh, "spec", "tracing") != nil {
		t.Error("spec.tracing must be removed from the Mesh")
	}
	if warningContaining(warnings, "spec.tracing", "TrafficTrace", "No mesh-wide MeshTrace") == "" {
		t.Errorf("expected the tracing-backends warning, got: %v", warnings)
	}

	// The shared backend converter still maps every zipkin field.
	_, b := parseMeshBackends([]byte(input))
	conv, _ := tracingBackendToNew(b.Tracing["jaeger"])
	zipkin := conv["zipkin"].(map[string]interface{})
	if conv["type"] != "Zipkin" || zipkin["url"] != "http://jaeger-collector:9411/api/v2/spans" ||
		zipkin["traceId128bit"] != true || zipkin["apiVersion"] != "httpProto" {
		t.Errorf("unexpected zipkin backend: %v", conv)
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
	// Mesh logging backends log nothing until a TrafficLog names one: no
	// mesh-wide MeshAccessLog.
	docs, warnings, err := TransformMesh([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected only the cleaned Mesh, got %d docs", len(docs))
	}
	if warningContaining(warnings, "spec.logging", "TrafficLog", "No mesh-wide MeshAccessLog") == "" {
		t.Errorf("expected the logging-backends warning, got: %v", warnings)
	}

	_, b := parseMeshBackends([]byte(input))
	file, _ := loggingBackendToNew(b.Logging["file-log"])
	tcp, _ := loggingBackendToNew(b.Logging["tcp-log"])
	if file["type"] != "File" || tcp["type"] != "Tcp" {
		t.Errorf("unexpected backends: %v / %v", file, tcp)
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
	// Cleaned Mesh + MeshMetric + MeshPassthrough = 3. Tracing and logging get no
	// mesh-wide companion (they were activated per TrafficTrace/TrafficLog).
	if len(docs) != 3 {
		t.Fatalf("expected 3 docs, got %d", len(docs))
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

func TestMeshNeedsMigration_UniversalFormat(t *testing.T) {
	// Universal Mesh with meshServices.mode: Exclusive at top level — no migration needed.
	alreadyDone := []byte(`meshServices:
  mode: Exclusive
mtls:
  backends:
  - name: builtin
    type: builtin
  enabledBackend: builtin
name: default
type: Mesh
`)
	if meshNeedsMigration(alreadyDone) {
		t.Error("expected meshNeedsMigration=false for Universal Mesh with top-level meshServices.mode: Exclusive")
	}

	// Universal Mesh without meshServices — needs migration.
	missingMode := []byte(`mtls:
  enabledBackend: builtin
name: default
type: Mesh
`)
	if !meshNeedsMigration(missingMode) {
		t.Error("expected meshNeedsMigration=true for Universal Mesh with no meshServices.mode")
	}

	// Universal Mesh with top-level metrics — needs migration.
	withMetrics := []byte(`metrics:
  backends: []
meshServices:
  mode: Exclusive
name: default
type: Mesh
`)
	if !meshNeedsMigration(withMetrics) {
		t.Error("expected meshNeedsMigration=true for Universal Mesh with top-level metrics")
	}
}

func TestDetectScenario_RulesAPI_NoTopLevelTargetRef(t *testing.T) {
	// MeshAccessLog with from[] but NO spec.targetRef — still ScenarioRules.
	input := `type: MeshAccessLog
name: my-access-log
mesh: default
spec:
  from:
  - default:
      backends:
      - type: File
        file:
          path: /dev/stdout
    targetRef:
      kind: Mesh
  to:
  - default:
      backends:
      - type: File
        file:
          path: /dev/stdout
    targetRef:
      kind: Mesh
`
	got, err := DetectScenario([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != ScenarioRules {
		t.Errorf("expected ScenarioRules for MeshAccessLog without spec.targetRef, got %v", got)
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
	docs, _, scenario, err := TransformDocument([]byte(input), TargetV2)
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

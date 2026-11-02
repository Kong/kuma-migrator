package migrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// ---- helpers ------------------------------------------------------------------

func transformWithOpts(t *testing.T, input string, opts TransformOptions) ([]map[string]interface{}, []string) {
	t.Helper()
	docs, warnings, _, err := TransformDocumentWithOptions([]byte(input), opts)
	if err != nil {
		t.Fatalf("TransformDocumentWithOptions: %v", err)
	}
	var out []map[string]interface{}
	for _, d := range docs {
		var m map[string]interface{}
		if err := yaml.Unmarshal(d, &m); err != nil {
			t.Fatalf("unmarshal output: %v", err)
		}
		out = append(out, m)
	}
	return out, warnings
}

func writeInput(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

const meshWithObservability = `apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
  logging:
    defaultBackend: file
    backends:
    - name: file
      type: file
      conf:
        path: /tmp/access.log
    - name: tcp
      type: tcp
      conf:
        address: logstash:5000
  tracing:
    defaultBackend: zipkin
    backends:
    - name: zipkin
      type: zipkin
      conf:
        url: http://zipkin:9411/api/v2/spans
  metrics:
    enabledBackend: prom
    backends:
    - name: prom
      type: prometheus
      conf:
        port: 5670
`

// ---- logging / tracing: no mesh-wide companion --------------------------------

func TestMeshObservability_NoMeshWideLoggingOrTracing(t *testing.T) {
	for _, target := range []TargetVersion{TargetV2, TargetV3} {
		docs, _ := transformWithOpts(t, meshWithObservability, TransformOptions{Target: target})
		if n := len(docsOfKind(docs, "MeshAccessLog")); n != 0 {
			t.Errorf("%s: no mesh-wide MeshAccessLog expected, got %d", target, n)
		}
		if n := len(docsOfKind(docs, "MeshTrace")); n != 0 {
			t.Errorf("%s: no mesh-wide MeshTrace expected, got %d", target, n)
		}
		// metrics.enabledBackend does enable metrics mesh-wide: keep MeshMetric.
		if n := len(docsOfKind(docs, "MeshMetric")); n != 1 {
			t.Errorf("%s: expected 1 MeshMetric, got %d", target, n)
		}
	}
}

// With the pre-pass index the warning separates referenced backends (carried
// into the converted MeshAccessLog) from unreferenced ones (inactive).
func TestMeshObservability_WarningUsesTrafficLogIndex(t *testing.T) {
	idx := LegacyObservabilityIndex{}
	idx.add("TrafficLog", "default", "")       // falls back to defaultBackend "file"
	idx.add("TrafficLog", "default", "file")   // explicit
	idx.add("TrafficLog", "other-mesh", "tcp") // different mesh, must not count
	_, warnings := transformWithOpts(t, meshWithObservability,
		TransformOptions{Target: TargetV2, LegacyObservability: idx})

	w := warningContaining(warnings, "spec.logging")
	if w == "" {
		t.Fatalf("expected a spec.logging warning, got: %v", warnings)
	}
	if !strings.Contains(w, "file (2 TrafficLog)") {
		t.Errorf("expected file referenced by 2 TrafficLogs: %s", w)
	}
	if !strings.Contains(w, "No TrafficLog in the input references tcp") || !strings.Contains(w, "nothing is lost") {
		t.Errorf("expected tcp reported inactive: %s", w)
	}

	tw := warningContaining(warnings, "spec.tracing")
	if !strings.Contains(tw, "No TrafficTrace in the input references zipkin") {
		t.Errorf("expected zipkin reported inactive (no TrafficTrace): %s", tw)
	}
}

func TestMeshObservability_WarningWithoutIndex(t *testing.T) {
	_, warnings := transformWithOpts(t, meshWithObservability, TransformOptions{Target: TargetV2})
	w := warningContaining(warnings, "spec.logging", "No mesh-wide MeshAccessLog")
	if w == "" || !strings.Contains(w, "was inactive") {
		t.Errorf("expected the rule-based logging warning, got: %v", warnings)
	}
}

// ---- metrics: only the enabled backend ----------------------------------------

func TestMeshMetrics_OnlyEnabledBackend(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
  metrics:
    enabledBackend: prom-2
    backends:
    - name: prom-1
      type: prometheus
      conf:
        port: 1111
    - name: prom-2
      type: prometheus
      conf:
        port: 2222
`
	docs, warnings := transformWithOpts(t, input, TransformOptions{Target: TargetV2})
	mm := docsOfKind(docs, "MeshMetric")
	if len(mm) != 1 {
		t.Fatalf("expected 1 MeshMetric, got %d", len(mm))
	}
	backends, _ := dig(mm[0], "spec", "default", "backends").([]interface{})
	if len(backends) != 1 || dig(backends[0].(map[string]interface{}), "prometheus", "port") != float64(2222) {
		t.Errorf("expected only the enabled prom-2 backend, got %v", backends)
	}
	if warningContaining(warnings, "only the enabled metrics backend") == "" {
		t.Errorf("expected a dropped-backends warning, got: %v", warnings)
	}
}

func TestMeshMetrics_NoEnabledBackend(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
  metrics:
    backends:
    - name: prom-1
      type: prometheus
`
	docs, warnings := transformWithOpts(t, input, TransformOptions{Target: TargetV2})
	if n := len(docsOfKind(docs, "MeshMetric")); n != 0 {
		t.Errorf("no MeshMetric expected when metrics were not enabled, got %d", n)
	}
	if warningContaining(warnings, "no enabledBackend", "nothing is lost") == "" {
		t.Errorf("expected the not-enabled warning, got: %v", warnings)
	}
}

// ---- system namespace ---------------------------------------------------------

func TestCompanionNamespace_DefaultsToKumaSystem(t *testing.T) {
	docs, _ := transformWithOpts(t, meshWithObservability, TransformOptions{Target: TargetV2})
	mm := docsOfKind(docs, "MeshMetric")[0]
	if ns := dig(mm, "metadata", "namespace"); ns != "kuma-system" {
		t.Errorf("namespace = %v, want kuma-system", ns)
	}
}

func TestCompanionNamespace_FromOptions(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
  metrics:
    enabledBackend: prom
    backends:
    - name: prom
      type: prometheus
  networking:
    outbound:
      passthrough: true
  mtls:
    enabledBackend: ca
    backends:
    - name: ca
      type: builtin
      mode: PERMISSIVE
`
	opts := TransformOptions{Target: TargetV3, SystemNamespace: "kong-mesh-system"}
	docs, _ := transformWithOpts(t, input, opts)
	for _, kind := range []string{"MeshMetric", "MeshPassthrough", "MeshTLS"} {
		got := docsOfKind(docs, kind)
		if len(got) != 1 {
			t.Fatalf("expected 1 %s, got %d", kind, len(got))
		}
		if ns := dig(got[0], "metadata", "namespace"); ns != "kong-mesh-system" {
			t.Errorf("%s namespace = %v, want kong-mesh-system", kind, ns)
		}
	}

	es := `apiVersion: kuma.io/v1alpha1
kind: ExternalService
mesh: default
metadata:
  name: httpbin
spec:
  tags:
    kuma.io/service: httpbin
  networking:
    address: httpbin.org:443
`
	mes, _ := transformWithOpts(t, es, opts)
	if ns := dig(docsOfKind(mes, "MeshExternalService")[0], "metadata", "namespace"); ns != "kong-mesh-system" {
		t.Errorf("MeshExternalService namespace = %v, want kong-mesh-system", ns)
	}
	mesDefault, _ := transformWithOpts(t, es, TransformOptions{Target: TargetV2})
	if ns := dig(docsOfKind(mesDefault, "MeshExternalService")[0], "metadata", "namespace"); ns != "kuma-system" {
		t.Errorf("MeshExternalService default namespace = %v, want kuma-system", ns)
	}

	mt := `apiVersion: kuma.io/v1alpha1
kind: MeshTrace
metadata:
  name: tracing
  namespace: kong-mesh-system
  labels:
    kuma.io/mesh: default
spec:
  targetRef:
    kind: Mesh
  default:
    backends:
    - type: OpenTelemetry
      openTelemetry:
        endpoint: otel-collector:4317
`
	otel, _ := transformWithOpts(t, mt, opts)
	motb := docsOfKind(otel, "MeshOpenTelemetryBackend")
	if len(motb) != 1 || dig(motb[0], "metadata", "namespace") != "kong-mesh-system" {
		t.Errorf("MeshOpenTelemetryBackend must use the system namespace, got %v", motb)
	}
}

func TestBuildTransformOptions_InfersSystemNamespace(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		want  string
	}{
		"kong-mesh-system namespace": {
			files: map[string]string{"mt.yaml": "kind: MeshTimeout\nmetadata:\n  name: t\n  namespace: kong-mesh-system\n"},
			want:  "kong-mesh-system",
		},
		"kuma-system namespace": {
			files: map[string]string{"mt.yaml": "kind: MeshTimeout\nmetadata:\n  name: t\n  namespace: kuma-system\n"},
			want:  "kuma-system",
		},
		"kong mesh kind": {
			files: map[string]string{"opa.yaml": "type: OPAPolicy\nname: opa\nmesh: default\n"},
			want:  "kong-mesh-system",
		},
		"no evidence": {
			files: map[string]string{"mesh.yaml": "kind: Mesh\nmetadata:\n  name: default\n"},
			want:  "",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for f, c := range tc.files {
				writeInput(t, dir, f, c)
			}
			if got := BuildTransformOptions(dir, TargetV2).SystemNamespace; got != tc.want {
				t.Errorf("SystemNamespace = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildTransformOptions_IndexesTrafficLogBackends(t *testing.T) {
	dir := t.TempDir()
	writeInput(t, dir, "tl.yaml", `apiVersion: kuma.io/v1alpha1
kind: TrafficLog
mesh: payments
metadata:
  name: tl-1
spec:
  sources:
  - match:
      kuma.io/service: '*'
  destinations:
  - match:
      kuma.io/service: '*'
  conf:
    backend: tcp
---
type: TrafficTrace
name: tt-1
mesh: payments
selectors:
- match:
    kuma.io/service: '*'
`)
	idx := BuildTransformOptions(dir, TargetV2).LegacyObservability
	if idx == nil {
		t.Fatal("index must be set after the pre-pass")
	}
	if got := idx.referencedBackends("payments", "TrafficLog", "file"); got["tcp"] != 1 || got["file"] != 0 {
		t.Errorf("TrafficLog refs = %v, want tcp:1", got)
	}
	if got := idx.referencedBackends("payments", "TrafficTrace", "zipkin"); got["zipkin"] != 1 {
		t.Errorf("TrafficTrace without conf.backend must count for the default backend, got %v", got)
	}
}

// ---- Universal companions -----------------------------------------------------

func TestCompanionFormat_UniversalMesh(t *testing.T) {
	input := `type: Mesh
name: default
metrics:
  enabledBackend: prom
  backends:
  - name: prom
    type: prometheus
    conf:
      port: 5670
networking:
  outbound:
    passthrough: true
mtls:
  enabledBackend: ca
  backends:
  - name: ca
    type: builtin
    mode: PERMISSIVE
`
	docs, _ := transformWithOpts(t, input, TransformOptions{Target: TargetV3, SystemNamespace: "kong-mesh-system"})
	for _, kind := range []string{"MeshMetric", "MeshPassthrough", "MeshTLS"} {
		got := docsOfKind(docs, kind)
		if len(got) != 1 {
			t.Fatalf("expected 1 %s, got %d", kind, len(got))
		}
		d := got[0]
		if d["type"] != kind || d["mesh"] != "default" || d["name"] == nil || d["spec"] == nil {
			t.Errorf("%s: expected Universal type/name/mesh/spec, got %v", kind, d)
		}
		for _, k := range []string{"kind", "apiVersion", "metadata"} {
			if _, ok := d[k]; ok {
				t.Errorf("%s: Universal companion must not carry %q: %v", kind, k, d)
			}
		}
	}
}

// ---- end to end ---------------------------------------------------------------

// A Kong Mesh input (OPAPolicy present) with a Mesh and a TrafficLog: the
// MeshAccessLog comes only from the TrafficLog, companions land in
// kong-mesh-system.
func TestMigrate_ObservabilityAndNamespaceEndToEnd(t *testing.T) {
	in := t.TempDir()
	out := t.TempDir()
	writeInput(t, in, "mesh.yaml", meshWithObservability)
	writeInput(t, in, "tl.yaml", `apiVersion: kuma.io/v1alpha1
kind: TrafficLog
mesh: default
metadata:
  name: all-traffic
spec:
  sources:
  - match:
      kuma.io/service: '*'
  destinations:
  - match:
      kuma.io/service: '*'
`)
	writeInput(t, in, "opa.yaml", `apiVersion: kuma.io/v1alpha1
kind: OPAPolicy
mesh: default
metadata:
  name: opa
spec:
  selectors:
  - match:
      kuma.io/service: '*'
  conf:
    policies:
    - inline: cGFja2FnZSB4Cg==
`)
	opts := BuildTransformOptions(in, TargetV2)
	if opts.SystemNamespace != "kong-mesh-system" {
		t.Fatalf("SystemNamespace = %q, want kong-mesh-system (OPAPolicy in input)", opts.SystemNamespace)
	}

	meshDocs, warnings := transformWithOpts(t, meshWithObservability, opts)
	if len(docsOfKind(meshDocs, "MeshAccessLog")) != 0 {
		t.Error("the Mesh must not produce a MeshAccessLog")
	}
	if ns := dig(docsOfKind(meshDocs, "MeshMetric")[0], "metadata", "namespace"); ns != "kong-mesh-system" {
		t.Errorf("MeshMetric namespace = %v, want kong-mesh-system", ns)
	}
	if w := warningContaining(warnings, "spec.logging", "file (1 TrafficLog)", "references tcp"); w == "" {
		t.Errorf("expected file referenced / tcp inactive, got: %v", warnings)
	}

	if err := Migrate(in, out, "", TargetV2); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	var mals []string
	_ = filepath.WalkDir(out, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.Contains(filepath.Base(p), "MeshAccessLog") {
			mals = append(mals, p)
		}
		return nil
	})
	if len(mals) != 1 {
		t.Fatalf("expected exactly 1 MeshAccessLog (from the TrafficLog), got %v", mals)
	}
	data, _ := os.ReadFile(mals[0])
	if !strings.Contains(string(data), "/tmp/access.log") {
		t.Errorf("the TrafficLog's MeshAccessLog must carry the default backend inline:\n%s", data)
	}
}

package migrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// ---- helpers ----------------------------------------------------------------

func writeTempFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func tempDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "kuma-migrator-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// ---- Plan -------------------------------------------------------------------

func TestPlan_WritesReportAndNoOutputYAML(t *testing.T) {
	in := tempDir(t)
	out := tempDir(t)

	writeTempFile(t, in, "timeout.yaml", `type: Timeout
mesh: default
name: my-timeout
sources:
  - match:
      kuma.io/service: '*'
destinations:
  - match:
      kuma.io/service: backend_demo_svc_3001
conf:
  connectTimeout: 5s
`)

	if err := Plan(in, out, ""); err != nil {
		t.Fatalf("Plan returned error: %v", err)
	}

	// Report file must exist.
	reportPath := filepath.Join(out, "migration-plan.md")
	if _, err := os.Stat(reportPath); os.IsNotExist(err) {
		t.Fatal("expected migration-plan.md to be written")
	}

	// No migrated YAML file should exist (dry run).
	entries, _ := os.ReadDir(out)
	for _, e := range entries {
		if e.Name() == "migration-plan.md" {
			continue
		}
		if strings.HasSuffix(e.Name(), ".yaml") || strings.HasSuffix(e.Name(), ".yml") {
			t.Errorf("Plan wrote an output YAML file unexpectedly: %s", e.Name())
		}
	}
}

func TestPlan_ReportContainsPlanTitle(t *testing.T) {
	in := tempDir(t)
	out := tempDir(t)

	writeTempFile(t, in, "mesh.yaml", `apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
  namespace: kong-mesh-system
spec: {}
`)

	if err := Plan(in, out, ""); err != nil {
		t.Fatalf("Plan: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(out, "migration-plan.md"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	body := string(content)
	if !strings.Contains(body, "Migration Plan") {
		t.Error("expected 'Migration Plan' in plan report title")
	}
	if !strings.Contains(body, "Dry run") {
		t.Error("expected 'Dry run' notice in plan report")
	}
	if !strings.Contains(body, "meshServices") {
		t.Error("expected meshServices advisory in report")
	}
	if !strings.Contains(body, "Exclusive") {
		t.Error("expected Exclusive mode mentioned in report")
	}
}

// ---- Migrate ----------------------------------------------------------------

func TestMigrate_WritesOutputFilesAndReport(t *testing.T) {
	in := tempDir(t)
	out := tempDir(t)

	writeTempFile(t, in, "timeout.yaml", `type: Timeout
mesh: default
name: my-timeout
sources:
  - match:
      kuma.io/service: '*'
destinations:
  - match:
      kuma.io/service: backend_demo_svc_3001
conf:
  connectTimeout: 5s
`)

	if err := Migrate(in, out, ""); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}

	// Migrated YAML must exist in the resiliency subfolder.
	outYAML := filepath.Join(out, "resiliency", "MeshTimeout-my-timeout.yaml")
	if _, err := os.Stat(outYAML); os.IsNotExist(err) {
		t.Fatalf("expected %s to be written", outYAML)
	}

	// Report must exist.
	if _, err := os.Stat(filepath.Join(out, "migration-report.md")); os.IsNotExist(err) {
		t.Fatal("expected migration-report.md to be written")
	}
}

func TestMigrate_OutputIsValidMeshTimeout(t *testing.T) {
	in := tempDir(t)
	out := tempDir(t)

	writeTempFile(t, in, "timeout.yaml", `type: Timeout
mesh: default
name: my-timeout
sources:
  - match:
      kuma.io/service: '*'
destinations:
  - match:
      kuma.io/service: backend_demo_svc_3001
conf:
  connectTimeout: 5s
`)

	if err := Migrate(in, out, ""); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "resiliency", "MeshTimeout-my-timeout.yaml"))
	if err != nil {
		t.Fatalf("read output: %v", err)
	}

	var obj map[string]interface{}
	if err := yaml.Unmarshal(data, &obj); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if obj["kind"] != "MeshTimeout" {
		t.Errorf("expected kind MeshTimeout, got %v", obj["kind"])
	}
	spec := obj["spec"].(map[string]interface{})
	tr := spec["targetRef"].(map[string]interface{})
	if tr["kind"] != "Mesh" {
		t.Errorf("expected targetRef.kind=Mesh, got %v", tr["kind"])
	}
}

// ---- runMigration summary counts --------------------------------------------

func TestRunMigration_SummaryCounts(t *testing.T) {
	in := tempDir(t)
	out := tempDir(t)

	// One ScenarioLegacy file.
	writeTempFile(t, in, "legacy.yaml", `type: Timeout
name: t1
sources:
  - match:
      kuma.io/service: '*'
destinations:
  - match:
      kuma.io/service: svc_ns_svc_80
conf:
  connectTimeout: 5s
`)
	// One already-migrated file (ScenarioPassthrough).
	writeTempFile(t, in, "modern.yaml", `apiVersion: kuma.io/v1alpha1
kind: MeshRetry
metadata:
  name: my-retry
spec:
  targetRef:
    kind: Mesh
  to:
    - targetRef:
        kind: MeshService
        name: backend
      default:
        http:
          numRetries: 3
`)
	// One non-policy file (skip).
	writeTempFile(t, in, "deployment.yaml", `apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  replicas: 1
`)

	report, err := runMigration(in, out, false, "")
	if err != nil {
		t.Fatalf("runMigration: %v", err)
	}

	if report.TotalFiles != 3 {
		t.Errorf("expected TotalFiles=3, got %d", report.TotalFiles)
	}
	if report.MigratedCount != 1 {
		t.Errorf("expected MigratedCount=1, got %d", report.MigratedCount)
	}
	if report.AlreadyDoneCount != 1 {
		t.Errorf("expected AlreadyDoneCount=1, got %d", report.AlreadyDoneCount)
	}
	if report.SkippedCount != 1 {
		t.Errorf("expected SkippedCount=1, got %d", report.SkippedCount)
	}
	if report.ErrorCount != 0 {
		t.Errorf("expected ErrorCount=0, got %d", report.ErrorCount)
	}
}

func TestRunMigration_NonYAMLFilesIgnored(t *testing.T) {
	in := tempDir(t)
	out := tempDir(t)

	writeTempFile(t, in, "notes.txt", "some notes")
	writeTempFile(t, in, "config.json", `{"key":"value"}`)
	writeTempFile(t, in, "policy.yaml", `type: Timeout
name: t1
sources:
  - match:
      kuma.io/service: '*'
destinations:
  - match:
      kuma.io/service: backend_ns_svc_80
conf:
  connectTimeout: 5s
`)

	report, err := runMigration(in, out, false, "")
	if err != nil {
		t.Fatalf("runMigration: %v", err)
	}
	// Only .yaml/.yml files are counted.
	if report.TotalFiles != 1 {
		t.Errorf("expected TotalFiles=1 (only .yaml), got %d", report.TotalFiles)
	}
}

func TestRunMigration_MultiDocYAML(t *testing.T) {
	in := tempDir(t)
	out := tempDir(t)

	// A multi-document YAML: one legacy policy + one already-migrated policy.
	writeTempFile(t, in, "multi.yaml", `type: Timeout
name: t1
sources:
  - match:
      kuma.io/service: '*'
destinations:
  - match:
      kuma.io/service: svc_ns_svc_80
conf:
  connectTimeout: 5s
---
apiVersion: kuma.io/v1alpha1
kind: MeshRetry
metadata:
  name: my-retry
spec:
  targetRef:
    kind: Mesh
  to:
    - targetRef:
        kind: MeshService
        name: backend
      default:
        http:
          numRetries: 3
`)

	report, err := runMigration(in, out, false, "")
	if err != nil {
		t.Fatalf("runMigration: %v", err)
	}
	if report.TotalFiles != 1 {
		t.Errorf("expected 1 file, got %d", report.TotalFiles)
	}
	// File has a legacy doc → labelled MIGRATED A (first migrated scenario wins).
	if report.MigratedCount != 1 {
		t.Errorf("expected MigratedCount=1, got %d", report.MigratedCount)
	}
	if len(report.Files) != 1 {
		t.Fatalf("expected 1 FileReport, got %d", len(report.Files))
	}
	if report.Files[0].Label != labelMigratedLegacy {
		t.Errorf("expected label %s, got %s", labelMigratedLegacy, report.Files[0].Label)
	}
}

func TestRunMigration_AddressMappingDedup(t *testing.T) {
	in := tempDir(t)
	out := tempDir(t)

	// Two files referencing the same legacy service address.
	writeTempFile(t, in, "deploy1.yaml", `apiVersion: apps/v1
kind: Deployment
metadata:
  name: svc-a
  namespace: demo
spec:
  template:
    spec:
      containers:
        - name: app
          env:
            - name: BACKEND_ADDR
              value: backend_demo_svc_3001.mesh
`)
	writeTempFile(t, in, "deploy2.yaml", `apiVersion: apps/v1
kind: Deployment
metadata:
  name: svc-b
  namespace: demo
spec:
  template:
    spec:
      containers:
        - name: app
          env:
            - name: SAME_ADDR
              value: backend_demo_svc_3001.mesh
`)

	report, err := runMigration(in, out, false, "")
	if err != nil {
		t.Fatalf("runMigration: %v", err)
	}
	// Address mapping for backend_demo_svc_3001 should appear only once.
	seen := map[string]int{}
	for _, h := range report.AddressMappings {
		seen[h.OldToken]++
	}
	for token, count := range seen {
		if count > 1 {
			t.Errorf("address mapping %q appears %d times, expected 1 (dedup failed)", token, count)
		}
	}
}

// ---- Mesh migration injects meshServices.mode in Migrate --------------------

func TestMigrate_MeshGetsExclusiveMode(t *testing.T) {
	in := tempDir(t)
	out := tempDir(t)

	writeTempFile(t, in, "mesh.yaml", `apiVersion: kuma.io/v1alpha1
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
`)

	if err := Migrate(in, out, ""); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "mesh", "Mesh-kong-mesh-system-default.yaml"))
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var obj map[string]interface{}
	if err := yaml.Unmarshal(data, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	spec := obj["spec"].(map[string]interface{})
	meshSvc, _ := spec["meshServices"].(map[string]interface{})
	if meshSvc == nil || meshSvc["mode"] != "Exclusive" {
		t.Errorf("expected spec.meshServices.mode=Exclusive in migrated Mesh, got %v", meshSvc)
	}
}

// ---- Report content ---------------------------------------------------------

func TestMigrate_ReportContainsMeshServiceAdvisory(t *testing.T) {
	in := tempDir(t)
	out := tempDir(t)

	writeTempFile(t, in, "timeout.yaml", `type: Timeout
name: t1
sources:
  - match:
      kuma.io/service: '*'
destinations:
  - match:
      kuma.io/service: backend_demo_svc_3001
conf:
  connectTimeout: 5s
`)

	if err := Migrate(in, out, ""); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(out, "migration-report.md"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	body := string(content)
	if !strings.Contains(body, "meshServices") {
		t.Error("expected meshServices advisory in migration report")
	}
	if !strings.Contains(body, "Exclusive") {
		t.Error("expected Exclusive mode mentioned in migration report")
	}
	if !strings.Contains(body, "Migration Report") {
		t.Error("expected 'Migration Report' in apply-mode report title")
	}
}

// ---- Mesh subdir detection and filter tests ----------------------------------

func TestRunMigration_ContextAndMeshDirDetected(t *testing.T) {
	in := tempDir(t)
	out := tempDir(t)

	// Create file at <in>/my-cp-global-ctx/mesh-default/resiliency/timeout.yaml
	// (context-first layout produced by current extract).
	subDir := filepath.Join(in, "my-cp-global-ctx", "mesh-default", "resiliency")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeTempFile(t, subDir, "timeout.yaml", `type: Timeout
name: my-timeout
mesh: default
sources:
  - match:
      kuma.io/service: '*'
destinations:
  - match:
      kuma.io/service: backend_demo_svc_3001
conf:
  connectTimeout: 5s
`)

	report, err := runMigration(in, out, false, "")
	if err != nil {
		t.Fatalf("runMigration: %v", err)
	}
	if report.TotalFiles != 1 {
		t.Errorf("expected TotalFiles=1, got %d", report.TotalFiles)
	}
	if len(report.Files) != 1 {
		t.Fatalf("expected 1 FileReport, got %d", len(report.Files))
	}
	fr := report.Files[0]
	if fr.MeshDir != "default" {
		t.Errorf("expected MeshDir=default, got %q", fr.MeshDir)
	}
	if fr.CPModeDir != "my-cp-global-ctx" {
		t.Errorf("expected CPModeDir=my-cp-global-ctx, got %q", fr.CPModeDir)
	}
}

func TestRunMigration_MeshFilter_SkipsOtherMesh(t *testing.T) {
	in := tempDir(t)
	out := tempDir(t)

	// Context-first layout: two mesh subdirs under the same context dir.
	for _, mesh := range []string{"default", "prod"} {
		subDir := filepath.Join(in, "my-cp-global-ctx", "mesh-"+mesh, "resiliency")
		if err := os.MkdirAll(subDir, 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		writeTempFile(t, subDir, "timeout.yaml", `apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: t
  labels:
    kuma.io/mesh: `+mesh+`
spec: {}
`)
	}

	report, err := runMigration(in, out, false, "default")
	if err != nil {
		t.Fatalf("runMigration: %v", err)
	}
	// Only the default mesh file should be counted.
	if report.TotalFiles != 1 {
		t.Errorf("expected TotalFiles=1 (only default mesh), got %d", report.TotalFiles)
	}
	for _, fr := range report.Files {
		if fr.MeshDir != "default" {
			t.Errorf("expected only default mesh files, got MeshDir=%q", fr.MeshDir)
		}
	}
}

func TestRunMigration_MeshFilter_KeepsLegacyFiles(t *testing.T) {
	in := tempDir(t)
	out := tempDir(t)

	// Legacy layout (no mesh subdir): <in>/global/resiliency/file.yaml
	subDir := filepath.Join(in, "global", "resiliency")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeTempFile(t, subDir, "timeout.yaml", `apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: legacy-timeout
spec: {}
`)

	// meshFilter set but no mesh subdir → file must still be processed.
	report, err := runMigration(in, out, false, "default")
	if err != nil {
		t.Fatalf("runMigration: %v", err)
	}
	if report.TotalFiles != 1 {
		t.Errorf("expected legacy file to be processed even with mesh filter, got TotalFiles=%d", report.TotalFiles)
	}
}

func TestMigrate_MeshSubdir_OutputMirrorsInputLayout(t *testing.T) {
	in := tempDir(t)
	out := tempDir(t)

	// Input: <in>/my-cp-global-ctx/mesh-default/resiliency/timeout.yaml
	// (context-first layout produced by current extract)
	subDir := filepath.Join(in, "my-cp-global-ctx", "mesh-default", "resiliency")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeTempFile(t, subDir, "timeout.yaml", `type: Timeout
name: my-timeout
mesh: default
sources:
  - match:
      kuma.io/service: '*'
destinations:
  - match:
      kuma.io/service: backend_demo_svc_3001
conf:
  connectTimeout: 5s
`)

	if err := Migrate(in, out, ""); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// Output must mirror context-first layout: <out>/my-cp-global-ctx/mesh-default/resiliency/MeshTimeout-my-timeout.yaml
	outPath := filepath.Join(out, "my-cp-global-ctx", "mesh-default", "resiliency", "MeshTimeout-my-timeout.yaml")
	if _, err := os.Stat(outPath); os.IsNotExist(err) {
		t.Errorf("expected output at %s", outPath)
	}
}

package extractor

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig writes a kumactl config YAML to a temp file and returns its path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp("", "kumactl-config-*.yaml")
	if err != nil {
		t.Fatalf("create temp config: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write config: %v", err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })
	return f.Name()
}

func TestResolveKumactlContext_Basic(t *testing.T) {
	cfg := writeConfig(t, `
controlPlanes:
  - name: prod
    coordinates:
      apiServer:
        url: https://cp.prod.example.com:5681
contexts:
  - name: prod-ctx
    controlPlane: prod
currentContext: prod-ctx
`)
	t.Setenv("KUMACTL_CONFIG", cfg)

	cpURL, resolvedCtx, _, err := resolveKumactlContext("prod-ctx")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cpURL != "https://cp.prod.example.com:5681" {
		t.Errorf("expected CP URL https://cp.prod.example.com:5681, got %s", cpURL)
	}
	if resolvedCtx != "prod-ctx" {
		t.Errorf("expected resolved context prod-ctx, got %s", resolvedCtx)
	}
}

func TestResolveKumactlContext_UsesCurrentContext(t *testing.T) {
	// When contextName is empty, should fall back to currentContext.
	cfg := writeConfig(t, `
controlPlanes:
  - name: local
    coordinates:
      apiServer:
        url: http://localhost:5681
contexts:
  - name: local
    controlPlane: local
currentContext: local
`)
	t.Setenv("KUMACTL_CONFIG", cfg)

	cpURL, resolvedCtx, _, err := resolveKumactlContext("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cpURL != "http://localhost:5681" {
		t.Errorf("expected CP URL http://localhost:5681, got %s", cpURL)
	}
	if resolvedCtx != "local" {
		t.Errorf("expected resolved context local, got %s", resolvedCtx)
	}
}

func TestResolveKumactlContext_MultipleContexts(t *testing.T) {
	cfg := writeConfig(t, `
controlPlanes:
  - name: zone1-cp
    coordinates:
      apiServer:
        url: https://zone1.example.com:5681
  - name: zone2-cp
    coordinates:
      apiServer:
        url: https://zone2.example.com:5681
contexts:
  - name: zone1
    controlPlane: zone1-cp
  - name: zone2
    controlPlane: zone2-cp
currentContext: zone1
`)
	t.Setenv("KUMACTL_CONFIG", cfg)

	// Request zone2 explicitly.
	cpURL, _, _, err := resolveKumactlContext("zone2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cpURL != "https://zone2.example.com:5681" {
		t.Errorf("expected zone2 CP URL, got %s", cpURL)
	}
}

func TestResolveKumactlContext_UnknownContext(t *testing.T) {
	cfg := writeConfig(t, `
controlPlanes:
  - name: local
    coordinates:
      apiServer:
        url: http://localhost:5681
contexts:
  - name: local
    controlPlane: local
currentContext: local
`)
	t.Setenv("KUMACTL_CONFIG", cfg)

	_, _, _, err := resolveKumactlContext("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown context")
	}
}

func TestResolveKumactlContext_MissingConfigFile(t *testing.T) {
	t.Setenv("KUMACTL_CONFIG", filepath.Join(t.TempDir(), "no-such-file.yaml"))

	_, _, _, err := resolveKumactlContext("any")
	if err == nil {
		t.Fatal("expected error when config file does not exist")
	}
}

func TestResolveKumactlContext_NoCurrentContext(t *testing.T) {
	cfg := writeConfig(t, `
controlPlanes:
  - name: local
    coordinates:
      apiServer:
        url: http://localhost:5681
contexts:
  - name: local
    controlPlane: local
`)
	t.Setenv("KUMACTL_CONFIG", cfg)

	_, _, _, err := resolveKumactlContext("")
	if err == nil {
		t.Fatal("expected error when no context specified and no currentContext set")
	}
}

func TestResolveKumactlContext_OrphanedControlPlane(t *testing.T) {
	// Context references a CP name that doesn't exist in controlPlanes.
	cfg := writeConfig(t, `
controlPlanes:
  - name: other-cp
    coordinates:
      apiServer:
        url: http://other:5681
contexts:
  - name: ctx1
    controlPlane: missing-cp
currentContext: ctx1
`)
	t.Setenv("KUMACTL_CONFIG", cfg)

	_, _, _, err := resolveKumactlContext("ctx1")
	if err == nil {
		t.Fatal("expected error when control plane name not found")
	}
}

// TestKumactlConfigPath_EnvOverride verifies KUMACTL_CONFIG env var is honoured.
func TestKumactlConfigPath_EnvOverride(t *testing.T) {
	t.Setenv("KUMACTL_CONFIG", "/custom/path/config")
	if got := kumactlConfigPath(); got != "/custom/path/config" {
		t.Errorf("expected /custom/path/config, got %s", got)
	}
}

func TestKumactlConfigPath_Default(t *testing.T) {
	t.Setenv("KUMACTL_CONFIG", "")
	got := kumactlConfigPath()
	if got == "" {
		t.Error("expected a non-empty default config path")
	}
	if filepath.Base(got) != "config" {
		t.Errorf("expected filename 'config', got %s", filepath.Base(got))
	}
}

package extractor

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- /config access failures --------------------------------------------------

// TestDetectKumactlCPMode_ForbiddenFallsBack checks that a 401/403 from /config
// (Kong Mesh 3.0 gates it behind VIEW_CONTROL_PLANE_METADATA) keeps the
// unknown-mode fallback rather than aborting extraction.
func TestDetectKumactlCPMode_ForbiddenFallsBack(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))
		mode, zone, env := detectKumactlCPMode(srv.URL, "tok")
		srv.Close()
		if mode != "" || zone != "" || env != "" {
			t.Errorf("status %d: expected empty mode/zone/env, got %q/%q/%q", status, mode, zone, env)
		}
	}
}

func TestCPConfigUnavailableWarning(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		w := cpConfigUnavailableWarning("https://cp:5682/config", status, nil)
		for _, want := range []string{"VIEW_CONTROL_PLANE_METADATA", "no origin filter", "Kubernetes skip list", "access-roles"} {
			if !strings.Contains(w, want) {
				t.Errorf("status %d: warning should contain %q, got: %s", status, want, w)
			}
		}
	}

	w := cpConfigUnavailableWarning("https://cp:5682/config", http.StatusInternalServerError, nil)
	if strings.Contains(w, "VIEW_CONTROL_PLANE_METADATA") {
		t.Errorf("a 500 is not an access problem; warning should not name the access type: %s", w)
	}
	if !strings.Contains(w, "500") || !strings.Contains(w, "no origin filter") {
		t.Errorf("500 warning should name the status and the consequences: %s", w)
	}
}

// ---- ContextMeta environment ------------------------------------------------

func TestContextMeta_EnvironmentRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := WriteContextMeta(dir, "cp-zone-ctx", "kumactl", "cp", CPModeZone, CPEnvUniversal, false); err != nil {
		t.Fatalf("WriteContextMeta: %v", err)
	}
	meta := ReadContextMeta(dir, "cp-zone-ctx")
	if meta == nil {
		t.Fatal("expected metadata")
	}
	if meta.Environment != CPEnvUniversal {
		t.Errorf("expected environment %q, got %q", CPEnvUniversal, meta.Environment)
	}
}

// TestContextMeta_OldFileHasNoEnvironment covers metadata written before the
// environment field existed: it must still parse, with Environment empty.
func TestContextMeta_OldFileHasNoEnvironment(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "old-ctx"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "old-ctx", ".kuma-migrator.json"),
		[]byte(`{"tool":"kubectl","context":"old","cpMode":"global"}`), 0644); err != nil {
		t.Fatal(err)
	}
	meta := ReadContextMeta(dir, "old-ctx")
	if meta == nil || meta.Tool != "kubectl" {
		t.Fatalf("expected parsed metadata, got %+v", meta)
	}
	if meta.Environment != "" {
		t.Errorf("expected empty environment, got %q", meta.Environment)
	}
}

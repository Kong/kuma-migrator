package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_MissingFile_ReturnsDefaults(t *testing.T) {
	t.Setenv("KUMA_MIGRATOR_CONFIG", filepath.Join(t.TempDir(), "no-such-file.yaml"))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// SkipSetForEnv must still return the built-in Kubernetes defaults.
	set := cfg.SkipSet()
	for _, kind := range DefaultSkipKindsKubernetes {
		if !set[kind] {
			t.Errorf("expected default kind %q in skip set", kind)
		}
	}
}

func TestLoad_EmptySkipList_ReturnsDefaults(t *testing.T) {
	f := writeTempConfig(t, `# no skip key`)
	t.Setenv("KUMA_MIGRATOR_CONFIG", f)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	set := cfg.SkipSet()
	if len(set) == 0 {
		t.Error("expected defaults when skip is absent")
	}
	for _, kind := range DefaultSkipKindsKubernetes {
		if !set[kind] {
			t.Errorf("expected default kind %q in skip set when skip is absent", kind)
		}
	}
}

// TestLoad_PerEnvSkipList verifies the new per-environment structured format.
func TestLoad_PerEnvSkipList(t *testing.T) {
	f := writeTempConfig(t, `
skip:
  kubernetes:
    - Dataplane
    - ZoneIngress
    - CustomKubeKind
  universal:
    - AccessRole
    - CustomUniversalKind
`)
	t.Setenv("KUMA_MIGRATOR_CONFIG", f)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	kubeSet := cfg.SkipSetForEnv("kubernetes")
	if !kubeSet["Dataplane"] || !kubeSet["ZoneIngress"] || !kubeSet["CustomKubeKind"] {
		t.Errorf("kubernetes skip set missing expected kinds: %v", kubeSet)
	}
	if kubeSet["AccessRole"] {
		t.Error("AccessRole should not be in the kubernetes skip set (not listed there)")
	}

	univSet := cfg.SkipSetForEnv("universal")
	if !univSet["AccessRole"] || !univSet["CustomUniversalKind"] {
		t.Errorf("universal skip set missing expected kinds: %v", univSet)
	}
	if univSet["Dataplane"] {
		t.Error("Dataplane should not be in the universal skip set (not listed there)")
	}
}

// TestLoad_LegacyFlatSkipList verifies backward compatibility with the old
// flat-list format: the same list is applied to both environments.
func TestLoad_LegacyFlatSkipList(t *testing.T) {
	f := writeTempConfig(t, `
skip:
  - Dataplane
  - CustomKind
`)
	t.Setenv("KUMA_MIGRATOR_CONFIG", f)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Flat list applies to both environments.
	for _, env := range []string{"kubernetes", "universal", ""} {
		set := cfg.SkipSetForEnv(env)
		if !set["Dataplane"] {
			t.Errorf("env=%q: expected Dataplane in skip set", env)
		}
		if !set["CustomKind"] {
			t.Errorf("env=%q: expected CustomKind in skip set", env)
		}
		// Default-only kinds must not be present (user specified an explicit list).
		if set["Zone"] {
			t.Errorf("env=%q: Zone should not be in explicit skip set", env)
		}
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	f := writeTempConfig(t, `skip: [unclosed`)
	t.Setenv("KUMA_MIGRATOR_CONFIG", f)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestSkipSet_LookupIsCaseSensitive(t *testing.T) {
	cfg := &Config{Skip: SkipKindsConfig{Kubernetes: []string{"Dataplane"}}}
	set := cfg.SkipSet()
	if !set["Dataplane"] {
		t.Error("expected Dataplane in skip set")
	}
	if set["dataplane"] {
		t.Error("skip set lookup should be case-sensitive")
	}
}

func TestSkipSetForEnv_Universal(t *testing.T) {
	cfg := &Config{} // no explicit skip list → use defaults
	set := cfg.SkipSetForEnv("universal")
	// Universal list must NOT contain workload-registration kinds.
	for _, kind := range []string{"Dataplane", "ZoneIngress", "ZoneEgress", "Workload"} {
		if set[kind] {
			t.Errorf("universal skip set must not contain %q", kind)
		}
	}
	// But shared infrastructure kinds should still be skipped.
	for _, kind := range []string{"AccessRole", "Zone", "HostnameGenerator"} {
		if !set[kind] {
			t.Errorf("universal skip set should contain %q", kind)
		}
	}
}

func TestSkipSetForEnv_Kubernetes(t *testing.T) {
	cfg := &Config{}
	set := cfg.SkipSetForEnv("kubernetes")
	for _, kind := range []string{"Dataplane", "ZoneIngress", "ZoneEgress", "Workload"} {
		if !set[kind] {
			t.Errorf("kubernetes skip set should contain %q", kind)
		}
	}
}

func TestSkipSetForEnv_UnknownFallsBackToKubernetes(t *testing.T) {
	cfg := &Config{}
	setUnknown := cfg.SkipSetForEnv("")
	setK8s := cfg.SkipSetForEnv("kubernetes")
	for kind := range setK8s {
		if !setUnknown[kind] {
			t.Errorf("unknown env should fall back to kubernetes defaults; missing %q", kind)
		}
	}
}

func TestSkipSetForEnv_ExplicitListTakesPrecedence(t *testing.T) {
	// When the user sets an explicit per-env skip list, it overrides the defaults.
	cfg := &Config{Skip: SkipKindsConfig{
		Kubernetes: []string{"CustomKind"},
		Universal:  []string{"CustomKind"},
	}}
	set := cfg.SkipSetForEnv("universal")
	if set["Dataplane"] {
		t.Error("explicit skip list should override universal defaults; Dataplane should not be skipped")
	}
	if !set["CustomKind"] {
		t.Error("explicit skip list entry CustomKind should be present")
	}
}

func TestLoad_AdminServerTLSSkipVerify(t *testing.T) {
	f := writeTempConfig(t, `
adminServer:
  tlsSkipVerify: true
`)
	t.Setenv("KUMA_MIGRATOR_CONFIG", f)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.AdminServer.TLSSkipVerify {
		t.Error("expected adminServer.tlsSkipVerify to be true")
	}
}

func TestLoad_AdminServerTLSSkipVerify_DefaultFalse(t *testing.T) {
	f := writeTempConfig(t, `# no adminServer section`)
	t.Setenv("KUMA_MIGRATOR_CONFIG", f)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AdminServer.TLSSkipVerify {
		t.Error("expected adminServer.tlsSkipVerify to default to false")
	}
}

func TestDefaultSkipKinds_ContainsExpected(t *testing.T) {
	expected := []string{
		"Dataplane", "AccessRole", "AccessRoleBinding",
		"ZoneEgress", "ZoneIngress", "Zone", "HostnameGenerator",
	}
	set := make(map[string]bool)
	for _, k := range DefaultSkipKinds {
		set[k] = true
	}
	for _, kind := range expected {
		if !set[kind] {
			t.Errorf("expected %q in DefaultSkipKinds", kind)
		}
	}
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp("", "kuma-migrator-config-*.yaml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })
	return f.Name()
}

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
	if len(cfg.Skip) == 0 {
		t.Error("expected default skip list, got empty")
	}
	set := cfg.SkipSet()
	for _, kind := range DefaultSkipKinds {
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
	if len(cfg.Skip) == 0 {
		t.Error("expected defaults when skip is absent")
	}
}

func TestLoad_CustomSkipList(t *testing.T) {
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
	set := cfg.SkipSet()
	if !set["Dataplane"] {
		t.Error("expected Dataplane in skip set")
	}
	if !set["CustomKind"] {
		t.Error("expected CustomKind in skip set")
	}
	// Default kinds not in custom list should be absent.
	if set["Zone"] {
		t.Error("Zone should not be in custom skip set")
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
	cfg := &Config{Skip: []string{"Dataplane"}}
	set := cfg.SkipSet()
	if !set["Dataplane"] {
		t.Error("expected Dataplane in skip set")
	}
	if set["dataplane"] {
		t.Error("skip set lookup should be case-sensitive")
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

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kong/kuma-migrator/pkg/migrator"
)

// TestToLatestFlagRegistered checks that migrate exposes --to-latest with the
// documented default and usage text.
func TestToLatestFlagRegistered(t *testing.T) {
	f := migrateCmd.Flags().Lookup("to-latest")
	if f == nil {
		t.Fatal("migrate: --to-latest is not registered")
	}
	if f.DefValue != "v2" {
		t.Errorf("migrate: --to-latest default = %q, want \"v2\"", f.DefValue)
	}
	if f.Usage != toLatestFlagUsage {
		t.Error("migrate: --to-latest usage text differs from the shared constant")
	}
}

// TestDryRunFlagRegistered checks that migrate exposes --dry-run, defaulting
// to false so a bare 'migrate' still writes output — --dry-run must be an
// explicit opt-in to the plan-only behaviour.
func TestDryRunFlagRegistered(t *testing.T) {
	f := migrateCmd.Flags().Lookup("dry-run")
	if f == nil {
		t.Fatal("migrate: --dry-run is not registered")
	}
	if f.DefValue != "false" {
		t.Errorf("migrate: --dry-run default = %q, want \"false\"", f.DefValue)
	}
}

// TestToLatestFlagRejectsInvalidValue verifies the value is validated before any
// work happens, so a typo fails fast instead of silently running as v2.
func TestToLatestFlagRejectsInvalidValue(t *testing.T) {
	if _, err := migrator.ParseTargetVersion("v4"); err == nil {
		t.Fatal("expected an error for --to-latest v4")
	} else if !strings.Contains(err.Error(), "v2") || !strings.Contains(err.Error(), "v3") {
		t.Errorf("error should name the accepted values, got: %v", err)
	}
}

// TestMigrateCmd_DryRunWritesPlanNotReport exercises migrateCmd's own RunE
// wiring directly (not just the underlying migrator.Plan/Migrate functions,
// already covered in pkg/migrator) — this is the one place a flag could be
// read backwards or wired to the wrong function without either package's own
// tests noticing.
func TestMigrateCmd_DryRunWritesPlanNotReport(t *testing.T) {
	in := t.TempDir()
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(in, "timeout.yaml"), []byte(`type: Timeout
name: t1
sources:
  - match: {kuma.io/service: '*'}
destinations:
  - match: {kuma.io/service: backend_demo_svc_3001}
conf:
  connectTimeout: 5s
`), 0644); err != nil {
		t.Fatal(err)
	}

	// Save and restore the package-level flag vars migrateCmd's RunE reads,
	// so this test doesn't leak state into any test that runs after it.
	oldIn, oldOut, oldMesh, oldTarget, oldDryRun :=
		migrateInputDir, migrateOutputDir, migrateMesh, migrateToLatest, migrateDryRun
	t.Cleanup(func() {
		migrateInputDir, migrateOutputDir, migrateMesh, migrateToLatest, migrateDryRun =
			oldIn, oldOut, oldMesh, oldTarget, oldDryRun
	})

	migrateInputDir, migrateOutputDir, migrateMesh, migrateToLatest, migrateDryRun =
		in, out, "", "v2", true

	if err := migrateCmd.RunE(migrateCmd, nil); err != nil {
		t.Fatalf("migrateCmd.RunE with --dry-run: %v", err)
	}

	if _, err := os.Stat(filepath.Join(out, "migration-plan.md")); err != nil {
		t.Errorf("expected migration-plan.md to be written under --dry-run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "migration-report.md")); !os.IsNotExist(err) {
		t.Error("migration-report.md should not be written under --dry-run")
	}
	entries, _ := os.ReadDir(out)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".yaml") {
			t.Errorf("--dry-run must not write output YAML, found %s", e.Name())
		}
	}
}

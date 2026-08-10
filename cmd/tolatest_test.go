package cmd

import (
	"strings"
	"testing"

	"github.com/Kong/kuma-migrator/pkg/migrator"
	"github.com/spf13/cobra"
)

// TestToLatestFlagRegistered checks that both commands expose --to-latest with
// the same default. A drift here would silently give plan and migrate different
// targets, so a plan would stop predicting what migrate does.
func TestToLatestFlagRegistered(t *testing.T) {
	for _, c := range []*cobra.Command{planCmd, migrateCmd} {
		f := c.Flags().Lookup("to-latest")
		if f == nil {
			t.Fatalf("%s: --to-latest is not registered", c.Name())
		}
		if f.DefValue != "v2" {
			t.Errorf("%s: --to-latest default = %q, want \"v2\"", c.Name(), f.DefValue)
		}
		if f.Usage != toLatestFlagUsage {
			t.Errorf("%s: --to-latest usage text differs from the shared constant", c.Name())
		}
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

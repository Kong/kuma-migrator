package migrator

import (
	"strings"
	"testing"
)

// TestWriteMarkdown_TargetHeader checks that the report records which major
// version the output was built for, and that a v3 report warns the output must
// not be applied to a 2.x control plane.
func TestWriteMarkdown_TargetHeader(t *testing.T) {
	cases := []struct {
		target       TargetVersion
		wantContains []string
		wantAbsent   []string
	}{
		{
			target:       TargetV2,
			wantContains: []string{"Target:", "latest 2.x"},
			wantAbsent:   []string{"not safe to apply to a 2.x control plane"},
		},
		{
			target:       TargetV3,
			wantContains: []string{"Target:", "3.0", "not safe to apply to a 2.x control plane"},
		},
	}
	for _, c := range cases {
		var b strings.Builder
		writeMarkdown(&b, &MigrationReport{
			Mode:      "apply",
			InputDir:  "/in",
			OutputDir: "/out",
			Target:    c.target,
		})
		got := b.String()
		for _, want := range c.wantContains {
			if !strings.Contains(got, want) {
				t.Errorf("target %v: report should contain %q", c.target, want)
			}
		}
		for _, absent := range c.wantAbsent {
			if strings.Contains(got, absent) {
				t.Errorf("target %v: report should not contain %q", c.target, absent)
			}
		}
	}
}

// TestMigrationReport_TargetDefaultsToV2 guards the zero value: a report built
// without an explicit target must describe the 2.x line, not 3.0.
func TestMigrationReport_TargetDefaultsToV2(t *testing.T) {
	r := &MigrationReport{}
	if r.Target.IsV3() {
		t.Error("zero-value MigrationReport.Target must not be v3")
	}
	if !strings.Contains(r.Target.Describe(), "2.x") {
		t.Errorf("zero-value target should describe the 2.x line, got %q", r.Target.Describe())
	}
}

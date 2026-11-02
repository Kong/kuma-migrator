package ui

import (
	"strings"
	"testing"

	"github.com/fatih/color"
)

func TestSummaryParts(t *testing.T) {
	color.NoColor = true

	got := strings.Join(summaryParts(SummaryCounts{Total: 3, Migrated: 1, Skipped: 2}), " | ")
	want := "3 file(s) processed | 1 migrated | 0 already migrated | 2 skipped | 0 errors"
	if got != want {
		t.Errorf("without scanned/manual:\n got %q\nwant %q", got, want)
	}

	got = strings.Join(summaryParts(SummaryCounts{Total: 5, Scanned: 2, Manual: 1, Errors: 1}), " | ")
	want = "5 file(s) processed | 0 migrated | 0 already migrated | 2 scanned | 1 manual | 0 skipped | 1 error(s)"
	if got != want {
		t.Errorf("with scanned/manual:\n got %q\nwant %q", got, want)
	}
}

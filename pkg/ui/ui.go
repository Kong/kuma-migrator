// Package ui provides colour-aware print helpers for kuma-migrator's CLI output.
// All functions respect the NO_COLOR environment variable and automatically
// disable colours when stdout is not a TTY (e.g. when piped or redirected).
package ui

import (
	"fmt"
	"strings"

	"github.com/fatih/color"
)

// ── Colour palette ────────────────────────────────────────────────────────────

var (
	bold      = color.New(color.Bold)
	faint     = color.New(color.Faint)
	green     = color.New(color.FgGreen)
	boldGreen = color.New(color.FgGreen, color.Bold)
	yellow    = color.New(color.FgYellow, color.Bold)
	red       = color.New(color.FgRed, color.Bold)
	cyan      = color.New(color.FgCyan)
	boldCyan  = color.New(color.FgCyan, color.Bold)
	blue      = color.New(color.FgBlue)
	white     = color.New(color.FgHiWhite, color.Bold)
)

// ── Icons ─────────────────────────────────────────────────────────────────────

const (
	iconOK     = "✓"
	iconWarn   = "⚠"
	iconError  = "✗"
	iconInfo   = "ℹ"
	iconArrow  = "→"
	iconBullet = "·"
)

// ── Section header ────────────────────────────────────────────────────────────

// Header prints the command banner:
//
//	kuma-migrator  extract
func Header(command string) {
	fmt.Println()
	fmt.Printf("  %s  %s\n", white.Sprint("kuma-migrator"), boldCyan.Sprint(command))
	fmt.Println()
}

// ── Key-value pairs (used in extract preamble) ────────────────────────────────

// KV prints a right-aligned label and a value on one line:
//
//	  Context        global-cp
func KV(label, value string) {
	fmt.Printf("  %-16s %s\n", faint.Sprint(label), value)
}

// ── Status notices ────────────────────────────────────────────────────────────

// Info prints a cyan informational line:
//
//	  ℹ  message
func Info(msg string) {
	fmt.Printf("  %s  %s\n", cyan.Sprint(iconInfo), msg)
}

// InfoIndented prints a continuation line under an Info call:
//
//	     message
func InfoIndented(msg string) {
	fmt.Printf("     %s\n", faint.Sprint(msg))
}

// Warn prints a yellow warning line:
//
//	  ⚠  message
func Warn(msg string) {
	fmt.Printf("  %s  %s\n", yellow.Sprint(iconWarn), yellow.Sprint(msg))
}

// WarnIndented prints a continuation line under a Warn call:
//
//	     message
func WarnIndented(msg string) {
	fmt.Printf("     %s\n", yellow.Sprint(msg))
}

// Errorf prints a red error line:
//
//	  ✗  message
func Errorf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("  %s  %s\n", red.Sprint(iconError), red.Sprint(msg))
}

// ── Extract progress ──────────────────────────────────────────────────────────

// Found prints the resource-discovery count line.
func Found(n int, noun string) {
	fmt.Printf("  %s  Found %s %s\n", faint.Sprint(iconBullet), bold.Sprintf("%d", n), faint.Sprint(noun))
}

// FileWritten prints the per-resource arrow line:
//
//	  →  routing/MeshGateway-my-gw.yaml
func FileWritten(sub, filename string) {
	fmt.Printf("  %s  %s/%s\n", cyan.Sprint(iconArrow), faint.Sprint(sub), cyan.Sprint(filename))
}

// ExtractDone prints the final summary for the extract command.
func ExtractDone(n int, dir string) {
	fmt.Println()
	fmt.Printf("  %s  Extracted %s resource(s) to %s\n",
		boldGreen.Sprint(iconOK),
		bold.Sprintf("%d", n),
		cyan.Sprint(dir),
	)
	fmt.Println()
}

// ── Migrate / Plan file labels ────────────────────────────────────────────────

// labelWidth is the fixed width of the scenario label column.
const labelWidth = 18

func padLabel(s string) string {
	if len(s) >= labelWidth {
		return s
	}
	return s + strings.Repeat(" ", labelWidth-len(s))
}

// FileMigrated prints a green success line for a migrated file.
//
//	  ✓  LEGACY             timeout-policy.yaml
func FileMigrated(scenario, filename string) {
	fmt.Printf("  %s  %s%s\n",
		boldGreen.Sprint(iconOK),
		boldGreen.Sprint(padLabel(scenario)),
		filename,
	)
}

// FileAlreadyMigrated prints a blue line for a passthrough file.
//
//	  ●  ALREADY MIGRATED   mesh-retry.yaml
func FileAlreadyMigrated(filename string) {
	fmt.Printf("  %s  %s%s\n",
		blue.Sprint("●"),
		blue.Sprint(padLabel("ALREADY MIGRATED")),
		faint.Sprint(filename),
	)
}

// FileSkipped prints a faint line for a skipped file.
//
//	  –  SKIP               deployment.yaml
func FileSkipped(filename, reason string) {
	label := "SKIP"
	line := filename
	if reason != "" {
		line = filename + faint.Sprintf(": %s", reason)
	}
	fmt.Printf("  %s  %s%s\n",
		faint.Sprint("–"),
		faint.Sprint(padLabel(label)),
		faint.Sprint(line),
	)
}

// FileError prints a red line for a file that could not be migrated.
//
//	  ✗  ERROR              broken.yaml
func FileError(filename string) {
	fmt.Printf("  %s  %s%s\n",
		red.Sprint(iconError),
		red.Sprint(padLabel("ERROR")),
		red.Sprint(filename),
	)
}

// FilePartialError prints a yellow line for a partially-migrated file.
func FilePartialError(filename string) {
	fmt.Printf("  %s  %s%s\n",
		yellow.Sprint(iconWarn),
		yellow.Sprint(padLabel("PARTIAL ERROR")),
		yellow.Sprint(filename),
	)
}

// DocError prints a per-document error indented under a file line.
func DocError(msg string) {
	fmt.Printf("       %s  %s\n", red.Sprint(iconError), red.Sprint(msg))
}

// DocWarn prints a per-document warning indented under a file line.
func DocWarn(msg string) {
	fmt.Printf("       %s  %s\n", yellow.Sprint(iconWarn), yellow.Sprint(msg))
}

// DocWorkload prints a [WORKLOAD] hit header.
func DocWorkload(msg string) {
	fmt.Printf("       %s  %s\n", cyan.Sprint("⚙"), cyan.Sprint(msg))
}

// DocWorkloadHit prints a single workload env-var hit.
func DocWorkloadHit(msg string) {
	fmt.Printf("         %s  %s\n", faint.Sprint(iconArrow), msg)
}

// DocAnnotation prints a [ANNOTATION] hit header.
func DocAnnotation(msg string) {
	fmt.Printf("       %s  %s\n", yellow.Sprint("⚑"), yellow.Sprint(msg))
}

// DocAnnotationHit prints a single annotation hit.
func DocAnnotationHit(msg string) {
	fmt.Printf("         %s  %s\n", faint.Sprint(iconArrow), msg)
}

// ── Migrate / Plan summary ────────────────────────────────────────────────────

// Summary prints the end-of-run summary line.
func Summary(total, migrated, alreadyDone, skipped, errors int) {
	fmt.Println()
	fmt.Printf("  %s\n", faint.Sprint(strings.Repeat("─", 60)))
	parts := []string{
		bold.Sprintf("%d", total) + faint.Sprint(" file(s) processed"),
	}
	if migrated > 0 {
		parts = append(parts, boldGreen.Sprintf("%d migrated", migrated))
	} else {
		parts = append(parts, faint.Sprintf("%d migrated", migrated))
	}
	if alreadyDone > 0 {
		parts = append(parts, blue.Sprintf("%d already migrated", alreadyDone))
	} else {
		parts = append(parts, faint.Sprintf("%d already migrated", alreadyDone))
	}
	if skipped > 0 {
		parts = append(parts, faint.Sprintf("%d skipped", skipped))
	} else {
		parts = append(parts, faint.Sprintf("%d skipped", skipped))
	}
	if errors > 0 {
		parts = append(parts, red.Sprintf("%d error(s)", errors))
	} else {
		parts = append(parts, faint.Sprintf("0 errors"))
	}
	fmt.Printf("  %s\n", strings.Join(parts, faint.Sprint("  ·  ")))
	fmt.Println()
}

// SectionHeader prints a bold section label (for env-var / annotation sections).
func SectionHeader(msg string) {
	fmt.Println()
	fmt.Printf("  %s  %s\n", cyan.Sprint(iconInfo), bold.Sprint(msg))
}

// SectionNote prints a faint note line under a section header.
func SectionNote(msg string) {
	fmt.Printf("     %s\n", faint.Sprint(msg))
}

// SectionItem prints a single item in a section list.
func SectionItem(msg string) {
	fmt.Printf("     %s  %s\n", faint.Sprint(iconArrow), msg)
}

// ReportWritten prints the location of the written report file.
func ReportWritten(path string) {
	fmt.Printf("  %s  Report written to %s\n", boldGreen.Sprint(iconOK), cyan.Sprint(path))
	fmt.Println()
}

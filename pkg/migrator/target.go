package migrator

import "fmt"

// TargetVersion selects which Kuma/Kong Mesh major version the migration output
// is intended to be applied to.
//
// The two lines differ enough that a single output cannot serve both: 3.0 removes
// a number of fields that are merely deprecated in the 2.x line, and some 3.0
// replacements (MeshOpenTelemetryBackend, SecureDataSource) do not exist at all
// before 2.14. Rewriting a manifest for 3.0 and applying it to a 2.13 control
// plane fails — sometimes silently — so the target is an explicit choice.
type TargetVersion int

const (
	// TargetV2 targets the latest 2.x release line (2.14.x). Fields removed in
	// 3.0 but still accepted in 2.14 are reported as forward-looking advisories
	// rather than rewritten, so the output stays applicable to a 2.x CP.
	TargetV2 TargetVersion = iota
	// TargetV3 targets 3.0. Fields removed in 3.0 are rewritten where the
	// manifest carries enough information to do so safely, and reported as
	// blocking work where it does not.
	TargetV3
)

// String returns the canonical flag spelling.
func (t TargetVersion) String() string {
	switch t {
	case TargetV3:
		return "v3"
	default:
		return "v2"
	}
}

// Describe returns a human-readable description of the target for report headers.
func (t TargetVersion) Describe() string {
	switch t {
	case TargetV3:
		return "v3 (Kuma / Kong Mesh 3.0)"
	default:
		return "v2 (latest 2.x — Kuma / Kong Mesh 2.14.x)"
	}
}

// IsV3 reports whether the target is the 3.0 line.
func (t TargetVersion) IsV3() bool { return t == TargetV3 }

// ParseTargetVersion converts a --to-latest flag value into a TargetVersion.
// Accepts "v2"/"2" and "v3"/"3". An empty string defaults to TargetV2.
func ParseTargetVersion(s string) (TargetVersion, error) {
	switch s {
	case "", "v2", "V2", "2":
		return TargetV2, nil
	case "v3", "V3", "3":
		return TargetV3, nil
	default:
		return TargetV2, fmt.Errorf("invalid --to-latest value %q: want \"v2\" or \"v3\"", s)
	}
}

// removalNote renders the tail of a warning about something removed in 3.0.
// Under TargetV3 the removal is active and the user must act; under TargetV2 the
// field still works and the note is forward-looking.
func (t TargetVersion) removalNote(replacement string) string {
	if t.IsV3() {
		return "removed in 3.0 — " + replacement
	}
	return "still accepted in 2.14 but removed in 3.0 — " + replacement
}

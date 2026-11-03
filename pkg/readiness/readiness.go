// Package readiness runs the Kong Mesh 3.0 readiness audit
// (github.com/Kong/kong-mesh-v3-readiness/preflight, the engine behind the
// kong-mesh-v3-preflight CLI) against a live control plane, writes its report and
// summarises it.
//
// The audit reads the control plane over its REST API: CP configuration, versions,
// zone state, and optionally Envoy config dumps. kuma-migrator's own checks read
// manifests. The two cover different ground, so the audit report is kept next to
// the extracted files and summarised in the migrate report.
package readiness

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Kong/kong-mesh-v3-readiness/preflight"
)

// Report file names, written into the context directory by extract and read
// back by migrate.
const (
	JSONFile = "v3-preflight.json"
	HTMLFile = "v3-preflight.html"
)

// DefaultResourceReadLimit matches the kong-mesh-v3-preflight CLI default.
const DefaultResourceReadLimit = 50000

// Report is the audit report.
type Report = preflight.Report

// Status values of Report.Status.
const (
	StatusClean        = preflight.StatusClean
	StatusBlockers     = preflight.StatusBlockers
	StatusInconclusive = preflight.StatusInconclusive
	StatusFailed       = preflight.StatusFailed
)

// Options configures one audit.
type Options struct {
	// Address is the control plane REST API base URL. Required.
	Address string
	// Token is the bearer token, without the "Bearer " prefix.
	Token string
	// Konnect marks a Konnect-hosted Global. Konnect serves no /config and manages
	// the Global's version, so the audit is told the CP is a Global, skips the
	// Global's own settings and version, and still audits every zone.
	Konnect bool
	// Mesh limits the audit to one mesh. Empty audits every mesh.
	Mesh string
	// LatestPatch is the latest 2.x patch the CP and zones are checked against.
	// Empty makes the version check a coverage gap (status inconclusive).
	LatestPatch string
	// InspectDataplanes fetches up to N Envoy config dumps. 0 skips it.
	InspectDataplanes int
	// ResourceReadLimit caps resource reads. 0 means unlimited.
	ResourceReadLimit int
	// HTTPClient reaches the CP. Required.
	HTTPClient *http.Client
	// Now stamps GeneratedAt. Zero means time.Now.
	Now time.Time
}

// Audit runs the audit. It always returns a report: when the audit cannot run
// (unreachable CP, not a Kuma endpoint) the report has status failed and the
// error is returned too.
func Audit(ctx context.Context, o Options) (Report, error) {
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	stamp := now.UTC().Format(time.RFC3339)
	rep, err := preflight.Audit(ctx, preflight.Options{
		Address:                             o.Address,
		Token:                               o.Token,
		Mesh:                                o.Mesh,
		InspectDataplanes:                   o.InspectDataplanes,
		LatestPatch:                         o.LatestPatch,
		HTTPClient:                          o.HTTPClient,
		ResourceReadLimit:                   o.ResourceReadLimit,
		KnownGlobalControlPlane:             o.Konnect,
		SkipAuditedControlPlaneConfig:       o.Konnect,
		SkipAuditedControlPlaneVersionCheck: o.Konnect,
	})
	if err != nil {
		return preflight.FailureReport(o.Address, err, stamp), err
	}
	rep.GeneratedAt = stamp
	return rep, nil
}

// Write writes the report as JSON and HTML into dir. It returns both paths.
func Write(dir string, rep Report) (jsonPath, htmlPath string, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	js, err := rep.RenderJSON()
	if err != nil {
		return "", "", err
	}
	page, err := rep.RenderHTML()
	if err != nil {
		return "", "", err
	}
	jsonPath = filepath.Join(dir, JSONFile)
	htmlPath = filepath.Join(dir, HTMLFile)
	if err := os.WriteFile(jsonPath, []byte(js), 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(htmlPath, []byte(page), 0o644); err != nil {
		return "", "", err
	}
	return jsonPath, htmlPath, nil
}

// Load reads dir/JSONFile. It returns (nil, nil) when the file does not exist.
func Load(dir string) (*Report, error) {
	data, err := os.ReadFile(filepath.Join(dir, JSONFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rep, err := preflight.ParseReport(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, JSONFile), err)
	}
	return &rep, nil
}

// Blockers returns the blocker findings, in report order.
func Blockers(rep Report) []preflight.Finding {
	var out []preflight.Finding
	for _, f := range rep.Findings {
		if f.Severity == preflight.SeverityBlocker {
			out = append(out, f)
		}
	}
	return out
}

// StatusLine is a one-line description of the report status. Blockers are
// counted as findings (Summary.Blockers sums their occurrences).
func StatusLine(rep Report) string {
	n := len(Blockers(rep))
	switch rep.Status {
	case StatusClean:
		return "clean: no blocker found"
	case StatusBlockers:
		return fmt.Sprintf("%d blocker(s) to fix before the 3.0 upgrade", n)
	case StatusInconclusive:
		if n > 0 {
			return fmt.Sprintf("inconclusive, %d blocker(s) found and %d check(s) not run", n, rep.Summary.CoverageGaps)
		}
		return fmt.Sprintf("inconclusive: %d check(s) could not run", rep.Summary.CoverageGaps)
	case StatusFailed:
		return "failed: the audit could not run (" + rep.Error + ")"
	default:
		return rep.Status
	}
}

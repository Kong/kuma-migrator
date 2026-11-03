package readiness

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Kong/kong-mesh-v3-readiness/preflight"

	"github.com/Kong/kuma-migrator/pkg/ui"
)

// RunOptions configures Run.
type RunOptions struct {
	Address, Token    string
	Konnect           bool
	Mesh              string
	LatestVersion     string // empty: look it up on GitHub
	InspectDataplanes int
	ResourceReadLimit int
	Timeout           time.Duration
	// HTTPClient reaches the control plane (built by the caller so TLS and
	// redirect settings match the extractor's).
	HTTPClient *http.Client
	// OutputDir receives JSONFile and HTMLFile.
	OutputDir string
}

// fetchLatest is a var so tests can avoid the network.
var fetchLatest = LatestPatch

// Run resolves the latest 2.x patch, runs the audit, writes both report files
// and prints a summary. The returned error is the audit error (report status
// failed) or a write error; the report is returned in both cases when produced.
func Run(o RunOptions) (Report, error) {
	latest := o.LatestVersion
	if latest == "" {
		fctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		v, err := fetchLatest(fctx, &http.Client{Timeout: 15 * time.Second})
		cancel()
		if err != nil {
			ui.Warn(fmt.Sprintf("Could not read the latest 2.%d patch from GitHub (%v). The version check is skipped and the audit reports inconclusive. Pass --latest-version to set it.",
				preflight.UpgradeTargetMinor, err))
		} else {
			latest = v
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), o.Timeout)
	defer cancel()
	rep, auditErr := Audit(ctx, Options{
		Address:           o.Address,
		Token:             o.Token,
		Konnect:           o.Konnect,
		Mesh:              o.Mesh,
		LatestPatch:       latest,
		InspectDataplanes: o.InspectDataplanes,
		ResourceReadLimit: o.ResourceReadLimit,
		HTTPClient:        o.HTTPClient,
	})
	_, htmlPath, werr := Write(o.OutputDir, rep)
	PrintSummary(rep, latest)
	if werr != nil {
		return rep, fmt.Errorf("write readiness report: %w", werr)
	}
	ui.ReportWritten(htmlPath)
	return rep, auditErr
}

// maxPrintedBlockers caps the blocker list printed to the terminal; the HTML
// report has all of them.
const maxPrintedBlockers = 15

// PrintSummary prints the status, the control plane and the blocker titles.
func PrintSummary(rep Report, latest string) {
	fmt.Println()
	ui.SectionHeader("3.0 readiness audit (live control plane)")
	cp := rep.ControlPlane
	if cp.Product != "" || cp.Version != "" {
		desc := cp.Product + " " + cp.Version
		if cp.Mode != "" {
			desc += ", " + cp.Mode
		}
		ui.SectionNote(desc)
	}
	if latest != "" {
		ui.SectionNote("versions checked against " + latest)
	}
	fmt.Println()
	switch rep.Status {
	case StatusClean:
		ui.Info(StatusLine(rep))
	case StatusFailed:
		ui.Errorf("%s", StatusLine(rep))
	default:
		ui.Warn(StatusLine(rep))
	}
	blockers := Blockers(rep)
	for i, f := range blockers {
		if i == maxPrintedBlockers {
			ui.SectionNote(fmt.Sprintf("… %d more in the HTML report", len(blockers)-i))
			break
		}
		ui.SectionItem(fmt.Sprintf("%s (%d)", f.Title, f.Count))
	}
	if n := len(rep.Coverage); n > 0 {
		ui.SectionNote(fmt.Sprintf("%d coverage gap(s): listed in the report", n))
	}
	if n := len(rep.Manual); n > 0 {
		ui.SectionNote(fmt.Sprintf("%d manual check(s): listed in the report", n))
	}
	fmt.Println()
}

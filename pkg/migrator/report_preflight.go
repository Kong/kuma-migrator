package migrator

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Kong/kuma-migrator/pkg/readiness"
)

// preflightAudit is one readiness report found in the input tree.
type preflightAudit struct {
	Dir    string // context directory relative to the input dir ("." for the root)
	Report *readiness.Report
	Err    error
}

// findPreflightAudits reads readiness.JSONFile from the input dir and from each
// of its immediate subdirectories (the context directories extract writes).
func findPreflightAudits(inputDir string) []preflightAudit {
	var out []preflightAudit
	add := func(rel string) {
		rep, err := readiness.Load(filepath.Join(inputDir, rel))
		if rep != nil || err != nil {
			out = append(out, preflightAudit{Dir: rel, Report: rep, Err: err})
		}
	}
	add(".")
	entries, err := os.ReadDir(inputDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			add(e.Name())
		}
	}
	return out
}

// writePreflightAudits summarises the readiness audits extract --preflight
// wrote. Written for both targets: the audit is about the running control plane,
// not the output of this run.
func writePreflightAudits(b *strings.Builder, r *MigrationReport) {
	audits := findPreflightAudits(r.InputDir)
	if len(audits) == 0 {
		return
	}
	line(b, "## Live Control Plane Audit")
	line(b, "")
	line(b, "Results of the 3.0 readiness audit (`extract --preflight`, engine of `kong-mesh-v3-preflight`).")
	line(b, "It reads the running control plane: versions, CP settings, zones, stored resources. The rest")
	line(b, "of this report covers the manifests. Fix the blockers from both before upgrading.")
	line(b, "")
	for _, a := range audits {
		html := filepath.ToSlash(filepath.Join(r.InputDir, a.Dir, readiness.HTMLFile))
		linef(b, "### `%s`", a.Dir)
		line(b, "")
		if a.Err != nil {
			linef(b, "Could not read the audit: %v", a.Err)
			line(b, "")
			continue
		}
		rep := *a.Report
		cp := strings.TrimSpace(rep.ControlPlane.Product + " " + rep.ControlPlane.Version)
		if rep.ControlPlane.Mode != "" {
			cp += ", " + rep.ControlPlane.Mode
		}
		line(b, "| | |")
		line(b, "|---|---|")
		linef(b, "| Status | **%s** |", readiness.StatusLine(rep))
		if cp != "" {
			linef(b, "| Control plane | %s |", cp)
		}
		if rep.GeneratedAt != "" {
			linef(b, "| Audited at | %s |", rep.GeneratedAt)
		}
		blockers := readiness.Blockers(rep)
		linef(b, "| Blockers | %d (%d occurrences) |", len(blockers), rep.Summary.Blockers)
		linef(b, "| Coverage gaps | %d |", rep.Summary.CoverageGaps)
		linef(b, "| Manual checks | %d |", len(rep.Manual))
		linef(b, "| Full report | `%s` |", html)
		line(b, "")
		if len(blockers) > 0 {
			line(b, "| Blocker | Count | Examples |")
			line(b, "|---|---:|---|")
			for _, f := range blockers {
				title := mdCell(f.Title)
				if f.Doc != "" {
					title = "[" + title + "](" + f.Doc + ")"
				}
				var ex []string
				for _, e := range f.Examples {
					ex = append(ex, "`"+mdCell(e)+"`")
				}
				more := ""
				if len(ex) > 3 {
					ex, more = ex[:3], ", …"
				}
				linef(b, "| %s | %d | %s%s |", title, f.Count, strings.Join(ex, ", "), more)
			}
			line(b, "")
		}
		if len(rep.Coverage) > 0 {
			line(b, "Not audited (the status is inconclusive until these are resolved):")
			line(b, "")
			for _, g := range rep.Coverage {
				linef(b, "- `%s`: %s", g.Path, mdCell(g.Reason))
			}
			line(b, "")
		}
	}
}

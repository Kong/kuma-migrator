package migrator

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kong/kong-mesh-v3-readiness/preflight"

	"github.com/Kong/kuma-migrator/pkg/readiness"
)

func TestReport_LivePreflightAudit(t *testing.T) {
	noUserConfig(t)
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{
		"p-zone-ctx/.kuma-migrator.json":             `{"tool":"kumactl","context":"p","cpMode":"zone"}`,
		"p-zone-ctx/mesh-default/resiliency/mt.yaml": "apiVersion: kuma.io/v1alpha1\nkind: MeshTimeout\nmetadata:\n  name: t\n  namespace: kuma-system\nspec:\n  targetRef:\n    kind: Mesh\n",
	})
	rep := preflight.Report{
		Schema: preflight.SchemaVersion, Tool: preflight.ToolName, Status: preflight.StatusBlockers,
		GeneratedAt:  "2026-11-03T10:00:00Z",
		ControlPlane: preflight.ControlPlane{Product: "Kong Mesh", Version: "2.14.5", Mode: "zone"},
		Meshes:       []string{"default"},
		Summary:      preflight.Summary{Blockers: 1},
		Findings: []preflight.Finding{{
			Severity: preflight.SeverityBlocker, Group: "control_plane", Category: "cp_config",
			Title: "KDS event-based watchdog | not enabled", Doc: "https://example.com/kds", Count: 1,
			Examples: []string{"a", "b", "c", "d"},
		}},
		Coverage: []preflight.CoverageGap{},
		Manual:   []preflight.ManualCheck{{Title: "Check Helm values"}},
	}
	if _, _, err := readiness.Write(filepath.Join(in, "p-zone-ctx"), rep); err != nil {
		t.Fatal(err)
	}
	for _, target := range []TargetVersion{TargetV2, TargetV3} {
		_, md := renderApply(t, in, out, target)
		for _, want := range []string{
			"## Live Control Plane Audit",
			"### `p-zone-ctx`",
			"| Status | **1 blocker(s) to fix before the 3.0 upgrade** |",
			"| Control plane | Kong Mesh 2.14.5, zone |",
			"[KDS event-based watchdog \\| not enabled](https://example.com/kds) | 1 | `a`, `b`, `c`, … |",
			"p-zone-ctx/" + readiness.HTMLFile,
		} {
			if !strings.Contains(md, want) {
				t.Errorf("%s: report missing %q", target, want)
			}
		}
	}
}

func TestReport_NoPreflightAuditNoSection(t *testing.T) {
	noUserConfig(t)
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{
		"p-zone-ctx/.kuma-migrator.json":             `{"tool":"kumactl","context":"p","cpMode":"zone"}`,
		"p-zone-ctx/mesh-default/resiliency/mt.yaml": "apiVersion: kuma.io/v1alpha1\nkind: MeshTimeout\nmetadata:\n  name: t\n  namespace: kuma-system\nspec:\n  targetRef:\n    kind: Mesh\n",
	})
	_, md := renderApply(t, in, out, TargetV3)
	if strings.Contains(md, "Live Control Plane Audit") {
		t.Error("section rendered without an audit file")
	}
}

func TestReport_BadPreflightAuditReported(t *testing.T) {
	noUserConfig(t)
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{
		"p-zone-ctx/" + readiness.JSONFile:           `{"tool_schema":"kong-mesh-v3-preflight/v1"}`,
		"p-zone-ctx/mesh-default/resiliency/mt.yaml": "apiVersion: kuma.io/v1alpha1\nkind: MeshTimeout\nmetadata:\n  name: t\n  namespace: kuma-system\nspec:\n  targetRef:\n    kind: Mesh\n",
	})
	_, md := renderApply(t, in, out, TargetV3)
	if !strings.Contains(md, "Could not read the audit") {
		t.Error("unreadable audit not reported")
	}
}

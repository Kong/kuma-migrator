package migrator

import (
	"strings"
	"testing"
)

const otelMeshTrace = `apiVersion: kuma.io/v1alpha1
kind: MeshTrace
metadata:
  name: otel
  namespace: kuma-system
  labels:
    kuma.io/mesh: default
spec:
  targetRef:
    kind: Mesh
  default:
    backends:
      - type: OpenTelemetry
        openTelemetry:
          endpoint: otel-collector:4317
`

const cleanMTP = `apiVersion: kuma.io/v1alpha1
kind: MeshTrafficPermission
metadata:
  name: clean
  namespace: kuma-system
  labels:
    kuma.io/mesh: default
spec:
  targetRef:
    kind: Mesh
  rules:
    - default:
        allow:
          - spiffeID: {type: Exact, value: "spiffe://default/ns/a/sa/b"}
`

// A passthrough document the v3 pass rewrites (here: inline OTel endpoint →
// MeshOpenTelemetryBackend companion + backendRef) must not be reported as
// ALREADY MIGRATED.
func TestPassthroughRewrittenByTargetIsLabelledRewritten(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		target    TargetVersion
		wantLabel string
	}{
		{"MeshTrace inline OTel, v3", otelMeshTrace, TargetV3, labelRewritten},
		{"MeshTrace inline OTel, v2", otelMeshTrace, TargetV2, labelAlreadyDone},
		{"clean MeshTrafficPermission, v3", cleanMTP, TargetV3, labelAlreadyDone},
		{"clean MeshTrafficPermission, v2", cleanMTP, TargetV2, labelAlreadyDone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in, out := tempDir(t), tempDir(t)
			writeTempFile(t, in, "policy.yaml", tc.input)
			report, err := runMigration(in, out, false, "", tc.target)
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Files) != 1 {
				t.Fatalf("want 1 file, got %d", len(report.Files))
			}
			fr := report.Files[0]
			if fr.Label != tc.wantLabel {
				t.Errorf("label = %q, want %q", fr.Label, tc.wantLabel)
			}
			if tc.wantLabel == labelRewritten {
				if report.MigratedCount != 1 || report.AlreadyDoneCount != 0 {
					t.Errorf("counts migrated=%d already=%d, want 1/0", report.MigratedCount, report.AlreadyDoneCount)
				}
				if !fr.Changes[0].Rewritten {
					t.Error("DocChange.Rewritten not set")
				}
				var sb strings.Builder
				writeMarkdown(&sb, report)
				md := sb.String()
				if !strings.Contains(md, "Passthrough, rewritten for the target") {
					t.Errorf("report does not show the rewrite in the scenario column:\n%s", md)
				}
				if strings.Contains(md, "## Already Migrated") {
					t.Errorf("rewritten file listed under Already Migrated:\n%s", md)
				}
			}
		})
	}
}

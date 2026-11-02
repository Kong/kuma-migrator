package migrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kong/kuma-migrator/pkg/extractor"
)

// renderApply runs the pipeline in write mode and returns the report and its
// Markdown.
func renderApply(t *testing.T, in, out string, target TargetVersion) (*MigrationReport, string) {
	t.Helper()
	r, err := runMigration(in, out, true, "", target)
	if err != nil {
		t.Fatal(err)
	}
	r.Mode = "apply"
	var b strings.Builder
	writeMarkdown(&b, r)
	return r, b.String()
}

// section returns md from the heading `from` up to the next "## " heading.
func section(t *testing.T, md, from string) string {
	t.Helper()
	i := strings.Index(md, from)
	if i < 0 {
		t.Fatalf("no %q in report:\n%s", from, md)
	}
	rest := md[i+len(from):]
	if j := strings.Index(rest, "\n## "); j >= 0 {
		rest = rest[:j]
	}
	return from + rest
}

// mustOrder fails unless every needle appears in s, in the given order.
func mustOrder(t *testing.T, s string, needles ...string) {
	t.Helper()
	last := -1
	for _, n := range needles {
		i := strings.Index(s, n)
		if i < 0 {
			t.Fatalf("missing %q in:\n%s", n, s)
		}
		if i < last {
			t.Fatalf("%q appears before the previous needle in:\n%s", n, s)
		}
		last = i
	}
}

const meshGatewayConfig = `apiVersion: kuma.io/v1alpha1
kind: MeshGatewayConfig
metadata:
  name: gw-config
spec:
  replicas: 2
`

const meshWithMTLS = `apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
  mtls:
    enabledBackend: ca-1
    backends:
    - name: ca-1
      type: builtin
`

const meshTraceInlineOtelV3 = `apiVersion: kuma.io/v1alpha1
kind: MeshTrace
metadata:
  name: trace
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
        endpoint: otel-collector.observability:4317
`

// ---- MeshGatewayConfig is removed in 3.0 -----------------------------------

func TestV3_MeshGatewayConfigGoesToManual(t *testing.T) {
	noUserConfig(t)
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{
		// kumactl on a Universal CP: the delete command must still be kubectl,
		// since MeshGatewayConfig only ever exists as a Kubernetes CRD.
		"g-global-ctx/.kuma-migrator.json":                      `{"tool":"kumactl","context":"g","cpMode":"global","environment":"universal"}`,
		"g-global-ctx/global-scoped-resources/routing/mgc.yaml": meshGatewayConfig,
	})
	r, md := renderApply(t, in, out, TargetV3)

	fr, dc, ok := findChange(r, "MeshGatewayConfig")
	if !ok {
		t.Fatal("MeshGatewayConfig not in report")
	}
	if fr.Label != labelManual {
		t.Errorf("label = %q, want %q", fr.Label, labelManual)
	}
	if len(fr.OutputRelPaths) != 0 {
		t.Errorf("must not be in the apply tree, got %v", fr.OutputRelPaths)
	}
	want := "g-global-ctx/" + ManualDir + "/global-scoped-resources/routing/MeshGatewayConfig-gw-config.yaml"
	if dc.ManualRelPath != want {
		t.Errorf("ManualRelPath = %q, want %q", dc.ManualRelPath, want)
	}
	if _, err := os.Stat(filepath.Join(out, want)); err != nil {
		t.Errorf("manual copy not written: %v", err)
	}
	if !strings.Contains(strings.Join(dc.Warnings, "\n"), "meshgatewayconfigs.kuma.io") {
		t.Errorf("expected the MeshGatewayConfig removal note, got %v", dc.Warnings)
	}
	del := section(t, md, "## Delete Before Upgrading to 3.0")
	if !strings.Contains(del, "kubectl delete MeshGatewayConfig gw-config\n") {
		t.Errorf("expected a cluster-scoped kubectl delete:\n%s", del)
	}
	if strings.Contains(del, "kumactl delete meshgatewayconfig") {
		t.Errorf("kumactl cannot address MeshGatewayConfig:\n%s", del)
	}
	if r.ManualCount != 1 {
		t.Errorf("ManualCount = %d, want 1", r.ManualCount)
	}
}

func TestV2_MeshGatewayConfigStaysInApplyTree(t *testing.T) {
	noUserConfig(t)
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{"routing/mgc.yaml": meshGatewayConfig})
	r, err := runMigration(in, out, false, "", TargetV2)
	if err != nil {
		t.Fatal(err)
	}
	fr, dc, _ := findChange(r, "MeshGatewayConfig")
	if dc.ManualRelPath != "" || len(fr.OutputRelPaths) != 1 {
		t.Errorf("v2 must keep MeshGatewayConfig in the apply tree, got %+v / %+v", fr, dc)
	}
}

// ---- Mesh ordering under v3 -------------------------------------------------

func TestV3Checklist_MeshExclusiveBeforeGlobalUpgrade_kubectl(t *testing.T) {
	noUserConfig(t)
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{
		"p-global-ctx/.kuma-migrator.json":         `{"tool":"kubectl","context":"p","cpMode":"global","environment":"kubernetes"}`,
		"p-global-ctx/mesh-default/mesh/mesh.yaml": meshWithMTLS,
	})
	_, md := renderApply(t, in, out, TargetV3)
	cl := section(t, md, "## Apply Checklist")

	patch := `kubectl patch mesh default --type merge -p '{"spec":{"meshServices":{"mode":"Exclusive"}}}'`
	// Exclusive comes first: on 2.14 a MeshIdentity in a mesh that is not
	// Exclusive stays MeshServicesDisabled and never becomes ready.
	mustOrder(t, cl,
		"Set `meshServices.mode: Exclusive` on every mesh",
		"`MeshServicesDisabled` and never becomes ready",
		patch,
		"Finish the `MeshIdentity` cutover",
		"Upgrade the Global Control Plane",
		"Apply the `Mesh` CRs",
		// The v3 Mesh drops spec.mtls from an object the input holds: replace.
		"kubectl replace -f "+filepath.Join(out, "p-global-ctx/mesh-default/mesh/Mesh-default.yaml")+" --save-config",
	)
	if strings.Contains(cl, "Apply `Mesh` CRs last") {
		t.Errorf("v3 must not use the v2 Mesh-last step:\n%s", cl)
	}
}

func TestV3Checklist_MeshExclusive_kumactl(t *testing.T) {
	noUserConfig(t)
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{
		"u-global-ctx/.kuma-migrator.json":         `{"tool":"kumactl","context":"u","cpMode":"global","environment":"universal"}`,
		"u-global-ctx/mesh-default/mesh/mesh.yaml": "type: Mesh\nname: default\n",
	})
	_, md := renderApply(t, in, out, TargetV3)
	cl := section(t, md, "## Apply Checklist")
	mustOrder(t, cl,
		`kumactl get mesh default -o yaml | yq '.meshServices.mode = "Exclusive"' | kumactl apply -f -`,
		"Upgrade the Global Control Plane",
	)
	// No spec.mtls in the input: no cutover step.
	if strings.Contains(cl, "MeshIdentity` cutover on the 2.x") {
		t.Errorf("no mesh carries mtls; cutover step should be absent:\n%s", cl)
	}
}

func TestV3Checklist_NoMeshInInputStillSetsExclusive(t *testing.T) {
	noUserConfig(t)
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{"resiliency/t.yaml": legacyTimeout})
	_, md := renderApply(t, in, out, TargetV3)
	cl := section(t, md, "## Apply Checklist")
	mustOrder(t, cl,
		"No `Mesh` was in the input",
		"kubectl get meshes",
		"Upgrade the Global Control Plane",
	)
}

func TestV2Checklist_MeshOrderUnchanged(t *testing.T) {
	noUserConfig(t)
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{
		"p-global-ctx/.kuma-migrator.json":         `{"tool":"kubectl","context":"p","cpMode":"global","environment":"kubernetes"}`,
		"p-global-ctx/mesh-default/mesh/mesh.yaml": meshWithMTLS,
	})
	_, md := renderApply(t, in, out, TargetV2)
	cl := section(t, md, "## Apply Checklist")
	mustOrder(t, cl, "Upgrade the Global Control Plane", "Apply `Mesh` CRs last")
	if strings.Contains(cl, "MeshIdentity` cutover") || strings.Contains(cl, "on every mesh, on the 2.x Global") {
		t.Errorf("v2 checklist must not carry the v3 pre-upgrade Mesh steps:\n%s", cl)
	}
}

// ---- MeshOpenTelemetryBackend before the policies that reference it ---------

func TestV3_MOTBAppliedBeforeObservability(t *testing.T) {
	noUserConfig(t)
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{
		"p-global-ctx/.kuma-migrator.json":                   `{"tool":"kubectl","context":"p","cpMode":"global","environment":"kubernetes"}`,
		"p-global-ctx/mesh-default/observability/trace.yaml": meshTraceInlineOtelV3,
	})
	r, md := renderApply(t, in, out, TargetV3)
	fr, _, _ := findChange(r, "MeshTrace")
	var motb string
	for _, p := range fr.OutputRelPaths {
		if strings.Contains(p, "MeshOpenTelemetryBackend") {
			motb = p
		}
	}
	if !strings.HasPrefix(motb, "p-global-ctx/mesh-default/"+backendsSubfolder+"/") {
		t.Fatalf("MOTB should be written under %s/, got %q (all: %v)", backendsSubfolder, motb, fr.OutputRelPaths)
	}
	cl := section(t, md, "## Apply Checklist")
	mustOrder(t, cl,
		filepath.Join(out, "p-global-ctx/mesh-default", backendsSubfolder)+"/",
		filepath.Join(out, "p-global-ctx/mesh-default/observability")+"/",
	)
}

func TestOutputSubfolder(t *testing.T) {
	if got := outputSubfolder("MeshOpenTelemetryBackend"); got != backendsSubfolder {
		t.Errorf("MOTB -> %q", got)
	}
	if got := outputSubfolder("MeshTrace"); got != "observability" {
		t.Errorf("MeshTrace -> %q", got)
	}
	if !isKindSubfolder(backendsSubfolder) {
		t.Error("backends must be a recognised kind subfolder so migrate output can be re-read")
	}
}

// ---- write tool per context -------------------------------------------------

func TestResolveContextTool(t *testing.T) {
	cases := []struct {
		name string
		meta *extractor.ContextMeta
		tool string
		k8s  bool
	}{
		{"no metadata", nil, "kubectl", false},
		{"kubectl", &extractor.ContextMeta{Tool: "kubectl", Environment: "kubernetes"}, "kubectl", false},
		{"kumactl universal", &extractor.ContextMeta{Tool: "kumactl", Environment: "universal"}, "kumactl", false},
		{"kumactl unknown env", &extractor.ContextMeta{Tool: "kumactl"}, "kumactl", false},
		{"kumactl kubernetes", &extractor.ContextMeta{Tool: "kumactl", Environment: "kubernetes"}, "kubectl", true},
		{"konnect", &extractor.ContextMeta{Tool: "kumactl", IsKonnect: true}, "kumactl", false},
	}
	for _, c := range cases {
		got := resolveContextTool(c.meta)
		if got.Tool != c.tool || got.KumactlOnKubernetes != c.k8s {
			t.Errorf("%s: got %+v, want tool=%s kumactlOnK8s=%v", c.name, got, c.tool, c.k8s)
		}
	}
}

func TestChecklist_KumactlOnKubernetesUsesKubectl(t *testing.T) {
	noUserConfig(t)
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{
		"k-global-ctx/.kuma-migrator.json":            `{"tool":"kumactl","context":"kctx","cpMode":"global","environment":"kubernetes"}`,
		"k-global-ctx/mesh-default/resiliency/t.yaml": "apiVersion: kuma.io/v1alpha1\nkind: MeshTimeout\nmetadata:\n  name: t\n  namespace: kuma-system\n  labels:\n    kuma.io/mesh: default\nspec:\n  targetRef:\n    kind: Mesh\n  to:\n  - targetRef:\n      kind: Mesh\n    default:\n      idleTimeout: 1s\n",
	})
	_, md := renderApply(t, in, out, TargetV2)
	cl := section(t, md, "## Apply Checklist")
	if strings.Contains(cl, "kumactl apply") || strings.Contains(cl, "kumactl config control-planes switch") {
		t.Errorf("kumactl cannot write to a Kubernetes-backed CP:\n%s", cl)
	}
	mustOrder(t, cl,
		"extracted with kumactl from a Kubernetes-backed control plane",
		"kumactl context `kctx`",
		"kubectl apply -f "+filepath.Join(out, "k-global-ctx/mesh-default/resiliency")+"/",
	)
}

func TestChecklist_KumactlOnKubernetesUniversalFilesWarns(t *testing.T) {
	noUserConfig(t)
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{
		"k-global-ctx/.kuma-migrator.json":            `{"tool":"kumactl","context":"kctx","cpMode":"global","environment":"kubernetes"}`,
		"k-global-ctx/mesh-default/resiliency/t.yaml": "type: MeshTimeout\nname: t\nmesh: default\nspec:\n  targetRef:\n    kind: Mesh\n",
	})
	_, md := renderApply(t, in, out, TargetV2)
	if !strings.Contains(section(t, md, "## Apply Checklist"), "--output-format kubernetes") {
		t.Errorf("Universal files on a Kubernetes CP need the re-extract hint:\n%s", md)
	}
}

func TestChecklist_KonnectKeepsKumactl(t *testing.T) {
	noUserConfig(t)
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{
		"kon-global-ctx/.kuma-migrator.json":            `{"tool":"kumactl","context":"konnect","cpMode":"global","isKonnect":true}`,
		"kon-global-ctx/mesh-default/resiliency/t.yaml": "type: MeshTimeout\nname: t\nmesh: default\nspec:\n  targetRef:\n    kind: Mesh\n",
	})
	_, md := renderApply(t, in, out, TargetV2)
	cl := section(t, md, "## Apply Checklist")
	mustOrder(t, cl, "Konnect-hosted CP", "kumactl config control-planes switch --name konnect", "kumactl apply -f ")
	if strings.Contains(cl, "use-context") {
		t.Errorf("kumactl 2.14/3.0 have no `config use-context`:\n%s", cl)
	}
}

// ---- non-policy documents the scan rewrites ---------------------------------

func TestV3_DataplaneRewrittenIsScannedNotSkipped(t *testing.T) {
	noUserConfig(t)
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{
		"u-zone-ctx/.kuma-migrator.json":        `{"tool":"kumactl","context":"u","cpMode":"zone","environment":"universal"}`,
		"u-zone-ctx/mesh-default/other/dp.yaml": universalDataplane,
		"u-zone-ctx/mesh-default/other/cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: plain\n",
	})
	r, md := renderApply(t, in, out, TargetV3)

	fr, dc, _ := findChange(r, "Dataplane")
	if fr.Label != labelScanned || !dc.Rewritten {
		t.Errorf("Dataplane: label=%q rewritten=%v, want %q/true", fr.Label, dc.Rewritten, labelScanned)
	}
	if got := scannedReason(fr); !strings.Contains(got, "rewritten") {
		t.Errorf("scannedReason = %q", got)
	}
	cm, _, _ := findChange(r, "ConfigMap")
	if cm.Label != labelSkipped {
		t.Errorf("unchanged ConfigMap label = %q, want %q", cm.Label, labelSkipped)
	}
	if got := skippedReason(cm); got != "not a Kuma policy; copied unchanged" {
		t.Errorf("skippedReason = %q", got)
	}
	if r.ScannedCount != 1 || r.SkippedCount != 1 {
		t.Errorf("ScannedCount=%d SkippedCount=%d, want 1/1", r.ScannedCount, r.SkippedCount)
	}
	if !strings.Contains(section(t, md, "## Scanned Non-Policy Resources"), "`dp.yaml` — not a policy; rewritten") {
		t.Errorf("Dataplane should be listed as scanned:\n%s", md)
	}
}

func TestSkippedReason_SkipListed(t *testing.T) {
	if got := skippedReason(FileReport{}); got != "on the skip list; not written" {
		t.Errorf("skippedReason = %q", got)
	}
}

// ---- documents the target rejects go to _manual/ ----------------------------

func TestProcessFile_RejectedByTargetGoesToManual(t *testing.T) {
	noUserConfig(t)
	orig := transformDocument
	t.Cleanup(func() { transformDocument = orig })
	converted := []byte("apiVersion: kuma.io/v1alpha1\nkind: MeshTrafficPermission\nmetadata:\n  name: mtp\n  namespace: kuma-system\nspec:\n  targetRef:\n    kind: Mesh\n  from:\n  - targetRef:\n      kind: Mesh\n    default:\n      action: Allow\n")
	transformDocument = func(raw []byte, opts TransformOptions) ([][]byte, []string, Scenario, error) {
		return [][]byte{converted}, []string{"a warning"}, ScenarioPassthrough,
			&RejectedByTargetError{Target: opts.Target, Rejected: []RejectedDoc{{Index: 0, Kind: "MeshTrafficPermission", Name: "mtp", Reasons: []string{"3.0 removes spec.from"}}}}
	}

	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{
		"p-global-ctx/.kuma-migrator.json":            `{"tool":"kubectl","context":"p","cpMode":"global","environment":"kubernetes"}`,
		"p-global-ctx/mesh-default/zero-trust/m.yaml": string(converted),
	})
	r, md := renderApply(t, in, out, TargetV3)

	fr, dc, _ := findChange(r, "MeshTrafficPermission")
	if !dc.Rejected || !strings.Contains(dc.ErrMsg, "3.0 removes spec.from") {
		t.Errorf("DocChange = %+v, want Rejected with the reason", dc)
	}
	want := "p-global-ctx/" + ManualDir + "/mesh-default/zero-trust/MeshTrafficPermission-kuma-system-mtp.yaml"
	if dc.ManualRelPath != want || len(fr.OutputRelPaths) != 0 {
		t.Errorf("ManualRelPath=%q OutputRelPaths=%v, want %q and none", dc.ManualRelPath, fr.OutputRelPaths, want)
	}
	if b, err := os.ReadFile(filepath.Join(out, want)); err != nil || !strings.Contains(string(b), "action: Allow") {
		t.Errorf("converted output not written to the manual path: %v", err)
	}
	if fr.Label != labelPartialError || r.ErrorCount != 1 {
		t.Errorf("label=%q ErrorCount=%d: rejected docs are reported like errored conversions", fr.Label, r.ErrorCount)
	}
	errs := section(t, md, "## Action Items")
	if !strings.Contains(errs, "Converted output kept at `"+want+"`") {
		t.Errorf("Errors section should point at the manual copy:\n%s", errs)
	}
	if strings.Contains(section(t, md, "## Apply Checklist"), "zero-trust") {
		t.Errorf("rejected doc must not be applied:\n%s", md)
	}
}

func TestProcessFile_RejectedByTargetUnderV2AlsoManual(t *testing.T) {
	noUserConfig(t)
	orig := transformDocument
	t.Cleanup(func() { transformDocument = orig })
	transformDocument = func(raw []byte, opts TransformOptions) ([][]byte, []string, Scenario, error) {
		return nil, nil, ScenarioPassthrough, &RejectedByTargetError{Target: opts.Target}
	}
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{"zero-trust/m.yaml": "apiVersion: kuma.io/v1alpha1\nkind: MeshTrafficPermission\nmetadata:\n  name: mtp\n"})
	r, err := runMigration(in, out, true, "", TargetV2)
	if err != nil {
		t.Fatal(err)
	}
	fr, dc, _ := findChange(r, "MeshTrafficPermission")
	if !strings.HasPrefix(dc.ManualRelPath, ManualDir+"/") || len(fr.OutputRelPaths) != 0 {
		t.Errorf("without output, the original goes to %s/: %+v / %+v", ManualDir, fr, dc)
	}
	rej := &RejectedByTargetError{Target: TargetV2, Rejected: []RejectedDoc{{Index: 1, Kind: "MeshTimeout", Name: "t", Reasons: []string{"nope"}}}}
	if got := rej.Error(); !strings.Contains(got, `MeshTimeout "t": nope`) || rej.IsRejected(0) || !rej.IsRejected(1) {
		t.Errorf("Error() = %q, IsRejected(0)=%v IsRejected(1)=%v", got, rej.IsRejected(0), rej.IsRejected(1))
	}
}

func TestProcessFile_RejectedByTargetOnlyListedDocsGoToManual(t *testing.T) {
	noUserConfig(t)
	orig := transformDocument
	t.Cleanup(func() { transformDocument = orig })
	ok := []byte("apiVersion: kuma.io/v1alpha1\nkind: MeshTimeout\nmetadata:\n  name: t\n  namespace: kuma-system\nspec:\n  targetRef:\n    kind: Mesh\n")
	bad := []byte("apiVersion: kuma.io/v1alpha1\nkind: MeshTimeout\nmetadata:\n  name: t-outbound\n  namespace: kuma-system\nspec:\n  targetRef:\n    kind: MeshService\n")
	transformDocument = func(raw []byte, opts TransformOptions) ([][]byte, []string, Scenario, error) {
		return [][]byte{ok, bad}, nil, ScenarioRules,
			&RejectedByTargetError{Target: opts.Target, Rejected: []RejectedDoc{{Index: 1, Kind: "MeshTimeout", Name: "t-outbound", Reasons: []string{"top-level MeshService"}}}}
	}
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{"resiliency/t.yaml": string(ok)})
	r, err := runMigration(in, out, true, "", TargetV3)
	if err != nil {
		t.Fatal(err)
	}
	fr, dc, _ := findChange(r, "MeshTimeout")
	if len(fr.OutputRelPaths) != 1 || !strings.Contains(fr.OutputRelPaths[0], "MeshTimeout-kuma-system-t.yaml") {
		t.Errorf("accepted doc must stay in the apply tree: %v", fr.OutputRelPaths)
	}
	if !strings.Contains(dc.ManualRelPath, "t-outbound") {
		t.Errorf("rejected doc must go to %s/: %q", ManualDir, dc.ManualRelPath)
	}
}

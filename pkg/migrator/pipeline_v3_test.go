package migrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree writes files (relative path → content) under root.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

// noUserConfig points config.Load at a missing file so the built-in skip lists apply.
func noUserConfig(t *testing.T) {
	t.Helper()
	t.Setenv("KUMA_MIGRATOR_CONFIG", filepath.Join(t.TempDir(), "absent.yaml"))
}

func findChange(r *MigrationReport, kind string) (FileReport, DocChange, bool) {
	for _, fr := range r.Files {
		for _, dc := range fr.Changes {
			if dc.InputKind == kind {
				return fr, dc, true
			}
		}
	}
	return FileReport{}, DocChange{}, false
}

const universalDataplane = `type: Dataplane
mesh: default
name: dp1
networking:
  address: 10.0.0.2
  transparentProxying:
    redirectPortInboundV6: 15010
  inbound:
  - port: 8080
    tags:
      kuma.io/service: web
`

// ---- skip list per environment ---------------------------------------------

func TestSkipPolicy(t *testing.T) {
	p := skipPolicy{
		kubernetes: map[string]bool{"Dataplane": true},
		universal:  map[string]bool{"Zone": true},
	}
	cases := []struct {
		env       string
		kind      string
		universal bool
		want      bool
	}{
		{"", "Dataplane", false, true},           // unknown env, Kubernetes shape → Kubernetes list
		{"", "Dataplane", true, false},           // unknown env, Universal shape → Universal list
		{"kubernetes", "Dataplane", true, true},  // recorded env wins over shape
		{"universal", "Dataplane", false, false}, // recorded env wins over shape
		{"universal", "Zone", false, true},
	}
	for _, c := range cases {
		p.env = c.env
		if got := p.skips(c.kind, c.universal); got != c.want {
			t.Errorf("env=%q kind=%s universalShape=%v: got %v, want %v", c.env, c.kind, c.universal, got, c.want)
		}
	}
}

func TestRunMigration_UniversalContextScansDataplane(t *testing.T) {
	noUserConfig(t)
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{
		"uni-zone-ctx/.kuma-migrator.json":           `{"tool":"kumactl","context":"uni","cpMode":"zone","environment":"universal"}`,
		"uni-zone-ctx/mesh-default/other/dp1.yaml":   universalDataplane,
		"k8s-zone-ctx/.kuma-migrator.json":           `{"tool":"kubectl","context":"k8s","cpMode":"zone","environment":"kubernetes"}`,
		"k8s-zone-ctx/mesh-default/other/dp2.yaml":   strings.Replace(universalDataplane, "name: dp1", "name: dp2", 1),
		"legacy-ctx/mesh-default/other/dp3.yaml":     strings.Replace(universalDataplane, "name: dp1", "name: dp3", 1),
		"legacy-ctx/mesh-default/other/dp4-k8s.yaml": "apiVersion: kuma.io/v1alpha1\nkind: Dataplane\nmetadata:\n  name: dp4\n",
	})
	r, err := runMigration(in, out, false, "", TargetV2)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]DocChange{}
	for _, fr := range r.Files {
		for _, dc := range fr.Changes {
			byName[dc.InputName] = dc
		}
	}
	// Universal context: scanned, deprecation found.
	if dc := byName["dp1"]; dc.Scenario == ScenarioSkipped && len(dc.Warnings) == 0 {
		t.Errorf("dp1 (universal context) should be scanned, got %+v", dc)
	}
	if !strings.Contains(strings.Join(byName["dp1"].Warnings, "\n"), "redirectPortInboundV6") {
		t.Errorf("dp1 should carry the redirectPortInboundV6 warning, got %v", byName["dp1"].Warnings)
	}
	// Kubernetes context: skipped even though the document is Universal-shaped.
	if len(byName["dp2"].Warnings) != 0 {
		t.Errorf("dp2 (kubernetes context) should be skipped, got warnings %v", byName["dp2"].Warnings)
	}
	// No metadata: Universal shape → scanned; Kubernetes shape → skipped.
	if len(byName["dp3"].Warnings) == 0 {
		t.Errorf("dp3 (no metadata, universal shape) should be scanned")
	}
	if len(byName["dp4"].Warnings) != 0 {
		t.Errorf("dp4 (no metadata, kubernetes shape) should be skipped, got %v", byName["dp4"].Warnings)
	}
}

func TestRunMigration_UserSkipListWins(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "cfg.yaml")
	if err := os.WriteFile(cfgPath, []byte("skip:\n  universal: [Dataplane]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUMA_MIGRATOR_CONFIG", cfgPath)
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{
		"uni-zone-ctx/.kuma-migrator.json":         `{"tool":"kumactl","context":"uni","cpMode":"zone","environment":"universal"}`,
		"uni-zone-ctx/mesh-default/other/dp1.yaml": universalDataplane,
	})
	r, err := runMigration(in, out, false, "", TargetV2)
	if err != nil {
		t.Fatal(err)
	}
	_, dc, _ := findChange(r, "Dataplane")
	if dc.Scenario != ScenarioSkipped || len(dc.Warnings) != 0 {
		t.Errorf("user skip list should skip Dataplane, got %+v", dc)
	}
}

// ---- v3 manual subtree ------------------------------------------------------

const trafficRoute = `type: TrafficRoute
mesh: default
name: tr1
sources:
- match:
    kuma.io/service: "*"
destinations:
- match:
    kuma.io/service: "*"
conf:
  destination:
    kuma.io/service: "*"
`

const meshGlobalRateLimit = `apiVersion: kuma.io/v1alpha1
kind: MeshGlobalRateLimit
metadata:
  name: mgrl
  namespace: kuma-system
  labels:
    kuma.io/mesh: default
spec:
  targetRef:
    kind: Mesh
`

const legacyTimeout = `type: Timeout
mesh: default
name: t1
sources:
- match:
    kuma.io/service: "*"
destinations:
- match:
    kuma.io/service: "*"
conf:
  connectTimeout: 5s
`

func v3Fixture(t *testing.T) (in, out string) {
	t.Helper()
	noUserConfig(t)
	in, out = t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{
		"prod-global-ctx/.kuma-migrator.json":             `{"tool":"kubectl","context":"prod","cpMode":"global","environment":"kubernetes"}`,
		"prod-global-ctx/mesh-default/routing/tr1.yaml":   trafficRoute,
		"prod-global-ctx/mesh-default/other/mgrl.yaml":    meshGlobalRateLimit,
		"prod-global-ctx/mesh-default/resiliency/t1.yaml": legacyTimeout,
	})
	return in, out
}

func TestMigrate_V3ManualSubtree(t *testing.T) {
	in, out := v3Fixture(t)
	r, err := runMigration(in, out, true, "", TargetV3)
	if err != nil {
		t.Fatal(err)
	}

	for _, kind := range []string{"TrafficRoute", "MeshGlobalRateLimit"} {
		fr, dc, ok := findChange(r, kind)
		if !ok {
			t.Fatalf("%s not in report", kind)
		}
		if !strings.HasPrefix(dc.ManualRelPath, "prod-global-ctx/"+ManualDir+"/mesh-default/") {
			t.Errorf("%s: ManualRelPath = %q, want under prod-global-ctx/%s/mesh-default/", kind, dc.ManualRelPath, ManualDir)
		}
		if len(fr.OutputRelPaths) != 0 {
			t.Errorf("%s: must not be in OutputRelPaths, got %v", kind, fr.OutputRelPaths)
		}
		if _, err := os.Stat(filepath.Join(out, dc.ManualRelPath)); err != nil {
			t.Errorf("%s: manual copy not written: %v", kind, err)
		}
	}
	// Nothing removed in 3.0 may sit in the apply tree.
	_ = filepath.Walk(filepath.Join(out, "prod-global-ctx", "mesh-default"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && (strings.Contains(p, "TrafficRoute") || strings.Contains(p, "MeshGlobalRateLimit")) {
			t.Errorf("removed kind in apply tree: %s", p)
		}
		return nil
	})

	_, dc, _ := findChange(r, "Timeout")
	if dc.ManualRelPath != "" || !strings.Contains(strings.Join(dc.Warnings, "\n"), "converted replacement") {
		t.Errorf("converted Timeout should stay in the apply tree with a delete note, got %+v", dc)
	}

	var b strings.Builder
	r.Mode = "apply"
	writeMarkdown(&b, r)
	md := b.String()
	checklist := md[strings.Index(md, "## Apply Checklist"):strings.Index(md, "## Delete Before Upgrading to 3.0")]
	if strings.Contains(checklist, ManualDir+"/mesh-default") || strings.Contains(checklist, "MeshGlobalRateLimit-mgrl") {
		t.Errorf("apply steps must not reference the manual subtree:\n%s", checklist)
	}
	if !strings.Contains(checklist, "Delete the resources 3.0 removes") {
		t.Errorf("v3 checklist should carry the delete step:\n%s", checklist)
	}
	if strings.Index(checklist, "Delete the resources 3.0 removes") > strings.Index(checklist, "Upgrade the Global Control Plane") {
		t.Error("delete step must come before the CP upgrade")
	}
	del := md[strings.Index(md, "## Delete Before Upgrading to 3.0"):]
	for _, want := range []string{
		"kubectl delete MeshGlobalRateLimit mgrl -n kuma-system",
		"kubectl delete TrafficRoute tr1",
		"kubectl delete Timeout t1",
		"meshglobalratelimits.kuma.io",
	} {
		if !strings.Contains(del, want) {
			t.Errorf("delete section should contain %q:\n%s", want, del)
		}
	}
	if strings.Contains(md, "## Original Resources to Delete") {
		t.Error("v3: removed kinds belong in the 3.0 delete section only, not in Original Resources to Delete")
	}
	if !strings.Contains(md, "Original kept at `prod-global-ctx/"+ManualDir) {
		t.Error("Errors section should point at the manual copy")
	}
}

// TestMigrate_V2MovesErroredOriginalsToManual: under v2 too, an errored
// original goes to _manual/ instead of the apply tree. Re-applying it to the 2.x
// CP it came from would change nothing, but listing it among the apply steps
// reads as if it had been migrated. MeshGlobalRateLimit still exists on 2.x and
// stays in the apply tree, and there is no 3.0 delete section.
func TestMigrate_V2MovesErroredOriginalsToManual(t *testing.T) {
	in, out := v3Fixture(t)
	r, err := runMigration(in, out, true, "", TargetV2)
	if err != nil {
		t.Fatal(err)
	}
	fr, dc, _ := findChange(r, "TrafficRoute")
	want := "prod-global-ctx/" + ManualDir + "/mesh-default/routing/TrafficRoute-tr1.yaml"
	if dc.ManualRelPath != want || len(fr.OutputRelPaths) != 0 {
		t.Errorf("v2: TrafficRoute original should be at %q only, got manual=%q out=%v", want, dc.ManualRelPath, fr.OutputRelPaths)
	}
	if _, err := os.Stat(filepath.Join(out, want)); err != nil {
		t.Errorf("v2: manual copy not written: %v", err)
	}
	mgrl, mdc, _ := findChange(r, "MeshGlobalRateLimit")
	if mdc.ManualRelPath != "" || len(mgrl.OutputRelPaths) != 1 {
		t.Errorf("v2: MeshGlobalRateLimit is valid on 2.x and stays in the apply tree: %+v", mgrl)
	}
	var b strings.Builder
	r.Mode = "apply"
	writeMarkdown(&b, r)
	md := b.String()
	if strings.Contains(md, "Delete Before Upgrading to 3.0") {
		t.Error("v2 report must not carry the 3.0 delete section")
	}
	kept := section(t, md, "## Kept Under `"+ManualDir+"/`")
	if !strings.Contains(kept, "`"+want+"`") || !strings.Contains(kept, "not converted, original kept") {
		t.Errorf("v2: Kept Under section should list the original with its reason:\n%s", kept)
	}
	cl := section(t, md, "## Apply Checklist")
	if strings.Contains(cl, "TrafficRoute") || strings.Contains(cl, filepath.Join(out, "prod-global-ctx/mesh-default/routing")) {
		t.Errorf("v2: no apply step may list the errored original:\n%s", cl)
	}
}

func TestManualDirFor(t *testing.T) {
	cases := []struct{ cp, dir, want string }{
		{"ctx", "/o/ctx/mesh-default/routing", "/o/ctx/" + ManualDir + "/mesh-default/routing"},
		{"ctx", "/o/ctx/global-scoped-resources/other", "/o/ctx/" + ManualDir + "/global-scoped-resources/other"},
		{"", "/o/routing", "/o/" + ManualDir + "/routing"},
	}
	for _, c := range cases {
		if got := filepath.ToSlash(manualDirFor("/o", c.cp, c.dir)); got != c.want {
			t.Errorf("manualDirFor(%q, %q) = %q, want %q", c.cp, c.dir, got, c.want)
		}
	}
}

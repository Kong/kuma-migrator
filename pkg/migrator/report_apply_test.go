package migrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A MeshTimeout with both from[] and to[], as the control plane creates its
// mesh-timeout-all-* defaults: no last-applied annotation, a resourceVersion and
// a uid copied by `kubectl get -o yaml`.
const cpCreatedFromToTimeout = `apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: mesh-timeout-all-default
  namespace: kong-mesh-system
  resourceVersion: "12345"
  uid: 0d6c7f2e-1111-2222-3333-444455556666
  labels:
    kuma.io/mesh: default
spec:
  targetRef:
    kind: Mesh
  from:
  - targetRef:
      kind: Mesh
    default:
      idleTimeout: 2h
  to:
  - targetRef:
      kind: Mesh
    default:
      idleTimeout: 1h
`

const unchangedRetry = `apiVersion: kuma.io/v1alpha1
kind: MeshRetry
metadata:
  name: retry
  namespace: kong-mesh-system
  labels:
    kuma.io/mesh: default
spec:
  targetRef:
    kind: Mesh
  to:
  - targetRef:
      kind: Mesh
    default:
      tcp:
        maxConnectAttempt: 3
`

// TestChecklist_RulesSplitReplacesExistingObject: the rules[] half keeps the
// name of an object the input holds and drops to[]/from[], so `kubectl apply`
// (three-way merge, no last-applied annotation) would keep the live to[] and
// be rejected. It must be replaced; the new -outbound half is applied.
func TestChecklist_RulesSplitReplacesExistingObject(t *testing.T) {
	noUserConfig(t)
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{
		"k-zone-ctx/.kuma-migrator.json":                  `{"tool":"kubectl","context":"k","cpMode":"zone","environment":"kubernetes"}`,
		"k-zone-ctx/mesh-default/resiliency/timeout.yaml": cpCreatedFromToTimeout,
		"k-zone-ctx/mesh-default/resiliency/retry.yaml":   unchangedRetry,
		"k-zone-ctx/mesh-other/resiliency/unchanged.yaml": strings.NewReplacer("default", "other", "name: retry", "name: retry-other").Replace(unchangedRetry),
	})
	r, md := renderApply(t, in, out, TargetV2)

	fr, _, _ := findChange(r, "MeshTimeout")
	base := "k-zone-ctx/mesh-default/resiliency/"
	replaced := base + "MeshTimeout-kong-mesh-system-mesh-timeout-all-default.yaml"
	outbound := base + "MeshTimeout-kong-mesh-system-mesh-timeout-all-default-outbound.yaml"
	if len(fr.ReplaceRelPaths) != 1 || fr.ReplaceRelPaths[0] != replaced {
		t.Fatalf("ReplaceRelPaths = %v, want only %q (OutputRelPaths %v)", fr.ReplaceRelPaths, replaced, fr.OutputRelPaths)
	}

	cl := section(t, md, "## Apply Checklist")
	mustOrder(t, cl,
		"`kubectl replace` is used for files that rewrite an object",
		"kubectl replace -f "+filepath.Join(out, replaced)+" --save-config",
		"kubectl apply -f "+filepath.Join(out, outbound),
	)
	if strings.Contains(cl, "kubectl apply -f "+filepath.Join(out, base)+"/\n") {
		t.Errorf("a directory apply would also apply the replaced file:\n%s", cl)
	}
	if !strings.Contains(cl, "kubectl apply -f "+filepath.Join(out, base, "MeshRetry-kong-mesh-system-retry.yaml")) {
		t.Errorf("the unchanged MeshRetry next to it is listed with kubectl apply:\n%s", cl)
	}
	// A directory with only unchanged pass-through keeps the directory apply.
	if !strings.Contains(cl, "kubectl apply -f "+filepath.Join(out, "k-zone-ctx/mesh-other/resiliency")+"/\n") {
		t.Errorf("unchanged pass-through needs no replace:\n%s", cl)
	}

	// extract copies resourceVersion/uid; both are write preconditions.
	b, err := os.ReadFile(filepath.Join(out, replaced))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "resourceVersion") || strings.Contains(string(b), "uid:") {
		t.Errorf("resourceVersion/uid must be stripped:\n%s", b)
	}
}

// TestChecklist_KumactlNeverReplaces: kumactl apply already replaces the whole
// spec of an existing resource (upsert: Get, SetSpec, Update).
func TestChecklist_KumactlNeverReplaces(t *testing.T) {
	noUserConfig(t)
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{
		"u-zone-ctx/.kuma-migrator.json": `{"tool":"kumactl","context":"u","cpMode":"zone","environment":"universal"}`,
		"u-zone-ctx/mesh-default/resiliency/t.yaml": "type: MeshTimeout\nname: t\nmesh: default\nspec:\n  targetRef:\n    kind: Mesh\n" +
			"  from:\n  - targetRef:\n      kind: Mesh\n    default:\n      idleTimeout: 2h\n" +
			"  to:\n  - targetRef:\n      kind: Mesh\n    default:\n      idleTimeout: 1h\n",
	})
	r, md := renderApply(t, in, out, TargetV2)
	fr, _, _ := findChange(r, "MeshTimeout")
	if len(fr.ReplaceRelPaths) != 1 {
		t.Errorf("the Universal rewrite is still detected: %v", fr.ReplaceRelPaths)
	}
	cl := section(t, md, "## Apply Checklist")
	if strings.Contains(cl, "kubectl replace") || strings.Contains(cl, "`kubectl replace` is used") {
		t.Errorf("kumactl contexts never use kubectl replace:\n%s", cl)
	}
	mustOrder(t, cl, "kumactl config control-planes switch --name u", "kumactl apply -f "+filepath.Join(out, "u-zone-ctx/mesh-default/resiliency/MeshTimeout-t.yaml"))
}

func TestResourceKey(t *testing.T) {
	k8s := func(ns string) []byte {
		return []byte("apiVersion: kuma.io/v1alpha1\nkind: MeshTimeout\nmetadata:\n  name: t\n  namespace: " + ns + "\n")
	}
	uni := func(mesh string) []byte { return []byte("type: MeshTimeout\nname: t\nmesh: " + mesh + "\n") }
	if resourceKey("c", k8s("a")) == resourceKey("c", k8s("b")) {
		t.Error("namespace is part of the identity")
	}
	if resourceKey("c", k8s("a")) == resourceKey("d", k8s("a")) {
		t.Error("the context is part of the identity")
	}
	if resourceKey("c", uni("m1")) == resourceKey("c", uni("m2")) {
		t.Error("Universal: the mesh is part of the identity")
	}
	if resourceKey("c", []byte("kind: MeshTimeout\n")) != "" {
		t.Error("no name, no identity")
	}
}

func TestStripServerSetIdentity(t *testing.T) {
	plain := []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n")
	if got := stripServerSetIdentity(plain); string(got) != string(plain) {
		t.Errorf("a document without resourceVersion/uid must be unchanged, got %q", got)
	}
	got := string(stripServerSetIdentity([]byte(cpCreatedFromToTimeout)))
	if strings.Contains(got, "resourceVersion") || strings.Contains(got, "uid:") || !strings.Contains(got, "kong-mesh-system") {
		t.Errorf("stripped:\n%s", got)
	}
	uni := []byte("type: Mesh\nname: default\n")
	if got := stripServerSetIdentity(uni); string(got) != string(uni) {
		t.Errorf("Universal documents have no metadata: %q", got)
	}
}

// TestV3DeleteCommand_KumactlTypeNames pins the `kumactl delete` type names
// that are not kebab-case (KumactlArg in kuma v2.14.5).
func TestV3DeleteCommand_KumactlTypeNames(t *testing.T) {
	cases := map[string]string{
		"HealthCheck": "kumactl delete healthcheck hc --mesh default",
		"ZoneEgress":  "kumactl delete zoneegress ze",
		"Timeout":     "kumactl delete timeout hc --mesh default",
	}
	for kind, want := range cases {
		name := "hc"
		if kind == "ZoneEgress" {
			name = "ze"
		}
		if got := v3DeleteCommand(v3Deletion{Kind: kind, Name: name, Mesh: "default"}, "kumactl"); got != want {
			t.Errorf("%s: got %q, want %q", kind, got, want)
		}
	}
}

// ---- documents the target rejects: shown with the reason ------------------

// TestReport_RejectedByTargetListedWithReason feeds the rejection cases the
// conversion code reports (via a fake transform) and checks the report lists
// each under "Kept Under _manual/" and "Action Items → Errors" with its reason,
// and that no apply step names it.
func TestReport_RejectedByTargetListedWithReason(t *testing.T) {
	noUserConfig(t)
	cases := []struct {
		file, kind, name, reason, doc string
	}{
		{"other/dp.yaml", "Dataplane", "backend-1", "networking.outbound[].tags is removed in 3.0; set backendRef",
			"type: Dataplane\nname: backend-1\nmesh: default\nnetworking:\n  address: 10.0.0.1\n  outbound:\n  - port: 33033\n    tags:\n      kuma.io/service: db\n"},
		{"routing/ms.yaml", "MeshService", "legacy-db", "spec.selector.dataplaneTags is an unknown field in 3.0",
			"type: MeshService\nname: legacy-db\nmesh: default\nspec:\n  selector:\n    dataplaneTags:\n      app: db\n"},
		{"routing/mes.yaml", "MeshExternalService", "example-api", "tls.verification.caCert.inline must become a SecureDataSource",
			"type: MeshExternalService\nname: example-api\nmesh: default\nspec:\n  tls:\n    verification:\n      caCert:\n        inline: Zm9v\n"},
		{"routing/gw.yaml", "MeshGateway", "edge-gateway", "Universal has no Gateway API and 3.0 removes MeshGateway",
			"type: MeshGateway\nname: edge-gateway\nmesh: default\nselectors:\n- match:\n    kuma.io/service: edge\nconf:\n  listeners:\n  - port: 8080\n    protocol: HTTP\n"},
	}
	byKind := map[string]string{}
	for _, c := range cases {
		byKind[c.kind] = c.reason
	}

	orig := transformDocument
	t.Cleanup(func() { transformDocument = orig })
	transformDocument = func(raw []byte, opts TransformOptions) ([][]byte, []string, Scenario, error) {
		kind, name := probeKindName(raw)
		return [][]byte{raw}, nil, ScenarioPassthrough, &RejectedByTargetError{
			Target:   opts.Target,
			Rejected: []RejectedDoc{{Index: 0, Kind: kind, Name: name, Reasons: []string{byKind[kind]}}},
		}
	}

	in, out := t.TempDir(), t.TempDir()
	files := map[string]string{
		"u-zone-ctx/.kuma-migrator.json": `{"tool":"kumactl","context":"u","cpMode":"zone","environment":"universal"}`,
	}
	for _, c := range cases {
		files["u-zone-ctx/mesh-default/"+c.file] = c.doc
	}
	writeTree(t, in, files)
	r, md := renderApply(t, in, out, TargetV3)
	if r.ErrorCount != len(cases) {
		t.Errorf("ErrorCount = %d, want %d", r.ErrorCount, len(cases))
	}

	kept := section(t, md, "## Kept Under `"+ManualDir+"/`")
	errs := section(t, md, "### Errors\n\nThese files")
	cl := section(t, md, "## Apply Checklist")
	for _, c := range cases {
		_, dc, ok := findChange(r, c.kind)
		if !ok || !dc.Rejected || dc.ManualRelPath == "" {
			t.Errorf("%s: want Rejected with a manual path, got %+v", c.kind, dc)
			continue
		}
		if _, err := os.Stat(filepath.Join(out, dc.ManualRelPath)); err != nil {
			t.Errorf("%s: manual file not written: %v", c.kind, err)
		}
		row := ""
		for _, l := range strings.Split(kept, "\n") {
			if strings.Contains(l, "`"+dc.ManualRelPath+"`") {
				row = l
			}
		}
		if row == "" || !strings.Contains(row, "`"+c.kind+"`") || !strings.Contains(row, "`"+c.name+"`") ||
			!strings.Contains(row, "rejected by the v3") || !strings.Contains(row, c.reason) {
			t.Errorf("%s: Kept Under row missing or incomplete: %q\n%s", c.kind, row, kept)
		}
		if !strings.Contains(errs, c.reason) || !strings.Contains(errs, "Converted output kept at `"+dc.ManualRelPath+"`") {
			t.Errorf("%s: Errors section should carry the reason and the path:\n%s", c.kind, errs)
		}
		if strings.Contains(cl, dc.ManualRelPath) || strings.Contains(cl, "-"+c.name+".yaml") {
			t.Errorf("%s: no apply step may name it:\n%s", c.kind, cl)
		}
	}
	if !strings.Contains(cl, "under `<context>/"+ManualDir+"/`") {
		t.Errorf("the Fix errors step should point at %s/:\n%s", ManualDir, cl)
	}
}

func TestMdCell(t *testing.T) {
	if got := mdCell("a | b\nc"); got != `a \| b c` {
		t.Errorf("mdCell = %q", got)
	}
}

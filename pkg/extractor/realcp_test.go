package extractor

// Regression tests for the extract bugs found running against real Kong Mesh
// 2.14.7 control planes (test/e2e/results/extract-*.md).

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func readYAML(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var obj map[string]interface{}
	if err := yaml.Unmarshal(data, &obj); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return obj
}

// ---- unlabelled resources on a federated zone -------------------------------

// The 2.14 defaulting webhook does not cover the legacy kinds, so a
// TrafficPermission created on a zone carries no kuma.io/origin label. It was
// created there (KDS labels every copy it syncs) and must be extracted.
func TestZoneOriginDecision(t *testing.T) {
	cases := []struct {
		origin           string
		keep, unlabelled bool
	}{
		{"zone", true, false},
		{"", true, true},
		{"global", false, false},
	}
	for _, c := range cases {
		keep, unlabelled := zoneOriginDecision(c.origin)
		if keep != c.keep || unlabelled != c.unlabelled {
			t.Errorf("origin %q: got keep=%v unlabelled=%v", c.origin, keep, unlabelled)
		}
	}
}

func TestWriteResourceFiles_FederatedZone_KeepsUnlabelledLegacyKinds(t *testing.T) {
	notes := &extractNotes{}
	data := []byte(`apiVersion: kuma.io/v1alpha1
kind: TrafficPermission
mesh: default
metadata:
  name: allow-all
spec: {}
---
apiVersion: kuma.io/v1alpha1
kind: ExternalService
mesh: default
metadata:
  name: httpbin
spec: {}
`)
	n, err := writeResourceFiles(data, t.TempDir(), nil, CPModeZone, "ctx-zone-ctx", "", "", "kubernetes", notes)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || notes.KeptUnlabelled["TrafficPermission"] != 1 || notes.KeptUnlabelled["ExternalService"] != 1 {
		t.Errorf("expected both kept and counted, got n=%d notes=%+v", n, notes)
	}
}

// ---- CP-generated MeshService -----------------------------------------------

func TestWriteResourceFiles_GeneratedMeshServiceSkippedHandWrittenKept(t *testing.T) {
	notes := &extractNotes{}
	dir := t.TempDir()
	// Both carry kuma.io/env: kubernetes (labels.Compute sets it on every
	// MeshService created on a zone); only the generated one has managed-by.
	data := []byte(`apiVersion: kuma.io/v1alpha1
kind: MeshService
metadata:
  name: backend
  namespace: kuma-demo
  labels:
    kuma.io/env: kubernetes
    kuma.io/managed-by: k8s-controller
    kuma.io/mesh: default
    kuma.io/origin: zone
spec: {}
---
apiVersion: kuma.io/v1alpha1
kind: MeshService
metadata:
  name: legacy-db
  namespace: kuma-demo
  labels:
    kuma.io/env: kubernetes
    kuma.io/mesh: default
    kuma.io/origin: zone
spec: {}
---
type: MeshService
name: web
mesh: default
labels:
  kuma.io/managed-by: meshservice-generator
spec: {}
`)
	n, err := writeResourceFiles(data, dir, nil, CPModeZone, "ctx-zone-ctx", "", "", "universal", notes)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || notes.Generated["MeshService"] != 2 {
		t.Fatalf("expected only the hand-written MeshService written, 2 generated counted; n=%d notes=%+v", n, notes)
	}
	if _, err := os.Stat(filepath.Join(dir, "ctx-zone-ctx", "mesh-default", "routing", "MeshService-kuma-demo-legacy-db.yaml")); err != nil {
		t.Errorf("hand-written MeshService not written: %v", err)
	}
}

// ---- server-side metadata ---------------------------------------------------

func TestWriteResourceFiles_StripsServerSideFields(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: t
  namespace: kuma-demo
  resourceVersion: "12345"
  uid: 0f6c1d2e-aaaa-bbbb-cccc-000000000000
  generation: 3
  creationTimestamp: "2026-10-01T00:00:00Z"
  selfLink: /apis/kuma.io/v1alpha1/namespaces/kuma-demo/meshtimeouts/t
  managedFields:
  - manager: kubectl
  ownerReferences:
  - apiVersion: kuma.io/v1alpha1
    kind: Mesh
    name: default
    uid: 11111111-2222-3333-4444-555555555555
  labels:
    kuma.io/mesh: default
    team: payments
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: '{"kind":"MeshTimeout"}'
    owner: team-a
spec:
  targetRef:
    kind: Mesh
status:
  foo: bar
`)
	if _, err := writeResourceFiles(data, dir, nil, "", "ctx", "", "", "kubernetes", nil); err != nil {
		t.Fatal(err)
	}
	obj := readYAML(t, filepath.Join(dir, "ctx", "mesh-default", "resiliency", "MeshTimeout-kuma-demo-t.yaml"))
	if _, ok := obj["status"]; ok {
		t.Error("status kept")
	}
	meta := obj["metadata"].(map[string]interface{})
	for _, f := range serverSideMetadata {
		if _, ok := meta[f]; ok {
			t.Errorf("metadata.%s kept", f)
		}
	}
	ann := meta["annotations"].(map[string]interface{})
	if _, ok := ann[lastAppliedAnnotation]; ok {
		t.Error("last-applied-configuration annotation kept")
	}
	if ann["owner"] != "team-a" {
		t.Errorf("user annotation lost: %v", ann)
	}
	if labels := meta["labels"].(map[string]interface{}); labels["team"] != "payments" || labels["kuma.io/mesh"] != "default" {
		t.Errorf("labels changed: %v", labels)
	}
	if meta["name"] != "t" || meta["namespace"] != "kuma-demo" {
		t.Errorf("identity changed: %v", meta)
	}
}

func TestWriteResourceFiles_StripsUniversalServerFields(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`type: MeshService
name: db
mesh: default
creationTime: "2026-10-01T00:00:00Z"
modificationTime: "2026-10-01T00:00:00Z"
kri: kri_msvc_default___db_
spec: {}
status:
  vips: []
`)
	if _, err := writeResourceFiles(data, dir, nil, "", "ctx", "default", "", "universal", nil); err != nil {
		t.Fatal(err)
	}
	obj := readYAML(t, filepath.Join(dir, "ctx", "mesh-default", "routing", "MeshService-db.yaml"))
	for _, f := range []string{"creationTime", "modificationTime", "kri", "status"} {
		if _, ok := obj[f]; ok {
			t.Errorf("%s kept", f)
		}
	}
	if obj["type"] != "MeshService" || obj["name"] != "db" || obj["mesh"] != "default" {
		t.Errorf("identity changed: %v", obj)
	}
}

// ---- layout -------------------------------------------------------------------

func TestWriteResourceFiles_MeshGoesUnderItsOwnMeshDir(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: payments
spec: {}
---
type: Mesh
name: default
`)
	n, err := writeResourceFiles(data, dir, nil, "", "ctx", "", "", "", nil)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for _, p := range []string{"mesh-payments/mesh/Mesh-payments.yaml", "mesh-default/mesh/Mesh-default.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, "ctx", p)); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

func TestWriteResourceFiles_MeshFilterAppliesToMesh(t *testing.T) {
	dir := t.TempDir()
	data := []byte("apiVersion: kuma.io/v1alpha1\nkind: Mesh\nmetadata:\n  name: payments\nspec: {}\n")
	if n, _ := writeResourceFiles(data, dir, nil, "", "ctx", "", "default", "", nil); n != 0 {
		t.Errorf("Mesh payments must be filtered out by --mesh default, got %d", n)
	}
}

// Legacy CRDs (MeshGatewayRoute, TrafficPermission, …) are cluster-scoped and
// store the mesh in a top-level field, with no kuma.io/mesh label.
func TestWriteResourceFiles_LegacyTopLevelMeshField(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`apiVersion: kuma.io/v1alpha1
kind: MeshGatewayRoute
mesh: payments
metadata:
  name: edge-http
spec: {}
`)
	if _, err := writeResourceFiles(data, dir, nil, "", "ctx", "", "", "kubernetes", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ctx", "mesh-payments", "routing", "MeshGatewayRoute-edge-http.yaml")); err != nil {
		t.Errorf("MeshGatewayRoute not under mesh-payments: %v", err)
	}
}

// ---- kubectl path end to end ------------------------------------------------

func TestDumpCRDInstances_UsesListObjectsAndStrips(t *testing.T) {
	orig := runKubectl
	t.Cleanup(func() { runKubectl = orig })
	runKubectl = func(_ string, args ...string) ([]byte, error) {
		if strings.Join(args, " ") != "get meshgatewayroutes -A -o json" {
			return nil, fmt.Errorf("unexpected kubectl call: %v", args)
		}
		return []byte(`{"items":[{"apiVersion":"kuma.io/v1alpha1","kind":"MeshGatewayRoute","mesh":"default",
			"metadata":{"name":"edge","resourceVersion":"9","uid":"u","ownerReferences":[{"kind":"Mesh","name":"default","uid":"x"}]},
			"spec":{"conf":{}}}]}`), nil
	}
	dir := t.TempDir()
	notes := &extractNotes{}
	n, err := dumpCRDInstances("k", crdEntry{Kind: "MeshGatewayRoute", Plural: "meshgatewayroutes"}, dir, CPModeZone, "k-zone-ctx", "", notes)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	obj := readYAML(t, filepath.Join(dir, "k-zone-ctx", "mesh-default", "routing", "MeshGatewayRoute-edge.yaml"))
	meta := obj["metadata"].(map[string]interface{})
	if _, ok := meta["resourceVersion"]; ok {
		t.Error("resourceVersion kept")
	}
	if _, ok := meta["ownerReferences"]; ok {
		t.Error("ownerReferences kept")
	}
	if obj["mesh"] != "default" {
		t.Errorf("top-level mesh field lost: %v", obj)
	}
	if notes.KeptUnlabelled["MeshGatewayRoute"] != 1 {
		t.Errorf("unlabelled resource not counted: %+v", notes)
	}
}

// ---- kumactl subcommand mapping ---------------------------------------------

func TestKumactlListArg(t *testing.T) {
	if got := kumactlListArg(resourceTypeEntry{Name: "HealthCheck", Path: "health-checks"}); got != "healthchecks" {
		t.Errorf("HealthCheck: got %q", got)
	}
	if got := kumactlListArg(resourceTypeEntry{Name: "TrafficPermission", Path: "traffic-permissions"}); got != "traffic-permissions" {
		t.Errorf("TrafficPermission: got %q", got)
	}
}

func fakeKumactl(t *testing.T, fn func(args []string) ([]byte, error)) *[][]string {
	t.Helper()
	var calls [][]string
	orig := execKumactl
	t.Cleanup(func() { execKumactl = orig })
	execKumactl = func(args ...string) ([]byte, error) {
		calls = append(calls, args)
		return fn(args)
	}
	return &calls
}

func TestDumpKumactlResources_HealthCheckUsesKumactlListArg(t *testing.T) {
	t.Setenv("KUMACTL_CONFIG", "")
	calls := fakeKumactl(t, func(args []string) ([]byte, error) {
		if args[3] != "healthchecks" {
			return nil, fmt.Errorf("Error: unknown flag: --mesh")
		}
		return []byte("type: HealthCheck\nname: hc-web\nmesh: default\nconf: {}\n"), nil
	})
	dir := t.TempDir()
	rt := resourceTypeEntry{Name: "HealthCheck", Path: "health-checks", Scope: "Mesh"}
	n, err := dumpKumactlResources("ctx", "http://cp.invalid:5681", "", rt, "default", dir, nil, "", "ctx-zone-ctx", "", "universal", nil)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v calls=%v", n, err, *calls)
	}
	want := []string{"--context", "ctx", "get", "healthchecks", "-o", "yaml", "--mesh", "default"}
	if strings.Join((*calls)[0], " ") != strings.Join(want, " ") {
		t.Errorf("kumactl args = %v, want %v", (*calls)[0], want)
	}
}

// An unknown `get` subcommand is not a scope problem: the type is read from
// the API instead of being silently dropped.
func TestDumpKumactlResources_UnknownSubcommandFallsBackToAPI(t *testing.T) {
	t.Setenv("KUMACTL_CONFIG", "")
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.String())
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("offset") == "" {
			fmt.Fprintf(w, `{"total":2,"items":[{"type":"FutureKind","name":"a","mesh":"default"}],"next":"http://%s%s?offset=1"}`, r.Host, r.URL.Path)
			return
		}
		fmt.Fprint(w, `{"total":2,"items":[{"type":"FutureKind","name":"b","mesh":"default"}],"next":null}`)
	}))
	defer srv.Close()
	fakeKumactl(t, func([]string) ([]byte, error) { return nil, fmt.Errorf("Error: unknown flag: --mesh") })

	dir := t.TempDir()
	rt := resourceTypeEntry{Name: "FutureKind", Path: "future-kinds", Scope: "Mesh"}
	n, err := dumpKumactlResources("ctx", srv.URL, "tok", rt, "default", dir, nil, "", "ctx-zone-ctx", "", "universal", nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("expected both pages written, got %d (requests %v)", n, paths)
	}
	if len(paths) == 0 || paths[0] != "/meshes/default/future-kinds" {
		t.Errorf("unexpected API requests: %v", paths)
	}
}

func TestDumpKumactlResources_UsageOutputIsNotEmptySuccess(t *testing.T) {
	t.Setenv("KUMACTL_CONFIG", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"total":1,"items":[{"type":"FutureGlobal","name":"x"}],"next":null}`)
	}))
	defer srv.Close()
	fakeKumactl(t, func([]string) ([]byte, error) {
		return []byte("Show resources\n\nUsage:\n  kumactl get [command]\n\nAvailable Commands:\n  meshes\n"), nil
	})
	rt := resourceTypeEntry{Name: "FutureGlobal", Path: "future-globals", Scope: "Global"}
	n, err := dumpKumactlResources("ctx", srv.URL, "", rt, "", t.TempDir(), nil, "", "ctx", "", "universal", nil)
	if err != nil || n != 1 {
		t.Errorf("usage text must trigger the API fallback: n=%d err=%v", n, err)
	}
}

func TestFetchResourcesHTTP_RefusesForeignNextLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"items":[],"next":"http://169.254.169.254/latest"}`)
	}))
	defer srv.Close()
	if _, err := fetchResourcesHTTP(srv.URL, "tok", "meshes", ""); err == nil {
		t.Error("expected a next link to another host to be refused")
	}
}

// ---- KUMACTL_CONFIG ---------------------------------------------------------

func TestKumactlArgs_PassesConfigFile(t *testing.T) {
	t.Setenv("KUMACTL_CONFIG", "/tmp/e2e/kumactl.config")
	got := strings.Join(kumactlArgs("ctx", "get", "meshes"), " ")
	if got != "--config-file /tmp/e2e/kumactl.config --context ctx get meshes" {
		t.Errorf("got %q", got)
	}
	t.Setenv("KUMACTL_CONFIG", "")
	if got := strings.Join(kumactlArgs("ctx", "get", "meshes"), " "); got != "--context ctx get meshes" {
		t.Errorf("without KUMACTL_CONFIG got %q", got)
	}
}

func TestListMeshNames_PassesConfigFile(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "kumactl.config")
	t.Setenv("KUMACTL_CONFIG", cfg)
	calls := fakeKumactl(t, func([]string) ([]byte, error) { return []byte("type: Mesh\nname: default\n"), nil })
	if _, err := listMeshNames("ctx", "http://cp:5681", ""); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0][0] != "--config-file" || (*calls)[0][1] != cfg {
		t.Errorf("kumactl not given --config-file: %v", *calls)
	}
}

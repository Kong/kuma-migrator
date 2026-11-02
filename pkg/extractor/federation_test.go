package extractor

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- federation detection: kumactl path (GET /config) -----------------------

func configServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/config" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDetectKumactlCPInfo_FederatedZone(t *testing.T) {
	srv := configServer(t, `{"mode":"zone","environment":"kubernetes","store":{"type":"kubernetes"},
		"multizone":{"zone":{"name":"zone-1","globalAddress":"grpcs://global:5685"}}}`)
	info := detectKumactlCPInfo(srv.URL, "")
	if info.Mode != CPModeZone || info.ZoneName != "zone-1" || !info.Federated {
		t.Fatalf("expected federated zone-1, got %+v", info)
	}
	if info.StoreType != "kubernetes" {
		t.Errorf("expected store type kubernetes, got %q", info.StoreType)
	}
	if originFilterMode(info) != CPModeZone {
		t.Errorf("federated zone must keep the zone origin filter, got %q", originFilterMode(info))
	}
}

// globalAddress is omitempty upstream, so a non-federated zone has no key at all.
func TestDetectKumactlCPInfo_NonFederatedZone(t *testing.T) {
	srv := configServer(t, `{"mode":"zone","environment":"universal","store":{"type":"postgres"},
		"multizone":{"zone":{"name":"zone-1"}}}`)
	info := detectKumactlCPInfo(srv.URL, "")
	if info.Mode != CPModeZone || info.Federated {
		t.Fatalf("expected non-federated zone, got %+v", info)
	}
	if !info.IsNonFederatedZone() {
		t.Error("IsNonFederatedZone should be true")
	}
	if got := originFilterMode(info); got != "" {
		t.Errorf("non-federated zone must get no origin filter, got %q", got)
	}
}

// Upstream IsFederatedZoneCP also requires a zone name.
func TestDetectKumactlCPInfo_GlobalAddressWithoutZoneNameIsNotFederated(t *testing.T) {
	srv := configServer(t, `{"mode":"zone","multizone":{"zone":{"globalAddress":"grpcs://g:5685"}}}`)
	if info := detectKumactlCPInfo(srv.URL, ""); info.Federated {
		t.Errorf("zone without a name must not count as federated: %+v", info)
	}
}

func TestDetectKumactlCPInfo_GlobalIsNeverFederated(t *testing.T) {
	srv := configServer(t, `{"mode":"global","environment":"kubernetes","store":{"type":"kubernetes"}}`)
	info := detectKumactlCPInfo(srv.URL, "")
	if info.Mode != CPModeGlobal || info.Federated || info.IsNonFederatedZone() {
		t.Errorf("unexpected info for global: %+v", info)
	}
	if originFilterMode(info) != CPModeGlobal {
		t.Errorf("global keeps its filter, got %q", originFilterMode(info))
	}
}

// ---- federation detection: kubectl path (CP Deployment env) -----------------

func deploymentJSON(env map[string]string) []byte {
	var parts []string
	for k, v := range env {
		parts = append(parts, fmt.Sprintf(`{"name":%q,"value":%q}`, k, v))
	}
	return []byte(`{"spec":{"template":{"spec":{"containers":[{"env":[` + strings.Join(parts, ",") + `]}]}}}}`)
}

func TestKubeCPInfoFromDeployment_FederatedZone(t *testing.T) {
	info := kubeCPInfoFromDeploymentJSON(deploymentJSON(map[string]string{
		"KUMA_MODE":                          "zone",
		"KUMA_MULTIZONE_ZONE_NAME":           "east",
		"KUMA_MULTIZONE_ZONE_GLOBAL_ADDRESS": "grpcs://global:5685",
		"KUMA_STORE_TYPE":                    "kubernetes",
		"KUMA_ENVIRONMENT":                   "kubernetes",
	}))
	if !info.Federated || info.ZoneName != "east" || info.StoreType != "kubernetes" {
		t.Errorf("expected federated zone east on kubernetes store, got %+v", info)
	}
}

// The Helm chart renders KUMA_MULTIZONE_ZONE_GLOBAL_ADDRESS in zone mode even
// when controlPlane.kdsGlobalAddress is unset, with an empty value.
func TestKubeCPInfoFromDeployment_EmptyGlobalAddressIsNotFederated(t *testing.T) {
	info := kubeCPInfoFromDeploymentJSON(deploymentJSON(map[string]string{
		"KUMA_MODE":                          "zone",
		"KUMA_MULTIZONE_ZONE_NAME":           "east",
		"KUMA_MULTIZONE_ZONE_GLOBAL_ADDRESS": "",
	}))
	if info.Federated || !info.IsNonFederatedZone() {
		t.Errorf("empty global address must mean non-federated, got %+v", info)
	}
}

func TestKubeCPInfoFromDeployment_Standalone(t *testing.T) {
	info := kubeCPInfoFromDeploymentJSON(deploymentJSON(map[string]string{"KUMA_MODE": "Standalone"}))
	if info.Mode != CPModeStandalone || info.Federated {
		t.Errorf("expected standalone, got %+v", info)
	}
	if originFilterMode(info) != CPModeStandalone {
		t.Errorf("standalone passes through (no filter applies to it), got %q", originFilterMode(info))
	}
}

// ---- zone origin filter: skipped unlabelled resources are reported ----------

const noLabelTimeout = `apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: legacy-timeout
spec: {}
`

const globalOriginTimeout = `apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: synced-timeout
  labels:
    kuma.io/origin: global
spec: {}
`

func TestWriteResourceFiles_FederatedZone_KeepsUnlabelledSkipsGlobalCopies(t *testing.T) {
	notes := &extractNotes{}
	data := []byte(noLabelTimeout + "---\n" + globalOriginTimeout)
	n, err := writeResourceFiles(data, t.TempDir(), map[string]bool{}, CPModeZone, "ctx-zone-ctx", "default", "", "kubernetes", notes)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("expected the unlabelled resource written and the global copy skipped, got %d", n)
	}
	if notes.KeptUnlabelled["MeshTimeout"] != 1 || notes.GlobalCopies != 1 || len(notes.ZoneOriginSkips) != 0 {
		t.Errorf("unexpected notes: %+v", notes)
	}
}

func TestWriteResourceFiles_FederatedZone_MeshFilterNotCounted(t *testing.T) {
	notes := &extractNotes{}
	_, _ = writeResourceFiles([]byte(globalOriginTimeout), t.TempDir(), map[string]bool{}, CPModeZone, "", "other", "default", "kubernetes", notes)
	if notes.GlobalCopies != 0 {
		t.Errorf("a resource excluded by --mesh must not be counted: %+v", notes)
	}
}

// A non-federated zone is extracted with filter mode "" (originFilterMode):
// unlabelled resources and leftovers from an earlier federation are all kept.
func TestWriteResourceFiles_NonFederatedZone_KeepsEverything(t *testing.T) {
	notes := &extractNotes{}
	info := CPInfo{Mode: CPModeZone, ZoneName: "z"}
	data := []byte(noLabelTimeout + "---\n" + globalOriginTimeout)
	n, err := writeResourceFiles(data, t.TempDir(), map[string]bool{}, originFilterMode(info), "ctx-zone-ctx", "default", "", "kubernetes", notes)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || notes.GlobalCopies != 0 || len(notes.KeptUnlabelled) != 0 {
		t.Errorf("expected both resources written and nothing counted, got n=%d notes=%+v", n, notes)
	}
}

func TestPrintExtractNotes_Smoke(t *testing.T) {
	printExtractNotes(nil)
	printExtractNotes(&extractNotes{
		ZoneOriginSkips: []ZoneOriginSkip{{Kind: "MeshTimeout", Name: "a", ZoneName: "z1"}},
		GlobalCopies:    2,
		KeptUnlabelled:  map[string]int{"TrafficPermission": 3, "ExternalService": 1},
		Generated:       map[string]int{"MeshService": 4},
	})
	if got := formatCounts(map[string]int{"b": 1, "a": 2}); got != "a 2, b 1" {
		t.Errorf("formatCounts = %q", got)
	}
}

// ---- kubectl path end to end with a fake kubectl ----------------------------

// fakeKubectl answers the calls ExtractViaKubectl makes for a zone CP with one
// MeshTimeout CRD holding an unlabelled, a zone-origin and a global-origin instance.
func fakeKubectl(t *testing.T, deploymentEnv map[string]string) {
	t.Helper()
	orig := runKubectl
	t.Cleanup(func() { runKubectl = orig })
	runKubectl = func(_ string, args ...string) ([]byte, error) {
		cmd := strings.Join(args, " ")
		switch {
		case cmd == "get deployment kuma-control-plane -n kuma-system -o json":
			return deploymentJSON(deploymentEnv), nil
		case strings.HasPrefix(cmd, "get deployment"):
			return nil, fmt.Errorf("not found")
		case cmd == "get crd -o json":
			return []byte(`{"items":[{"spec":{"group":"kuma.io","names":{"kind":"MeshTimeout","plural":"meshtimeouts"},"versions":[{"name":"v1alpha1"}]}}]}`), nil
		case cmd == "get meshtimeouts -A -o json":
			return []byte(`{"items":[
				{"apiVersion":"kuma.io/v1alpha1","kind":"MeshTimeout","metadata":{"namespace":"kuma-system","name":"old","labels":{"kuma.io/mesh":"default"}},"spec":{}},
				{"apiVersion":"kuma.io/v1alpha1","kind":"MeshTimeout","metadata":{"namespace":"kuma-system","name":"new","labels":{"kuma.io/mesh":"default","kuma.io/origin":"zone"}},"spec":{}},
				{"apiVersion":"kuma.io/v1alpha1","kind":"MeshTimeout","metadata":{"namespace":"kuma-system","name":"synced","labels":{"kuma.io/mesh":"default","kuma.io/origin":"global"}},"spec":{}}]}`), nil
		}
		return nil, fmt.Errorf("unexpected kubectl call: %s", cmd)
	}
	t.Setenv("KUMA_MIGRATOR_CONFIG", filepath.Join(t.TempDir(), "absent.yaml"))
}

func extractedTimeouts(t *testing.T, dir string) []string {
	t.Helper()
	var names []string
	_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && strings.HasPrefix(fi.Name(), "MeshTimeout-") {
			names = append(names, fi.Name())
		}
		return nil
	})
	return names
}

func TestExtractViaKubectl_NonFederatedZoneExtractsUnlabelled(t *testing.T) {
	fakeKubectl(t, map[string]string{
		"KUMA_MODE": "zone", "KUMA_MULTIZONE_ZONE_NAME": "east", "KUMA_MULTIZONE_ZONE_GLOBAL_ADDRESS": "",
		"KUMA_STORE_TYPE": "kubernetes",
	})
	out := t.TempDir()
	if err := ExtractViaKubectl("kctx", out, "", "kubernetes"); err != nil {
		t.Fatal(err)
	}
	if got := extractedTimeouts(t, out); len(got) != 3 {
		t.Errorf("non-federated zone: expected every MeshTimeout extracted, got %v", got)
	}
	meta := ReadContextMeta(out, "kctx-zone-ctx")
	if meta == nil || meta.Federated == nil || *meta.Federated {
		t.Fatalf("expected federated=false in metadata, got %+v", meta)
	}
	if meta.StoreType != "kubernetes" || meta.Environment != CPEnvKubernetes {
		t.Errorf("unexpected store/env in metadata: %+v", meta)
	}
}

func TestExtractViaKubectl_FederatedZoneKeepsUnlabelled(t *testing.T) {
	fakeKubectl(t, map[string]string{
		"KUMA_MODE": "zone", "KUMA_MULTIZONE_ZONE_NAME": "east",
		"KUMA_MULTIZONE_ZONE_GLOBAL_ADDRESS": "grpcs://global:5685",
	})
	out := t.TempDir()
	if err := ExtractViaKubectl("kctx", out, "", "kubernetes"); err != nil {
		t.Fatal(err)
	}
	got := extractedTimeouts(t, out)
	if len(got) != 2 || strings.Contains(strings.Join(got, ","), "-synced") {
		t.Errorf("federated zone: expected the zone-origin and unlabelled MeshTimeouts only, got %v", got)
	}
	meta := ReadContextMeta(out, "kctx-zone-ctx")
	if meta == nil || meta.Federated == nil || !*meta.Federated {
		t.Fatalf("expected federated=true in metadata, got %+v", meta)
	}
}

func TestDumpCRDInstances_FederatedZoneCountsUnlabelled(t *testing.T) {
	fakeKubectl(t, nil)
	notes := &extractNotes{}
	n, err := dumpCRDInstances("kctx", crdEntry{Kind: "MeshTimeout", Plural: "meshtimeouts"}, t.TempDir(), CPModeZone, "kctx-zone-ctx", "", notes)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || notes.KeptUnlabelled["MeshTimeout"] != 1 || notes.GlobalCopies != 1 {
		t.Errorf("expected 2 written, 1 unlabelled kept, 1 global copy skipped; got n=%d notes=%+v", n, notes)
	}
}

// ---- ContextMeta new fields ------------------------------------------------

func TestContextMeta_FederationAndStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	meta := contextMetaFor("kumactl", "cp", CPEnvUniversal, CPInfo{Mode: CPModeZone, StoreType: "postgres"}, false)
	if err := WriteContextMetaFile(dir, "cp-zone-ctx", meta); err != nil {
		t.Fatal(err)
	}
	got := ReadContextMeta(dir, "cp-zone-ctx")
	if got == nil || got.Federated == nil || *got.Federated || got.StoreType != "postgres" {
		t.Errorf("round trip lost fields: %+v", got)
	}

	global := contextMetaFor("kubectl", "g", CPEnvKubernetes, CPInfo{Mode: CPModeGlobal}, false)
	if global.Federated != nil {
		t.Errorf("federated must be unset outside zone mode: %+v", global)
	}
}

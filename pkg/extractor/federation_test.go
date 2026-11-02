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

func TestWriteResourceFiles_FederatedZone_ReportsNoLabelSkips(t *testing.T) {
	var skips []ZoneOriginSkip
	data := []byte(noLabelTimeout + "---\n" + globalOriginTimeout)
	n, err := writeResourceFiles(data, t.TempDir(), map[string]bool{}, CPModeZone, "ctx-zone-ctx", "default", "", "kubernetes", &skips)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expected nothing written on a federated zone, got %d", n)
	}
	if len(skips) != 1 {
		t.Fatalf("expected only the unlabelled resource reported (a global-origin copy is expected), got %+v", skips)
	}
	s := skips[0]
	if !s.NoOriginLabel || s.Kind != "MeshTimeout" || s.Name != "legacy-timeout" || s.Mesh != "default" {
		t.Errorf("unexpected skip record: %+v", s)
	}
}

func TestWriteResourceFiles_FederatedZone_GatewayLocalNoLabelNotReported(t *testing.T) {
	var skips []ZoneOriginSkip
	data := []byte("apiVersion: kuma.io/v1alpha1\nkind: MeshGatewayInstance\nmetadata:\n  name: gw\nspec: {}\n")
	n, _ := writeResourceFiles(data, t.TempDir(), map[string]bool{}, CPModeZone, "", "", "", "kubernetes", &skips)
	if n != 1 || len(skips) != 0 {
		t.Errorf("gateway-local kind without label must be kept and not reported: n=%d skips=%+v", n, skips)
	}
}

func TestWriteResourceFiles_FederatedZone_MeshFilterNotReported(t *testing.T) {
	var skips []ZoneOriginSkip
	_, _ = writeResourceFiles([]byte(noLabelTimeout), t.TempDir(), map[string]bool{}, CPModeZone, "", "other", "default", "kubernetes", &skips)
	if len(skips) != 0 {
		t.Errorf("a resource excluded by --mesh is not lost and must not be reported: %+v", skips)
	}
}

// A non-federated zone is extracted with filter mode "" (originFilterMode):
// unlabelled resources and leftovers from an earlier federation are all kept.
func TestWriteResourceFiles_NonFederatedZone_KeepsEverything(t *testing.T) {
	var skips []ZoneOriginSkip
	info := CPInfo{Mode: CPModeZone, ZoneName: "z"}
	data := []byte(noLabelTimeout + "---\n" + globalOriginTimeout)
	n, err := writeResourceFiles(data, t.TempDir(), map[string]bool{}, originFilterMode(info), "ctx-zone-ctx", "default", "", "kubernetes", &skips)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || len(skips) != 0 {
		t.Errorf("expected both resources written and nothing reported, got n=%d skips=%+v", n, skips)
	}
}

func TestPrintZoneOriginSkips_SplitsReasons(t *testing.T) {
	// Smoke test: both kinds of entry print without panicking.
	printZoneOriginSkips([]ZoneOriginSkip{
		{Kind: "MeshTimeout", Name: "a", ZoneName: "z1"},
		{Kind: "MeshRetry", Name: "b", Mesh: "default", NoOriginLabel: true},
	})
}

// ---- kubectl path end to end with a fake kubectl ----------------------------

// fakeKubectl answers the calls ExtractViaKubectl makes for a zone CP with one
// MeshTimeout CRD holding an unlabelled and a zone-origin instance.
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
				{"kind":"MeshTimeout","metadata":{"namespace":"kuma-system","name":"old","labels":{"kuma.io/mesh":"default"}}},
				{"kind":"MeshTimeout","metadata":{"namespace":"kuma-system","name":"new","labels":{"kuma.io/mesh":"default","kuma.io/origin":"zone"}}}]}`), nil
		case strings.HasPrefix(cmd, "get meshtimeouts "):
			name := args[2]
			return []byte("apiVersion: kuma.io/v1alpha1\nkind: MeshTimeout\nmetadata:\n  name: " + name + "\n  namespace: kuma-system\nspec: {}\n"), nil
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
	if got := extractedTimeouts(t, out); len(got) != 2 {
		t.Errorf("non-federated zone: expected both MeshTimeouts extracted, got %v", got)
	}
	meta := ReadContextMeta(out, "kctx-zone-ctx")
	if meta == nil || meta.Federated == nil || *meta.Federated {
		t.Fatalf("expected federated=false in metadata, got %+v", meta)
	}
	if meta.StoreType != "kubernetes" || meta.Environment != CPEnvKubernetes {
		t.Errorf("unexpected store/env in metadata: %+v", meta)
	}
}

func TestExtractViaKubectl_FederatedZoneSkipsUnlabelled(t *testing.T) {
	fakeKubectl(t, map[string]string{
		"KUMA_MODE": "zone", "KUMA_MULTIZONE_ZONE_NAME": "east",
		"KUMA_MULTIZONE_ZONE_GLOBAL_ADDRESS": "grpcs://global:5685",
	})
	out := t.TempDir()
	if err := ExtractViaKubectl("kctx", out, "", "kubernetes"); err != nil {
		t.Fatal(err)
	}
	got := extractedTimeouts(t, out)
	if len(got) != 1 || !strings.Contains(got[0], "-new") {
		t.Errorf("federated zone: expected only the zone-origin MeshTimeout, got %v", got)
	}
	meta := ReadContextMeta(out, "kctx-zone-ctx")
	if meta == nil || meta.Federated == nil || !*meta.Federated {
		t.Fatalf("expected federated=true in metadata, got %+v", meta)
	}
}

func TestDumpCRDInstances_FederatedZoneRecordsNoLabelSkip(t *testing.T) {
	fakeKubectl(t, nil)
	var skips []ZoneOriginSkip
	n, err := dumpCRDInstances("kctx", crdEntry{Kind: "MeshTimeout", Plural: "meshtimeouts"}, t.TempDir(), CPModeZone, "kctx-zone-ctx", "", &skips)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || len(skips) != 1 || !skips[0].NoOriginLabel || skips[0].Name != "old" || skips[0].Mesh != "default" {
		t.Errorf("expected 1 written and the unlabelled one reported, got n=%d skips=%+v", n, skips)
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

package extractor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ---- listKumaResourceTypes --------------------------------------------------

func TestListKumaResourceTypes_FiltersInsightKinds(t *testing.T) {
	// Insight kinds are excluded by name, regardless of the readOnly flag.
	payload := resourceTypeList{
		Resources: []resourceTypeEntry{
			{Name: "MeshTimeout", Path: "meshtimeouts", Scope: "Mesh", ReadOnly: false},
			{Name: "DataplaneInsight", Path: "dataplane-insights", Scope: "Mesh", ReadOnly: true},
			{Name: "MeshInsight", Path: "mesh-insights", Scope: "Global", ReadOnly: true},
			{Name: "Mesh", Path: "meshes", Scope: "Global", ReadOnly: false},
		},
	}
	srv := fakeResourcesServer(t, payload)

	types, err := listKumaResourceTypes(srv.URL, map[string]bool{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(types) != 2 {
		t.Fatalf("expected 2 types (Insight kinds excluded), got %d", len(types))
	}
	names := make([]string, len(types))
	for i, rt := range types {
		names[i] = rt.Name
	}
	for _, want := range []string{"MeshTimeout", "Mesh"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("expected %q in result, got %v", want, names)
		}
	}
}

func TestListKumaResourceTypes_ReadOnlyAPIServerIncludesPolicies(t *testing.T) {
	// When the Kuma API server runs in read-only mode, every resource type is
	// reported with readOnly=true. The migrator must still return policy types.
	payload := resourceTypeList{
		Resources: []resourceTypeEntry{
			{Name: "MeshTimeout", Path: "meshtimeouts", Scope: "Mesh", ReadOnly: true},
			{Name: "MeshTrafficPermission", Path: "meshtrafficpermissions", Scope: "Mesh", ReadOnly: true},
			{Name: "DataplaneInsight", Path: "dataplane-insights", Scope: "Mesh", ReadOnly: true},
		},
	}
	srv := fakeResourcesServer(t, payload)

	types, err := listKumaResourceTypes(srv.URL, map[string]bool{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Policies must be returned even though readOnly=true; only Insight is excluded.
	if len(types) != 2 {
		t.Fatalf("expected 2 types, got %d: %v", len(types), types)
	}
	for _, rt := range types {
		if rt.Name == "DataplaneInsight" {
			t.Error("DataplaneInsight should have been filtered out")
		}
	}
}

func TestListKumaResourceTypes_ScopePreserved(t *testing.T) {
	payload := resourceTypeList{
		Resources: []resourceTypeEntry{
			{Name: "MeshRetry", Path: "meshretries", Scope: "Mesh", ReadOnly: false},
			{Name: "Zone", Path: "zones", Scope: "Global", ReadOnly: false},
		},
	}
	srv := fakeResourcesServer(t, payload)

	types, err := listKumaResourceTypes(srv.URL, map[string]bool{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byName := make(map[string]resourceTypeEntry)
	for _, rt := range types {
		byName[rt.Name] = rt
	}
	if byName["MeshRetry"].Scope != "Mesh" {
		t.Errorf("expected MeshRetry scope=Mesh, got %q", byName["MeshRetry"].Scope)
	}
	if byName["Zone"].Scope != "Global" {
		t.Errorf("expected Zone scope=Global, got %q", byName["Zone"].Scope)
	}
}

func TestListKumaResourceTypes_PathPreserved(t *testing.T) {
	payload := resourceTypeList{
		Resources: []resourceTypeEntry{
			{Name: "MeshCircuitBreaker", Path: "meshcircuitbreakers", Scope: "Mesh", ReadOnly: false},
		},
	}
	srv := fakeResourcesServer(t, payload)

	types, err := listKumaResourceTypes(srv.URL, map[string]bool{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(types) != 1 || types[0].Path != "meshcircuitbreakers" {
		t.Errorf("expected path meshcircuitbreakers, got %v", types)
	}
}

func TestListKumaResourceTypes_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	_, err := listKumaResourceTypes(srv.URL, map[string]bool{}, "")
	if err == nil {
		t.Fatal("expected error on server 500")
	}
}

func TestListKumaResourceTypes_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)

	_, err := listKumaResourceTypes(srv.URL, map[string]bool{}, "")
	if err == nil {
		t.Fatal("expected error on invalid JSON")
	}
}

func TestListKumaResourceTypes_EmptyList(t *testing.T) {
	payload := resourceTypeList{Resources: []resourceTypeEntry{}}
	srv := fakeResourcesServer(t, payload)

	types, err := listKumaResourceTypes(srv.URL, map[string]bool{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(types) != 0 {
		t.Errorf("expected empty result, got %d entries", len(types))
	}
}

func TestListKumaResourceTypes_SkipListFiltersKinds(t *testing.T) {
	payload := resourceTypeList{
		Resources: []resourceTypeEntry{
			{Name: "MeshTimeout", Path: "meshtimeouts", Scope: "Mesh", ReadOnly: false},
			{Name: "Dataplane", Path: "dataplanes", Scope: "Mesh", ReadOnly: false},
			{Name: "Zone", Path: "zones", Scope: "Global", ReadOnly: false},
		},
	}
	srv := fakeResourcesServer(t, payload)

	skipSet := map[string]bool{"Dataplane": true, "Zone": true}
	types, err := listKumaResourceTypes(srv.URL, skipSet, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(types) != 1 || types[0].Name != "MeshTimeout" {
		t.Errorf("expected only MeshTimeout after skip filtering, got %v", types)
	}
}

// ---- listMeshNames ----------------------------------------------------------

func TestListMeshNames_ParsesKubernetesStyle(t *testing.T) {
	// listMeshNames calls kumactl which we can't stub easily, but we can test
	// the parsing logic directly via the YAML → name extraction.
	docs := `apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec: {}
---
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: prod
spec: {}`

	names := parseMeshNamesFromYAML([]byte(docs))
	if len(names) != 2 {
		t.Fatalf("expected 2 mesh names, got %d: %v", len(names), names)
	}
	want := map[string]bool{"default": true, "prod": true}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected mesh name %q", n)
		}
	}
}

func TestListMeshNames_ParsesUniversalStyle(t *testing.T) {
	// Universal Kuma meshes use a top-level "name" field without metadata.
	docs := `type: Mesh
name: universal-mesh`

	names := parseMeshNamesFromYAML([]byte(docs))
	if len(names) != 1 || names[0] != "universal-mesh" {
		t.Errorf("expected [universal-mesh], got %v", names)
	}
}

func TestListMeshNames_ParsesKubernetesMeshList(t *testing.T) {
	// Konnect / Kubernetes-style kumactl returns a MeshList document, not ---stream.
	doc := `apiVersion: kuma.io/v1alpha1
kind: MeshList
items:
- apiVersion: kuma.io/v1alpha1
  kind: Mesh
  metadata:
    name: british-airways-mesh
  spec: {}
- apiVersion: kuma.io/v1alpha1
  kind: Mesh
  metadata:
    name: internal-mesh
  spec: {}`

	names := parseMeshNamesFromYAML([]byte(doc))
	if len(names) != 2 {
		t.Fatalf("expected 2 mesh names from MeshList, got %d: %v", len(names), names)
	}
	want := map[string]bool{"british-airways-mesh": true, "internal-mesh": true}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected mesh name %q", n)
		}
	}
}

func TestListMeshNames_EmptyYAML(t *testing.T) {
	names := parseMeshNamesFromYAML([]byte(""))
	// Should fall back to ["default"].
	if len(names) != 1 || names[0] != "default" {
		t.Errorf("expected fallback [default], got %v", names)
	}
}

// ---- isEmptyResult ----------------------------------------------------------

func TestIsEmptyResult_NilIsNotEmpty(t *testing.T) {
	if isEmptyResult(nil) {
		t.Error("nil error should not be treated as empty result")
	}
}

func TestIsEmptyResult_NoResourcesFound(t *testing.T) {
	if !isEmptyResult(fakeErr("Error from server (NotFound): no resources found")) {
		t.Error("expected isEmptyResult=true for 'no resources found'")
	}
}

func TestIsEmptyResult_NotFound(t *testing.T) {
	if !isEmptyResult(fakeErr("resource type not found")) {
		t.Error("expected isEmptyResult=true for 'not found'")
	}
}

func TestIsEmptyResult_RealError(t *testing.T) {
	if isEmptyResult(fakeErr("connection refused")) {
		t.Error("expected isEmptyResult=false for connection refused")
	}
}

// ---- isUnknownMeshFlag ------------------------------------------------------

func TestIsUnknownMeshFlag_Match(t *testing.T) {
	if !isUnknownMeshFlag(fakeErr("Error: unknown flag: --mesh")) {
		t.Error("expected isUnknownMeshFlag=true for 'unknown flag: --mesh'")
	}
}

func TestIsUnknownMeshFlag_NoMatch(t *testing.T) {
	for _, msg := range []string{"connection refused", "no resources found", "unauthorized"} {
		if isUnknownMeshFlag(fakeErr(msg)) {
			t.Errorf("expected isUnknownMeshFlag=false for %q", msg)
		}
	}
}

func TestIsUnknownMeshFlag_Nil(t *testing.T) {
	if isUnknownMeshFlag(nil) {
		t.Error("expected isUnknownMeshFlag=false for nil")
	}
}

func TestListKumaResourceTypes_SendsBearerToken(t *testing.T) {
	var gotAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resourceTypeList{})
	}))
	t.Cleanup(srv.Close)

	_, err := listKumaResourceTypes(srv.URL, map[string]bool{}, "kpat_mytoken123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuthHeader != "Bearer kpat_mytoken123" {
		t.Errorf("expected Authorization header %q, got %q", "Bearer kpat_mytoken123", gotAuthHeader)
	}
}

func TestListKumaResourceTypes_NoTokenOmitsHeader(t *testing.T) {
	var gotAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resourceTypeList{})
	}))
	t.Cleanup(srv.Close)

	_, err := listKumaResourceTypes(srv.URL, map[string]bool{}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuthHeader != "" {
		t.Errorf("expected no Authorization header, got %q", gotAuthHeader)
	}
}

// ---- dumpKumactlResources Konnect path --------------------------------------

// TestDumpKumactlResources_KonnectAddsFormatKubernetes verifies that when the
// CP URL is identified as Konnect, dumpKumactlResources makes a direct HTTP
// GET to <cpURL>/<path>?format=kubernetes (global-scoped resource).
func TestDumpKumactlResources_KonnectAddsFormatKubernetes(t *testing.T) {
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		// Return a minimal Kubernetes-style list so writeResourceFiles has something to process.
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"kind":"MeshTimeoutList","items":[]}`))
	}))
	defer srv.Close()

	// Override the Konnect URL check so our test server is treated as Konnect.
	old := konnectURLCheck
	konnectURLCheck = func(url string) bool { return url == srv.URL }
	defer func() { konnectURLCheck = old }()

	rt := resourceTypeEntry{Name: "MeshTimeout", Path: "meshtimeouts", Scope: "Global"}
	dir := t.TempDir()
	_, err := dumpKumactlResources("ctx", srv.URL, "token", rt, "", dir, map[string]bool{}, CPModeGlobal, "my-cp-global-ctx", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotURL != "/meshtimeouts?format=kubernetes" {
		t.Errorf("expected request to /meshtimeouts?format=kubernetes, got %q", gotURL)
	}
}

// TestDumpKumactlResources_KonnectMeshScopedURL verifies that mesh-scoped Konnect
// requests use the /meshes/<mesh>/<path>?format=kubernetes URL pattern.
func TestDumpKumactlResources_KonnectMeshScopedURL(t *testing.T) {
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		w.Write([]byte(`{"kind":"MeshTimeoutList","items":[]}`))
	}))
	defer srv.Close()

	old := konnectURLCheck
	konnectURLCheck = func(url string) bool { return url == srv.URL }
	defer func() { konnectURLCheck = old }()

	rt := resourceTypeEntry{Name: "MeshTimeout", Path: "meshtimeouts", Scope: "Mesh"}
	dir := t.TempDir()
	_, err := dumpKumactlResources("ctx", srv.URL, "token", rt, "default", dir, map[string]bool{}, CPModeGlobal, "my-cp-global-ctx", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotURL != "/meshes/default/meshtimeouts?format=kubernetes" {
		t.Errorf("expected request to /meshes/default/meshtimeouts?format=kubernetes, got %q", gotURL)
	}
}

// TestDumpKumactlResources_KonnectSendsAuthHeader verifies the bearer token is
// forwarded in the Authorization header on the Konnect HTTP path.
func TestDumpKumactlResources_KonnectSendsAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{"kind":"MeshTimeoutList","items":[]}`))
	}))
	defer srv.Close()

	old := konnectURLCheck
	konnectURLCheck = func(url string) bool { return url == srv.URL }
	defer func() { konnectURLCheck = old }()

	rt := resourceTypeEntry{Name: "MeshTimeout", Path: "meshtimeouts", Scope: "Global"}
	dir := t.TempDir()
	_, err := dumpKumactlResources("ctx", srv.URL, "kpat_secret", rt, "", dir, map[string]bool{}, CPModeGlobal, "my-cp-global-ctx", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer kpat_secret" {
		t.Errorf("expected Authorization: Bearer kpat_secret, got %q", gotAuth)
	}
}

// ---- helpers ----------------------------------------------------------------

// fakeResourcesServer starts an httptest server that returns the given payload
// at GET /_resources.
func fakeResourcesServer(t *testing.T, payload resourceTypeList) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_resources" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(srv.Close)
	return srv
}

type errString string

func (e errString) Error() string { return string(e) }

func fakeErr(msg string) error { return errString(msg) }


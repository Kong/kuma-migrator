package extractor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ---- listKumaResourceTypes --------------------------------------------------

func TestListKumaResourceTypes_FiltersReadOnly(t *testing.T) {
	payload := resourceTypeList{
		Resources: []resourceTypeEntry{
			{Name: "MeshTimeout", Path: "meshtimeouts", Scope: "Mesh", ReadOnly: false},
			{Name: "DataplaneInsight", Path: "dataplane-insights", Scope: "Mesh", ReadOnly: true},
			{Name: "MeshInsight", Path: "mesh-insights", Scope: "Global", ReadOnly: true},
			{Name: "Mesh", Path: "meshes", Scope: "Global", ReadOnly: false},
		},
	}
	srv := fakeResourcesServer(t, payload)

	types, err := listKumaResourceTypes(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(types) != 2 {
		t.Fatalf("expected 2 writable types, got %d", len(types))
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

func TestListKumaResourceTypes_ScopePreserved(t *testing.T) {
	payload := resourceTypeList{
		Resources: []resourceTypeEntry{
			{Name: "MeshRetry", Path: "meshretries", Scope: "Mesh", ReadOnly: false},
			{Name: "Zone", Path: "zones", Scope: "Global", ReadOnly: false},
		},
	}
	srv := fakeResourcesServer(t, payload)

	types, err := listKumaResourceTypes(srv.URL)
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

	types, err := listKumaResourceTypes(srv.URL)
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

	_, err := listKumaResourceTypes(srv.URL)
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

	_, err := listKumaResourceTypes(srv.URL)
	if err == nil {
		t.Fatal("expected error on invalid JSON")
	}
}

func TestListKumaResourceTypes_EmptyList(t *testing.T) {
	payload := resourceTypeList{Resources: []resourceTypeEntry{}}
	srv := fakeResourcesServer(t, payload)

	types, err := listKumaResourceTypes(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(types) != 0 {
		t.Errorf("expected empty result, got %d entries", len(types))
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


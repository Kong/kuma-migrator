package extractor

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// ---- detectKumactlCPMode ----------------------------------------------------

func TestDetectKumactlCPMode_Global(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/config" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"mode":"global","environment":"kubernetes"}`))
		}
	}))
	defer srv.Close()

	got, _, _ := detectKumactlCPMode(srv.URL, "")
	if got != CPModeGlobal {
		t.Errorf("expected %q, got %q", CPModeGlobal, got)
	}
}

func TestDetectKumactlCPMode_Zone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/config" {
			w.Write([]byte(`{"mode":"zone","multizone":{"zone":{"name":"eu-west"}}}`))
		}
	}))
	defer srv.Close()

	gotMode, gotZone, _ := detectKumactlCPMode(srv.URL, "")
	if gotMode != CPModeZone {
		t.Errorf("expected mode %q, got %q", CPModeZone, gotMode)
	}
	if gotZone != "eu-west" {
		t.Errorf("expected zone name %q, got %q", "eu-west", gotZone)
	}
}

func TestDetectKumactlCPMode_Standalone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"mode":"Standalone"}`)) // mixed case from older versions
	}))
	defer srv.Close()

	got, _, _ := detectKumactlCPMode(srv.URL, "")
	if got != CPModeStandalone {
		t.Errorf("expected %q, got %q", CPModeStandalone, got)
	}
}

func TestDetectKumactlCPMode_Error(t *testing.T) {
	// Point at a server that immediately closes the connection.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	got, _, _ := detectKumactlCPMode(srv.URL, "")
	if got != "" {
		t.Errorf("expected empty string on error, got %q", got)
	}
}

func TestDetectKumactlCPMode_Unreachable(t *testing.T) {
	got, _, _ := detectKumactlCPMode("http://127.0.0.1:19999", "") // nothing listening
	if got != "" {
		t.Errorf("expected empty string for unreachable server, got %q", got)
	}
}

func TestDetectKumactlCPMode_ReturnsEnvironment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/config" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"mode":"global","environment":"universal"}`))
		}
	}))
	defer srv.Close()

	_, _, env := detectKumactlCPMode(srv.URL, "")
	if env != CPEnvUniversal {
		t.Errorf("expected env %q, got %q", CPEnvUniversal, env)
	}
}

func TestCPModeDirectoryLabel_Global(t *testing.T) {
	if got := cpModeDirectoryLabel("prod-cp", CPModeGlobal); got != "prod-cp-global-ctx" {
		t.Errorf("expected prod-cp-global-ctx, got %q", got)
	}
}

func TestCPModeDirectoryLabel_Zone(t *testing.T) {
	if got := cpModeDirectoryLabel("eu-west", CPModeZone); got != "eu-west-zone-ctx" {
		t.Errorf("expected eu-west-zone-ctx, got %q", got)
	}
}

func TestCPModeDirectoryLabel_Standalone(t *testing.T) {
	if got := cpModeDirectoryLabel("my-cp", CPModeStandalone); got != "my-cp-standalone-ctx" {
		t.Errorf("expected my-cp-standalone-ctx, got %q", got)
	}
}

func TestCPModeDirectoryLabel_Unknown(t *testing.T) {
	if got := cpModeDirectoryLabel("my-cp", ""); got != "my-cp-unknown-ctx" {
		t.Errorf("expected my-cp-unknown-ctx, got %q", got)
	}
}

// ---- isGatewayLocalKind -----------------------------------------------------

func TestIsZoneOnlyKind(t *testing.T) {
	// Only strictly zone-local kinds that are never synced to Global CP.
	zoneOnly := []string{"MeshGatewayInstance", "MeshGatewayConfig"}
	for _, k := range zoneOnly {
		if !isZoneOnlyKind(k) {
			t.Errorf("expected %q to be zone-only", k)
		}
	}
	// MeshGateway and route CRDs can be created on either Global or Zone CP — not zone-only.
	notZoneOnly := []string{"MeshGateway", "MeshHTTPRoute", "MeshTCPRoute", "MeshGatewayRoute", "MeshTimeout", "Mesh"}
	for _, k := range notZoneOnly {
		if isZoneOnlyKind(k) {
			t.Errorf("expected %q NOT to be zone-only", k)
		}
	}
}

func TestIsGatewayLocalKind(t *testing.T) {
	// All gateway CRD kinds may lack the origin label on a Zone CP when created there directly.
	gatewayKinds := []string{
		"MeshGatewayInstance", "MeshGatewayConfig",
		"MeshGateway", "MeshHTTPRoute", "MeshTCPRoute", "MeshGatewayRoute",
	}
	for _, k := range gatewayKinds {
		if !isGatewayLocalKind(k) {
			t.Errorf("expected %q to be gateway-local", k)
		}
	}
	// Non-gateway policy kinds are not gateway-local.
	nonGatewayKinds := []string{"MeshTimeout", "Mesh", "MeshRetry", "MeshHealthCheck"}
	for _, k := range nonGatewayKinds {
		if isGatewayLocalKind(k) {
			t.Errorf("expected %q NOT to be gateway-local", k)
		}
	}
}

// ---- writeResourceFiles zone filter -----------------------------------------

func TestWriteResourceFiles_ZoneFilter_KeepsZoneOrigin(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: my-timeout
  labels:
    kuma.io/origin: zone
spec: {}
`)
	n, err := writeResourceFiles(data, dir, map[string]bool{}, CPModeZone, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 file written, got %d", n)
	}
}

func TestWriteResourceFiles_ZoneFilter_SkipsGlobalOrigin(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: global-timeout
  labels:
    kuma.io/origin: global
spec: {}
`)
	n, err := writeResourceFiles(data, dir, map[string]bool{}, CPModeZone, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 files written (global-origin skipped on zone CP), got %d", n)
	}
}

func TestWriteResourceFiles_ZoneFilter_SkipsNoLabel(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: no-label-timeout
spec: {}
`)
	n, err := writeResourceFiles(data, dir, map[string]bool{}, CPModeZone, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 files written (no origin label skipped on zone CP), got %d", n)
	}
}

func TestWriteResourceFiles_GlobalMode_SkipsZoneOnlyKinds(t *testing.T) {
	dir := t.TempDir()
	// MeshGatewayInstance and MeshGatewayConfig must be skipped on global CP.
	// MeshGateway is created on the Global CP and must be kept.
	data := []byte(`apiVersion: kuma.io/v1alpha1
kind: MeshGateway
metadata:
  name: global-gw
spec: {}
---
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayInstance
metadata:
  name: zone-gw-inst
  namespace: kong-mesh-system
spec: {}
---
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayConfig
metadata:
  name: zone-gw-config
  namespace: kong-mesh-system
spec: {}
---
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: global-timeout
spec: {}
`)
	n, err := writeResourceFiles(data, dir, map[string]bool{}, CPModeGlobal, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// MeshGateway and MeshTimeout kept; MeshGatewayInstance and MeshGatewayConfig skipped.
	if n != 2 {
		t.Errorf("expected 2 files (MeshGateway + MeshTimeout kept; instance+config skipped), got %d", n)
	}
}

func TestWriteResourceFiles_GlobalMode_KeepsAll(t *testing.T) {
	dir := t.TempDir()
	// Three docs: one zone, one global, one no label — all kept on global CP.
	data := []byte(`apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: zone-timeout
  labels:
    kuma.io/origin: zone
spec: {}
---
apiVersion: kuma.io/v1alpha1
kind: MeshRetry
metadata:
  name: global-retry
  labels:
    kuma.io/origin: global
spec: {}
---
apiVersion: kuma.io/v1alpha1
kind: MeshHealthCheck
metadata:
  name: no-label-hc
spec: {}
`)
	n, err := writeResourceFiles(data, dir, map[string]bool{}, CPModeGlobal, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 files on global CP, got %d", n)
	}
}

func TestWriteResourceFiles_UnknownMode_KeepsAll(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: some-timeout
  labels:
    kuma.io/origin: global
spec: {}
`)
	n, err := writeResourceFiles(data, dir, map[string]bool{}, "", "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 file on unknown CP mode, got %d", n)
	}
}

func TestWriteResourceFiles_ZoneFilter_MultiDoc(t *testing.T) {
	dir := t.TempDir()
	// Two docs: one zone-origin (kept), one global-origin (skipped).
	data := []byte(`apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: local-timeout
  labels:
    kuma.io/origin: zone
spec: {}
---
apiVersion: kuma.io/v1alpha1
kind: MeshRetry
metadata:
  name: synced-retry
  labels:
    kuma.io/origin: global
spec: {}
`)
	n, err := writeResourceFiles(data, dir, map[string]bool{}, CPModeZone, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 file (zone-origin only), got %d", n)
	}
	// Confirm the written file is the zone-origin one.
	entries, _ := os.ReadDir(filepath.Join(dir, "resiliency"))
	if len(entries) != 1 || entries[0].Name() != "MeshTimeout-local-timeout.yaml" {
		t.Errorf("unexpected files in resiliency/: %v", entries)
	}
}

func TestWriteResourceFiles_ZoneFilter_KeepsGatewayInstanceWithNoLabel(t *testing.T) {
	dir := t.TempDir()
	// MeshGatewayInstance and MeshGatewayConfig are zone-local and lack the origin label.
	data := []byte(`apiVersion: kuma.io/v1alpha1
kind: MeshGatewayInstance
metadata:
  name: my-gw-instance
  namespace: kong-mesh-system
spec: {}
---
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayConfig
metadata:
  name: my-gw-config
  namespace: kong-mesh-system
spec: {}
`)
	n, err := writeResourceFiles(data, dir, map[string]bool{}, CPModeZone, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 files (MeshGatewayInstance + MeshGatewayConfig kept without origin label), got %d", n)
	}
}

func TestWriteResourceFiles_ZoneFilter_SkipsMeshGatewayWithGlobalLabel(t *testing.T) {
	dir := t.TempDir()
	// MeshGateway is created on Global CP and synced to zones with kuma.io/origin: global.
	// The normal origin filter should skip it on zone CP extraction.
	data := []byte(`apiVersion: kuma.io/v1alpha1
kind: MeshGateway
metadata:
  name: synced-gw
  labels:
    kuma.io/origin: global
spec: {}
`)
	n, err := writeResourceFiles(data, dir, map[string]bool{}, CPModeZone, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 files (MeshGateway with origin=global skipped on zone CP), got %d", n)
	}
}

func TestWriteResourceFiles_ZoneFilter_KeepsMeshGatewayWithNoLabel(t *testing.T) {
	dir := t.TempDir()
	// MeshGateway without origin label on Zone CP → created directly on this zone → kept.
	data := []byte(`apiVersion: kuma.io/v1alpha1
kind: MeshGateway
metadata:
  name: zone-local-gw
spec: {}
`)
	n, err := writeResourceFiles(data, dir, map[string]bool{}, CPModeZone, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 file (MeshGateway without origin label kept on zone CP as zone-local), got %d", n)
	}
}

// ---- Konnect detection ------------------------------------------------------

func TestDetectKumactlCPMode_Konnect(t *testing.T) {
	// Konnect URLs always contain api.konghq.com — mode must be global without
	// hitting any endpoint (the /config endpoint does not exist on Konnect).
	konnectURLs := []string{
		"https://eu.api.konghq.com/v1/mesh/control-planes/abc123/api",
		"https://us.api.konghq.com/v1/mesh/control-planes/xyz/api",
		"https://au.api.konghq.com/v1/mesh/control-planes/def456/api",
	}
	for _, u := range konnectURLs {
		mode, zone, env := detectKumactlCPMode(u, "test-token")
		if mode != CPModeGlobal {
			t.Errorf("Konnect URL %q: expected mode %q, got %q", u, CPModeGlobal, mode)
		}
		if zone != "" {
			t.Errorf("Konnect URL %q: expected empty zone, got %q", u, zone)
		}
		if env != CPEnvKubernetes {
			t.Errorf("Konnect URL %q: expected env %q, got %q", u, CPEnvKubernetes, env)
		}
	}
}

func TestIsKonnectURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://eu.api.konghq.com/v1/mesh/control-planes/abc/api", true},
		{"https://us.api.konghq.com/v1/mesh/control-planes/abc/api", true},
		{"http://localhost:5681", false},
		{"https://kuma-cp.internal:5682", false},
		{"https://my-kuma.example.com/api", false},
	}
	for _, c := range cases {
		if got := isKonnectURL(c.url); got != c.want {
			t.Errorf("isKonnectURL(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

// ---- Universal / list format parsing ----------------------------------------

func TestWriteResourceFiles_UniversalFormatSingleResource(t *testing.T) {
	dir := t.TempDir()
	// kumactl in Universal mode returns type/name instead of kind/metadata.name.
	data := []byte(`type: MeshMetric
name: my-metrics
mesh: default
spec: {}
`)
	n, err := writeResourceFiles(data, dir, map[string]bool{}, CPModeGlobal, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 file from Universal format resource, got %d", n)
	}
}

func TestWriteResourceFiles_UniversalFormatList(t *testing.T) {
	dir := t.TempDir()
	// kumactl Universal list response: {total: N, items: [{type: ..., name: ...}]}
	data := []byte(`total: 2
items:
- type: MeshMetric
  name: metrics-1
  mesh: default
  spec: {}
- type: MeshAccessLog
  name: access-log-1
  mesh: default
  spec: {}
`)
	n, err := writeResourceFiles(data, dir, map[string]bool{}, CPModeGlobal, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 files from Universal list, got %d", n)
	}
}

func TestWriteResourceFiles_KubernetesListFormat(t *testing.T) {
	dir := t.TempDir()
	// kumactl on Kubernetes returns a MeshMetricList document.
	data := []byte(`apiVersion: kuma.io/v1alpha1
kind: MeshMetricList
items:
- apiVersion: kuma.io/v1alpha1
  kind: MeshMetric
  metadata:
    name: my-metrics
    namespace: kuma-system
  spec: {}
- apiVersion: kuma.io/v1alpha1
  kind: MeshMetric
  metadata:
    name: other-metrics
    namespace: kuma-system
  spec: {}
`)
	n, err := writeResourceFiles(data, dir, map[string]bool{}, CPModeGlobal, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 files from Kubernetes list format, got %d", n)
	}
}

func TestWriteResourceFiles_EmptyKubernetesList(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`apiVersion: kuma.io/v1alpha1
kind: MeshMetricList
items: []
`)
	n, err := writeResourceFiles(data, dir, map[string]bool{}, CPModeGlobal, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 files from empty list, got %d", n)
	}
}

func TestWriteResourceFiles_ZoneFilter_SkipsNonGatewayNoLabel(t *testing.T) {
	dir := t.TempDir()
	// MeshTimeout without origin label — not a gateway-local kind, must be skipped on zone CP.
	data := []byte(`apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: no-label-timeout
spec: {}
`)
	n, err := writeResourceFiles(data, dir, map[string]bool{}, CPModeZone, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 files (non-gateway kind without origin label skipped on zone CP), got %d", n)
	}
}

// ---- Mesh subdir and mesh filter tests --------------------------------------

func TestWriteResourceFiles_MeshSubdir_CreatesSubdirPerMesh(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: my-timeout
spec: {}
`)
	// Context-first layout: cpModeDir is the context dir label, meshName is the mesh.
	n, err := writeResourceFiles(data, dir, map[string]bool{}, CPModeGlobal, "my-cp-global-ctx", "default", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 file written, got %d", n)
	}
	// File must be under <dir>/my-cp-global-ctx/default/resiliency/
	entries, _ := os.ReadDir(filepath.Join(dir, "my-cp-global-ctx", "default", "resiliency"))
	if len(entries) != 1 || entries[0].Name() != "MeshTimeout-my-timeout.yaml" {
		t.Errorf("unexpected files in my-cp-global-ctx/default/resiliency/: %v", entries)
	}
}

func TestWriteResourceFiles_GlobalScopedGoesToGlobalSubdir(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: global-resource
spec: {}
`)
	// No meshName → global-scoped resource goes to <cpModeDir>/global/<sub>/
	n, err := writeResourceFiles(data, dir, map[string]bool{}, CPModeGlobal, "my-cp-global-ctx", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 file written, got %d", n)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "my-cp-global-ctx", "global", "resiliency"))
	if len(entries) != 1 || entries[0].Name() != "MeshTimeout-global-resource.yaml" {
		t.Errorf("unexpected files in my-cp-global-ctx/global/resiliency/: %v", entries)
	}
}

func TestWriteResourceFiles_MeshFilter_SkipsOtherMesh(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: prod-timeout
spec: {}
`)
	// meshName="prod", meshFilter="default" → should be skipped.
	n, err := writeResourceFiles(data, dir, map[string]bool{}, CPModeGlobal, "global", "prod", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 files (prod mesh skipped when filter=default), got %d", n)
	}
}

func TestWriteResourceFiles_MeshFilter_KeepsMatchingMesh(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: default-timeout
spec: {}
`)
	// meshName="default", meshFilter="default" → should be kept.
	n, err := writeResourceFiles(data, dir, map[string]bool{}, CPModeGlobal, "global", "default", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 file (matching mesh filter), got %d", n)
	}
}

func TestWriteResourceFiles_MeshFilter_KeepsGlobalScopedResources(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: global-resource
spec: {}
`)
	// meshName="" (global-scoped), meshFilter="default" → must NOT be filtered out.
	n, err := writeResourceFiles(data, dir, map[string]bool{}, CPModeGlobal, "global", "", "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 file (global-scoped resources not filtered by mesh filter), got %d", n)
	}
}

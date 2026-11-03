package readiness

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeCP is a minimal 2.14 control plane: one Mesh without
// meshServices.mode Exclusive, every other collection empty.
type fakeCP struct {
	mu    sync.Mutex
	paths []string
	auth  []string
}

func (f *fakeCP) seen(p string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, x := range f.paths {
		if x == p {
			return true
		}
	}
	return false
}

func newFakeCP(t *testing.T) (*fakeCP, *httptest.Server) {
	t.Helper()
	f := &fakeCP{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.paths = append(f.paths, r.URL.Path)
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/":
			_, _ = io.WriteString(w, `{"product":"Kuma","version":"2.14.5","mode":"zone"}`)
		case "/config":
			_, _ = io.WriteString(w, `{"mode":"zone","environment":"universal"}`)
		case "/meshes":
			_, _ = io.WriteString(w, `{"total":1,"items":[{"type":"Mesh","name":"default"}],"next":null}`)
		default:
			_, _ = io.WriteString(w, `{"total":0,"items":[],"next":null}`)
		}
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func TestRun_WritesReportsAndSendsToken(t *testing.T) {
	f, srv := newFakeCP(t)
	dir := t.TempDir()
	rep, err := Run(RunOptions{
		Address: srv.URL, Token: "tok", LatestVersion: "2.14.7",
		Timeout: 30e9, HTTPClient: srv.Client(), OutputDir: dir,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Status == StatusFailed || rep.Status == StatusClean {
		t.Fatalf("status = %q, want blockers or inconclusive (Mesh not Exclusive, CP on 2.14.5)", rep.Status)
	}
	if rep.Summary.Blockers == 0 {
		t.Errorf("no blocker found; findings: %+v", rep.Findings)
	}
	for _, name := range []string{JSONFile, HTMLFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s not written: %v", name, err)
		}
	}
	got, err := Load(dir)
	if err != nil || got == nil {
		t.Fatalf("Load: %v, %v", got, err)
	}
	if got.Status != rep.Status || got.GeneratedAt == "" {
		t.Errorf("round trip: status %q→%q, generatedAt %q", rep.Status, got.Status, got.GeneratedAt)
	}
	if !f.seen("/config") {
		t.Error("self-hosted audit should read /config")
	}
	for _, a := range f.auth {
		if a != "Bearer tok" {
			t.Fatalf("Authorization = %q, want Bearer tok", a)
		}
	}
}

func TestRun_KonnectSkipsGlobalConfig(t *testing.T) {
	f, srv := newFakeCP(t)
	rep, err := Run(RunOptions{
		Address: srv.URL, Konnect: true, LatestVersion: "2.14.7",
		Timeout: 30e9, HTTPClient: srv.Client(), OutputDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if f.seen("/config") {
		t.Error("Konnect audit read /config; Konnect serves none")
	}
	if rep.ControlPlane.Mode != "global" {
		t.Errorf("mode = %q, want global", rep.ControlPlane.Mode)
	}
}

func TestRun_UnreachableWritesFailedReport(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close()
	dir := t.TempDir()
	rep, err := Run(RunOptions{Address: addr, LatestVersion: "2.14.7", Timeout: 5e9, HTTPClient: http.DefaultClient, OutputDir: dir})
	if err == nil {
		t.Fatal("want an error for an unreachable CP")
	}
	if rep.Status != StatusFailed {
		t.Errorf("status = %q, want failed", rep.Status)
	}
	got, lerr := Load(dir)
	if lerr != nil || got == nil || got.Status != StatusFailed {
		t.Errorf("failed report not written: %+v, %v", got, lerr)
	}
	if !strings.HasPrefix(StatusLine(rep), "failed") {
		t.Errorf("StatusLine = %q", StatusLine(rep))
	}
}

func TestRun_LatestFromGitHubWhenUnset(t *testing.T) {
	_, srv := newFakeCP(t)
	old := fetchLatest
	t.Cleanup(func() { fetchLatest = old })
	called := false
	fetchLatest = func(context.Context, *http.Client) (string, error) { called = true; return "2.14.7", nil }
	if _, err := Run(RunOptions{Address: srv.URL, Timeout: 30e9, HTTPClient: srv.Client(), OutputDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("latest patch not looked up")
	}
}

func TestLoad_Missing(t *testing.T) {
	rep, err := Load(t.TempDir())
	if rep != nil || err != nil {
		t.Errorf("Load on empty dir = %v, %v; want nil, nil", rep, err)
	}
}

func TestLatestPatch_FollowsPagination(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			_, _ = io.WriteString(w, `[{"tag_name":"2.14.7"},{"tag_name":"2.14.8","prerelease":true},{"tag_name":"2.13.12"}]`)
			return
		}
		w.Header().Set("Link", fmt.Sprintf(`<%s/?page=2>; rel="next", <%s/?page=2>; rel="last"`, srv.URL, srv.URL))
		_, _ = io.WriteString(w, `[{"tag_name":"3.0.1"},{"tag_name":"2.14.6"},{"tag_name":"2.14.9","draft":true}]`)
	}))
	defer srv.Close()
	old := releasesURL
	releasesURL = srv.URL + "/"
	defer func() { releasesURL = old }()
	got, err := LatestPatch(context.Background(), srv.Client())
	if err != nil || got != "2.14.7" {
		t.Errorf("LatestPatch = %q, %v; want 2.14.7", got, err)
	}
}

func TestLatestPatch_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer srv.Close()
	old := releasesURL
	releasesURL = srv.URL
	defer func() { releasesURL = old }()
	if _, err := LatestPatch(context.Background(), srv.Client()); err == nil {
		t.Error("want an error on 403")
	}
}

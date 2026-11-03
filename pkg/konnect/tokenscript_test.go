package konnect

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func baseOpts() TokenScriptOptions {
	return TokenScriptOptions{
		Region:      "eu",
		Scope:       ScopeMesh,
		AccountName: "kuma-migrator-readonly",
		ExpiresAt:   time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC),
		GeneratedBy: "test",
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		mut     func(*TokenScriptOptions)
		wantErr string
	}{
		{"ok", func(*TokenScriptOptions) {}, ""},
		{"bad region", func(o *TokenScriptOptions) { o.Region = "mars" }, "--region"},
		{"bad scope", func(o *TokenScriptOptions) { o.Scope = "all" }, "--scope"},
		{"cp with org scope", func(o *TokenScriptOptions) { o.Scope = ScopeOrg; o.ControlPlanes = []string{"a"} }, "--control-plane only"},
		{"shell injection in cp", func(o *TokenScriptOptions) { o.ControlPlanes = []string{`a"; rm -rf /`} }, "--control-plane"},
		{"dollar in account", func(o *TokenScriptOptions) { o.AccountName = "a$(id)" }, "--account-name"},
		{"no expiry", func(o *TokenScriptOptions) { o.ExpiresAt = time.Time{} }, "expiry"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := baseOpts()
			tc.mut(&o)
			err := o.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestTokenScriptContent(t *testing.T) {
	o := baseOpts()
	o.ControlPlanes = []string{"prod-global", "dev-global"}
	mesh, err := TokenScript(o)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`REGION="eu"`, `EXPIRES_AT="2026-11-17T08:00:00Z"`, `CONTROL_PLANES="prod-global dev-global"`, `role_name: "Viewer"`, `"Mesh Control Planes"`} {
		if !strings.Contains(mesh, want) {
			t.Errorf("mesh script lacks %q", want)
		}
	}
	if strings.Contains(mesh, "organization-admin-readonly") {
		t.Error("mesh script must not touch the org read-only team")
	}

	o = baseOpts()
	o.Scope = ScopeOrg
	org, err := TokenScript(o)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(org, "Organization Admin (Read Only)") || strings.Contains(org, "assigned-roles") {
		t.Error("org script should use team membership, not role assignment")
	}
}

// fakeKonnect records the write calls the script makes.
type fakeKonnect struct {
	mu    sync.Mutex
	calls []string
	roles []map[string]string
	auth  []string
}

func (f *fakeKonnect) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		body, _ := io.ReadAll(r.Body)
		key := r.Method + " " + r.URL.Path
		f.calls = append(f.calls, key)
		w.Header().Set("Content-Type", "application/json")
		switch key {
		case "GET /v3/system-accounts":
			_, _ = io.WriteString(w, `{"data":[]}`)
		case "POST /v3/system-accounts":
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"sa-1"}`)
		case "GET /v1/mesh/control-planes":
			_, _ = io.WriteString(w, `{"data":[{"id":"cp-1","name":"prod-global"},{"id":"cp-2","name":"dev-global"}]}`)
		case "POST /v3/system-accounts/sa-1/assigned-roles":
			var m map[string]string
			if err := json.Unmarshal(body, &m); err != nil {
				t.Errorf("assigned-roles body: %v", err)
			}
			f.roles = append(f.roles, m)
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{}`)
		case "GET /v3/teams":
			_, _ = io.WriteString(w, `{"data":[{"id":"t-0","name":"Organization Admin"},{"id":"t-1","name":"Organization Admin (Read Only)"}]}`)
		case "POST /v3/teams/t-1/system-accounts":
			w.WriteHeader(http.StatusCreated)
		case "POST /v3/system-accounts/sa-1/access-tokens":
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"token":"spat_0123456789abcdefghijklmnop"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"not found"}`)
		}
	})
}

// runScript renders the script with both API hosts pointed at srv and runs it.
func runScript(t *testing.T, o TokenScriptOptions, srvURL string) (string, error) {
	t.Helper()
	for _, bin := range []string{"bash", "curl", "jq"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	script, err := TokenScript(o)
	if err != nil {
		t.Fatal(err)
	}
	script = strings.Replace(script, `GLOBAL_API="https://global.api.konghq.com"`, `GLOBAL_API="`+srvURL+`"`, 1)
	script = strings.Replace(script, `REGION_API="https://${REGION}.api.konghq.com"`, `REGION_API="`+srvURL+`"`, 1)
	path := filepath.Join(t.TempDir(), "token.sh")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", path)
	cmd.Env = append(os.Environ(), "KONNECT_ADMIN_PAT=kpat_admin_secret_value")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestTokenScriptRunsMeshScope(t *testing.T) {
	fk := &fakeKonnect{}
	srv := httptest.NewServer(fk.handler(t))
	defer srv.Close()

	o := baseOpts()
	o.ControlPlanes = []string{"prod-global"}
	out, err := runScript(t, o, srv.URL)
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	if len(fk.roles) != 1 || fk.roles[0]["entity_id"] != "cp-1" || fk.roles[0]["role_name"] != "Viewer" ||
		fk.roles[0]["entity_type_name"] != "Mesh Control Planes" || fk.roles[0]["entity_region"] != "eu" {
		t.Fatalf("role assignments = %v, want one Viewer on cp-1", fk.roles)
	}
	if !strings.Contains(out, "spat_0123456789abcdefghijklmnop") {
		t.Errorf("token not printed:\n%s", out)
	}
	if !strings.Contains(out, "/v1/mesh/control-planes/cp-1/api") || strings.Contains(out, "cp-2") {
		t.Errorf("kumactl commands should cover cp-1 only:\n%s", out)
	}
	if strings.Contains(out, "kpat_admin_secret_value") {
		t.Error("the admin token must never be printed")
	}
	for _, a := range fk.auth {
		if a != "Bearer kpat_admin_secret_value" {
			t.Errorf("request sent with Authorization %q", a)
		}
	}
}

func TestTokenScriptRunsAllMeshCPs(t *testing.T) {
	fk := &fakeKonnect{}
	srv := httptest.NewServer(fk.handler(t))
	defer srv.Close()
	out, err := runScript(t, baseOpts(), srv.URL)
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	if len(fk.roles) != 2 {
		t.Fatalf("got %d role assignments, want 2: %v", len(fk.roles), fk.roles)
	}
}

func TestTokenScriptUnknownControlPlane(t *testing.T) {
	fk := &fakeKonnect{}
	srv := httptest.NewServer(fk.handler(t))
	defer srv.Close()
	o := baseOpts()
	o.ControlPlanes = []string{"missing"}
	out, err := runScript(t, o, srv.URL)
	if err == nil || !strings.Contains(out, "no Mesh control plane named missing") {
		t.Fatalf("want a failure naming the control plane, got err=%v\n%s", err, out)
	}
	for _, c := range fk.calls {
		if strings.HasSuffix(c, "/access-tokens") {
			t.Fatal("no token may be minted when a control plane is missing")
		}
	}
}

func TestTokenScriptRunsOrgScope(t *testing.T) {
	fk := &fakeKonnect{}
	srv := httptest.NewServer(fk.handler(t))
	defer srv.Close()
	o := baseOpts()
	o.Scope = ScopeOrg
	out, err := runScript(t, o, srv.URL)
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	joined := strings.Join(fk.calls, "\n")
	if !strings.Contains(joined, "POST /v3/teams/t-1/system-accounts") || strings.Contains(joined, "assigned-roles") {
		t.Fatalf("calls:\n%s", joined)
	}
	if !strings.Contains(out, "Added to Organization Admin (Read Only)") {
		t.Errorf("output:\n%s", out)
	}
}

func TestTokenScriptAPIErrorStops(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"Unauthorized"}`)
	}))
	defer srv.Close()
	out, err := runScript(t, baseOpts(), srv.URL)
	if err == nil || !strings.Contains(out, "HTTP 401") {
		t.Fatalf("want HTTP 401 failure, got err=%v\n%s", err, out)
	}
}

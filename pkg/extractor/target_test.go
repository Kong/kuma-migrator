package extractor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveKumactlTarget(t *testing.T) {
	cfg := `contexts:
- name: konnect
  controlPlane: k
- name: local
  controlPlane: l
controlPlanes:
- name: k
  coordinates:
    apiServer:
      url: https://eu.api.konghq.com/v1/mesh/control-planes/abc/api
      headers:
      - key: authorization
        value: Bearer kpat_x
- name: l
  coordinates:
    apiServer:
      url: http://localhost:5681/
currentContext: local
`
	p := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUMACTL_CONFIG", p)

	k, err := ResolveKumactlTarget("konnect")
	if err != nil {
		t.Fatal(err)
	}
	if !k.Konnect || k.Token != "kpat_x" || k.Address != "https://eu.api.konghq.com/v1/mesh/control-planes/abc" {
		t.Errorf("konnect target = %+v", k)
	}
	l, err := ResolveKumactlTarget("")
	if err != nil {
		t.Fatal(err)
	}
	if l.Konnect || l.Context != "local" || l.Address != "http://localhost:5681" || l.Token != "" {
		t.Errorf("current-context target = %+v", l)
	}
	if u := TargetFromURL("https://us.api.konghq.com/v1/mesh/control-planes/x/api/", "t"); !u.Konnect || u.Address != "https://us.api.konghq.com/v1/mesh/control-planes/x" {
		t.Errorf("TargetFromURL = %+v", u)
	}
}

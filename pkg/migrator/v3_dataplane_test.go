package migrator

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func scanObj(t *testing.T, input string, target TargetVersion) (map[string]interface{}, string, []string) {
	t.Helper()
	out, warnings := ScanForDeprecations([]byte(input), target)
	var obj map[string]interface{}
	if err := yaml.Unmarshal(out, &obj); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, out)
	}
	return obj, string(out), warnings
}

func joined(ws []string) string { return strings.Join(ws, "\n") }

func digPath(t *testing.T, v interface{}, path ...interface{}) interface{} {
	t.Helper()
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(map[string]interface{})
			if !ok {
				return nil
			}
			v = m[k]
		case int:
			s, ok := v.([]interface{})
			if !ok || k >= len(s) {
				return nil
			}
			v = s[k]
		}
	}
	return v
}

const universalGatewayDP = `type: Dataplane
mesh: default
name: gw-01
labels:
  kuma.io/gateway: "true"
networking:
  address: 192.168.0.1
  gateway:
    type: BUILTIN
    tags:
      kuma.io/service: edge-gateway
      region: eu
`

func TestFixDataplaneGateway_V3RemovesGatewayAndMovesTags(t *testing.T) {
	obj, out, ws := scanObj(t, universalGatewayDP, TargetV3)
	if digPath(t, obj, "networking", "gateway") != nil {
		t.Fatalf("networking.gateway must be removed under v3:\n%s", out)
	}
	if got := digPath(t, obj, "labels", "region"); got != "eu" {
		t.Errorf("gateway tag region must move to labels, got %v:\n%s", got, out)
	}
	if digPath(t, obj, "labels", "kuma.io/service") != nil {
		t.Errorf("reserved kuma.io/service must not become a label:\n%s", out)
	}
	if digPath(t, obj, "labels", "kuma.io/gateway") != nil {
		t.Errorf("kuma.io/gateway label must be removed under v3:\n%s", out)
	}
	j := joined(ws)
	for _, want := range []string{"networking.gateway (type BUILTIN)", "no inbounds", "--exclude-inbound-ports",
		"traffic.kuma.io/exclude-inbound-ports", "affinityTags", "kuma.io/service=edge-gateway", "fails to register"} {
		if !strings.Contains(j, want) {
			t.Errorf("expected %q in warnings, got: %v", want, ws)
		}
	}
	if strings.Contains(j, "rejected at parse time") {
		t.Errorf("old wording must be gone: %v", ws)
	}
}

func TestFixDataplaneGateway_V3Delegated(t *testing.T) {
	in := `type: Dataplane
mesh: default
name: kong-01
networking:
  address: 192.168.0.2
  gateway:
    type: DELEGATED
    tags:
      kuma.io/service: kong
`
	obj, _, ws := scanObj(t, in, TargetV3)
	if digPath(t, obj, "networking", "gateway") != nil {
		t.Errorf("DELEGATED gateway must be removed too")
	}
	if digPath(t, obj, "labels") != nil {
		t.Errorf("no labels map should be created when nothing moves, got %v", obj["labels"])
	}
	if !strings.Contains(joined(ws), "type DELEGATED") {
		t.Errorf("expected DELEGATED removal warning, got %v", ws)
	}
}

func TestFixDataplaneGateway_V2Unchanged(t *testing.T) {
	out, ws := ScanForDeprecations([]byte(universalGatewayDP), TargetV2)
	if string(out) != universalGatewayDP {
		t.Errorf("v2 output must be unchanged, got:\n%s", out)
	}
	if strings.Contains(joined(ws), "gateway") {
		t.Errorf("v2 must not warn on networking.gateway: %v", ws)
	}
}

func TestFixDataplaneGateway_KubernetesShape(t *testing.T) {
	in := `apiVersion: kuma.io/v1alpha1
kind: Dataplane
metadata:
  name: gw
  labels:
    kuma.io/gateway: "true"
spec:
  networking:
    address: 10.0.0.1
    gateway:
      type: DELEGATED
      tags:
        zone-group: a
`
	obj, out, _ := scanObj(t, in, TargetV3)
	if digPath(t, obj, "spec", "networking", "gateway") != nil {
		t.Errorf("spec.networking.gateway must be removed:\n%s", out)
	}
	if digPath(t, obj, "metadata", "labels", "zone-group") != "a" {
		t.Errorf("tag must move to metadata.labels:\n%s", out)
	}
	if digPath(t, obj, "metadata", "labels", "kuma.io/gateway") != nil {
		t.Errorf("kuma.io/gateway label must be removed:\n%s", out)
	}
}

func TestFixDataplaneInboundProtocolAndTags_V3(t *testing.T) {
	in := `type: Dataplane
mesh: default
name: web-01
labels:
  team: a
networking:
  address: 192.168.0.1
  inbound:
    - port: 8080
      tags:
        kuma.io/service: web
        kuma.io/protocol: http
        version: v1
    - port: 9090
      protocol: grpc
      tags:
        kuma.io/protocol: http
`
	obj, out, ws := scanObj(t, in, TargetV3)
	if got := digPath(t, obj, "networking", "inbound", 0, "protocol"); got != "http" {
		t.Errorf("protocol must be copied from kuma.io/protocol, got %v:\n%s", got, out)
	}
	if got := digPath(t, obj, "networking", "inbound", 1, "protocol"); got != "grpc" {
		t.Errorf("an explicit protocol must not be overwritten, got %v", got)
	}
	if digPath(t, obj, "networking", "inbound", 0, "tags") != nil {
		t.Errorf("inbound tags must be removed after the move:\n%s", out)
	}
	if digPath(t, obj, "labels", "version") != "v1" || digPath(t, obj, "labels", "team") != "a" {
		t.Errorf("labels must hold moved tags and keep existing ones:\n%s", out)
	}
	j := joined(ws)
	for _, want := range []string{"Auto-set inbound[].protocol", "inbound port 8080=http", "disagree",
		"version=v1", "kuma.io/service=web", "reserved"} {
		if !strings.Contains(j, want) {
			t.Errorf("expected %q in warnings, got: %v", want, ws)
		}
	}
}

func TestFixDataplaneInboundTags_ConflictKeepsTags(t *testing.T) {
	in := `type: Dataplane
mesh: default
name: multi
networking:
  address: 192.168.0.1
  inbound:
    - port: 8080
      tags:
        app: a
    - port: 8081
      tags:
        app: b
`
	obj, out, ws := scanObj(t, in, TargetV3)
	if digPath(t, obj, "labels", "app") != "a" {
		t.Errorf("first value wins:\n%s", out)
	}
	if digPath(t, obj, "networking", "inbound", 0, "tags") != nil {
		t.Errorf("non-conflicting inbound tags must be removed:\n%s", out)
	}
	if digPath(t, obj, "networking", "inbound", 1, "tags", "app") != "b" {
		t.Errorf("conflicting inbound must keep its tags:\n%s", out)
	}
	j := joined(ws)
	if !strings.Contains(j, "conflict") || !strings.Contains(j, "inbound port 8081") {
		t.Errorf("expected conflict warning naming the inbound, got: %v", ws)
	}
	// Residual warn-only check still fires on the left-over tags.
	if !strings.Contains(j, "1 inbound(s) declare networking.inbound[].tags") {
		t.Errorf("expected residual inbound-tags warning, got: %v", ws)
	}
}

func TestFixDataplaneInboundTags_V2Untouched(t *testing.T) {
	in := `type: Dataplane
mesh: default
name: web-01
networking:
  address: 192.168.0.1
  inbound:
    - port: 8080
      tags:
        kuma.io/service: web
        kuma.io/protocol: http
`
	out, _ := ScanForDeprecations([]byte(in), TargetV2)
	if string(out) != in {
		t.Errorf("v2 must not rewrite inbound tags/protocol, got:\n%s", out)
	}
}

func TestFixDataplaneOutboundTags_V3(t *testing.T) {
	in := `type: Dataplane
mesh: default
name: web-01
networking:
  address: 192.168.0.1
  outbound:
    - port: 3000
      tags:
        kuma.io/service: postgres_db_svc_5432
        version: v2
    - port: 3001
      tags:
        kuma.io/service: redis
    - port: 3002
      tags:
        kuma.io/service: x
      backendRef:
        kind: MeshService
        name: x
        port: 80
`
	obj, out, ws := scanObj(t, in, TargetV3)
	ref := digPath(t, obj, "networking", "outbound", 0, "backendRef")
	if digPath(t, ref, "kind") != "MeshService" || digPath(t, ref, "labels", labelDisplayName) != "postgres" ||
		digPath(t, ref, "labels", labelK8sNamespace) != "db" || digPath(t, ref, "port") != float64(5432) {
		t.Errorf("decodable outbound must become a backendRef, got %v:\n%s", ref, out)
	}
	if digPath(t, obj, "networking", "outbound", 0, "tags") != nil {
		t.Errorf("converted outbound must lose its tags:\n%s", out)
	}
	if digPath(t, obj, "networking", "outbound", 1, "tags", "kuma.io/service") != "redis" {
		t.Errorf("undecodable outbound must be left in place:\n%s", out)
	}
	if digPath(t, obj, "networking", "outbound", 2, "tags") != nil {
		t.Errorf("tags next to a backendRef must be removed:\n%s", out)
	}
	j := joined(ws)
	for _, want := range []string{"Auto-converted to backendRef", "version=v2", "name: redis, port: <MeshService port>",
		"already set backendRef"} {
		if !strings.Contains(j, want) {
			t.Errorf("expected %q in warnings, got: %v", want, ws)
		}
	}
}

func TestFixDataplaneReachableServices_V3(t *testing.T) {
	in := `type: Dataplane
mesh: default
name: web-01
networking:
  address: 192.168.0.1
  transparentProxying:
    reachableServices:
      - backend_demo_svc_8080
      - legacy-universal
    reachableBackends:
      refs:
        - kind: MeshService
          name: redis
          namespace: redis-system
          port: 6379
`
	obj, out, ws := scanObj(t, in, TargetV3)
	refs, _ := digPath(t, obj, "networking", "transparentProxying", "reachableBackends", "refs").([]interface{})
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %v:\n%s", refs, out)
	}
	if digPath(t, refs[0], "name") != nil || digPath(t, refs[0], "labels", labelDisplayName) != "redis" ||
		digPath(t, refs[0], "labels", labelK8sNamespace) != "redis-system" {
		t.Errorf("name/namespace must move to labels, got %v", refs[0])
	}
	if digPath(t, refs[1], "labels", labelDisplayName) != "backend" || digPath(t, refs[1], "labels", labelK8sNamespace) != "demo" ||
		digPath(t, refs[1], "port") != float64(8080) {
		t.Errorf("reachableServices entry must become a ref, got %v", refs[1])
	}
	left, _ := digPath(t, obj, "networking", "transparentProxying", "reachableServices").([]interface{})
	if len(left) != 1 || left[0] != "legacy-universal" {
		t.Errorf("undecodable entry must stay, got %v", left)
	}
	j := joined(ws)
	for _, want := range []string{"Auto-converted to reachableBackends", "could not be converted", "[legacy-universal]",
		"selects by labels only"} {
		if !strings.Contains(j, want) {
			t.Errorf("expected %q in warnings, got: %v", want, ws)
		}
	}
	// reachableBackends exists, so no "no outbounds" advisory.
	if strings.Contains(j, "without reachableBackends") {
		t.Errorf("unexpected missing-reachableBackends warning: %v", ws)
	}
}

func TestFixDataplaneReachableBackends_V2Advisory(t *testing.T) {
	in := `type: Dataplane
mesh: default
name: web-01
networking:
  address: 192.168.0.1
  transparentProxying:
    reachableBackends:
      refs:
        - kind: MeshService
          name: redis
`
	out, ws := ScanForDeprecations([]byte(in), TargetV2)
	if string(out) != in {
		t.Errorf("v2 must not rewrite refs, got:\n%s", out)
	}
	if !strings.Contains(joined(ws), "2.14 already accepts the labels form") {
		t.Errorf("expected v2 advisory, got %v", ws)
	}
}

func TestWarnDataplaneV3Removals(t *testing.T) {
	in := `type: Dataplane
mesh: default
name: web-01
networking:
  address: 10.0.0.1
  advertisedAddress: 1.2.3.4
  inbound:
    - port: 8080
      protocol: http
  transparentProxying:
    redirectPortInbound: 15006
    redirectPortOutbound: 15001
    ipFamilyMode: DualStack
probes:
  port: 9000
`
	_, ws := ScanForDeprecations([]byte(in), TargetV3)
	j := joined(ws)
	for _, want := range []string{"advertisedAddress (1.2.3.4)", "removed in 3.1", "--transparent-proxy",
		"ipFamilyMode is removed", "without reachableBackends", "restrictOutbound", "probes (virtual probes)"} {
		if !strings.Contains(j, want) {
			t.Errorf("expected %q in warnings, got: %v", want, ws)
		}
	}
	_, v2 := ScanForDeprecations([]byte(in), TargetV2)
	for _, w := range v2 {
		if strings.Contains(w, "advertisedAddress") || strings.Contains(w, "3.1") || strings.Contains(w, "probes") {
			t.Errorf("v3-only removal leaked into v2: %v", v2)
		}
	}
}

func TestDecodeServiceTagPort(t *testing.T) {
	cases := []struct {
		in       string
		name, ns string
		port     int
		ok       bool
	}{
		{"backend_demo_svc_8080", "backend", "demo", 8080, true},
		{"my_app_ns_svc_80", "my_app", "ns", 80, true},
		{"backend", "", "", 0, false},
		{"backend_svc_80", "", "", 0, false},
		{"backend_demo_svc_x", "", "", 0, false},
		{"backend_demo_svc_70000", "", "", 0, false},
	}
	for _, c := range cases {
		n, ns, p, ok := decodeServiceTagPort(c.in)
		if n != c.name || ns != c.ns || p != c.port || ok != c.ok {
			t.Errorf("decodeServiceTagPort(%q) = %q %q %d %v, want %q %q %d %v", c.in, n, ns, p, ok, c.name, c.ns, c.port, c.ok)
		}
	}
}

package migrator

import (
	"strings"
	"testing"
)

// Value-level fixes in the legacy conf conversions, each found by applying the
// converted output to Kong Mesh 2.14.7 / 3.0.1 (test/e2e). The inputs are the e2e
// fixtures; the comment above each test quotes the server error it regresses.

// ---- ProxyTemplate operations ------------------------------------------------

// MeshProxyPatch "custom-template" is invalid:
// spec.default.appendModifications[1].httpFilter.operation: Unsupported value: "addLast":
// supported values: "Remove", "Patch", "AddFirst", "AddBefore", "AddAfter", "AddLast"
func TestLegacyConf_ProxyTemplate_PositionalOperationsRecased(t *testing.T) {
	out, warnings := transformOne(t, `
apiVersion: kuma.io/v1alpha1
kind: ProxyTemplate
mesh: default
metadata:
  name: custom-template
spec:
  selectors:
  - match:
      kuma.io/service: backend_kuma-demo_svc_3001
  conf:
    modifications:
    - cluster:
        operation: patch
        match:
          name: outbound:127.0.0.1:5432
        value: |
          connectTimeout: 5s
    - httpFilter:
        operation: addLast
        match:
          listenerName: inbound:10.0.0.1:8080
        value: |
          name: envoy.filters.http.cors
    - httpFilter:
        operation: addFirst
        value: 'name: a'
    - networkFilter:
        operation: addBefore
        match: {name: envoy.filters.network.tcp_proxy}
        value: 'name: b'
    - networkFilter:
        operation: addAfter
        match: {name: envoy.filters.network.tcp_proxy}
        value: 'name: c'
    - httpFilter:
        operation: remove
        match: {name: envoy.filters.http.cors}
    - listener:
        operation: add
        value: 'name: l'
`)
	mods := at(t, out, "spec", "default", "appendModifications").([]interface{})
	want := []struct{ kind, op string }{
		{"cluster", "Patch"},
		{"httpFilter", "AddLast"},
		{"httpFilter", "AddFirst"},
		{"networkFilter", "AddBefore"},
		{"networkFilter", "AddAfter"},
		{"httpFilter", "Remove"},
		{"listener", "Add"},
	}
	if len(mods) != len(want) {
		t.Fatalf("appendModifications = %v, want %d entries", mods, len(want))
	}
	for i, w := range want {
		if got := at(t, mods[i].(map[string]interface{}), w.kind, "operation"); got != w.op {
			t.Errorf("appendModifications[%d].%s.operation = %v, want %s", i, w.kind, got, w.op)
		}
	}
	for _, w := range warnings {
		if strings.Contains(w, "operation") {
			t.Errorf("valid legacy operations must convert silently, got: %s", w)
		}
	}
}

// virtualHost takes a plain Add on MeshProxyPatch (Enum=Add;Remove;Patch), unlike
// the two filter kinds; rewriting it to AddLast would be rejected.
func TestLegacyConf_ProxyTemplate_VirtualHostKeepsAdd(t *testing.T) {
	out, warnings := transformOne(t, `
type: ProxyTemplate
name: pt
mesh: default
selectors:
  - match: {kuma.io/service: web_demo_svc_80}
conf:
  modifications:
    - virtualHost:
        operation: add
        value: 'name: vh'
    - httpFilter:
        operation: add
        value: 'name: f'
    - cluster:
        operation: addLast
        value: 'name: c'
    - cluster:
        operation: replace
        value: 'name: c'
`)
	mods := at(t, out, "spec", "default", "appendModifications").([]interface{})
	if got := at(t, mods[0].(map[string]interface{}), "virtualHost", "operation"); got != "Add" {
		t.Errorf("virtualHost.operation = %v, want Add", got)
	}
	if got := at(t, mods[1].(map[string]interface{}), "httpFilter", "operation"); got != "AddLast" {
		t.Errorf("httpFilter add → %v, want AddLast", got)
	}
	if !hasWarning(warnings, "modifications[1].httpFilter used operation \"add\"") {
		t.Errorf("expected the add → AddLast warning, got: %v", warnings)
	}
	if !hasWarning(warnings, "modifications[2].cluster.operation \"addLast\" is not accepted on a MeshProxyPatch cluster") {
		t.Errorf("expected a warning for a positional operation on cluster, got: %v", warnings)
	}
	if !hasWarning(warnings, "modifications[3].cluster.operation \"replace\" is not a ProxyTemplate operation") {
		t.Errorf("expected an unknown-operation warning, got: %v", warnings)
	}
	if hasWarning(warnings, "virtualHost") {
		t.Errorf("virtualHost add must convert silently, got: %v", warnings)
	}
}

// ---- Header names ------------------------------------------------------------

// The MeshHealthCheck "hc-web" is invalid:
// spec.to[0].default.http.requestHeadersToAdd.add[0].name: Invalid value: "Accept": ...
// in body should match '^[a-z0-9!#$%&'*+\-.^_\x60\|~]+$'
// * spec.to[0].default.http.requestHeadersToAdd.set[0].name: Invalid value: "Content-Type"
func TestLegacyConf_HealthCheck_LowercasesHeaderNames(t *testing.T) {
	out, warnings := transformOne(t, `
apiVersion: kuma.io/v1alpha1
kind: HealthCheck
mesh: default
metadata:
  name: hc-web
spec:
  sources:
  - match:
      kuma.io/service: frontend_kuma-demo_svc_8080
  destinations:
  - match:
      kuma.io/service: backend_kuma-demo_svc_3001
  conf:
    interval: 10s
    timeout: 2s
    unhealthyThreshold: 3
    healthyThreshold: 1
    http:
      path: /health
      requestHeadersToAdd:
      - append: false
        header:
          key: Content-Type
          value: application/json
      - header:
          key: Accept
          value: application/json
      - header:
          key: x-already-lower
          value: "1"
`)
	def := at(t, out, "spec", "to").([]interface{})[0].(map[string]interface{})["default"].(map[string]interface{})
	add := at(t, def, "http", "requestHeadersToAdd", "add").([]interface{})
	set := at(t, def, "http", "requestHeadersToAdd", "set").([]interface{})
	if got := set[0].(map[string]interface{})["name"]; got != "content-type" {
		t.Errorf("set[0].name = %v, want content-type", got)
	}
	if got := add[0].(map[string]interface{})["name"]; got != "accept" {
		t.Errorf("add[0].name = %v, want accept", got)
	}
	if got := add[1].(map[string]interface{})["name"]; got != "x-already-lower" {
		t.Errorf("add[1].name = %v, want x-already-lower", got)
	}
	for _, h := range append(add, set...) {
		name := h.(map[string]interface{})["name"].(string)
		if !headerNamePattern.MatchString(name) {
			t.Errorf("header name %q does not match the HeaderName schema", name)
		}
	}
	if !hasWarning(warnings, `"Content-Type" → "content-type", "Accept" → "accept"`) {
		t.Errorf("expected one warning listing both renames, got: %v", warnings)
	}
	if hasWarning(warnings, "x-already-lower") {
		t.Errorf("an already lower-case name must not be reported, got: %v", warnings)
	}
}

func TestLegacyConf_RateLimit_LowercasesHeaderNames(t *testing.T) {
	out, warnings := transformOne(t, `
type: RateLimit
name: rl
mesh: default
destinations:
  - match: {kuma.io/service: backend_demo_svc_3001}
conf:
  http:
    requests: 5
    interval: 10s
    onRateLimit:
      headers:
        - {key: X-RateLimited, value: "true"}
        - {key: "Bad Header", value: "x", append: false}
`)
	rule := at(t, out, "spec", "rules").([]interface{})[0].(map[string]interface{})
	add := at(t, rule, "default", "local", "http", "onRateLimit", "headers", "add").([]interface{})
	if got := add[0].(map[string]interface{})["name"]; got != "x-ratelimited" {
		t.Errorf("onRateLimit.headers.add[0].name = %v, want x-ratelimited", got)
	}
	if !hasWarning(warnings, `RateLimit "rl": conf.http.onRateLimit.headers header name(s) lower-cased`) {
		t.Errorf("expected lower-case warning, got: %v", warnings)
	}
	// A space is not a token character: lower-casing cannot save it.
	if !hasWarning(warnings, `"Bad Header" contain characters a header name cannot have`) {
		t.Errorf("expected an invalid-name warning, got: %v", warnings)
	}
}

// ---- Bandwidth units ---------------------------------------------------------

// The MeshFaultInjection "fi-backend" is invalid:
// spec.from[0].default.http.responseBandwidth[0].limit: must be in kbps/Mbps/Gbps units
func TestLegacyConf_FaultInjection_NormalisesBandwidthUnit(t *testing.T) {
	out, warnings := transformOne(t, `
apiVersion: kuma.io/v1alpha1
kind: FaultInjection
mesh: default
metadata:
  name: fi-backend
spec:
  sources:
  - match:
      kuma.io/service: frontend_kuma-demo_svc_8080
      kuma.io/protocol: http
  destinations:
  - match:
      kuma.io/service: backend_kuma-demo_svc_3001
      kuma.io/protocol: http
  conf:
    responseBandwidth:
      limit: 50 mbps
      percentage: 50
`)
	from := at(t, out, "spec", "from").([]interface{})[0].(map[string]interface{})
	fault := at(t, from, "default", "http").([]interface{})[0].(map[string]interface{})
	if got := at(t, fault, "responseBandwidth", "limit"); got != "50Mbps" {
		t.Errorf("responseBandwidth.limit = %v, want 50Mbps", got)
	}
	if !hasWarning(warnings, `conf.responseBandwidth.limit "50 mbps" written as "50Mbps"`) {
		t.Errorf("expected unit warning, got: %v", warnings)
	}
}

func TestNormalizeBandwidthLimit(t *testing.T) {
	cases := []struct {
		in       interface{}
		want     interface{}
		warnPart string
	}{
		{"50 mbps", "50Mbps", "written as"},
		{"10kbps", "10kbps", ""},
		{"10 kbps", "10kbps", "written as"},
		{"2gbps", "2Gbps", "written as"},
		{"1Gbps", "1Gbps", ""},
		{"100Mbps", "100Mbps", ""},
		{"1.5mbps", "1.5mbps", "copied unchanged"},
		{"fast", "fast", "copied unchanged"},
		{float64(50), float64(50), "not a string"},
	}
	for _, c := range cases {
		got, warnings := normalizeBandwidthLimit(c.in, "fi")
		if got != c.want {
			t.Errorf("normalizeBandwidthLimit(%v) = %v, want %v", c.in, got, c.want)
		}
		if c.warnPart == "" && len(warnings) != 0 {
			t.Errorf("normalizeBandwidthLimit(%v): unexpected warnings %v", c.in, warnings)
		}
		if c.warnPart != "" && !hasWarning(warnings, c.warnPart) {
			t.Errorf("normalizeBandwidthLimit(%v): want warning containing %q, got %v", c.in, c.warnPart, warnings)
		}
	}
}

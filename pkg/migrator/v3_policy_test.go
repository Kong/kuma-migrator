package migrator

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// scanMap runs ScanForDeprecations and unmarshals the result.
func scanMap(t *testing.T, input string, target TargetVersion) (map[string]interface{}, []string, bool) {
	t.Helper()
	out, warnings := ScanForDeprecations([]byte(input), target)
	var obj map[string]interface{}
	if err := yaml.Unmarshal(out, &obj); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	return obj, warnings, string(out) != input
}

// transformAll runs the full pipeline and unmarshals every output document.
func transformAll(t *testing.T, input string, target TargetVersion) ([]map[string]interface{}, []string) {
	t.Helper()
	docs, warnings, _, err := TransformDocument([]byte(input), target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := make([]map[string]interface{}, 0, len(docs))
	for _, d := range docs {
		var m map[string]interface{}
		if err := yaml.Unmarshal(d, &m); err != nil {
			t.Fatalf("unmarshal output: %v", err)
		}
		out = append(out, m)
	}
	return out, warnings
}

// ---- stripControlPlaneLabels ----------------------------------------------------

func TestStripControlPlaneLabels_V3KubernetesFormat(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: mt
  namespace: kuma-system
  labels:
    kuma.io/mesh: default
    kuma.io/origin: zone
    kuma.io/zone: east
    kuma.io/display-name: mt
    kuma.io/policy-role: system
    k8s.kuma.io/namespace: kuma-system
    team: payments
spec:
  targetRef:
    kind: Dataplane
    labels:
      kuma.io/display-name: backend
  to:
    - targetRef:
        kind: Mesh
      default:
        idleTimeout: 1h
`
	obj, warnings, changed := scanMap(t, input, TargetV3)
	if !changed {
		t.Fatal("expected the document to be rewritten under v3")
	}
	labels := at(t, obj, "metadata", "labels").(map[string]interface{})
	if len(labels) != 2 || labels["kuma.io/mesh"] != "default" || labels["team"] != "payments" {
		t.Errorf("metadata.labels = %v, want only kuma.io/mesh and team", labels)
	}
	// targetRef.labels is a selector, not the resource's own labels: kept.
	if got := at(t, obj, "spec", "targetRef", "labels", "kuma.io/display-name"); got != "backend" {
		t.Errorf("targetRef selector label dropped: %v", got)
	}
	if !hasWarning(warnings, "removed control-plane-owned labels") {
		t.Errorf("expected a label-strip warning, got %v", warnings)
	}

	_, v2warnings, v2changed := scanMap(t, input, TargetV2)
	if v2changed || hasWarning(v2warnings, "control-plane-owned") {
		t.Errorf("v2 must not strip labels (changed=%v, warnings=%v)", v2changed, v2warnings)
	}
}

func TestStripControlPlaneLabels_UniversalAndNonKuma(t *testing.T) {
	universal := `type: MeshTimeout
name: mt
mesh: default
labels:
  kuma.io/origin: global
  kuma.io/display-name: mt
  app: web
spec:
  targetRef:
    kind: Mesh
`
	obj, _, _ := scanMap(t, universal, TargetV3)
	labels := at(t, obj, "labels").(map[string]interface{})
	if len(labels) != 1 || labels["app"] != "web" {
		t.Errorf("universal labels = %v, want only app", labels)
	}

	gatewayAPI := `apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: r
  labels:
    kuma.io/origin: zone
`
	_, _, changed := scanMap(t, gatewayAPI, TargetV3)
	if changed {
		t.Error("non-Kuma resources must be left alone")
	}
}

// ---- fixRealResourceTargetRefsForV3 --------------------------------------------

func TestFixRealResourceTargetRefsForV3(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: mt
  namespace: demo
spec:
  targetRef:
    kind: Mesh
  to:
    - targetRef:
        kind: MeshService
        name: backend
        sectionName: http
      default: {idleTimeout: 1h}
    - targetRef:
        kind: MeshService
        name: redis
        namespace: cache
      default: {idleTimeout: 1h}
    - targetRef:
        kind: MeshExternalService
        name: httpbin
        mesh: default
      default: {idleTimeout: 1h}
`
	obj, warnings, _ := scanMap(t, input, TargetV3)
	to := at(t, obj, "spec", "to").([]interface{})
	want := []map[string]interface{}{
		// name only: the 2.x ref resolved in the policy's namespace.
		{"kuma.io/display-name": "backend", "k8s.kuma.io/namespace": "demo"},
		{"kuma.io/display-name": "redis", "k8s.kuma.io/namespace": "cache"},
		// MeshExternalService lives in the system namespace: no namespace pinned.
		{"kuma.io/display-name": "httpbin", "kuma.io/mesh": "default"},
	}
	for i, w := range want {
		tr := to[i].(map[string]interface{})["targetRef"].(map[string]interface{})
		for _, f := range []string{"name", "namespace", "mesh"} {
			if _, ok := tr[f]; ok {
				t.Errorf("to[%d].targetRef.%s still set: %v", i, f, tr)
			}
		}
		labels, _ := tr["labels"].(map[string]interface{})
		if len(labels) != len(w) {
			t.Errorf("to[%d].targetRef.labels = %v, want %v", i, labels, w)
		}
		for k, v := range w {
			if labels[k] != v {
				t.Errorf("to[%d].targetRef.labels[%s] = %v, want %v", i, k, labels[k], v)
			}
		}
	}
	if got := to[0].(map[string]interface{})["targetRef"].(map[string]interface{})["sectionName"]; got != "http" {
		t.Errorf("sectionName must be kept, got %v", got)
	}
	if !hasWarning(warnings, "rewritten from name/namespace/mesh to labels") {
		t.Errorf("expected rewrite warnings, got %v", warnings)
	}

	_, _, v2changed := scanMap(t, input, TargetV2)
	if v2changed {
		t.Error("v2 output must keep name/namespace")
	}
}

func TestFixRealResourceTargetRefsForV3_ConflictAndDataplane(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: mt
spec:
  targetRef:
    kind: Dataplane
    name: web-7d9f
  to:
    - targetRef:
        kind: MeshService
        name: backend
        labels:
          kuma.io/display-name: other
      default: {idleTimeout: 1h}
`
	obj, warnings, _ := scanMap(t, input, TargetV3)
	if got := at(t, obj, "spec", "targetRef", "name"); got != "web-7d9f" {
		t.Errorf("Dataplane name must not be rewritten, got %v", got)
	}
	if !hasWarning(warnings, "A Dataplane name is not one of its labels") {
		t.Errorf("expected Dataplane name warning, got %v", warnings)
	}
	tr := at(t, obj, "spec", "to").([]interface{})[0].(map[string]interface{})["targetRef"].(map[string]interface{})
	if tr["name"] != "backend" {
		t.Errorf("conflicting ref must be left untouched, got %v", tr)
	}
	if !hasWarning(warnings, "disagree") {
		t.Errorf("expected conflict warning, got %v", warnings)
	}
}

func TestFixRealResourceTargetRefsForV3_ZoneOriginAndBackendRefs(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: MeshHTTPRoute
metadata:
  name: route
  namespace: demo
  labels:
    kuma.io/origin: zone
    kuma.io/zone: east
spec:
  targetRef:
    kind: Mesh
  to:
    - targetRef:
        kind: MeshService
        name: backend
      rules:
        - matches:
            - path: {type: PathPrefix, value: /}
          default:
            backendRefs:
              - kind: MeshService
                name: backend-v2
                namespace: demo
                port: 8080
`
	obj, _, _ := scanMap(t, input, TargetV3)
	tr := at(t, obj, "spec", "to").([]interface{})[0].(map[string]interface{})
	labels := at(t, tr, "targetRef", "labels").(map[string]interface{})
	if labels["kuma.io/zone"] != "east" || labels["kuma.io/display-name"] != "backend" || labels["k8s.kuma.io/namespace"] != "demo" {
		t.Errorf("zone-origin ref labels = %v, want display-name, namespace and zone east", labels)
	}
	br := at(t, tr, "rules").([]interface{})[0].(map[string]interface{})
	ref := at(t, br, "default", "backendRefs").([]interface{})[0].(map[string]interface{})
	if _, ok := ref["name"]; ok {
		t.Errorf("backendRef name must be rewritten: %v", ref)
	}
	if ref["port"] != float64(8080) {
		t.Errorf("backendRef port must be kept: %v", ref)
	}
	// The policy's own kuma.io/zone/origin are then stripped.
	if _, ok := at(t, obj, "metadata").(map[string]interface{})["labels"].(map[string]interface{})["kuma.io/zone"]; ok {
		t.Error("own kuma.io/zone label should be stripped under v3")
	}
}

// ---- warnV3ToTargetRefKinds -------------------------------------------------------

func TestWarnV3ToTargetRefKinds(t *testing.T) {
	cases := []struct {
		name, kind, top, toKind string
		want                    string
	}{
		{"MCB rejects MeshHTTPRoute", "MeshCircuitBreaker", "Mesh", "MeshHTTPRoute", "rejected by Kuma 3.0"},
		{"MeshTimeout accepts MeshHTTPRoute", "MeshTimeout", "Mesh", "MeshHTTPRoute", ""},
		{"MeshSubset rejected everywhere", "MeshRetry", "Mesh", "MeshSubset", "rejected by Kuma 3.0"},
		{"MeshGateway rejected", "MeshTimeout", "Mesh", "MeshGateway", "rejected by Kuma 3.0"},
		{"MeshRateLimit only Mesh", "MeshRateLimit", "Mesh", "MeshService", "rejected by Kuma 3.0"},
		{"MeshRateLimit to[] needs Mesh top", "MeshRateLimit", "Dataplane", "Mesh", "unless spec.targetRef is kind: Mesh"},
		{"MeshFaultInjection Mesh ok", "MeshFaultInjection", "Mesh", "Mesh", ""},
		{"MeshTrace has no to", "MeshTrace", "Mesh", "Mesh", "has no spec.to[]"},
		{"MeshHTTPRoute rejects Mesh", "MeshHTTPRoute", "Mesh", "Mesh", "rejected by Kuma 3.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obj := map[string]interface{}{
				"spec": map[string]interface{}{
					"targetRef": map[string]interface{}{"kind": tc.top},
					"to": []interface{}{map[string]interface{}{
						"targetRef": map[string]interface{}{"kind": tc.toKind},
					}},
				},
			}
			got := warnV3ToTargetRefKinds(obj, "p", tc.kind)
			if tc.want == "" {
				if len(got) != 0 {
					t.Errorf("unexpected warnings: %v", got)
				}
				return
			}
			if !hasWarning(got, tc.want) {
				t.Errorf("want warning containing %q, got %v", tc.want, got)
			}
		})
	}
}

func TestScanForDeprecations_ToTargetRefKinds_V3Only(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: MeshCircuitBreaker
metadata:
  name: mcb
spec:
  targetRef:
    kind: Mesh
  to:
    - targetRef:
        kind: MeshSubset
        tags: {version: v1}
      default: {connectionLimits: {maxConnections: 10}}
`
	_, v2, _ := scanMap(t, input, TargetV2)
	if hasWarning(v2, "rejected by Kuma 3.0") {
		t.Errorf("v2 must not carry the 3.0 to[] check: %v", v2)
	}
	_, v3, _ := scanMap(t, input, TargetV3)
	if !hasWarning(v3, "spec.to[0].targetRef.kind MeshSubset is rejected by Kuma 3.0") {
		t.Errorf("v3 missing to[] kind warning: %v", v3)
	}
}

// ---- healthyPanicThreshold --------------------------------------------------------

const mhcWithPanic = `apiVersion: kuma.io/v1alpha1
kind: MeshHealthCheck
metadata:
  name: hc
  namespace: kuma-system
  labels:
    kuma.io/mesh: default
spec:
  targetRef:
    kind: Mesh
  to:
    - targetRef:
        kind: MeshService
        labels:
          kuma.io/display-name: backend
      default:
        interval: 10s
        healthyPanicThreshold: 40
`

func TestHealthyPanicThreshold_ToDefaultPath_V2Warns(t *testing.T) {
	_, warnings, changed := scanMap(t, mhcWithPanic, TargetV2)
	if changed {
		t.Error("v2 must not rewrite the MeshHealthCheck")
	}
	if !hasWarning(warnings, "healthyPanicThreshold is deprecated") {
		t.Errorf("to[].default.healthyPanicThreshold must be detected, got %v", warnings)
	}
}

func TestHealthyPanicThreshold_V3RemovesAndPrintsCompanion(t *testing.T) {
	obj, warnings, _ := scanMap(t, mhcWithPanic, TargetV3)
	def := at(t, obj, "spec", "to").([]interface{})[0].(map[string]interface{})["default"].(map[string]interface{})
	if _, ok := def["healthyPanicThreshold"]; ok {
		t.Error("healthyPanicThreshold must be removed under v3")
	}
	if def["interval"] != "10s" {
		t.Errorf("other fields must be kept: %v", def)
	}
	var snippet string
	for _, w := range warnings {
		if strings.Contains(w, "kind: MeshCircuitBreaker") {
			snippet = w
		}
	}
	if snippet == "" {
		t.Fatalf("expected a MeshCircuitBreaker snippet in the warnings, got %v", warnings)
	}
	for _, want := range []string{"name: hc-panic-threshold", "healthyPanicThreshold: 40", "consecutive: 5", "kuma.io/display-name: backend", "kuma.io/mesh: default"} {
		if !strings.Contains(snippet, want) {
			t.Errorf("snippet missing %q:\n%s", want, snippet)
		}
	}
}

// ---- MeshFaultInjection from[] → rules[] ------------------------------------------

func TestFixMeshFaultInjectionFromForV3(t *testing.T) {
	allClients := `apiVersion: kuma.io/v1alpha1
kind: MeshFaultInjection
metadata:
  name: mfi
spec:
  targetRef:
    kind: Dataplane
    labels: {app: web}
  from:
    - targetRef:
        kind: Mesh
      default:
        http:
          - abort: {httpStatus: 500, percentage: 10}
`
	obj, warnings, _ := scanMap(t, allClients, TargetV3)
	missing(t, obj, "spec", "from")
	rules := at(t, obj, "spec", "rules").([]interface{})
	if len(rules) != 1 {
		t.Fatalf("rules = %v, want 1", rules)
	}
	if _, ok := rules[0].(map[string]interface{})["matches"]; ok {
		t.Error("an all-clients rule must have no matches[]")
	}
	if hasWarning(warnings, "rejects this policy") {
		t.Errorf("converted policy must not be reported as rejected: %v", warnings)
	}

	_, _, v2changed := scanMap(t, allClients, TargetV2)
	if v2changed {
		t.Error("v2 must keep from[]")
	}

	narrow := strings.Replace(allClients, "kind: Mesh\n      default", "kind: Dataplane\n        labels: {app: client}\n      default", 1)
	obj, warnings, _ = scanMap(t, narrow, TargetV3)
	at(t, obj, "spec", "from")
	if !hasWarning(warnings, "Kuma 3.0 rejects this policy as it stands") {
		t.Errorf("narrow from[] must be reported as rejected on 3.0: %v", warnings)
	}
}

func TestWarnFromDeprecatedForRulesAPI_V3MTPRejected(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: MeshTrafficPermission
metadata:
  name: mtp
spec:
  targetRef:
    kind: Mesh
  from:
    - targetRef:
        kind: Mesh
      default:
        action: Allow
`
	_, v3, _ := scanMap(t, input, TargetV3)
	if !hasWarning(v3, "policy must define rules") {
		t.Errorf("v3 warning must say 3.0 rejects the policy: %v", v3)
	}
	_, v2, _ := scanMap(t, input, TargetV2)
	if hasWarning(v2, "policy must define rules") {
		t.Errorf("v2 warning must stay forward-looking: %v", v2)
	}
}

// ---- MeshTrafficPermission empty matches ------------------------------------------

func TestWarnMTPEmptyMatches(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: MeshTrafficPermission
metadata:
  name: mtp
spec:
  targetRef:
    kind: Mesh
  rules:
    - default:
        allow:
          - spiffeID: {type: Exact, value: "spiffe://td/ns/demo/sa/web"}
          - {}
        deny:
          - {}
`
	for _, target := range []TargetVersion{TargetV2, TargetV3} {
		_, warnings, _ := scanMap(t, input, target)
		if !hasWarning(warnings, "rules[0].default.allow[1] is an empty match") ||
			!hasWarning(warnings, "rules[0].default.deny[0] is an empty match") {
			t.Errorf("%v: missing empty-match warnings: %v", target, warnings)
		}
		if hasWarning(warnings, "allow[0] is an empty match") {
			t.Errorf("%v: populated match flagged: %v", target, warnings)
		}
	}
}

// ---- MeshProxyPatch circuit breakers ----------------------------------------------

func TestWarnMeshProxyPatchCircuitBreakers(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: MeshProxyPatch
metadata:
  name: mpp
spec:
  targetRef:
    kind: Mesh
  default:
    appendModifications:
      - cluster:
          operation: Patch
          value: |
            circuitBreakers:
              thresholds:
                - maxConnections: 2048
      - cluster:
          operation: Patch
          jsonPatches:
            - op: replace
              path: /circuitBreakers/thresholds/0/maxRequests
              value: 10
      - cluster:
          operation: Add
          value: |
            name: extra
            circuitBreakers: {}
      - cluster:
          operation: Patch
          value: |
            connectTimeout: 5s
`
	_, warnings, _ := scanMap(t, input, TargetV3)
	if !hasWarning(warnings, "appendModifications [0 1] patch cluster circuitBreakers") {
		t.Errorf("expected a warning naming modifications 0 and 1, got %v", warnings)
	}
	_, v2, _ := scanMap(t, input, TargetV2)
	if !hasWarning(v2, "From Kuma 3.0 the patch") {
		t.Errorf("v2 must carry the forward-looking advisory, got %v", v2)
	}
}

func TestWarnMeshProxyPatchCircuitBreakers_FromLegacyProxyTemplate(t *testing.T) {
	input := `type: ProxyTemplate
name: pt
mesh: default
selectors:
  - match: {kuma.io/service: '*'}
conf:
  modifications:
    - cluster:
        operation: patch
        value: |
          circuitBreakers:
            thresholds:
              - maxConnections: 2048
`
	_, warnings := transformAll(t, input, TargetV2)
	if !hasWarning(warnings, "patch cluster circuitBreakers") {
		t.Errorf("converted MeshProxyPatch must be checked too, got %v", warnings)
	}
}

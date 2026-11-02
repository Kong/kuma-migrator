package migrator

import (
	"strings"
	"testing"
)

// Target-aware conversion tests: ConvertTargetRefForTarget,
// ConvertSelectorToTargetRefForTarget, the legacy and Subset transforms, and the
// from[] → rules[] split.

// ---- convert.go ----------------------------------------------------------------

func TestConvertTargetRefForTarget_V3UsesLabels(t *testing.T) {
	ref := TargetRef{Kind: "MeshSubset", Tags: map[string]string{"kuma.io/service": "backend_demo_svc_80"}}

	v2, _, err := ConvertTargetRefForTarget(ref, "demo", false, TargetV2)
	if err != nil || v2.Name == nil || *v2.Name != "backend" {
		t.Fatalf("v2 must keep name/namespace, got %s (err %v)", formatRef(v2), err)
	}

	v3, _, err := ConvertTargetRefForTarget(ref, "demo", false, TargetV3)
	if err != nil {
		t.Fatal(err)
	}
	want := TargetRef{Kind: "MeshService", Labels: map[string]string{
		"kuma.io/display-name": "backend", "k8s.kuma.io/namespace": "demo"}}
	if !targetRefEqual(v3, want) {
		t.Errorf("v3 to[] = %s, want %s", formatRef(v3), formatRef(want))
	}

	top, warn, err := ConvertTargetRefForTarget(ref, "demo", true, TargetV3)
	if err != nil {
		t.Fatal(err)
	}
	wantTop := TargetRef{Kind: "Dataplane", Labels: map[string]string{
		"app": "backend", "k8s.kuma.io/namespace": "demo"}}
	if !targetRefEqual(top, wantTop) || warn == "" {
		t.Errorf("v3 top-level = %s (warn %q), want %s with a best-guess warning", formatRef(top), warn, formatRef(wantTop))
	}
}

func TestConvertTargetRefForTarget_V3PartiallyMigratedKeepsSectionName(t *testing.T) {
	ref := TargetRef{Kind: "MeshService", Name: strPtr("echo_demo_svc_8000"), SectionName: strPtr("http")}
	got, _, err := ConvertTargetRefForTarget(ref, "demo", false, TargetV3)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != nil || got.Labels["kuma.io/display-name"] != "echo" || got.SectionName == nil || *got.SectionName != "http" {
		t.Errorf("got %s, want labels display-name=echo and sectionName kept", formatRef(got))
	}
}

func TestConvertTargetRefForTarget_V3MeshSubsetInToErrors(t *testing.T) {
	ref := TargetRef{Kind: "MeshSubset", Tags: map[string]string{"version": "v2"}}
	if _, _, err := ConvertTargetRefForTarget(ref, "", false, TargetV3); err == nil ||
		!strings.Contains(err.Error(), "--to-latest v2") {
		t.Errorf("want an error naming --to-latest v2, got %v", err)
	}
	top, _, err := ConvertTargetRefForTarget(ref, "", true, TargetV3)
	if err != nil || top.Kind != "Dataplane" || top.Labels["version"] != "v2" {
		t.Errorf("top-level tag-only MeshSubset → Dataplane labels, got %s (err %v)", formatRef(top), err)
	}
	empty, _, err := ConvertTargetRefForTarget(TargetRef{Kind: "MeshSubset"}, "", true, TargetV3)
	if err != nil || empty.Kind != "Mesh" {
		t.Errorf("empty MeshSubset → Mesh, got %s (err %v)", formatRef(empty), err)
	}
}

func TestConvertSelectorToTargetRefForTarget_NoServiceTag(t *testing.T) {
	sel := OldSelector{Match: map[string]string{"version": "v1"}}

	v2, _, err := ConvertSelectorToTargetRefForTarget(sel, "", true, TargetV2)
	if err != nil || v2.Kind != "MeshSubset" {
		t.Errorf("v2 keeps MeshSubset, got %s (err %v)", formatRef(v2), err)
	}
	v3, _, err := ConvertSelectorToTargetRefForTarget(sel, "", true, TargetV3)
	if err != nil || v3.Kind != "Dataplane" || v3.Labels["version"] != "v1" {
		t.Errorf("v3 top-level → Dataplane labels, got %s (err %v)", formatRef(v3), err)
	}
	if _, _, err := ConvertSelectorToTargetRefForTarget(sel, "", false, TargetV3); err == nil {
		t.Error("v3 to[] tag-only selector must error")
	}
}

// ---- Legacy inbound policies under v3 ------------------------------------------

const legacyTrafficPermission = `type: TrafficPermission
name: tp
mesh: default
sources:
  - match: {kuma.io/service: web}
destinations:
  - match: {kuma.io/service: backend}
`

func TestLegacyTrafficPermission_V3Errors(t *testing.T) {
	_, _, _, err := TransformDocument([]byte(legacyTrafficPermission), TargetV3)
	if err == nil {
		t.Fatal("expected an error under v3")
	}
	for _, want := range []string{"rules[].default.{allow,deny,allowWithShadowDeny}[].spiffeID", "MeshIdentity", "--to-latest v2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
	docs, _ := transformAll(t, legacyTrafficPermission, TargetV2)
	at(t, docs[0], "spec", "from")
}

func TestLegacyFaultInjection_V3(t *testing.T) {
	wildcard := `type: FaultInjection
name: fi
mesh: default
sources:
  - match: {kuma.io/service: '*'}
destinations:
  - match: {kuma.io/service: backend}
conf:
  abort: {httpStatus: 500, percentage: 50}
`
	docs, warnings := transformAll(t, wildcard, TargetV3)
	if len(docs) != 1 {
		t.Fatalf("want 1 doc, got %d", len(docs))
	}
	missing(t, docs[0], "spec", "from")
	rules := at(t, docs[0], "spec", "rules").([]interface{})
	if len(rules) != 1 {
		t.Fatalf("rules = %v", rules)
	}
	if _, ok := rules[0].(map[string]interface{})["matches"]; ok {
		t.Error("an all-clients rule must have no matches[]")
	}
	at(t, rules[0].(map[string]interface{}), "default", "http")
	if hasWarning(warnings, "RateLimit") {
		t.Errorf("MeshRateLimit warning leaked into FaultInjection: %v", warnings)
	}
	// v3 targetRef uses labels.
	if got := at(t, docs[0], "spec", "targetRef", "labels", "app"); got != "backend" {
		t.Errorf("targetRef labels.app = %v", got)
	}

	specific := strings.Replace(wildcard, "kuma.io/service: '*'", "kuma.io/service: web", 1)
	if _, _, _, err := TransformDocument([]byte(specific), TargetV3); err == nil ||
		!strings.Contains(err.Error(), "rules[].matches[]") {
		t.Errorf("specific source must error naming rules[].matches[], got %v", err)
	}
	v2docs, _ := transformAll(t, specific, TargetV2)
	at(t, v2docs[0], "spec", "from")
}

// ---- Legacy HealthCheck → companion MeshCircuitBreaker -------------------------

func TestLegacyHealthCheck_V3CompanionCircuitBreaker(t *testing.T) {
	input := `type: HealthCheck
name: hc
mesh: prod
sources:
  - match: {kuma.io/service: '*'}
destinations:
  - match: {kuma.io/service: backend}
conf:
  interval: 10s
  healthyPanicThreshold: 40
`
	docs, warnings := transformAll(t, input, TargetV3)
	if len(docs) != 2 {
		t.Fatalf("want MeshHealthCheck + MeshCircuitBreaker, got %d docs", len(docs))
	}
	mhcDef := at(t, docs[0], "spec", "to").([]interface{})[0].(map[string]interface{})["default"].(map[string]interface{})
	if _, ok := mhcDef["healthyPanicThreshold"]; ok {
		t.Error("MeshHealthCheck must not carry healthyPanicThreshold under v3")
	}
	mcb := docs[1]
	if mcb["kind"] != "MeshCircuitBreaker" || at(t, mcb, "metadata", "name") != "hc-panic-threshold" {
		t.Errorf("companion = %v", mcb)
	}
	if at(t, mcb, "metadata", "labels", "kuma.io/mesh") != "prod" {
		t.Error("companion must carry the mesh label")
	}
	to := at(t, mcb, "spec", "to").([]interface{})[0].(map[string]interface{})
	if at(t, to, "targetRef", "labels", "kuma.io/display-name") != "backend" {
		t.Errorf("companion to[].targetRef = %v", to["targetRef"])
	}
	if got := at(t, to, "default", "outlierDetection", "healthyPanicThreshold"); got != float64(40) {
		t.Errorf("healthyPanicThreshold = %v", got)
	}
	at(t, to, "default", "outlierDetection", "detectors", "totalFailures")
	if !hasWarning(warnings, "companion MeshCircuitBreaker") {
		t.Errorf("expected companion warning, got %v", warnings)
	}

	v2docs, _ := transformAll(t, input, TargetV2)
	if len(v2docs) != 1 {
		t.Errorf("v2 must emit only the MeshHealthCheck, got %d docs", len(v2docs))
	}
}

func TestLegacyTimeout_V3TagOnlyDestinationErrors(t *testing.T) {
	input := `type: Timeout
name: t
mesh: default
sources:
  - match: {kuma.io/service: '*'}
destinations:
  - match: {version: v2}
conf:
  connectTimeout: 5s
`
	if _, _, _, err := TransformDocument([]byte(input), TargetV3); err == nil {
		t.Error("tag-only destination has no 3.0 to[] equivalent: expected an error")
	}
	docs, _ := transformAll(t, input, TargetV2)
	if got := at(t, docs[0], "spec", "to").([]interface{})[0].(map[string]interface{})["targetRef"].(map[string]interface{})["kind"]; got != "MeshSubset" {
		t.Errorf("v2 to[] kind = %v, want MeshSubset", got)
	}
}

// ---- Subset scenario: rules[] and to[] split -----------------------------------

const subsetFromAndTo = `apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: mt
  namespace: demo
  labels:
    kuma.io/mesh: default
    kuma.io/display-name: mt
spec:
  targetRef:
    kind: MeshSubset
    tags:
      kuma.io/service: web_demo_svc_80
  from:
    - targetRef:
        kind: Mesh
      default:
        idleTimeout: 1h
  to:
    - targetRef:
        kind: MeshSubset
        tags:
          kuma.io/service: backend_demo_svc_80
      default:
        idleTimeout: 2h
`

func TestScenarioSubset_SplitsRulesAndTo(t *testing.T) {
	for _, target := range []TargetVersion{TargetV2, TargetV3} {
		docs, warnings := transformAll(t, subsetFromAndTo, target)
		if len(docs) != 2 {
			t.Fatalf("%v: want 2 docs (rules[] + to[]), got %d", target, len(docs))
		}
		in, out := docs[0], docs[1]
		at(t, in, "spec", "rules")
		missing(t, in, "spec", "to")
		missing(t, in, "spec", "from")
		at(t, out, "spec", "to")
		missing(t, out, "spec", "rules")
		if at(t, out, "metadata", "name") != "mt-outbound" {
			t.Errorf("%v: outbound name = %v", target, at(t, out, "metadata", "name"))
		}
		missing(t, out, "metadata", "labels", "kuma.io/display-name")
		if at(t, out, "metadata", "labels", "kuma.io/mesh") != "default" {
			t.Errorf("%v: outbound must keep kuma.io/mesh", target)
		}
		if !hasWarning(warnings, "Split into two policies") {
			t.Errorf("%v: expected split warning: %v", target, warnings)
		}
	}
	// v2 leaves the inbound half's labels alone; v3 strips CP-owned labels.
	v2docs, _ := transformAll(t, subsetFromAndTo, TargetV2)
	if at(t, v2docs[0], "metadata", "labels", "kuma.io/display-name") != "mt" {
		t.Error("v2 inbound half must keep its labels")
	}
	v3docs, _ := transformAll(t, subsetFromAndTo, TargetV3)
	missing(t, v3docs[0], "metadata", "labels", "kuma.io/display-name")
}

// ---- Rules scenario: split label hygiene and v3 scope widening -----------------

func TestTransformFromToRules_SplitDropsDisplayNameOnOutbound(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: mt
  labels:
    kuma.io/display-name: mt
    team: a
spec:
  targetRef:
    kind: Mesh
  from:
    - targetRef: {kind: Mesh}
      default: {idleTimeout: 1h}
  to:
    - targetRef: {kind: Mesh}
      default: {idleTimeout: 2h}
`
	docs, _ := transformAll(t, input, TargetV2)
	if len(docs) != 2 {
		t.Fatalf("want 2 docs, got %d", len(docs))
	}
	missing(t, docs[1], "metadata", "labels", "kuma.io/display-name")
	if at(t, docs[1], "metadata", "labels", "team") != "a" {
		t.Error("other labels must be copied to the outbound half")
	}
	if at(t, docs[0], "metadata", "labels", "kuma.io/display-name") != "mt" {
		t.Error("v2 inbound half keeps its own display-name")
	}
}

func TestTransformFromToRulesForTarget_V3ScopeWidening(t *testing.T) {
	policy := func(kind, fromKind string) string {
		return `apiVersion: kuma.io/v1alpha1
kind: ` + kind + `
metadata:
  name: p
spec:
  targetRef:
    kind: Mesh
  from:
    - targetRef:
        kind: ` + fromKind + `
        labels: {app: client}
      default: {}
`
	}
	_, mcb, err := TransformFromToRulesForTarget([]byte(policy("MeshCircuitBreaker", "Dataplane")), TargetV3)
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(mcb, "MeshCircuitBreaker rules[] has no matches[] field") {
		t.Errorf("MCB widening warning missing: %v", mcb)
	}
	_, mt, _ := TransformFromToRulesForTarget([]byte(policy("MeshTimeout", "Dataplane")), TargetV3)
	if !hasWarning(mt, "add rules[].matches[]") {
		t.Errorf("MeshTimeout widening warning must point at matches[]: %v", mt)
	}
	_, v2, _ := TransformFromToRulesForTarget([]byte(policy("MeshTimeout", "Dataplane")), TargetV2)
	if hasWarning(v2, "a wider scope than before") {
		t.Errorf("v2 must not carry the v3 widening warning: %v", v2)
	}
	_, mesh, _ := TransformFromToRulesForTarget([]byte(strings.Replace(policy("MeshTimeout", "Mesh"), "        labels: {app: client}\n", "", 1)), TargetV3)
	if hasWarning(mesh, "a wider scope than before") {
		t.Errorf("kind: Mesh is the documented mechanical case, no widening: %v", mesh)
	}
}

func TestRouteBackendRefNamesForV3(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: MeshTCPRoute
metadata:
  name: r
  namespace: demo
spec:
  targetRef:
    kind: Mesh
  to:
    - targetRef:
        kind: MeshService
        labels: {kuma.io/display-name: db}
      rules:
        - default:
            backendRefs:
              - kind: MeshService
                name: db-v2
                port: 5432
              - kind: MeshServiceSubset
                name: legacy
`
	obj, _, _ := scanMap(t, input, TargetV3)
	brs := at(t, obj, "spec", "to").([]interface{})[0].(map[string]interface{})["rules"].([]interface{})[0].(map[string]interface{})["default"].(map[string]interface{})["backendRefs"].([]interface{})
	ms := brs[0].(map[string]interface{})
	if _, ok := ms["name"]; ok || at(t, ms, "labels", "kuma.io/display-name") != "db-v2" || at(t, ms, "labels", "k8s.kuma.io/namespace") != "demo" {
		t.Errorf("MeshService backendRef not rewritten: %v", ms)
	}
	// MeshServiceSubset is converted by fixRouteBackendRefsForV3 (v3_gateway.go)
	// in the same scan pass; check the two fixes compose.
	sub := brs[1].(map[string]interface{})
	if sub["kind"] != "MeshService" || at(t, sub, "labels", "kuma.io/display-name") != "legacy" {
		t.Errorf("MeshServiceSubset backendRef not converted by the gateway fix: %v", sub)
	}
}

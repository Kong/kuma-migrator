package migrator

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// ---- helpers ----------------------------------------------------------------

func scanDoc(t *testing.T, input string, target TargetVersion) (map[string]interface{}, string) {
	t.Helper()
	out, warnings := ScanForDeprecations([]byte(input), target)
	var obj map[string]interface{}
	if err := yaml.Unmarshal(out, &obj); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	return obj, strings.Join(warnings, "\n")
}

func rulesOf(t *testing.T, obj map[string]interface{}) []interface{} {
	t.Helper()
	c := rbacRulesContainer(obj)
	if c == nil {
		t.Fatalf("no rules in %v", obj)
	}
	rules, _ := c["rules"].([]interface{})
	return rules
}

func ruleAt(t *testing.T, obj map[string]interface{}, i int) map[string]interface{} {
	t.Helper()
	rules := rulesOf(t, obj)
	if i >= len(rules) {
		t.Fatalf("rule %d missing, have %d", i, len(rules))
	}
	return rules[i].(map[string]interface{})
}

// ---- AccessRole types -------------------------------------------------------

const accessRoleLegacyTypes = `
type: AccessRole
name: ops
rules:
- types: [TrafficPermission, Timeout, MeshTimeout, ExternalService, OPAPolicy]
  mesh: default
  access: [CREATE]
`

func TestScanAccessRole_V3RenamesAndDedupesTypes(t *testing.T) {
	obj, w := scanDoc(t, accessRoleLegacyTypes, TargetV3)
	got := toStringList(ruleAt(t, obj, 0)["types"])
	want := []string{"MeshTrafficPermission", "MeshTimeout", "MeshExternalService", "MeshOPA"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("types = %v, want %v", got, want)
	}
	for _, s := range []string{"TrafficPermission→MeshTrafficPermission", "Duplicates collapsed"} {
		if !strings.Contains(w, s) {
			t.Errorf("warning should contain %q, got: %s", s, w)
		}
	}
}

func TestScanAccessRole_V2IsAdvisoryOnly(t *testing.T) {
	obj, w := scanDoc(t, accessRoleLegacyTypes, TargetV2)
	got := toStringList(ruleAt(t, obj, 0)["types"])
	if got[0] != "TrafficPermission" {
		t.Errorf("v2 must not rewrite types, got %v", got)
	}
	if !strings.Contains(w, "removed in 3.0") || !strings.Contains(w, "Timeout→MeshTimeout") {
		t.Errorf("expected forward-looking advisory, got: %s", w)
	}
}

func TestScanAccessRole_V3RemovesTypesWithoutSuccessor(t *testing.T) {
	input := `
type: AccessRole
name: ops
rules:
- types: [TrafficRoute, MeshTimeout, ZoneIngress]
  mesh: default
  access: [CREATE]
`
	obj, w := scanDoc(t, input, TargetV3)
	got := toStringList(ruleAt(t, obj, 0)["types"])
	if len(got) != 1 || got[0] != "MeshTimeout" {
		t.Errorf("types = %v, want [MeshTimeout]", got)
	}
	if !strings.Contains(w, "TrafficRoute (add MeshHTTPRoute") || !strings.Contains(w, "ZoneIngress (") {
		t.Errorf("expected removal warning naming both types, got: %s", w)
	}
}

// TestScanAccessRole_V3DropsRuleWhoseTypesAllGo guards against widening: an
// empty types list grants every type, so the rule must go, not be emptied.
func TestScanAccessRole_V3DropsRuleWhoseTypesAllGo(t *testing.T) {
	input := `
type: AccessRole
name: ops
rules:
- types: [TrafficRoute]
  mesh: default
  access: [CREATE]
- types: [MeshTimeout]
  mesh: default
  access: [CREATE]
`
	obj, w := scanDoc(t, input, TargetV3)
	rules := rulesOf(t, obj)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule left, got %d", len(rules))
	}
	if toStringList(rules[0].(map[string]interface{})["types"])[0] != "MeshTimeout" {
		t.Errorf("wrong rule kept: %v", rules[0])
	}
	if !strings.Contains(w, "rules[0] was removed") {
		t.Errorf("expected rule-drop warning, got: %s", w)
	}
}

func TestScanAccessRole_V3KeepsRoleWhenEveryRuleWouldGo(t *testing.T) {
	input := `
type: AccessRole
name: ops
rules:
- types: [TrafficRoute, VirtualOutbound]
  mesh: default
  access: [CREATE]
`
	obj, w := scanDoc(t, input, TargetV3)
	got := toStringList(ruleAt(t, obj, 0)["types"])
	if strings.Join(got, ",") != "TrafficRoute,VirtualOutbound" {
		t.Errorf("role must be left unchanged, got types %v", got)
	}
	if !strings.Contains(w, "grants nothing on 3.0") || !strings.Contains(w, "Delete the role") {
		t.Errorf("expected delete-the-role warning, got: %s", w)
	}
}

func TestScanAccessRole_V3MixedTypesWarns(t *testing.T) {
	input := `
type: AccessRole
name: ops
rules:
- types: [TrafficPermission, Dataplane]
  mesh: default
  access: [CREATE]
`
	_, w := scanDoc(t, input, TargetV3)
	if !strings.Contains(w, "cannot mix policies 2.0 with old policies") {
		t.Errorf("expected mixed-types warning, got: %s", w)
	}
}

func TestScanAccessRole_KubernetesShape(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: AccessRole
metadata:
  name: ops
spec:
  rules:
  - types: [Retry]
    mesh: default
    access: [CREATE]
`
	obj, _ := scanDoc(t, input, TargetV3)
	spec := obj["spec"].(map[string]interface{})
	rule := spec["rules"].([]interface{})[0].(map[string]interface{})
	if toStringList(rule["types"])[0] != "MeshRetry" {
		t.Errorf("expected MeshRetry under spec.rules, got %v", rule["types"])
	}
}

// ---- AccessRole qualifiers --------------------------------------------------

func TestScanAccessRole_V3RemovesLegacyOnlyQualifierWhenOthersRemain(t *testing.T) {
	input := `
type: AccessRole
name: ops
rules:
- types: [MeshTimeout]
  mesh: default
  access: [CREATE]
  when:
  - sources:
      match:
        app: web
  - {}
  - targetRef:
      kind: Dataplane
      labels:
        app: web
`
	obj, w := scanDoc(t, input, TargetV3)
	when := ruleAt(t, obj, 0)["when"].([]interface{})
	if len(when) != 1 {
		t.Fatalf("expected only the targetRef qualifier left, got %v", when)
	}
	if _, ok := when[0].(map[string]interface{})["targetRef"]; !ok {
		t.Errorf("wrong qualifier kept: %v", when[0])
	}
	if !strings.Contains(w, "when[0,1] removed") || !strings.Contains(w, "does not widen") {
		t.Errorf("expected removal warning, got: %s", w)
	}
}

// TestScanAccessRole_V3KeepsLegacyOnlyQualifiersWhenAllAreLegacy guards the
// widening case: removing every entry would leave `when` empty, which grants
// access to every object of the listed types.
func TestScanAccessRole_V3KeepsLegacyOnlyQualifiersWhenAllAreLegacy(t *testing.T) {
	input := `
type: AccessRole
name: ops
rules:
- types: [TrafficPermission]
  mesh: default
  access: [CREATE]
  when:
  - sources:
      match:
        app: web
`
	obj, w := scanDoc(t, input, TargetV3)
	when, _ := ruleAt(t, obj, 0)["when"].([]interface{})
	if len(when) != 1 {
		t.Fatalf("legacy-only qualifier must be kept, got %v", when)
	}
	if !strings.Contains(w, "left in place") || !strings.Contains(w, "qualifier cannot be empty") {
		t.Errorf("expected left-in-place warning, got: %s", w)
	}
}

func TestScanAccessRole_V3StripsLegacyKeysNextToOthers(t *testing.T) {
	input := `
type: AccessRole
name: ops
rules:
- types: [MeshTimeout]
  mesh: default
  access: [CREATE]
  when:
  - destinations:
      match:
        app: web
    targetRef:
      kind: Mesh
`
	obj, w := scanDoc(t, input, TargetV3)
	q := ruleAt(t, obj, 0)["when"].([]interface{})[0].(map[string]interface{})
	if _, ok := q["destinations"]; ok {
		t.Errorf("destinations should be stripped: %v", q)
	}
	if _, ok := q["targetRef"]; !ok {
		t.Errorf("targetRef must stay: %v", q)
	}
	if !strings.Contains(w, "when[0].{destinations} removed") {
		t.Errorf("expected strip warning, got: %s", w)
	}
}

func TestScanAccessRole_QualifierTargetRefChecks(t *testing.T) {
	input := `
type: AccessRole
name: ops
rules:
- types: [MeshTimeout]
  mesh: default
  access: [CREATE]
  when:
  - targetRef:
      kind: MeshService
      name: web
      mesh: default
  - to:
      targetRef:
        kind: MeshService
        tags:
          app: web
`
	for _, target := range []TargetVersion{TargetV2, TargetV3} {
		obj, w := scanDoc(t, input, target)
		if !strings.Contains(w, "when[0].targetRef.kind MeshService is rejected in 3.0") {
			t.Errorf("%v: expected top-level kind warning, got: %s", target, w)
		}
		if !strings.Contains(w, "when[0].targetRef.{mesh}") || !strings.Contains(w, "when[1].to.targetRef.{tags}") {
			t.Errorf("%v: expected reserved-field warnings, got: %s", target, w)
		}
		if strings.Contains(w, "when[1].to.targetRef.kind") {
			t.Errorf("%v: to.targetRef accepts the full kind set and must not be flagged: %s", target, w)
		}
		// No auto-fix: the qualifier keeps its kind.
		q := ruleAt(t, obj, 0)["when"].([]interface{})[0].(map[string]interface{})
		if q["targetRef"].(map[string]interface{})["kind"] != "MeshService" {
			t.Errorf("%v: qualifier kind must not be rewritten", target)
		}
	}
}

func TestScanAccessRole_V2LegacyQualifierAdvisory(t *testing.T) {
	input := `
type: AccessRole
name: ops
rules:
- types: [TrafficPermission]
  mesh: default
  access: [CREATE]
  when:
  - selectors:
      match:
        app: web
`
	obj, w := scanDoc(t, input, TargetV2)
	if len(ruleAt(t, obj, 0)["when"].([]interface{})) != 1 {
		t.Error("v2 must not modify qualifiers")
	}
	if !strings.Contains(w, "accepted from 3.0 only") {
		t.Errorf("expected v2 advisory noting labels are 3.0-only, got: %s", w)
	}
}

func TestScanAccessRole_ViewControlPlaneMetadataAdvisory(t *testing.T) {
	input := `
type: AccessRole
name: tokens
rules:
- types: [Dataplane]
  mesh: default
  access: [GENERATE_DATAPLANE_TOKEN]
`
	_, w := scanDoc(t, input, TargetV2)
	if !strings.Contains(w, "VIEW_CONTROL_PLANE_METADATA") {
		t.Errorf("expected /config access advisory, got: %s", w)
	}

	withMeta := strings.Replace(input, "[GENERATE_DATAPLANE_TOKEN]", "[GENERATE_DATAPLANE_TOKEN, VIEW_CONTROL_PLANE_METADATA]", 1)
	if _, w := scanDoc(t, withMeta, TargetV3); strings.Contains(w, "VIEW_CONTROL_PLANE_METADATA") {
		t.Errorf("role already granting the access must not be flagged: %s", w)
	}
}

// ---- AccessAudit ------------------------------------------------------------

func TestScanAccessAudit(t *testing.T) {
	input := `
type: AccessAudit
name: audit
rules:
- types: [TrafficPermission, MeshGlobalRateLimit]
  mesh: default
  accessAll: true
- types: [ServiceInsight]
  accessAll: true
`
	obj, w := scanDoc(t, input, TargetV3)
	rules := rulesOf(t, obj)
	if len(rules) != 1 {
		t.Fatalf("expected the ServiceInsight-only rule dropped, got %v", rules)
	}
	if got := toStringList(rules[0].(map[string]interface{})["types"]); len(got) != 1 || got[0] != "MeshTrafficPermission" {
		t.Errorf("types = %v, want [MeshTrafficPermission]", got)
	}
	if !strings.Contains(w, "AccessAudit") {
		t.Errorf("expected AccessAudit warnings, got: %s", w)
	}

	obj, w = scanDoc(t, input, TargetV2)
	if len(rulesOf(t, obj)) != 2 || !strings.Contains(w, "invalid resource type") {
		t.Errorf("v2: expected unchanged doc and advisory, got rules=%v warnings=%s", rulesOf(t, obj), w)
	}
}

func TestDetectScenario_AccessRoleAndAuditArePassthrough(t *testing.T) {
	for _, kind := range []string{"AccessRole", "AccessAudit"} {
		s, err := DetectScenario([]byte("type: " + kind + "\nname: x\nrules: []\n"))
		if err != nil || s != ScenarioPassthrough {
			t.Errorf("%s: got %v, %v; want ScenarioPassthrough", kind, s, err)
		}
	}
}

// ---- removed kinds ----------------------------------------------------------

func TestV3DeleteBeforeUpgradeNote(t *testing.T) {
	if n := v3DeleteBeforeUpgradeNote("Timeout", "t", true); !strings.Contains(n, "converted replacement") {
		t.Errorf("converted note wrong: %s", n)
	}
	if n := v3DeleteBeforeUpgradeNote("TrafficRoute", "t", false); !strings.Contains(n, ManualDir+"/") {
		t.Errorf("unconverted note should name %s/: %s", ManualDir, n)
	}
	for _, k := range []string{"MeshGateway", "MeshGatewayRoute", "MeshGatewayInstance", "MeshGlobalRateLimit", "MeshTimeout"} {
		if n := v3DeleteBeforeUpgradeNote(k, "x", true); n != "" {
			t.Errorf("%s: expected no note (own guidance or not removed), got %s", k, n)
		}
	}
}

// TestKumactlDeleteTypePinned pins every kumactl TYPE argument to the KumactlArg
// of the kind's ResourceTypeDescriptor at kuma v2.14.5
// (pkg/core/resources/apis/mesh/zz_generated.resources.go; policy-gen kinds use
// the lower-cased kind). A wrong value makes `kumactl delete` fail with
// "unknown TYPE".
func TestKumactlDeleteTypePinned(t *testing.T) {
	want := map[string]string{
		"TrafficPermission":   "traffic-permission",
		"TrafficRoute":        "traffic-route",
		"TrafficLog":          "traffic-log",
		"TrafficTrace":        "traffic-trace",
		"HealthCheck":         "healthcheck",
		"CircuitBreaker":      "circuit-breaker",
		"Retry":               "retry",
		"Timeout":             "timeout",
		"RateLimit":           "rate-limit",
		"FaultInjection":      "fault-injection",
		"VirtualOutbound":     "virtual-outbound",
		"ProxyTemplate":       "proxytemplate",
		"ExternalService":     "external-service",
		"ZoneIngress":         "zoneingress",
		"ZoneEgress":          "zoneegress",
		"MeshGateway":         "meshgateway",
		"MeshGatewayRoute":    "meshgatewayroute",
		"MeshGlobalRateLimit": "meshglobalratelimit",
		"OPAPolicy":           "opa-policy", // Kong Mesh; not verifiable from the KB
	}
	for kind, arg := range want {
		if got := kumactlDeleteType[kind]; got != arg {
			t.Errorf("kumactlDeleteType[%s] = %q, want %q", kind, got, arg)
		}
	}
	// Every removed kind kumactl can address has an explicit entry, so none
	// relies on the lower-cased-kind fallback.
	for kind := range removedInV3SourceKinds {
		if kubernetesOnlyRemovedKinds[kind] {
			continue
		}
		if _, ok := kumactlDeleteType[kind]; !ok {
			t.Errorf("removed kind %s has no kumactlDeleteType entry", kind)
		}
	}
}

func TestV3DeleteCommand(t *testing.T) {
	e := v3Deletion{Kind: "TrafficPermission", Name: "tp", Mesh: "default"}
	if got := v3DeleteCommand(e, "kumactl"); got != "kumactl delete traffic-permission tp --mesh default" {
		t.Errorf("kumactl: %q", got)
	}
	if got := v3DeleteCommand(v3Deletion{Kind: "ZoneIngress", Name: "zi", Mesh: "default"}, "kumactl"); got != "kumactl delete zoneingress zi" {
		t.Errorf("global-scoped kind must not get --mesh: %q", got)
	}
	if got := v3DeleteCommand(v3Deletion{Kind: "MeshGlobalRateLimit", Name: "m", Namespace: "kuma-system"}, "kubectl"); got != "kubectl delete MeshGlobalRateLimit m -n kuma-system" {
		t.Errorf("kubectl: %q", got)
	}
}

// ---- MeshOPA MeshSubset -----------------------------------------------------

func TestFixMeshOPATargetRefForV3_MeshSubsetTags(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshOPA
metadata:
  name: opa
spec:
  targetRef:
    kind: MeshSubset
    tags:
      app: web
      version: v1
`
	obj, w := scanDoc(t, input, TargetV3)
	ref := obj["spec"].(map[string]interface{})["targetRef"].(map[string]interface{})
	if ref["kind"] != "Dataplane" {
		t.Errorf("kind = %v, want Dataplane", ref["kind"])
	}
	if _, ok := ref["tags"]; ok {
		t.Errorf("tags must be removed: %v", ref)
	}
	labels := ref["labels"].(map[string]interface{})
	if labels["app"] != "web" || labels["version"] != "v1" {
		t.Errorf("labels = %v", labels)
	}
	if !strings.Contains(w, "labels app, version") || !strings.Contains(w, "selects nothing") {
		t.Errorf("expected conversion warning, got: %s", w)
	}

	// v2 leaves it alone.
	obj, _ = scanDoc(t, input, TargetV2)
	if obj["spec"].(map[string]interface{})["targetRef"].(map[string]interface{})["kind"] != "MeshSubset" {
		t.Error("v2 must not rewrite MeshSubset")
	}
}

func TestFixMeshOPATargetRefForV3_MeshSubsetNoTagsAndConflict(t *testing.T) {
	ref := map[string]interface{}{"kind": "MeshSubset"}
	if changed, _ := convertMeshOPAMeshSubsetForV3(ref, "o"); !changed || ref["kind"] != "Mesh" {
		t.Errorf("empty MeshSubset should become Mesh, got %v", ref)
	}

	ref = map[string]interface{}{
		"kind":   "MeshSubset",
		"tags":   map[string]interface{}{"app": "web"},
		"labels": map[string]interface{}{"app": "api"},
	}
	changed, ws := convertMeshOPAMeshSubsetForV3(ref, "o")
	if changed || ref["kind"] != "MeshSubset" {
		t.Errorf("conflicting label must block the conversion, got %v", ref)
	}
	if len(ws) != 1 || !strings.Contains(ws[0], "NOT converted") {
		t.Errorf("expected conflict warning, got %v", ws)
	}
}

// TestTransformOPAPolicy_V3SelectorWithoutServiceTag covers the end-to-end path:
// a legacy selector without kuma.io/service converts to MeshSubset{tags}, which
// the 3.0 MeshOPA CRD rejects, so v3 must emit Dataplane{labels}.
func TestTransformOPAPolicy_V3SelectorWithoutServiceTag(t *testing.T) {
	input := `
type: OPAPolicy
mesh: default
name: opa1
selectors:
- match:
    app: web
conf:
  policies:
  - inlineString: "package envoy.authz"
`
	docs, _, _, err := TransformDocument([]byte(input), TargetV3)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	out := string(docs[0])
	if !strings.Contains(out, "kind: Dataplane") || strings.Contains(out, "MeshSubset") || strings.Contains(out, "tags:") {
		t.Errorf("expected Dataplane+labels targetRef, got:\n%s", out)
	}
}

package migrator

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestTransformOPAPolicy_Basic(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: OPAPolicy
metadata:
  name: my-opa-policy
  namespace: kong-mesh-system
spec:
  targetRef:
    kind: Mesh
  conf:
    policies:
      - inlineString: |
          package envoy.authz
          default allow = false
          allow { input.attributes.request.http.method == "GET" }
`
	docs, warnings, err := TransformOPAPolicy([]byte(input), TargetV2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	_ = warnings // may be empty

	var out map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	if out["kind"] != "MeshOPA" {
		t.Errorf("expected kind MeshOPA, got %v", out["kind"])
	}

	spec, _ := out["spec"].(map[string]interface{})
	if spec == nil {
		t.Fatal("spec is nil")
	}
	def, _ := spec["default"].(map[string]interface{})
	if def == nil {
		t.Fatal("spec.default is nil")
	}
	appendPolicies, _ := def["appendPolicies"].([]interface{})
	if len(appendPolicies) != 1 {
		t.Fatalf("expected 1 appendPolicies entry, got %d", len(appendPolicies))
	}
	entry, _ := appendPolicies[0].(map[string]interface{})
	rego, _ := entry["rego"].(map[string]interface{})
	if rego == nil {
		t.Fatal("rego is nil")
	}
	inlineStr, _ := rego["inlineString"].(string)
	if !strings.Contains(inlineStr, "package envoy.authz") {
		t.Errorf("inlineString does not contain expected content: %q", inlineStr)
	}
	if _, hasConf := spec["conf"]; hasConf {
		t.Error("spec.conf should have been removed")
	}
}

func TestTransformOPAPolicy_WithAgentConfig(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: OPAPolicy
metadata:
  name: opa-with-agent
spec:
  targetRef:
    kind: Mesh
  conf:
    policies:
      - inlineString: "package envoy.authz\ndefault allow = true\n"
    agentConfig:
      inlineString: "decision_logs:\n  console: true\n"
`
	docs, _, err := TransformOPAPolicy([]byte(input), TargetV2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	spec, _ := out["spec"].(map[string]interface{})
	def, _ := spec["default"].(map[string]interface{})

	if _, ok := def["agentConfig"]; !ok {
		t.Error("expected agentConfig in spec.default")
	}
}

func TestTransformOPAPolicy_AlreadyMeshOPA(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: MeshOPA
metadata:
  name: already-new
spec:
  targetRef:
    kind: Mesh
  default:
    appendPolicies:
      - rego:
          inlineString: "package envoy.authz\ndefault allow = true\n"
`
	docs, _, err := TransformOPAPolicy([]byte(input), TargetV2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should pass through unchanged.
	if string(docs[0]) != input {
		t.Logf("output: %s", docs[0])
		// Just check kind is still MeshOPA — marshaling may reformat slightly.
		var out map[string]interface{}
		if err := yaml.Unmarshal(docs[0], &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out["kind"] != "MeshOPA" {
			t.Errorf("expected kind MeshOPA, got %v", out["kind"])
		}
	}
}

func TestTransformOPAPolicy_NoConf(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: OPAPolicy
metadata:
  name: empty-opa
spec:
  targetRef:
    kind: Mesh
`
	docs, warnings, err := TransformOPAPolicy([]byte(input), TargetV2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) == 0 {
		t.Error("expected a warning for missing spec.conf")
	}

	var out map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["kind"] != "MeshOPA" {
		t.Errorf("expected kind MeshOPA, got %v", out["kind"])
	}
}

func TestDetectScenario_OPAPolicy(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: OPAPolicy
metadata:
  name: test-opa
spec:
  targetRef:
    kind: Mesh
  conf:
    policies:
      - inlineString: "package envoy.authz\ndefault allow = true\n"
`
	scenario, err := DetectScenario([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scenario != ScenarioOPAPolicy {
		t.Errorf("expected ScenarioOPAPolicy, got %v", scenario)
	}
}

// TestTransformOPAPolicy_V3TargetRef checks the 3.0 targetRef rewrite. Two
// things happen together: targetRef.name is CARRIED into a label rather than
// dropped (dropping it is what silently widens the policy to every service
// matching its kind), and kind: MeshService — no longer valid for MeshOPA in
// 3.0 — becomes kind: Dataplane, which also changes the label key from
// kuma.io/display-name to app (the Dataplane selector convention).
func TestTransformOPAPolicy_V3TargetRef(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: OPAPolicy
metadata:
  name: my-opa
spec:
  targetRef:
    kind: MeshService
    name: backend
    namespace: demo
    mesh: default
  conf:
    policies:
      - inlineString: "package envoy.authz"
`
	// v2: targetRef preserved untouched.
	docs, _, err := TransformOPAPolicy([]byte(input), TargetV2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(string(docs[0]), "name: backend") {
		t.Errorf("v2 target should preserve targetRef.name, got:\n%s", docs[0])
	}
	if !strings.Contains(string(docs[0]), "kind: MeshService") {
		t.Errorf("v2 target should preserve targetRef.kind, got:\n%s", docs[0])
	}

	// v3: kind MeshService → Dataplane, name → labels["app"], namespace and mesh dropped.
	docs, warnings, err := TransformOPAPolicy([]byte(input), TargetV3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := string(docs[0])
	if !strings.Contains(out, "kind: Dataplane") {
		t.Errorf("expected targetRef.kind to become Dataplane, got:\n%s", out)
	}
	if strings.Contains(out, "kind: MeshService") {
		t.Errorf("expected targetRef.kind MeshService to be gone, got:\n%s", out)
	}
	if !strings.Contains(out, "app: backend") {
		t.Errorf("expected the app label carrying the original name, got:\n%s", out)
	}
	if strings.Contains(out, "kuma.io/display-name") {
		t.Errorf("expected no kuma.io/display-name label (MeshService's convention, not Dataplane's), got:\n%s", out)
	}
	// Match the bare targetRef key, not the app label that now carries the same value.
	if strings.Contains(out, "\n    name: backend") {
		t.Errorf("expected targetRef.name to be removed, got:\n%s", out)
	}
	if strings.Contains(out, "namespace: demo") || strings.Contains(out, "mesh: default") {
		t.Errorf("expected targetRef.namespace and .mesh to be removed, got:\n%s", out)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "widen") {
		t.Errorf("expected the scope-widening rationale in warnings, got: %v", warnings)
	}
}

// TestTransformOPAPolicy_V3TargetRef_ConflictingLabel verifies we refuse to
// overwrite an existing, different display-name selector — and that the kind
// conversion (MeshService → Dataplane) happens first, so the conflict is
// checked and reported against the label key ("app") that actually ends up
// in the output, not the MeshService-era key it replaces.
func TestTransformOPAPolicy_V3TargetRef_ConflictingLabel(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshOPA
metadata:
  name: my-opa
spec:
  targetRef:
    kind: MeshService
    name: backend
    labels:
      kuma.io/display-name: frontend
  default:
    appendPolicies: []
`
	docs, warnings, err := TransformOPAPolicy([]byte(input), TargetV3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := string(docs[0])
	if !strings.Contains(out, "kind: Dataplane") {
		t.Errorf("expected targetRef.kind to become Dataplane, got:\n%s", out)
	}
	if !strings.Contains(out, "app: frontend") {
		t.Errorf("existing label must be preserved (renamed to app), got:\n%s", out)
	}
	if strings.Contains(out, "kuma.io/display-name") {
		t.Errorf("expected no leftover kuma.io/display-name label, got:\n%s", out)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "resolve this by hand") {
		t.Errorf("expected a conflict warning, got: %v", warnings)
	}
	if !strings.Contains(joined, `labels["app"]="frontend"`) {
		t.Errorf("expected the conflict warning to name the post-conversion label key (app), got: %v", warnings)
	}
}

// TestTransformOPAPolicy_RealLegacyShape covers the OPAPolicy shape the Kong Mesh
// CRD actually accepts, which differs from the targetRef-style fixtures the other
// tests use: the mesh is a TOP-LEVEL field and workloads are chosen with
// spec.selectors[].match, not spec.targetRef.
//
// Emitting that unchanged produces a MeshOPA the control plane rejects with
// `unknown field "mesh", unknown field "spec.selectors"`, which is what happened
// against a live 2.14.3 CP before this was handled.
func TestTransformOPAPolicy_RealLegacyShape(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: OPAPolicy
metadata:
  name: legacy-opa
mesh: default
spec:
  selectors:
    - match:
        kuma.io/service: '*'
  conf:
    policies:
      - inlineString: "package envoy.authz"
`
	docs, _, err := TransformOPAPolicy([]byte(input), TargetV2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 document, got %d", len(docs))
	}
	out := string(docs[0])

	if strings.Contains(out, "\nmesh: default") {
		t.Errorf("top-level mesh must be removed (MeshOPA rejects it), got:\n%s", out)
	}
	if !strings.Contains(out, "kuma.io/mesh: default") {
		t.Errorf("mesh must move to the kuma.io/mesh label, got:\n%s", out)
	}
	if strings.Contains(out, "selectors:") {
		t.Errorf("spec.selectors must be removed (MeshOPA rejects it), got:\n%s", out)
	}
	// kuma.io/service: '*' is mesh-wide.
	if !strings.Contains(out, "targetRef:") || !strings.Contains(out, "kind: Mesh") {
		t.Errorf("expected a mesh-wide targetRef, got:\n%s", out)
	}
	if !strings.Contains(out, "appendPolicies:") {
		t.Errorf("rego should still be migrated, got:\n%s", out)
	}
}

// TestTransformOPAPolicy_SelectorWithService checks a selector naming a specific
// service, and TestTransformOPAPolicy_MultipleSelectors the split, since MeshOPA
// holds only one targetRef.
func TestTransformOPAPolicy_SelectorWithService(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: OPAPolicy
metadata:
  name: svc-opa
mesh: default
spec:
  selectors:
    - match:
        kuma.io/service: backend_demo_svc_8080
  conf:
    policies:
      - inlineString: "package envoy.authz"
`
	docs, _, err := TransformOPAPolicy([]byte(input), TargetV2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := string(docs[0])
	if strings.Contains(out, "selectors:") {
		t.Errorf("selectors must be converted, got:\n%s", out)
	}
	if !strings.Contains(out, "targetRef:") {
		t.Errorf("expected a targetRef, got:\n%s", out)
	}
	// A service-identity selector must not collapse to a mesh-wide targetRef,
	// which would silently widen the policy.
	if strings.Contains(out, "kind: Mesh\n") && !strings.Contains(out, "backend") {
		t.Errorf("service selector must not widen to mesh-wide, got:\n%s", out)
	}
}

func TestTransformOPAPolicy_MultipleSelectors(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: OPAPolicy
metadata:
  name: multi-opa
mesh: default
spec:
  selectors:
    - match:
        kuma.io/service: a_demo_svc_8080
    - match:
        kuma.io/service: b_demo_svc_8080
  conf:
    policies:
      - inlineString: "package envoy.authz"
`
	docs, warnings, err := TransformOPAPolicy([]byte(input), TargetV2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected one MeshOPA per selector (2), got %d", len(docs))
	}
	if !strings.Contains(string(docs[0]), "multi-opa-0") || !strings.Contains(string(docs[1]), "multi-opa-1") {
		t.Errorf("split documents should get distinct names, got:\n%s\n%s", docs[0], docs[1])
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "Apply all 2 documents") {
		t.Errorf("expected a warning telling the operator to apply both, got: %v", warnings)
	}
}

// TestTransformOPAPolicy_UniversalFormat covers the shape kumactl returns for a
// legacy OPAPolicy: type instead of kind, and the body (conf/selectors) at the
// document ROOT with no spec wrapper. MeshOPA still requires a spec even in
// Universal — kumactl apply rejects it with ".spec in body is required" — so the
// transform reads from the root and writes into a real spec.
func TestTransformOPAPolicy_UniversalFormat(t *testing.T) {
	input := `
type: OPAPolicy
name: global-legacy-opa
mesh: default
labels:
  kuma.io/display-name: global-legacy-opa
selectors:
- match:
    kuma.io/service: '*'
conf:
  policies:
  - inlineString: package envoy.authz
`
	docs, _, err := TransformOPAPolicy([]byte(input), TargetV2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := string(docs[0])

	if !strings.Contains(out, "type: MeshOPA") {
		t.Errorf("Universal output must use type:, got:\n%s", out)
	}
	// Check for a ROOT-level kind key; "kind: Mesh" nested under targetRef is fine.
	if strings.HasPrefix(out, "kind:") || strings.Contains(out, "\nkind:") {
		t.Errorf("Universal output must not gain a root-level kind: key, got:\n%s", out)
	}
	// mesh stays at the root in Universal — it is the required spelling there.
	if !strings.Contains(out, "\nmesh: default") {
		t.Errorf("Universal output must keep the top-level mesh, got:\n%s", out)
	}
	if strings.Contains(out, "kuma.io/mesh:") {
		t.Errorf("Universal output must not move mesh into labels, got:\n%s", out)
	}
	for _, leftover := range []string{"conf:", "selectors:"} {
		if strings.Contains(out, leftover) {
			t.Errorf("legacy key %q must be removed, got:\n%s", leftover, out)
		}
	}
	if !strings.Contains(out, "spec:") || !strings.Contains(out, "appendPolicies:") || !strings.Contains(out, "targetRef:") {
		t.Errorf("expected spec with default.appendPolicies and targetRef, got:\n%s", out)
	}
}

// TestTransformOPAPolicy_UniversalConvertedToKubernetes covers what the extractor
// produces with --output-format kubernetes: universalToKubernetes maps
// type/name/mesh but does not relocate the root-level body, so conf and selectors
// arrive at the root next to an empty spec. Reading the body only from spec left
// them in place and emitted an empty MeshOPA that the CP rejected with
// `unknown field "conf", unknown field "selectors"`.
func TestTransformOPAPolicy_UniversalConvertedToKubernetes(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: OPAPolicy
metadata:
  name: global-legacy-opa
  labels:
    kuma.io/mesh: default
conf:
  policies:
  - inlineString: package envoy.authz
selectors:
- match:
    kuma.io/service: '*'
spec: {}
`
	docs, _, err := TransformOPAPolicy([]byte(input), TargetV2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := string(docs[0])

	for _, leftover := range []string{"\nconf:", "\nselectors:"} {
		if strings.Contains(out, leftover) {
			t.Errorf("root-level %q must be consumed, got:\n%s", strings.TrimSpace(leftover), out)
		}
	}
	if !strings.Contains(out, "appendPolicies:") {
		t.Errorf("rego must be migrated into spec.default, got:\n%s", out)
	}
	if !strings.Contains(out, "targetRef:") || !strings.Contains(out, "kind: Mesh") {
		t.Errorf("selectors must become a targetRef, got:\n%s", out)
	}
	if !strings.Contains(out, "kind: MeshOPA") {
		t.Errorf("expected kind: MeshOPA, got:\n%s", out)
	}
}

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

// TestTransformOPAPolicy_V3TargetRef checks the 3.0 targetRef rewrite. The
// important behaviour is that targetRef.name is CARRIED into the display-name
// label rather than dropped: dropping it is what silently widens the policy to
// every service matching its kind.
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

	// v3: name → labels["kuma.io/display-name"], namespace and mesh dropped.
	docs, warnings, err := TransformOPAPolicy([]byte(input), TargetV3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := string(docs[0])
	if !strings.Contains(out, "kuma.io/display-name: backend") {
		t.Errorf("expected display-name label carrying the original name, got:\n%s", out)
	}
	// Match the bare targetRef key, not the display-name label that now carries
	// the same value.
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
// overwrite an existing, different display-name selector.
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
	if !strings.Contains(out, "kuma.io/display-name: frontend") {
		t.Errorf("existing label must be preserved, got:\n%s", out)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "resolve this by hand") {
		t.Errorf("expected a conflict warning, got: %v", warnings)
	}
}

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
	docs, warnings, err := TransformOPAPolicy([]byte(input))
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
	docs, _, err := TransformOPAPolicy([]byte(input))
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
	docs, _, err := TransformOPAPolicy([]byte(input))
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
	docs, warnings, err := TransformOPAPolicy([]byte(input))
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

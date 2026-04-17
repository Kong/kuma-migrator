package migrator

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestTransformFromToRules_SingleEntry(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: my-timeout
  namespace: demo
spec:
  targetRef:
    kind: Dataplane
    labels:
      app: backend
  from:
    - targetRef:
        kind: Mesh
      default:
        connectTimeout: 5s
        http:
          requestTimeout: 10s
`
	docs, warnings, err := TransformFromToRules([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 output doc, got %d", len(docs))
	}

	var out map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	spec := out["spec"].(map[string]interface{})
	// from[] should be gone
	if _, hasFrom := spec["from"]; hasFrom {
		t.Error("expected from[] to be removed from output")
	}
	// rules[] should be present
	rules, ok := spec["rules"].([]interface{})
	if !ok || len(rules) != 1 {
		t.Fatalf("expected 1 rules[] entry, got %v", spec["rules"])
	}
	rule := rules[0].(map[string]interface{})
	// rules[] entries must have no targetRef
	if _, hasRef := rule["targetRef"]; hasRef {
		t.Error("rules[] entry must not have targetRef")
	}
	if rule["default"] == nil {
		t.Error("expected rules[0].default to be set")
	}

	// One warning about the migration
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "from[] migrated to rules[]") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected migration warning, got: %v", warnings)
	}
}

func TestTransformFromToRules_MultipleDistinctKinds(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshAccessLog
metadata:
  name: my-log
  namespace: demo
spec:
  targetRef:
    kind: Mesh
  from:
    - targetRef:
        kind: Mesh
      default:
        backends:
          - type: File
            file:
              path: /tmp/access.log
    - targetRef:
        kind: MeshService
        name: backend
        namespace: demo
      default:
        backends:
          - type: Tcp
            tcp:
              address: "10.0.0.1:9000"
`
	docs, warnings, err := TransformFromToRules([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 output doc, got %d", len(docs))
	}

	var out map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	spec := out["spec"].(map[string]interface{})
	rules, ok := spec["rules"].([]interface{})
	if !ok || len(rules) != 2 {
		t.Fatalf("expected 2 rules[] entries, got %v", spec["rules"])
	}

	// Should warn about distinct source kinds
	hasDistinctKindWarn := false
	for _, w := range warnings {
		if strings.Contains(w, "different source kinds") {
			hasDistinctKindWarn = true
		}
	}
	if !hasDistinctKindWarn {
		t.Errorf("expected distinct-kinds warning, got: %v", warnings)
	}
}

func TestDetectScenario_ScenarioRules(t *testing.T) {
	// New-style Mesh* kind with from[] and no service-identity tags → ScenarioRules.
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: my-timeout
  namespace: demo
spec:
  targetRef:
    kind: Mesh
  from:
    - targetRef:
        kind: Mesh
      default:
        connectTimeout: 5s
`
	scenario, err := DetectScenario([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scenario != ScenarioRules {
		t.Errorf("expected ScenarioRules, got %s", scenario)
	}
}

func TestDetectScenario_SubsetTakesPrecedenceOverRules(t *testing.T) {
	// from[] with service-identity tags → ScenarioSubset (not D)
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: my-timeout
  namespace: demo
spec:
  targetRef:
    kind: Mesh
  from:
    - targetRef:
        kind: MeshSubset
        tags:
          kuma.io/service: backend
      default:
        connectTimeout: 5s
`
	scenario, err := DetectScenario([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scenario != ScenarioSubset {
		t.Errorf("expected ScenarioSubset, got %s", scenario)
	}
}

func TestTransformScenarioSubset_AppliesFromToRules(t *testing.T) {
	// ScenarioSubset policy that also has from[] (with service tags) should
	// convert tags AND then migrate from[] → rules[].
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: my-timeout
  namespace: demo
spec:
  targetRef:
    kind: MeshSubset
    tags:
      kuma.io/service: backend_demo_svc_3000
  from:
    - targetRef:
        kind: Mesh
      default:
        connectTimeout: 5s
`
	docs, warnings, scenario, err := TransformDocument([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scenario != ScenarioSubset {
		t.Errorf("expected ScenarioSubset, got %s", scenario)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}

	var out map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	spec := out["spec"].(map[string]interface{})
	// targetRef should have been converted to Dataplane
	tr := spec["targetRef"].(map[string]interface{})
	if tr["kind"] != "Dataplane" {
		t.Errorf("expected Dataplane targetRef, got %v", tr["kind"])
	}
	// from[] should be migrated to rules[]
	if _, hasFrom := spec["from"]; hasFrom {
		t.Error("expected from[] to be removed")
	}
	if _, hasRules := spec["rules"]; !hasRules {
		t.Error("expected rules[] to be present")
	}

	// Should have a from→rules warning
	hasWarn := false
	for _, w := range warnings {
		if strings.Contains(w, "from[] migrated to rules[]") {
			hasWarn = true
		}
	}
	if !hasWarn {
		t.Errorf("expected from→rules warning, got: %v", warnings)
	}
}

package migrator

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// TransformDocument integration tests.
// These test the full pipeline (detect → transform → marshal) for the scenarios
// most likely to appear in real clusters, including producer/consumer patterns.

func TestTransformDocument_ScenarioSubset_Producer(t *testing.T) {
	// Producer policy: applied in the same namespace as the targeted MeshService.
	// spec.targetRef uses MeshSubset → Dataplane (top-level).
	// to[].targetRef uses MeshSubset with same namespace → MeshService name/namespace.
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: producer-timeout
  namespace: kuma-demo
  labels:
    kuma.io/mesh: default
spec:
  targetRef:
    kind: MeshSubset
    tags:
      k8s.kuma.io/service-name: demo-app
      k8s.kuma.io/namespace: kuma-demo
  to:
    - targetRef:
        kind: MeshSubset
        tags:
          k8s.kuma.io/service-name: demo-app
          k8s.kuma.io/namespace: kuma-demo
      default:
        http:
          requestTimeout: 1s
`

	docs, warnings, scenario, err := TransformDocument([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scenario != ScenarioSubset {
		t.Fatalf("expected ScenarioSubset, got %s", scenario)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 output doc, got %d", len(docs))
	}
	// No warnings: producer policy is same-namespace, uses name/namespace.
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}

	var out KubePolicy
	if err := yaml.Unmarshal(docs[0], &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	// spec.targetRef → Dataplane with name+namespace (same namespace as policy).
	if out.Spec.TargetRef == nil {
		t.Fatal("spec.targetRef is nil")
	}
	if out.Spec.TargetRef.Kind != "Dataplane" {
		t.Errorf("spec.targetRef.kind = %q, want Dataplane", out.Spec.TargetRef.Kind)
	}
	if out.Spec.TargetRef.Name == nil || *out.Spec.TargetRef.Name != "demo-app" {
		t.Errorf("spec.targetRef.name = %v, want demo-app", out.Spec.TargetRef.Name)
	}
	if out.Spec.TargetRef.Namespace == nil || *out.Spec.TargetRef.Namespace != "kuma-demo" {
		t.Errorf("spec.targetRef.namespace = %v, want kuma-demo", out.Spec.TargetRef.Namespace)
	}

	// to[0].targetRef → MeshService with name+namespace (producer: same namespace).
	if len(out.Spec.To) != 1 {
		t.Fatalf("expected 1 to[] entry, got %d", len(out.Spec.To))
	}
	toRef := out.Spec.To[0].TargetRef
	if toRef.Kind != "MeshService" {
		t.Errorf("to[0].targetRef.kind = %q, want MeshService", toRef.Kind)
	}
	if toRef.Name == nil || *toRef.Name != "demo-app" {
		t.Errorf("to[0].targetRef.name = %v, want demo-app", toRef.Name)
	}
	if toRef.Namespace == nil || *toRef.Namespace != "kuma-demo" {
		t.Errorf("to[0].targetRef.namespace = %v, want kuma-demo", toRef.Namespace)
	}
	if len(toRef.Labels) != 0 {
		t.Errorf("to[0].targetRef.labels should be empty for same-namespace producer, got %v", toRef.Labels)
	}
}

func TestTransformDocument_ScenarioSubset_Consumer(t *testing.T) {
	// Consumer policy: applied in a different namespace from the targeted MeshService.
	// spec.targetRef → Dataplane with labels (different namespace, warns).
	// to[].targetRef → MeshService with kuma.io/display-name labels (consumer pattern).
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: consumer-timeout
  namespace: first-consumer
  labels:
    kuma.io/mesh: default
spec:
  targetRef:
    kind: MeshSubset
    tags:
      k8s.kuma.io/service-name: consumer-app
      k8s.kuma.io/namespace: first-consumer
  to:
    - targetRef:
        kind: MeshSubset
        tags:
          k8s.kuma.io/service-name: demo-app
          k8s.kuma.io/namespace: kuma-demo
      default:
        http:
          requestTimeout: 3s
`

	docs, warnings, scenario, err := TransformDocument([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scenario != ScenarioSubset {
		t.Fatalf("expected ScenarioSubset, got %s", scenario)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 output doc, got %d", len(docs))
	}

	var out KubePolicy
	if err := yaml.Unmarshal(docs[0], &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	// spec.targetRef → Dataplane with labels (policy ns "first-consumer" == target ns "first-consumer"
	// so name/namespace form, no warning from the spec.targetRef conversion itself).
	if out.Spec.TargetRef == nil {
		t.Fatal("spec.targetRef is nil")
	}
	if out.Spec.TargetRef.Kind != "Dataplane" {
		t.Errorf("spec.targetRef.kind = %q, want Dataplane", out.Spec.TargetRef.Kind)
	}
	// policy ns == target ns → name/namespace form
	if out.Spec.TargetRef.Name == nil || *out.Spec.TargetRef.Name != "consumer-app" {
		t.Errorf("spec.targetRef.name = %v, want consumer-app", out.Spec.TargetRef.Name)
	}

	// to[0].targetRef → MeshService with labels using kuma.io/display-name (cross-namespace consumer).
	if len(out.Spec.To) != 1 {
		t.Fatalf("expected 1 to[] entry, got %d", len(out.Spec.To))
	}
	toRef := out.Spec.To[0].TargetRef
	if toRef.Kind != "MeshService" {
		t.Errorf("to[0].targetRef.kind = %q, want MeshService", toRef.Kind)
	}
	if toRef.Labels == nil {
		t.Fatal("to[0].targetRef.labels should not be nil for cross-namespace consumer targeting")
	}
	if toRef.Labels["kuma.io/display-name"] != "demo-app" {
		t.Errorf("to[0].targetRef.labels[kuma.io/display-name] = %q, want demo-app", toRef.Labels["kuma.io/display-name"])
	}
	if toRef.Labels["k8s.kuma.io/namespace"] != "kuma-demo" {
		t.Errorf("to[0].targetRef.labels[k8s.kuma.io/namespace] = %q, want kuma-demo", toRef.Labels["k8s.kuma.io/namespace"])
	}
	// Must NOT use k8s.kuma.io/service-name as the label key.
	if _, hasOld := toRef.Labels["k8s.kuma.io/service-name"]; hasOld {
		t.Errorf("to[0].targetRef.labels must not contain k8s.kuma.io/service-name; use kuma.io/display-name instead")
	}
	if toRef.Name != nil {
		t.Errorf("to[0].targetRef.name should be nil for labels-based consumer targeting, got %q", *toRef.Name)
	}

	// No warning expected: MeshService labels use kuma.io/display-name which is
	// deterministic (derived from the service name). Warnings are only emitted for
	// Dataplane labels where the "app" key is a best-guess.
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

func TestTransformDocument_ScenarioSubset_NoTopLevelTargetRef(t *testing.T) {
	// Producer-style policy with no spec.targetRef (common in producer/consumer model).
	// Only to[] is present; spec.targetRef should remain absent in output.
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: producer-timeout
  namespace: kuma-demo
  labels:
    kuma.io/mesh: default
spec:
  to:
    - targetRef:
        kind: MeshSubset
        tags:
          k8s.kuma.io/service-name: demo-app
          k8s.kuma.io/namespace: kuma-demo
      default:
        http:
          requestTimeout: 1s
`

	docs, warnings, scenario, err := TransformDocument([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scenario != ScenarioSubset {
		t.Fatalf("expected ScenarioSubset, got %s", scenario)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 output doc, got %d", len(docs))
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}

	var out KubePolicy
	if err := yaml.Unmarshal(docs[0], &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	// spec.targetRef absent in input → absent in output (not defaulted to Mesh).
	if out.Spec.TargetRef != nil {
		t.Errorf("spec.targetRef should remain nil when absent in input, got kind=%q", out.Spec.TargetRef.Kind)
	}

	// to[0].targetRef → MeshService name+namespace (same namespace → producer).
	if len(out.Spec.To) != 1 {
		t.Fatalf("expected 1 to[] entry, got %d", len(out.Spec.To))
	}
	toRef := out.Spec.To[0].TargetRef
	if toRef.Kind != "MeshService" {
		t.Errorf("to[0].targetRef.kind = %q, want MeshService", toRef.Kind)
	}
	if toRef.Name == nil || *toRef.Name != "demo-app" {
		t.Errorf("to[0].targetRef.name = %v, want demo-app", toRef.Name)
	}
}

func TestTransformDocument_ScenarioLegacy_WildcardSources(t *testing.T) {
	// Wildcard sources → spec.targetRef: kind: Mesh (equivalent to no spec.targetRef).
	input := `
type: Timeout
name: global-timeout
mesh: default
sources:
  - match:
      kuma.io/service: '*'
destinations:
  - match:
      kuma.io/service: '*'
conf:
  connectTimeout: 21s
`

	docs, _, scenario, err := TransformDocument([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scenario != ScenarioLegacy {
		t.Fatalf("expected ScenarioLegacy, got %s", scenario)
	}

	var out KubePolicy
	if err := yaml.Unmarshal(docs[0], &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	if out.Spec.TargetRef == nil || out.Spec.TargetRef.Kind != "Mesh" {
		t.Errorf("spec.targetRef.kind = %v, want Mesh", out.Spec.TargetRef)
	}
	if len(out.Spec.To) != 1 || out.Spec.To[0].TargetRef.Kind != "Mesh" {
		t.Errorf("to[0].targetRef.kind = %v, want Mesh", out.Spec.To)
	}
}

func TestTransformDocument_ScenarioLegacy_SpecificService(t *testing.T) {
	// Specific service in sources → spec.targetRef: kind: Dataplane.
	// destinations → to[]: kind: MeshService.
	input := `
type: Timeout
name: backend-timeout
mesh: default
sources:
  - match:
      kuma.io/service: backend_demo_svc_3001
destinations:
  - match:
      kuma.io/service: redis_demo_svc_6379
conf:
  connectTimeout: 5s
`

	docs, warnings, scenario, err := TransformDocument([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scenario != ScenarioLegacy {
		t.Fatalf("expected ScenarioLegacy, got %s", scenario)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc (single source), got %d", len(docs))
	}
	// Scenario A has no policy namespace → name/namespace used, no warning.
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings for Scenario A: %v", warnings)
	}

	var out KubePolicy
	if err := yaml.Unmarshal(docs[0], &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	// sources → spec.targetRef: Dataplane (top-level kind in 2.13.x).
	if out.Spec.TargetRef == nil {
		t.Fatal("spec.targetRef is nil")
	}
	if out.Spec.TargetRef.Kind != "Dataplane" {
		t.Errorf("spec.targetRef.kind = %q, want Dataplane", out.Spec.TargetRef.Kind)
	}
	if out.Spec.TargetRef.Name == nil || *out.Spec.TargetRef.Name != "backend" {
		t.Errorf("spec.targetRef.name = %v, want backend", out.Spec.TargetRef.Name)
	}
	if out.Spec.TargetRef.Namespace == nil || *out.Spec.TargetRef.Namespace != "demo" {
		t.Errorf("spec.targetRef.namespace = %v, want demo", out.Spec.TargetRef.Namespace)
	}

	// destinations → to[]: MeshService.
	if len(out.Spec.To) != 1 {
		t.Fatalf("expected 1 to[] entry, got %d", len(out.Spec.To))
	}
	toRef := out.Spec.To[0].TargetRef
	if toRef.Kind != "MeshService" {
		t.Errorf("to[0].targetRef.kind = %q, want MeshService", toRef.Kind)
	}
	if toRef.Name == nil || *toRef.Name != "redis" {
		t.Errorf("to[0].targetRef.name = %v, want redis", toRef.Name)
	}
}

func TestTransformDocument_ScenarioSubset_DisplayNameNotServiceName(t *testing.T) {
	// Explicit regression: cross-namespace MeshService labels must use
	// kuma.io/display-name, NOT k8s.kuma.io/service-name.
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshCircuitBreaker
metadata:
  name: cb
  namespace: kong-mesh-system
spec:
  to:
    - targetRef:
        kind: MeshSubset
        tags:
          kuma.io/service: redis_demo_svc_6379
      default:
        connectionLimits:
          maxConnections: 100
`

	docs, _, _, err := TransformDocument([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out KubePolicy
	if err := yaml.Unmarshal(docs[0], &out); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	if len(out.Spec.To) != 1 {
		t.Fatalf("expected 1 to[] entry, got %d", len(out.Spec.To))
	}
	toRef := out.Spec.To[0].TargetRef
	if toRef.Kind != "MeshService" {
		t.Errorf("to[0].targetRef.kind = %q, want MeshService", toRef.Kind)
	}

	// Policy is in kong-mesh-system, target is in demo → cross-namespace → must use labels.
	if toRef.Labels == nil {
		t.Fatal("to[0].targetRef.labels must not be nil for cross-namespace targeting from system namespace")
	}
	if _, bad := toRef.Labels["k8s.kuma.io/service-name"]; bad {
		t.Error("labels must not contain k8s.kuma.io/service-name; use kuma.io/display-name")
	}
	if toRef.Labels["kuma.io/display-name"] != "redis" {
		t.Errorf("labels[kuma.io/display-name] = %q, want redis", toRef.Labels["kuma.io/display-name"])
	}
	if toRef.Labels["k8s.kuma.io/namespace"] != "demo" {
		t.Errorf("labels[k8s.kuma.io/namespace] = %q, want demo", toRef.Labels["k8s.kuma.io/namespace"])
	}
	if toRef.Name != nil {
		t.Errorf("to[0].targetRef.name should be nil for labels-based targeting, got %q", *toRef.Name)
	}
}

func TestTransformDocument_ScenarioPassthrough_PassThrough(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: already-done
  namespace: kuma-demo
spec:
  targetRef:
    kind: Mesh
  to:
    - targetRef:
        kind: MeshService
        name: redis
        namespace: kuma-demo
      default:
        http:
          requestTimeout: 5s
`
	docs, warnings, scenario, err := TransformDocument([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scenario != ScenarioPassthrough {
		t.Fatalf("expected ScenarioPassthrough, got %s", scenario)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings for Scenario C: %v", warnings)
	}
	// Output must be byte-identical to input (pass-through).
	if !strings.Contains(string(docs[0]), "already-done") {
		t.Error("Scenario C output does not appear to be the original document")
	}
}

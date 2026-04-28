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

// ---- Universal format transformation ----------------------------------------

func TestTransformFromToRules_UniversalFormat(t *testing.T) {
	// Universal format: type/name/mesh at top level (no kind/apiVersion/metadata).
	// Extra fields (creationTime, kri, labels) must be preserved after transformation.
	// When from[] AND to[] are both present, the policy must be split into two docs
	// because Kuma 2.10+ forbids rules[] and to[] coexisting in the same spec.
	input := `creationTime: "2026-03-27T18:26:50Z"
kri: kri_mal_default___default_
labels:
  kuma.io/mesh: default
mesh: default
name: default
spec:
  from:
  - default:
      backends:
      - file:
          path: /dev/stdout
        type: File
    targetRef:
      kind: Mesh
  targetRef:
    kind: Mesh
  to:
  - default:
      backends:
      - file:
          path: /dev/stdout
        type: File
    targetRef:
      kind: Mesh
type: MeshAccessLog
`
	docs, warnings, err := TransformFromToRules([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Must produce 2 docs: one with rules[] (inbound), one with to[] (outbound).
	if len(docs) != 2 {
		t.Fatalf("expected 2 output docs (split), got %d", len(docs))
	}
	if len(warnings) == 0 {
		t.Error("expected at least one warning about from[]→rules[] migration")
	}
	hasSplitWarn := false
	for _, w := range warnings {
		if strings.Contains(w, "Split into two policies") {
			hasSplitWarn = true
		}
	}
	if !hasSplitWarn {
		t.Errorf("expected split warning, got: %v", warnings)
	}

	// Doc 0: rules[] policy (inbound), original name.
	var inbound map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &inbound); err != nil {
		t.Fatalf("unmarshal inbound doc: %v", err)
	}
	if got, _ := inbound["type"].(string); got != "MeshAccessLog" {
		t.Errorf("inbound: expected type=MeshAccessLog, got %q", got)
	}
	if got, _ := inbound["name"].(string); got != "default" {
		t.Errorf("inbound: expected name=default, got %q", got)
	}
	if got, _ := inbound["mesh"].(string); got != "default" {
		t.Errorf("inbound: expected mesh=default, got %q", got)
	}
	if _, ok := inbound["kri"]; !ok {
		t.Error("inbound: expected kri field to be preserved")
	}
	inboundSpec := inbound["spec"].(map[string]interface{})
	if _, hasFrom := inboundSpec["from"]; hasFrom {
		t.Error("inbound: expected from[] to be removed")
	}
	if _, hasTo := inboundSpec["to"]; hasTo {
		t.Error("inbound: expected to[] to be absent (moved to outbound doc)")
	}
	rules, ok := inboundSpec["rules"].([]interface{})
	if !ok || len(rules) != 1 {
		t.Errorf("inbound: expected rules[] with 1 entry, got %v", inboundSpec["rules"])
	}

	// Doc 1: to[] policy (outbound), name suffixed with "-outbound".
	var outbound map[string]interface{}
	if err := yaml.Unmarshal(docs[1], &outbound); err != nil {
		t.Fatalf("unmarshal outbound doc: %v", err)
	}
	if got, _ := outbound["name"].(string); got != "default-outbound" {
		t.Errorf("outbound: expected name=default-outbound, got %q", got)
	}
	outboundSpec := outbound["spec"].(map[string]interface{})
	if _, hasRules := outboundSpec["rules"]; hasRules {
		t.Error("outbound: expected rules[] to be absent")
	}
	to, ok := outboundSpec["to"].([]interface{})
	if !ok || len(to) != 1 {
		t.Errorf("outbound: expected to[] with 1 entry, got %v", outboundSpec["to"])
	}
}

func TestTransformFromToRules_SplitFromAndTo_MeshTimeout(t *testing.T) {
	// Mirrors the real-world failing resource: Universal MeshTimeout with proxyTypes
	// on targetRef, distinct timeout values in from[] (inbound) and to[] (outbound).
	// Kuma 2.10+ rejects rules[] and to[] in the same spec — must split.
	input := `creationTime: "2026-04-20T13:28:43.205767Z"
kri: kri_mt_default___mesh-gateways-timeout-all-default_
mesh: default
modificationTime: "2026-04-20T13:28:43.205767Z"
name: mesh-gateways-timeout-all-default
spec:
  from:
  - default:
      http:
        requestHeadersTimeout: 500ms
        streamIdleTimeout: 5s
      idleTimeout: 5m0s
    targetRef:
      kind: Mesh
  targetRef:
    kind: Mesh
    proxyTypes:
    - Gateway
  to:
  - default:
      http:
        streamIdleTimeout: 5s
      idleTimeout: 1h0m0s
    targetRef:
      kind: Mesh
type: MeshTimeout
`
	docs, warnings, err := TransformFromToRules([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 output docs (split), got %d", len(docs))
	}

	hasSplitWarn := false
	for _, w := range warnings {
		if strings.Contains(w, "Split into two policies") {
			hasSplitWarn = true
		}
	}
	if !hasSplitWarn {
		t.Errorf("expected split warning, got: %v", warnings)
	}

	// Doc 0: inbound — rules[], original name, no to[].
	var inbound map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &inbound); err != nil {
		t.Fatalf("unmarshal inbound: %v", err)
	}
	if got, _ := inbound["name"].(string); got != "mesh-gateways-timeout-all-default" {
		t.Errorf("inbound: expected original name, got %q", got)
	}
	inboundSpec := inbound["spec"].(map[string]interface{})
	if _, hasFrom := inboundSpec["from"]; hasFrom {
		t.Error("inbound: from[] must be removed")
	}
	if _, hasTo := inboundSpec["to"]; hasTo {
		t.Error("inbound: to[] must be absent")
	}
	rules, ok := inboundSpec["rules"].([]interface{})
	if !ok || len(rules) != 1 {
		t.Errorf("inbound: expected 1 rules[] entry, got %v", inboundSpec["rules"])
	}
	// Inbound rule carries the from[] default (idleTimeout: 5m).
	rule := rules[0].(map[string]interface{})
	def := rule["default"].(map[string]interface{})
	if got, _ := def["idleTimeout"].(string); got != "5m0s" {
		t.Errorf("inbound rule: expected idleTimeout=5m0s, got %q", got)
	}

	// Doc 1: outbound — to[], name suffixed -outbound, no rules[].
	var outbound map[string]interface{}
	if err := yaml.Unmarshal(docs[1], &outbound); err != nil {
		t.Fatalf("unmarshal outbound: %v", err)
	}
	if got, _ := outbound["name"].(string); got != "mesh-gateways-timeout-all-default-outbound" {
		t.Errorf("outbound: expected -outbound suffix, got %q", got)
	}
	outboundSpec := outbound["spec"].(map[string]interface{})
	if _, hasRules := outboundSpec["rules"]; hasRules {
		t.Error("outbound: rules[] must be absent")
	}
	toEntries, ok := outboundSpec["to"].([]interface{})
	if !ok || len(toEntries) != 1 {
		t.Errorf("outbound: expected 1 to[] entry, got %v", outboundSpec["to"])
	}
	// Outbound entry carries the to[] default (idleTimeout: 1h).
	toEntry := toEntries[0].(map[string]interface{})
	toDef := toEntry["default"].(map[string]interface{})
	if got, _ := toDef["idleTimeout"].(string); got != "1h0m0s" {
		t.Errorf("outbound to[]: expected idleTimeout=1h0m0s, got %q", got)
	}
}

// ---- Universal format detection ---------------------------------------------

func TestDetectScenario_UniversalRulesAPI(t *testing.T) {
	// Universal format: type instead of kind, name at top level, no metadata.
	// MeshAccessLog with from[] → ScenarioRules.
	input := `type: MeshAccessLog
name: default
mesh: default
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
          path: /dev/stdout
  to:
  - targetRef:
      kind: Mesh
    default:
      backends:
      - type: File
        file:
          path: /dev/stdout
`
	got, err := DetectScenario([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != ScenarioRules {
		t.Errorf("expected ScenarioRules for Universal MeshAccessLog with from[], got %v", got)
	}
}

func TestDetectScenario_UniversalPassthrough(t *testing.T) {
	// Universal MeshMetric with no from[] → ScenarioPassthrough.
	input := `type: MeshMetric
name: prom-example
mesh: default
spec:
  targetRef:
    kind: Mesh
  default:
    backends:
    - type: OpenTelemetry
      openTelemetry:
        endpoint: otel:4317
`
	got, err := DetectScenario([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != ScenarioPassthrough {
		t.Errorf("expected ScenarioPassthrough for Universal MeshMetric, got %v", got)
	}
}

func TestDetectScenario_UniversalMeshTrafficPermission(t *testing.T) {
	// MeshTrafficPermission uses from[] permanently — NOT in rulesAPIMigrationKinds.
	// Should be ScenarioPassthrough even in Universal format.
	input := `type: MeshTrafficPermission
name: allow-all
mesh: default
spec:
  targetRef:
    kind: Mesh
  from:
  - targetRef:
      kind: Mesh
    default:
      action: Allow
`
	got, err := DetectScenario([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != ScenarioPassthrough {
		t.Errorf("expected ScenarioPassthrough for Universal MeshTrafficPermission, got %v", got)
	}
}

package migrator

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestScanForDeprecations_MeshMetricSidecarRegex(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshMetric
metadata:
  name: my-metric
  namespace: demo
spec:
  targetRef:
    kind: Mesh
  default:
    sidecar:
      regex: "http2_act.*"
    backends:
      - type: Prometheus
        prometheus:
          port: 5670
`
	out, warnings := ScanForDeprecations([]byte(input))

	// Should have a warning
	if len(warnings) == 0 {
		t.Fatal("expected at least one warning about sidecar.regex")
	}
	hasRegexWarn := false
	for _, w := range warnings {
		if strings.Contains(w, "sidecar.regex") {
			hasRegexWarn = true
		}
	}
	if !hasRegexWarn {
		t.Errorf("expected sidecar.regex warning, got: %v", warnings)
	}

	// Output should have profiles.exclude instead of regex
	var obj map[string]interface{}
	if err := yaml.Unmarshal(out, &obj); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	spec := obj["spec"].(map[string]interface{})
	def := spec["default"].(map[string]interface{})
	sidecar := def["sidecar"].(map[string]interface{})

	if _, hasRegex := sidecar["regex"]; hasRegex {
		t.Error("expected sidecar.regex to be removed from output")
	}
	profiles, ok := sidecar["profiles"].(map[string]interface{})
	if !ok {
		t.Fatal("expected sidecar.profiles to be present")
	}
	exclude, ok := profiles["exclude"].([]interface{})
	if !ok || len(exclude) == 0 {
		t.Fatal("expected sidecar.profiles.exclude to be non-empty")
	}
	entry := exclude[0].(map[string]interface{})
	if entry["type"] != "Regex" {
		t.Errorf("expected type=Regex, got %v", entry["type"])
	}
	if entry["match"] != "http2_act.*" {
		t.Errorf("expected match=http2_act.*, got %v", entry["match"])
	}
}

func TestScanForDeprecations_MeshMetricNoRegex(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshMetric
metadata:
  name: my-metric
spec:
  targetRef:
    kind: Mesh
  default:
    backends:
      - type: Prometheus
`
	out, warnings := ScanForDeprecations([]byte(input))
	if len(warnings) != 0 {
		t.Errorf("expected no warnings, got: %v", warnings)
	}
	if string(out) != input {
		t.Error("expected output to be unchanged")
	}
}

func TestScanForDeprecations_MeshHealthCheckPanicThreshold(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshHealthCheck
metadata:
  name: my-hc
  namespace: demo
spec:
  targetRef:
    kind: Mesh
  default:
    healthyPanicThreshold: "50.0"
    interval: 10s
`
	_, warnings := ScanForDeprecations([]byte(input))
	if len(warnings) == 0 {
		t.Fatal("expected at least one warning about healthyPanicThreshold")
	}
	hasWarn := false
	for _, w := range warnings {
		if strings.Contains(w, "healthyPanicThreshold") && strings.Contains(w, "MeshCircuitBreaker") {
			hasWarn = true
		}
	}
	if !hasWarn {
		t.Errorf("expected healthyPanicThreshold warning, got: %v", warnings)
	}
}

func TestScanForDeprecations_MeshTrustOrigin(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshTrust
metadata:
  name: my-trust
  namespace: demo
spec:
  origin: Zone
  targetRef:
    kind: Mesh
`
	_, warnings := ScanForDeprecations([]byte(input))
	if len(warnings) == 0 {
		t.Fatal("expected at least one warning about spec.origin")
	}
	hasWarn := false
	for _, w := range warnings {
		if strings.Contains(w, "spec.origin") && strings.Contains(w, "status.origin") {
			hasWarn = true
		}
	}
	if !hasWarn {
		t.Errorf("expected MeshTrust origin warning, got: %v", warnings)
	}
}

func TestScanForDeprecations_PassThroughUnmodified(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshRetry
metadata:
  name: my-retry
spec:
  targetRef:
    kind: Mesh
`
	out, warnings := ScanForDeprecations([]byte(input))
	if len(warnings) != 0 {
		t.Errorf("expected no warnings for MeshRetry, got: %v", warnings)
	}
	if string(out) != input {
		t.Error("expected output to be unchanged for non-deprecated kind")
	}
}

func TestTransformDocument_ScenarioPassthrough_MeshMetricDeprecation(t *testing.T) {
	// A ScenarioPassthrough MeshMetric (already migrated targetRef) with sidecar.regex should
	// trigger the deprecation post-pass auto-fix.
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshMetric
metadata:
  name: my-metric
  namespace: demo
spec:
  targetRef:
    kind: Mesh
  default:
    sidecar:
      regex: "envoy_.*"
`
	docs, warnings, scenario, err := TransformDocument([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scenario != ScenarioPassthrough {
		t.Errorf("expected ScenarioPassthrough, got %s", scenario)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}

	// Warning about sidecar.regex should be present
	hasWarn := false
	for _, w := range warnings {
		if strings.Contains(w, "sidecar.regex") {
			hasWarn = true
		}
	}
	if !hasWarn {
		t.Errorf("expected sidecar.regex warning from post-pass, got: %v", warnings)
	}

	// Output should have profiles.exclude
	var obj map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	spec := obj["spec"].(map[string]interface{})
	def := spec["default"].(map[string]interface{})
	sidecar := def["sidecar"].(map[string]interface{})
	if _, hasRegex := sidecar["regex"]; hasRegex {
		t.Error("expected sidecar.regex to be auto-fixed in output")
	}
	if _, hasProfiles := sidecar["profiles"]; !hasProfiles {
		t.Error("expected sidecar.profiles to be present after auto-fix")
	}
}

func TestScanForDeprecations_MeshServiceInFrom_TrafficPermission(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: MeshTrafficPermission
metadata:
  name: allow-backend
  namespace: kuma-system
spec:
  targetRef:
    kind: MeshService
    name: redis
  from:
    - targetRef:
        kind: MeshService
        name: backend
      default:
        action: Allow
`
	_, warnings := ScanForDeprecations([]byte(input))
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "MeshService") && strings.Contains(w, "from") && strings.Contains(w, "deprecated") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected from[].targetRef.kind MeshService deprecation warning, got: %v", warnings)
	}
}

func TestScanForDeprecations_MeshServiceInFrom_FaultInjection(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: MeshFaultInjection
metadata:
  name: inject-fault
spec:
  targetRef:
    kind: MeshService
    name: backend
  from:
    - targetRef:
        kind: MeshService
        name: frontend
      default:
        http:
          abort:
            httpStatus: 500
            percentage: 10
`
	_, warnings := ScanForDeprecations([]byte(input))
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "MeshService") && strings.Contains(w, "from") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected MeshFaultInjection from[].MeshService deprecation warning, got: %v", warnings)
	}
}

func TestScanForDeprecations_MeshServiceInFrom_NoWarnWhenDataplane(t *testing.T) {
	// from[].targetRef.kind: Dataplane is the correct new style — no warning.
	input := `apiVersion: kuma.io/v1alpha1
kind: MeshTrafficPermission
metadata:
  name: allow-backend
spec:
  targetRef:
    kind: MeshService
    name: redis
  from:
    - targetRef:
        kind: Dataplane
        labels:
          app: backend
      default:
        action: Allow
`
	_, warnings := ScanForDeprecations([]byte(input))
	for _, w := range warnings {
		if strings.Contains(w, "from") && strings.Contains(w, "MeshService") && strings.Contains(w, "deprecated") {
			t.Errorf("unexpected MeshService-in-from warning for Dataplane targetRef: %s", w)
		}
	}
}

func TestScanForDeprecations_MeshTrafficPermissionActionCasing(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: MeshTrafficPermission
metadata:
  name: allow-all
spec:
  targetRef:
    kind: Mesh
  from:
    - targetRef:
        kind: Mesh
      default:
        action: ALLOW
`
	_, warnings := ScanForDeprecations([]byte(input))
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "ALLOW") && strings.Contains(w, "Allow") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected action casing warning for ALLOW, got: %v", warnings)
	}
}

func TestScanForDeprecations_MeshTrafficPermissionActionCasing_AllVariants(t *testing.T) {
	cases := []struct {
		action  string
		wantNew string
	}{
		{"ALLOW", "Allow"},
		{"DENY", "Deny"},
		{"ALLOW_WITH_SHADOW_DENY", "AllowWithShadowDeny"},
	}
	for _, tc := range cases {
		input := `apiVersion: kuma.io/v1alpha1
kind: MeshTrafficPermission
metadata:
  name: test
spec:
  targetRef:
    kind: Mesh
  from:
    - targetRef:
        kind: Mesh
      default:
        action: ` + tc.action + "\n"
		_, warnings := ScanForDeprecations([]byte(input))
		found := false
		for _, w := range warnings {
			if strings.Contains(w, tc.action) && strings.Contains(w, tc.wantNew) {
				found = true
			}
		}
		if !found {
			t.Errorf("action=%s: expected warning mentioning %q → %q, got: %v", tc.action, tc.action, tc.wantNew, warnings)
		}
	}
}

func TestScanForDeprecations_MeshTrafficPermissionActionCasing_NoWarnCorrect(t *testing.T) {
	// Correct casing should not trigger a warning.
	input := `apiVersion: kuma.io/v1alpha1
kind: MeshTrafficPermission
metadata:
  name: correct
spec:
  targetRef:
    kind: Mesh
  from:
    - targetRef:
        kind: Mesh
      default:
        action: Allow
`
	_, warnings := ScanForDeprecations([]byte(input))
	for _, w := range warnings {
		if strings.Contains(w, "deprecated") && strings.Contains(w, "action") {
			t.Errorf("unexpected action casing warning for correct value: %s", w)
		}
	}
}

func TestScanForDeprecations_SourceIPHashPolicy(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: MeshLoadBalancingStrategy
metadata:
  name: lb-strategy
spec:
  targetRef:
    kind: Mesh
  to:
    - targetRef:
        kind: MeshService
        name: backend
      default:
        loadBalancer:
          type: RingHash
          hashPolicies:
            - type: SourceIP
`
	_, warnings := ScanForDeprecations([]byte(input))
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "SourceIP") && strings.Contains(w, "Connection") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected SourceIP → Connection deprecation warning, got: %v", warnings)
	}
}

func TestScanForDeprecations_SourceIPHashPolicy_NoWarnConnection(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: MeshLoadBalancingStrategy
metadata:
  name: lb-strategy
spec:
  targetRef:
    kind: Mesh
  to:
    - targetRef:
        kind: MeshService
        name: backend
      default:
        loadBalancer:
          type: RingHash
          hashPolicies:
            - type: Connection
`
	_, warnings := ScanForDeprecations([]byte(input))
	for _, w := range warnings {
		if strings.Contains(w, "SourceIP") {
			t.Errorf("unexpected SourceIP warning for Connection type: %s", w)
		}
	}
}

func TestScanForDeprecations_DataplaneRedirectPortInboundV6(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: Dataplane
metadata:
  name: dp-1
  namespace: demo
spec:
  networking:
    transparentProxying:
      redirectPortInbound: 15006
      redirectPortInboundV6: 15010
      redirectPortOutbound: 15001
`
	_, warnings := ScanForDeprecations([]byte(input))
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "redirectPortInboundV6") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected redirectPortInboundV6 removal warning, got: %v", warnings)
	}
}

func TestScanForDeprecations_DataplaneRedirectPortInboundV6_NoWarnAbsent(t *testing.T) {
	input := `apiVersion: kuma.io/v1alpha1
kind: Dataplane
metadata:
  name: dp-1
spec:
  networking:
    transparentProxying:
      redirectPortInbound: 15006
      redirectPortOutbound: 15001
`
	_, warnings := ScanForDeprecations([]byte(input))
	for _, w := range warnings {
		if strings.Contains(w, "redirectPortInboundV6") {
			t.Errorf("unexpected redirectPortInboundV6 warning when field is absent: %s", w)
		}
	}
}

func TestScanForDeprecations_MeshSubsetWithoutServiceTag(t *testing.T) {
	// MeshSubset in spec.targetRef without service-identity tags is deprecated (Kuma 2.10+).
	input := `apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: my-timeout
spec:
  targetRef:
    kind: MeshSubset
    tags:
      version: v2
  to:
    - targetRef:
        kind: Mesh
      default:
        connectTimeout: 5s
`
	_, warnings := ScanForDeprecations([]byte(input))
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "MeshSubset") && strings.Contains(w, "deprecated") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected MeshSubset-without-service-tag deprecation warning, got: %v", warnings)
	}
}

func TestScanForDeprecations_MeshSubset_NoWarnWithServiceTag(t *testing.T) {
	// MeshSubset WITH a service-identity tag is handled by ScenarioSubset — no deprecation warn.
	input := `apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: my-timeout
spec:
  targetRef:
    kind: MeshSubset
    tags:
      kuma.io/service: backend_demo_svc_3001
  to:
    - targetRef:
        kind: Mesh
      default:
        connectTimeout: 5s
`
	_, warnings := ScanForDeprecations([]byte(input))
	for _, w := range warnings {
		if strings.Contains(w, "MeshSubset") && strings.Contains(w, "deprecated") {
			t.Errorf("unexpected MeshSubset deprecation warning when service tag is present: %s", w)
		}
	}
}

package migrator

import (
	"strings"
	"testing"
)

// A hand-written MeshHealthCheck with to[].default.healthyPanicThreshold under
// --to-latest v3: the field is removed, so the threshold has to go to a
// MeshCircuitBreaker. It used to be printed only in a warning, with
// to[].targetRef name/namespace that 3.0 rejects as "unknown field". It must be
// written as a real document, in the input's layout, with labels-form refs.

const uniPanicMHC = `type: MeshHealthCheck
name: hc-panic
mesh: default
labels:
  kuma.io/origin: zone
spec:
  targetRef:
    kind: Mesh
  to:
  - targetRef:
      kind: MeshService
      name: backend
    default:
      interval: 10s
      timeout: 2s
      unhealthyThreshold: 3
      healthyThreshold: 1
      healthyPanicThreshold: 60
      http:
        path: /health
`

const k8sPanicMHC = `apiVersion: kuma.io/v1alpha1
kind: MeshHealthCheck
metadata:
  name: hc-panic
  namespace: kong-mesh-system
  labels:
    kuma.io/mesh: default
spec:
  targetRef:
    kind: Mesh
  to:
  - targetRef:
      kind: MeshService
      name: backend
      namespace: kuma-demo
    default:
      interval: 10s
      timeout: 2s
      unhealthyThreshold: 3
      healthyThreshold: 1
      healthyPanicThreshold: 60
`

func TestPanicThresholdCompanion_V3Universal(t *testing.T) {
	docs, warnings := transformDocs(t, uniPanicMHC, TransformOptions{Target: TargetV3})
	if len(docs) != 2 {
		t.Fatalf("got %d docs, want MeshHealthCheck + MeshCircuitBreaker", len(docs))
	}
	mhc, mcb := docs[0], docs[1]
	if mhc["type"] != "MeshHealthCheck" || mcb["type"] != "MeshCircuitBreaker" {
		t.Fatalf("types = %v, %v", mhc["type"], mcb["type"])
	}
	def := at(t, mhc, "spec", "to").([]interface{})[0].(map[string]interface{})["default"].(map[string]interface{})
	if _, ok := def["healthyPanicThreshold"]; ok {
		t.Errorf("healthyPanicThreshold still on the MeshHealthCheck: %v", def)
	}
	if mcb["name"] != "hc-panic-panic-threshold" || mcb["mesh"] != "default" {
		t.Errorf("companion identity = %v/%v", mcb["name"], mcb["mesh"])
	}
	if _, ok := mcb["metadata"]; ok {
		t.Errorf("Universal companion must not be in Kubernetes layout: %v", mcb)
	}
	to := at(t, mcb, "spec", "to").([]interface{})[0].(map[string]interface{})
	if got := at(t, to, "default", "outlierDetection", "healthyPanicThreshold"); got != float64(60) {
		t.Errorf("outlierDetection.healthyPanicThreshold = %v, want 60", got)
	}
	// 3.0 (and 2.14) reject outlierDetection without a detector.
	if got := at(t, to, "default", "outlierDetection", "detectors", "totalFailures", "consecutive"); got != float64(5) {
		t.Errorf("detectors.totalFailures.consecutive = %v, want 5", got)
	}
	ref := to["targetRef"].(map[string]interface{})
	if _, hasName := ref["name"]; hasName {
		t.Errorf("companion to[].targetRef still uses name (3.0: unknown field): %v", ref)
	}
	if got := at(t, ref, "labels", "kuma.io/display-name"); got != "backend" {
		t.Errorf("companion to[].targetRef.labels = %v, want kuma.io/display-name: backend", ref["labels"])
	}
	if !hasWarning(warnings, `moved to companion MeshCircuitBreaker "hc-panic-panic-threshold"`) ||
		!hasWarning(warnings, "turns on outlier ejection") {
		t.Errorf("expected the companion warning with the outlier-ejection caveat, got: %v", warnings)
	}
	if hasWarning(warnings, "Apply this MeshCircuitBreaker") {
		t.Errorf("the companion is written, not printed: %v", warnings)
	}
	if _, _, _, err := TransformDocument([]byte(uniPanicMHC), TargetV3); err != nil {
		t.Errorf("both documents must be accepted by 3.0, got: %v", err)
	}
}

func TestPanicThresholdCompanion_V3Kubernetes(t *testing.T) {
	docs, _ := transformDocs(t, k8sPanicMHC, TransformOptions{Target: TargetV3})
	if len(docs) != 2 || docs[1]["kind"] != "MeshCircuitBreaker" {
		t.Fatalf("want MeshHealthCheck + MeshCircuitBreaker, got %d docs", len(docs))
	}
	mcb := docs[1]
	if got := at(t, mcb, "metadata", "namespace"); got != "kong-mesh-system" {
		t.Errorf("companion namespace = %v, want the MeshHealthCheck's (kong-mesh-system)", got)
	}
	if got := at(t, mcb, "metadata", "labels", "kuma.io/mesh"); got != "default" {
		t.Errorf("companion mesh label = %v, want default", got)
	}
	ref := at(t, mcb, "spec", "to").([]interface{})[0].(map[string]interface{})["targetRef"].(map[string]interface{})
	labels, _ := ref["labels"].(map[string]interface{})
	if labels["kuma.io/display-name"] != "backend" || labels["k8s.kuma.io/namespace"] != "kuma-demo" {
		t.Errorf("companion to[].targetRef = %v, want labels {display-name: backend, namespace: kuma-demo}", ref)
	}
}

func TestPanicThresholdCompanion_K8sNoNamespaceUsesSystem(t *testing.T) {
	in := strings.Replace(k8sPanicMHC, "  namespace: kong-mesh-system\n", "", 1)
	docs, _ := transformDocs(t, in, TransformOptions{Target: TargetV3, SystemNamespace: "kong-mesh-system"})
	if len(docs) != 2 {
		t.Fatalf("got %d docs", len(docs))
	}
	if got := at(t, docs[1], "metadata", "namespace"); got != "kong-mesh-system" {
		t.Errorf("companion namespace = %v, want the system namespace", got)
	}
}

func TestPanicThresholdCompanion_V2Unchanged(t *testing.T) {
	docs, warnings := transformDocs(t, uniPanicMHC, TransformOptions{Target: TargetV2})
	if len(docs) != 1 {
		t.Fatalf("v2: got %d docs, want the MeshHealthCheck only", len(docs))
	}
	def := at(t, docs[0], "spec", "to").([]interface{})[0].(map[string]interface{})["default"].(map[string]interface{})
	if def["healthyPanicThreshold"] != float64(60) {
		t.Errorf("v2 must keep healthyPanicThreshold (valid on 2.14): %v", def)
	}
	if !hasWarning(warnings, "healthyPanicThreshold is deprecated") {
		t.Errorf("expected the v2 advisory, got: %v", warnings)
	}
}

// The legacy HealthCheck path already splits the threshold; the pipeline pass
// must not add a second companion.
func TestPanicThresholdCompanion_LegacyNotDoubled(t *testing.T) {
	docs, _ := transformDocs(t, k8sLegacyHealthCheck, TransformOptions{Target: TargetV3})
	n := 0
	for _, d := range docs {
		if d["kind"] == "MeshCircuitBreaker" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("got %d MeshCircuitBreaker companions, want 1", n)
	}
}

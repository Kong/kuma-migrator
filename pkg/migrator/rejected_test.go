package migrator

import (
	"errors"
	"strings"
	"testing"
)

// ---- RejectedByTargetError ----------------------------------------------------

func TestTransformDocument_MTPWithFromIsRejectedByV3(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshTrafficPermission
metadata:
  name: mtp
  namespace: kuma-system
spec:
  targetRef:
    kind: Mesh
  from:
    - targetRef:
        kind: Mesh
      default:
        action: Allow
`
	docs, warnings, _, err := TransformDocumentWithOptions([]byte(input), TransformOptions{Target: TargetV3})
	rej, ok := AsRejectedByTarget(err)
	if !ok {
		t.Fatalf("err = %v, want *RejectedByTargetError", err)
	}
	var target *RejectedByTargetError
	if !errors.As(err, &target) {
		t.Fatal("errors.As must find the marker")
	}
	// The contract: docs and warnings come back populated alongside the marker.
	if len(docs) != 1 || len(warnings) == 0 {
		t.Fatalf("docs=%d warnings=%d, want the converted output alongside the marker", len(docs), len(warnings))
	}
	if len(rej.Rejected) != 1 || rej.Rejected[0].Index != 0 || !rej.IsRejected(0) ||
		rej.Rejected[0].Kind != "MeshTrafficPermission" || rej.Rejected[0].Name != "mtp" {
		t.Fatalf("Rejected = %+v, want doc 0 MeshTrafficPermission mtp", rej.Rejected)
	}
	if !strings.Contains(rej.Error(), "spec.from") {
		t.Errorf("error does not name spec.from: %v", rej)
	}

	// v2 keeps it: 2.14 still accepts from[].
	if _, _, _, err := TransformDocumentWithOptions([]byte(input), TransformOptions{Target: TargetV2}); err != nil {
		t.Errorf("v2: unexpected error %v", err)
	}
}

func TestTransformDocument_MFIFromMeshIsNotRejected(t *testing.T) {
	// Every from[] entry targets kind: Mesh, so the v3 scan rewrites it to rules[].
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshFaultInjection
metadata:
  name: mfi
  namespace: kuma-system
spec:
  targetRef:
    kind: Mesh
  from:
    - targetRef:
        kind: Mesh
      default:
        http:
          - abort: {httpStatus: 500, percentage: 10}
`
	if _, _, _, err := TransformDocumentWithOptions([]byte(input), TransformOptions{Target: TargetV3}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTransformDocument_SubsetMTPFromIsRejectedByV3(t *testing.T) {
	// Subset scenario: the from[] MeshSubset cannot be expressed on 3.0, and the
	// policy still carries from[] — it must not land in the apply tree.
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshTrafficPermission
metadata:
  name: mtp
  namespace: kuma-system
spec:
  targetRef:
    kind: Mesh
  from:
    - targetRef:
        kind: MeshSubset
        tags:
          kuma.io/service: web_demo_svc_80
      default:
        action: Allow
`
	docs, _, _, err := TransformDocumentWithOptions([]byte(input), TransformOptions{Target: TargetV3})
	if _, ok := AsRejectedByTarget(err); !ok || len(docs) != 1 {
		t.Fatalf("err = %v docs = %d, want RejectedByTargetError with the doc", err, len(docs))
	}
}

func TestV3RejectionReasons(t *testing.T) {
	cases := []struct {
		name, doc, want string
	}{
		{"to[] MeshSubset", `
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata: {name: tm, namespace: kuma-system}
spec:
  targetRef: {kind: Mesh}
  to:
    - targetRef: {kind: MeshSubset, tags: {version: v2}}
      default: {idleTimeout: 1s}
`, "MeshSubset"},
		{"Dataplane by name", `
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata: {name: tm, namespace: kuma-system}
spec:
  targetRef: {kind: Dataplane, name: web-1234}
  to:
    - targetRef: {kind: Mesh}
      default: {idleTimeout: 1s}
`, "by name/namespace"},
		{"top-level MeshService", `
apiVersion: kuma.io/v1alpha1
kind: MeshRetry
metadata: {name: r, namespace: kuma-system}
spec:
  targetRef: {kind: MeshService, labels: {kuma.io/display-name: web}}
  to:
    - targetRef: {kind: Mesh}
      default: {http: {numRetries: 1}}
`, "only top-level kinds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := rejectedByTarget([][]byte{[]byte(tc.doc)}, TargetV3)
			rej, ok := AsRejectedByTarget(err)
			if !ok || !strings.Contains(rej.Error(), tc.want) {
				t.Fatalf("err = %v, want a rejection mentioning %q", err, tc.want)
			}
			if err := rejectedByTarget([][]byte{[]byte(tc.doc)}, TargetV2); err != nil {
				t.Errorf("v2 must never mark a document rejected: %v", err)
			}
		})
	}

	valid := `
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata: {name: tm, namespace: kuma-system}
spec:
  targetRef: {kind: Dataplane, labels: {app: web}}
  to:
    - targetRef: {kind: MeshService, labels: {kuma.io/display-name: backend}}
      default: {idleTimeout: 1s}
`
	if err := rejectedByTarget([][]byte{[]byte(valid)}, TargetV3); err != nil {
		t.Errorf("valid 3.0 policy marked rejected: %v", err)
	}
}

// ---- Producer policies keep their role on 3.0 ---------------------------------

func TestV3ProducerPolicyGetsZoneLabel(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: tm
  namespace: demo
  labels:
    kuma.io/zone: east
    kuma.io/policy-role: producer
spec:
  targetRef: {kind: Mesh}
  to:
    - targetRef: {kind: MeshService, name: backend}
      default: {idleTimeout: 1s}
`
	out, warnings, _ := scanMap(t, input, TargetV3)
	to := at(t, out, "spec", "to").([]interface{})[0].(map[string]interface{})
	labels := at(t, to, "targetRef", "labels").(map[string]interface{})
	want := map[string]interface{}{
		"kuma.io/display-name":  "backend",
		"k8s.kuma.io/namespace": "demo",
		"kuma.io/zone":          "east",
	}
	if len(labels) != 3 {
		t.Errorf("labels = %v, want exactly %v", labels, want)
	}
	for k, v := range want {
		if labels[k] != v {
			t.Errorf("labels[%s] = %v, want %v", k, labels[k], v)
		}
	}
	if !hasWarning(warnings, "added kuma.io/zone: east") {
		t.Errorf("missing zone-pin advisory: %v", warnings)
	}
}

func TestV3ProducerPolicyWithoutZoneWarns(t *testing.T) {
	// No role label: producer is inferred from the same-namespace to[] target.
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: tm
  namespace: demo
spec:
  targetRef: {kind: Mesh}
  to:
    - targetRef: {kind: MeshService, name: backend, namespace: demo}
      default: {idleTimeout: 1s}
`
	out, warnings, _ := scanMap(t, input, TargetV3)
	to := at(t, out, "spec", "to").([]interface{})[0].(map[string]interface{})
	missing(t, to, "targetRef", "labels", "kuma.io/zone")
	if !hasWarning(warnings, "`kuma.io/zone: <zone-name>`") || !hasWarning(warnings, "spec.to[0].targetRef") {
		t.Errorf("missing add-the-zone-label warning: %v", warnings)
	}
}

func TestV3NonProducerPoliciesAreNotZonePinned(t *testing.T) {
	cases := map[string]string{
		"consumer role": `
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: tm
  namespace: demo
  labels: {kuma.io/zone: east, kuma.io/policy-role: consumer}
spec:
  targetRef: {kind: Mesh}
  to:
    - targetRef: {kind: MeshService, name: backend, namespace: demo}
      default: {idleTimeout: 1s}
`,
		"cross-namespace target": `
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: tm
  namespace: demo
  labels: {kuma.io/zone: east}
spec:
  targetRef: {kind: Mesh}
  to:
    - targetRef: {kind: MeshService, name: backend, namespace: other}
      default: {idleTimeout: 1s}
`,
		"system namespace": `
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: tm
  namespace: kuma-system
  labels: {kuma.io/zone: east}
spec:
  targetRef: {kind: Mesh}
  to:
    - targetRef: {kind: MeshService, name: backend, namespace: kuma-system}
      default: {idleTimeout: 1s}
`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			out, warnings, _ := scanMap(t, input, TargetV3)
			to := at(t, out, "spec", "to").([]interface{})[0].(map[string]interface{})
			missing(t, to, "targetRef", "labels", "kuma.io/zone")
			if hasWarning(warnings, "producer policy") {
				t.Errorf("unexpected producer advisory: %v", warnings)
			}
		})
	}
}

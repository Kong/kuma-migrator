package migrator

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// Regression tests for the selector-generation fixes: Universal Subset input,
// Dataplane name-vs-labels at the top level, reserved tags in label selectors,
// and the 2.x from[] form of MeshTrafficPermission/MeshFaultInjection.

func unmarshalDoc(t *testing.T, doc []byte) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := yaml.Unmarshal(doc, &out); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, doc)
	}
	return out
}

// ---- Universal Subset input keeps its identity --------------------------------

const universalSubsetRetry = `
type: MeshRetry
name: retry-backend
mesh: payments
labels:
  team: core
creationTime: "2026-01-01T00:00:00Z"
spec:
  targetRef:
    kind: MeshSubset
    tags:
      kuma.io/service: frontend_demo_svc_8080
  to:
    - targetRef:
        kind: MeshSubset
        tags:
          kuma.io/service: backend_demo_svc_3001
      default:
        http:
          numRetries: 3
`

func TestScenarioSubset_UniversalKeepsTopLevelFields(t *testing.T) {
	for _, target := range []TargetVersion{TargetV2, TargetV3} {
		t.Run(target.String(), func(t *testing.T) {
			docs, _, scenario, err := TransformDocument([]byte(universalSubsetRetry), target)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if scenario != ScenarioSubset {
				t.Fatalf("scenario = %s, want Subset", scenario)
			}
			if len(docs) != 1 {
				t.Fatalf("got %d docs, want 1", len(docs))
			}
			out := unmarshalDoc(t, docs[0])
			if out["type"] != "MeshRetry" || out["name"] != "retry-backend" || out["mesh"] != "payments" {
				t.Errorf("Universal identity lost: type=%v name=%v mesh=%v\n%s", out["type"], out["name"], out["mesh"], docs[0])
			}
			for _, k := range []string{"kind", "apiVersion", "metadata"} {
				if _, ok := out[k]; ok {
					t.Errorf("Universal output must not grow a %q field:\n%s", k, docs[0])
				}
			}
			if out["creationTime"] != "2026-01-01T00:00:00Z" {
				t.Errorf("creationTime dropped:\n%s", docs[0])
			}
			if at(t, out, "labels", "team") != "core" {
				t.Errorf("own labels dropped:\n%s", docs[0])
			}
			if got := at(t, out, "spec", "targetRef", "kind"); got != "Dataplane" {
				t.Errorf("spec.targetRef.kind = %v, want Dataplane", got)
			}
			if got := at(t, out, "spec", "targetRef", "labels", "app"); got != "frontend" {
				t.Errorf("spec.targetRef.labels.app = %v, want frontend", got)
			}
			to := at(t, out, "spec", "to").([]interface{})[0].(map[string]interface{})
			if got := at(t, to, "targetRef", "kind"); got != "MeshService" {
				t.Errorf("to[0].targetRef.kind = %v, want MeshService", got)
			}
			if got := at(t, to, "default", "http", "numRetries"); got != float64(3) {
				t.Errorf("to[0].default lost: %v", got)
			}
		})
	}
}

func TestScenarioSubset_UniversalSplitKeepsLayout(t *testing.T) {
	input := `
type: MeshTimeout
name: tm
mesh: default
spec:
  targetRef:
    kind: Mesh
  from:
    - targetRef:
        kind: MeshSubset
        tags:
          kuma.io/service: web_demo_svc_80
      default:
        idleTimeout: 10s
  to:
    - targetRef:
        kind: MeshSubset
        tags:
          kuma.io/service: backend_demo_svc_3001
      default:
        idleTimeout: 20s
`
	docs, warnings, scenario, err := TransformDocument([]byte(input), TargetV2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scenario != ScenarioSubset || len(docs) != 2 {
		t.Fatalf("scenario %s, %d docs; want Subset, 2", scenario, len(docs))
	}
	in, out := unmarshalDoc(t, docs[0]), unmarshalDoc(t, docs[1])
	if in["type"] != "MeshTimeout" || in["name"] != "tm" || in["mesh"] != "default" {
		t.Errorf("inbound half lost identity: %v", in)
	}
	if out["type"] != "MeshTimeout" || out["name"] != "tm-outbound" || out["mesh"] != "default" {
		t.Errorf("outbound half lost identity: %v", out)
	}
	missing(t, in, "spec", "to")
	missing(t, in, "spec", "from")
	if _, ok := at(t, in, "spec", "rules").([]interface{}); !ok {
		t.Errorf("inbound half has no rules[]: %v", in)
	}
	missing(t, out, "spec", "rules")
	missing(t, out, "spec", "from")
	if !hasWarning(warnings, "tm-outbound") {
		t.Errorf("missing split warning: %v", warnings)
	}
}

func TestScenarioSubset_PreservesUnmodelledTargetRefFields(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: tm
  namespace: demo
  annotations:
    note: keep-me
spec:
  targetRef:
    kind: MeshSubset
    proxyTypes: [Sidecar]
    tags:
      kuma.io/service: web_demo_svc_80
  to:
    - targetRef:
        kind: Mesh
      default:
        idleTimeout: 10s
`
	docs, _, _, err := TransformDocument([]byte(input), TargetV2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := unmarshalDoc(t, docs[0])
	if at(t, out, "metadata", "annotations", "note") != "keep-me" {
		t.Errorf("metadata.annotations dropped:\n%s", docs[0])
	}
	pt, _ := at(t, out, "spec", "targetRef", "proxyTypes").([]interface{})
	if len(pt) != 1 || pt[0] != "Sidecar" {
		t.Errorf("spec.targetRef.proxyTypes dropped:\n%s", docs[0])
	}
}

func TestTransformDocument_GeneratedDocWithoutNameIsAnError(t *testing.T) {
	// No metadata.name / name: the converted document would be written as
	// other/unknown.yaml; it must be an error instead.
	input := `
kind: MeshRetry
spec:
  targetRef:
    kind: MeshSubset
    tags:
      kuma.io/service: web_demo_svc_80
`
	if _, _, _, err := TransformDocument([]byte(input), TargetV2); err == nil ||
		!strings.Contains(err.Error(), "lost its identity") {
		t.Fatalf("err = %v, want an identity-loss error", err)
	}
}

func TestUnnamedOutputError(t *testing.T) {
	cases := map[string]bool{
		"apiVersion: v1\nkind: X\nmetadata:\n  name: a\n":         false,
		"type: MeshRetry\nname: a\n":                              false,
		"apiVersion: \"\"\nkind: \"\"\nmetadata:\n  name: \"\"\n": true,
		"kind: X\nmetadata: {}\n":                                 true,
		"name: a\n":                                               true,
	}
	for doc, wantErr := range cases {
		if err := unnamedOutputError([]byte(doc), 0); (err != nil) != wantErr {
			t.Errorf("unnamedOutputError(%q) = %v, wantErr %v", doc, err, wantErr)
		}
	}
}

// ---- Top-level Dataplane is selected by labels on v2 -------------------------

func TestLegacy_TopLevelDataplaneUsesLabelsOnV2(t *testing.T) {
	input := `
type: Timeout
name: t
mesh: default
sources:
  - match: {kuma.io/service: web_demo_svc_80}
destinations:
  - match: {kuma.io/service: backend_demo_svc_3001}
conf:
  connectTimeout: 5s
`
	docs, warnings, _, err := TransformDocument([]byte(input), TargetV2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := unmarshalDoc(t, docs[0])
	tr := at(t, out, "spec", "targetRef").(map[string]interface{})
	if _, ok := tr["name"]; ok {
		t.Errorf("spec.targetRef must not carry a Dataplane name: %v", tr)
	}
	if at(t, tr, "labels", "app") != "web" || at(t, tr, "labels", "k8s.kuma.io/namespace") != "demo" {
		t.Errorf("spec.targetRef.labels = %v, want {app: web, k8s.kuma.io/namespace: demo}", tr["labels"])
	}
	if !hasWarning(warnings, "best-guess pod label") {
		t.Errorf("missing best-guess warning: %v", warnings)
	}
}

func TestOPAPolicy_SelectorUsesDataplaneLabelsOnV2(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: OPAPolicy
metadata:
  name: opa
mesh: default
spec:
  selectors:
    - match:
        kuma.io/service: backend_demo_svc_3001
  conf:
    policies:
      - inlineString: "package envoy.authz"
`
	docs, _, _, err := TransformDocument([]byte(input), TargetV2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := unmarshalDoc(t, docs[0])
	tr := at(t, out, "spec", "targetRef").(map[string]interface{})
	if tr["kind"] != "Dataplane" || tr["name"] != nil || at(t, tr, "labels", "app") != "backend" {
		t.Errorf("spec.targetRef = %v, want Dataplane labels {app: backend, ...}", tr)
	}
}

// ---- Reserved tags are not copied into labels selectors -----------------------

func TestLegacy_FaultInjectionDropsProtocolTagFromLabels(t *testing.T) {
	input := `
type: FaultInjection
name: fi
mesh: default
sources:
  - match: {kuma.io/service: frontend_demo_svc_8080}
destinations:
  - match:
      kuma.io/service: backend_demo_svc_3001
      kuma.io/protocol: http
conf:
  abort:
    httpStatus: 500
    percentage: 50
`
	docs, warnings, _, err := TransformDocument([]byte(input), TargetV2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := unmarshalDoc(t, docs[0])
	labels := at(t, out, "spec", "targetRef", "labels").(map[string]interface{})
	if _, ok := labels["kuma.io/protocol"]; ok {
		t.Errorf("kuma.io/protocol must not be a Dataplane label selector: %v", labels)
	}
	if labels["app"] != "backend" || labels["k8s.kuma.io/namespace"] != "demo" || len(labels) != 2 {
		t.Errorf("labels = %v, want {app: backend, k8s.kuma.io/namespace: demo}", labels)
	}
	if !hasWarning(warnings, "kuma.io/protocol=http") {
		t.Errorf("missing dropped-tag warning naming kuma.io/protocol: %v", warnings)
	}
}

// ---- 2.x from[] of MTP/MFI uses MeshSubset, not deprecated MeshService --------

func TestLegacy_InboundFromUsesMeshSubsetOnV2(t *testing.T) {
	for _, tc := range []struct{ legacy, newKind, conf string }{
		{"TrafficPermission", "MeshTrafficPermission", ""},
		{"FaultInjection", "MeshFaultInjection", "conf:\n  abort: {httpStatus: 500, percentage: 50}\n"},
	} {
		t.Run(tc.legacy, func(t *testing.T) {
			input := "type: " + tc.legacy + `
name: p
mesh: default
sources:
  - match: {kuma.io/service: web_demo_svc_80, version: v2}
  - match: {kuma.io/service: '*'}
destinations:
  - match: {kuma.io/service: backend_demo_svc_3001}
` + tc.conf
			docs, warnings, _, err := TransformDocument([]byte(input), TargetV2)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			out := unmarshalDoc(t, docs[0])
			if out["type"] != tc.newKind {
				t.Fatalf("type = %v, want %s (Universal input stays Universal)", out["type"], tc.newKind)
			}
			from := at(t, out, "spec", "from").([]interface{})
			first := at(t, from[0].(map[string]interface{}), "targetRef").(map[string]interface{})
			if first["kind"] != "MeshSubset" || at(t, first, "tags", "kuma.io/service") != "web_demo_svc_80" ||
				at(t, first, "tags", "version") != "v2" {
				t.Errorf("from[0].targetRef = %v, want MeshSubset{kuma.io/service: web_demo_svc_80, version: v2}", first)
			}
			second := at(t, from[1].(map[string]interface{}), "targetRef").(map[string]interface{})
			if second["kind"] != "Mesh" {
				t.Errorf("from[1].targetRef = %v, want kind: Mesh for a wildcard source", second)
			}
			// The tool's own scan must not flag its own output as deprecated.
			if hasWarning(warnings, "from[].targetRef.kind MeshService is deprecated") {
				t.Errorf("v2 output trips the MeshService-in-from[] deprecation: %v", warnings)
			}
		})
	}
}

func TestScenarioSubset_MTPFromKeepsMeshSubsetOnV2(t *testing.T) {
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
    - targetRef:
        kind: MeshService
        name: api_demo_svc_8080
      default:
        action: Allow
`
	docs, warnings, scenario, err := TransformDocument([]byte(input), TargetV2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scenario != ScenarioSubset {
		t.Fatalf("scenario = %s, want Subset", scenario)
	}
	from := at(t, unmarshalDoc(t, docs[0]), "spec", "from").([]interface{})
	for i, want := range []string{"web_demo_svc_80", "api_demo_svc_8080"} {
		tr := at(t, from[i].(map[string]interface{}), "targetRef").(map[string]interface{})
		if tr["kind"] != "MeshSubset" || at(t, tr, "tags", "kuma.io/service") != want {
			t.Errorf("from[%d].targetRef = %v, want MeshSubset{kuma.io/service: %s}", i, tr, want)
		}
	}
	if hasWarning(warnings, "from[].targetRef.kind MeshService is deprecated") {
		t.Errorf("v2 output trips the MeshService-in-from[] deprecation: %v", warnings)
	}
}

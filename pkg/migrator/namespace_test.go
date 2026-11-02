package migrator

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// e2e (Kong Mesh 2.14.7 and 3.0.1, Kubernetes): every legacy conversion and the
// OPAPolicy conversion were written without metadata.namespace, so `kubectl apply`
// put them in the caller's namespace, where the CP gives them the workload-owner
// role. They must land in the system namespace, which always yields role system.

func transformDocs(t *testing.T, input string, opts TransformOptions) ([]map[string]interface{}, []string) {
	t.Helper()
	docs, warnings, _, err := TransformDocumentWithOptions([]byte(input), opts)
	if err != nil {
		if _, ok := AsRejectedByTarget(err); !ok {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	out := make([]map[string]interface{}, 0, len(docs))
	for _, d := range docs {
		var obj map[string]interface{}
		if err := yaml.Unmarshal(d, &obj); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		out = append(out, obj)
	}
	return out, warnings
}

func namespaceOf(obj map[string]interface{}) interface{} {
	md, _ := obj["metadata"].(map[string]interface{})
	return md["namespace"]
}

const k8sLegacyHealthCheck = `
apiVersion: kuma.io/v1alpha1
kind: HealthCheck
mesh: default
metadata:
  name: hc-web
spec:
  sources:
  - match:
      kuma.io/service: frontend_kuma-demo_svc_8080
  destinations:
  - match:
      kuma.io/service: backend_kuma-demo_svc_3001
  conf:
    interval: 10s
    timeout: 2s
    healthyPanicThreshold: 60
`

func TestSystemNamespace_KubernetesLegacy(t *testing.T) {
	for _, target := range []TargetVersion{TargetV2, TargetV3} {
		docs, warnings := transformDocs(t, k8sLegacyHealthCheck,
			TransformOptions{Target: target, SystemNamespace: "kong-mesh-system"})
		wantDocs := 1
		if target.IsV3() {
			wantDocs = 2 // + the MeshCircuitBreaker carrying healthyPanicThreshold
		}
		if len(docs) != wantDocs {
			t.Fatalf("%s: got %d docs, want %d", target, len(docs), wantDocs)
		}
		for _, d := range docs {
			if got := namespaceOf(d); got != "kong-mesh-system" {
				t.Errorf("%s: %v metadata.namespace = %v, want kong-mesh-system", target, d["kind"], got)
			}
		}
		// The namespace came from the input tree: no guess to warn about.
		if hasWarning(warnings, "metadata.namespace set to") {
			t.Errorf("%s: unexpected default-namespace warning: %v", target, warnings)
		}
	}
}

func TestSystemNamespace_DefaultWhenUnknown(t *testing.T) {
	docs, warnings := transformDocs(t, k8sLegacyHealthCheck, TransformOptions{Target: TargetV2})
	if got := namespaceOf(docs[0]); got != "kuma-system" {
		t.Errorf("metadata.namespace = %v, want kuma-system", got)
	}
	if !hasWarning(warnings, `MeshHealthCheck "hc-web": metadata.namespace set to kuma-system`) {
		t.Errorf("expected a warning that the namespace is the default, got: %v", warnings)
	}
}

func TestSystemNamespace_UniversalLegacyHasNone(t *testing.T) {
	docs, _ := transformDocs(t, `
type: Timeout
name: t
mesh: default
sources:
  - match: {kuma.io/service: '*'}
destinations:
  - match: {kuma.io/service: '*'}
conf:
  connectTimeout: 5s
`, TransformOptions{Target: TargetV2, SystemNamespace: "kong-mesh-system"})
	if docs[0]["type"] != "MeshTimeout" {
		t.Fatalf("type = %v, want MeshTimeout (Universal layout)", docs[0]["type"])
	}
	if _, ok := docs[0]["metadata"]; ok {
		t.Errorf("Universal output must not gain metadata: %v", docs[0])
	}
}

func TestSystemNamespace_OPAPolicy(t *testing.T) {
	docs, _ := transformDocs(t, `
apiVersion: kuma.io/v1alpha1
kind: OPAPolicy
mesh: default
metadata:
  name: opa-backend
spec:
  selectors:
  - match:
      kuma.io/service: backend_kuma-demo_svc_3001
  conf:
    policies:
    - inlineString: |
        package envoy.authz
        default allow = false
`, TransformOptions{Target: TargetV2, SystemNamespace: "kong-mesh-system"})
	if docs[0]["kind"] != "MeshOPA" {
		t.Fatalf("kind = %v, want MeshOPA", docs[0]["kind"])
	}
	if got := namespaceOf(docs[0]); got != "kong-mesh-system" {
		t.Errorf("MeshOPA metadata.namespace = %v, want kong-mesh-system", got)
	}
}

func TestSystemNamespace_SubsetKeepsItsOwnNamespace(t *testing.T) {
	in := `
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: t
  %s
  labels:
    kuma.io/mesh: default
spec:
  targetRef:
    kind: MeshSubset
    tags:
      kuma.io/service: web_kuma-demo_svc_80
  to:
  - targetRef:
      kind: Mesh
    default:
      connectionTimeout: 2s
`
	docs, _ := transformDocs(t, strings.Replace(in, "%s", "namespace: kuma-demo", 1),
		TransformOptions{Target: TargetV2, SystemNamespace: "kong-mesh-system"})
	if got := namespaceOf(docs[0]); got != "kuma-demo" {
		t.Errorf("metadata.namespace = %v, want kuma-demo (an existing namespace is kept)", got)
	}
	docs, _ = transformDocs(t, strings.Replace(in, "%s", "", 1),
		TransformOptions{Target: TargetV2, SystemNamespace: "kong-mesh-system"})
	if got := namespaceOf(docs[0]); got != "kong-mesh-system" {
		t.Errorf("metadata.namespace = %v, want kong-mesh-system (none in the input)", got)
	}
}

func TestSetMissingSystemNamespace_SkipsClusterScopedAndForeignKinds(t *testing.T) {
	docs := [][]byte{
		[]byte("apiVersion: kuma.io/v1alpha1\nkind: Mesh\nmetadata:\n  name: default\n"),
		[]byte("apiVersion: kuma.io/v1alpha1\nkind: MeshGatewayConfig\nmetadata:\n  name: c\n"),
		[]byte("apiVersion: gateway.networking.k8s.io/v1\nkind: GatewayClass\nmetadata:\n  name: g\n"),
	}
	out, warnings, err := setMissingSystemNamespace(docs, TransformOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range out {
		if strings.Contains(string(d), "namespace") {
			t.Errorf("unexpected namespace on %s", d)
		}
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

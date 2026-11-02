package migrator

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

const routeWithSubsets = `
apiVersion: kuma.io/v1alpha1
kind: MeshHTTPRoute
metadata:
  name: split
  namespace: kuma-demo
spec:
  targetRef:
    kind: Mesh
  to:
    - targetRef:
        kind: MeshService
        name: frontend
      rules:
        - matches:
            - path:
                type: PathPrefix
                value: /
          default:
            filters:
              - type: RequestMirror
                requestMirror:
                  backendRef:
                    kind: MeshServiceSubset
                    tags:
                      kuma.io/service: shadow_kuma-demo_svc_9090
            backendRefs:
              - kind: MeshServiceSubset
                tags:
                  kuma.io/service: payments_kuma-demo_svc_8080
                weight: 90
              - kind: MeshServiceSubset
                tags:
                  kuma.io/service: payments
                  version: v2
                weight: 10
              - kind: MeshService
                labels:
                  kuma.io/display-name: other
                tags:
                  version: v1
`

func routeRefs(t *testing.T, raw []byte) ([]interface{}, map[string]interface{}) {
	t.Helper()
	var obj map[string]interface{}
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	def := obj["spec"].(map[string]interface{})["to"].([]interface{})[0].(map[string]interface{})["rules"].([]interface{})[0].(map[string]interface{})["default"].(map[string]interface{})
	mirror := def["filters"].([]interface{})[0].(map[string]interface{})["requestMirror"].(map[string]interface{})["backendRef"].(map[string]interface{})
	return def["backendRefs"].([]interface{}), mirror
}

func TestFixRouteBackendRefsForV3_MeshHTTPRoute(t *testing.T) {
	out, warnings := ScanForDeprecations([]byte(routeWithSubsets), TargetV3)
	refs, mirror := routeRefs(t, out)

	// Sole kuma.io/service tag → MeshService by labels, port decoded from the tag.
	first := refs[0].(map[string]interface{})
	labels, _ := first["labels"].(map[string]interface{})
	if first["kind"] != "MeshService" || labels["kuma.io/display-name"] != "payments" ||
		labels["k8s.kuma.io/namespace"] != "kuma-demo" || first["port"] != float64(8080) {
		t.Errorf("backendRefs[0] not rewritten as expected: %v", first)
	}
	if _, ok := first["tags"]; ok {
		t.Errorf("backendRefs[0] still carries tags: %v", first)
	}
	if first["weight"] != float64(90) {
		t.Errorf("weight must be preserved: %v", first)
	}

	// Extra tag → no equivalent, left as-is and warned.
	if second := refs[1].(map[string]interface{}); second["kind"] != "MeshServiceSubset" {
		t.Errorf("a subset with extra tags must not be rewritten: %v", second)
	}

	// Mirror backendRef is rewritten too.
	if mirror["kind"] != "MeshService" || mirror["port"] != float64(9090) {
		t.Errorf("requestMirror.backendRef not rewritten: %v", mirror)
	}

	joined := strings.Join(warnings, "\n")
	for _, want := range []string{
		"backendRefs[0] rewritten from kind: MeshServiceSubset",
		"requestMirror.backendRef rewritten",
		"backendRefs[1].kind is MeshServiceSubset",
		"split the destination into separate MeshService",
		"backendRefs[2].tags",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected warning containing %q, got: %v", want, warnings)
		}
	}
}

func TestFixRouteBackendRefsForV3_V2AdvisoryOnly(t *testing.T) {
	out, warnings := ScanForDeprecations([]byte(routeWithSubsets), TargetV2)
	if string(out) != routeWithSubsets {
		t.Errorf("v2 must not rewrite MeshServiceSubset (valid on 2.14), got:\n%s", out)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "still accepted in 2.14") || !strings.Contains(joined, "--to-latest v3 does this automatically") {
		t.Errorf("expected forward-looking advisory, got: %v", warnings)
	}
}

func TestFixRouteBackendRefsForV3_MeshTCPRouteUniversalName(t *testing.T) {
	input := `
type: MeshTCPRoute
name: tcp
mesh: default
spec:
  targetRef:
    kind: Mesh
  to:
    - targetRef:
        kind: MeshService
        name: redis
      rules:
        - default:
            backendRefs:
              - kind: MeshServiceSubset
                name: redis
`
	out, warnings := ScanForDeprecations([]byte(input), TargetV3)
	var obj map[string]interface{}
	if err := yaml.Unmarshal(out, &obj); err != nil {
		t.Fatal(err)
	}
	ref := obj["spec"].(map[string]interface{})["to"].([]interface{})[0].(map[string]interface{})["rules"].([]interface{})[0].(map[string]interface{})["default"].(map[string]interface{})["backendRefs"].([]interface{})[0].(map[string]interface{})
	labels, _ := ref["labels"].(map[string]interface{})
	if ref["kind"] != "MeshService" || labels["kuma.io/display-name"] != "redis" || ref["name"] != nil {
		t.Errorf("MeshTCPRoute backendRef not rewritten: %v", ref)
	}
	if _, ok := labels["k8s.kuma.io/namespace"]; ok {
		t.Errorf("no namespace label expected for a Universal name: %v", labels)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "No port could be derived") {
		t.Errorf("expected missing-port note, got: %v", warnings)
	}
}

func TestFixRouteBackendRefsForV3_CleanRouteSilent(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshHTTPRoute
metadata:
  name: clean
spec:
  targetRef:
    kind: Mesh
  to:
    - targetRef:
        kind: MeshService
        labels:
          kuma.io/display-name: backend
      rules:
        - matches:
            - path:
                type: PathPrefix
                value: /
          default:
            backendRefs:
              - kind: MeshService
                labels:
                  kuma.io/display-name: backend
                port: 80
`
	out, warnings := ScanForDeprecations([]byte(input), TargetV3)
	if string(out) != input {
		t.Errorf("clean route must pass through unchanged")
	}
	for _, w := range warnings {
		if strings.Contains(w, "backendRefs") {
			t.Errorf("unexpected backendRef warning: %s", w)
		}
	}
}

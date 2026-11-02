package migrator

import (
	"strings"
	"testing"
)

const routePortsMeshService = `apiVersion: kuma.io/v1alpha1
kind: MeshService
metadata:
  name: backend
  namespace: demo
  labels:
    kuma.io/mesh: default
spec:
  ports:
    - port: 80
      targetPort: 8080
      name: http
      appProtocol: http
    - port: 9090
      targetPort: 9090
`

func routeWithRef(ref string) string {
	return `apiVersion: kuma.io/v1alpha1
kind: MeshHTTPRoute
metadata:
  name: split
  namespace: demo
  labels:
    kuma.io/mesh: default
spec:
  targetRef: {kind: Mesh}
  to:
    - targetRef: {kind: MeshService, name: backend}
      rules:
        - matches: [{path: {type: PathPrefix, value: /}}]
          default:
            backendRefs:
              - ` + ref + `
`
}

func routePortsOpts(t *testing.T, target TargetVersion, withService bool) TransformOptions {
	t.Helper()
	dir := tempDir(t)
	if withService {
		writeTempFile(t, dir, "ms.yaml", routePortsMeshService)
	}
	return BuildTransformOptions(dir, target)
}

func TestRouteBackendRefPorts(t *testing.T) {
	cases := []struct {
		name     string
		ref      string
		target   TargetVersion
		withMS   bool
		wantWarn string // "" = no port warning
	}{
		{"port number exists", "{kind: MeshService, name: backend, namespace: demo, port: 80}", TargetV3, true, ""},
		{"port number missing", "{kind: MeshService, name: backend, namespace: demo, port: 8080}", TargetV3, true, "port is 8080, which MeshService \"backend\""},
		{"targetPort is not a match", "{kind: MeshService, name: backend, port: 8080}", TargetV3, true, "never targetPort"},
		{"sectionName by name", "{kind: MeshService, name: backend, sectionName: http}", TargetV3, true, ""},
		{"sectionName by number", "{kind: MeshService, name: backend, sectionName: '9090'}", TargetV3, true, ""},
		{"sectionName missing", "{kind: MeshService, name: backend, sectionName: grpc}", TargetV3, true, "sectionName is grpc"},
		{"labels form missing port", "{kind: MeshService, labels: {kuma.io/display-name: backend, k8s.kuma.io/namespace: demo}, port: 81}", TargetV3, true, "port is 81"},
		{"no port named", "{kind: MeshService, name: backend}", TargetV3, true, ""},
		{"other namespace not indexed", "{kind: MeshService, name: backend, namespace: other, port: 81}", TargetV3, true, "not in the input"},
		{"service not in input", "{kind: MeshService, name: backend, port: 81}", TargetV3, false, "not in the input"},
		{"v2 does not check", "{kind: MeshService, name: backend, port: 8080}", TargetV2, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := routePortsOpts(t, tc.target, tc.withMS)
			_, warnings, _, err := TransformDocumentWithOptions([]byte(routeWithRef(tc.ref)), opts)
			if err != nil {
				t.Fatal(err)
			}
			var portWarns []string
			for _, w := range warnings {
				if strings.Contains(w, "backendRefs are all unresolved answers 500") {
					portWarns = append(portWarns, w)
				}
			}
			if tc.wantWarn == "" {
				if len(portWarns) > 0 {
					t.Errorf("unexpected port warning: %v", portWarns)
				}
				return
			}
			if len(portWarns) != 1 || !strings.Contains(portWarns[0], tc.wantWarn) {
				t.Errorf("want one port warning containing %q, got %v", tc.wantWarn, portWarns)
			}
		})
	}
}

// The "not in the input" note is per run, not per ref or per document.
func TestRouteBackendRefPorts_MissingNotedOncePerRun(t *testing.T) {
	opts := routePortsOpts(t, TargetV3, false)
	count := 0
	for i := 0; i < 3; i++ {
		_, warnings, _, err := TransformDocumentWithOptions([]byte(routeWithRef("{kind: MeshService, name: backend, port: 81}")), opts)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range warnings {
			if strings.Contains(w, "not in the input") {
				count++
			}
		}
	}
	if count != 1 {
		t.Errorf("missing-MeshService note printed %d times, want 1", count)
	}
}

// Universal MeshService (no namespace) and Universal route.
func TestRouteBackendRefPorts_Universal(t *testing.T) {
	dir := tempDir(t)
	writeTempFile(t, dir, "ms.yaml", `type: MeshService
name: backend
mesh: prod
spec:
  ports:
    - port: 80
      name: http
`)
	opts := BuildTransformOptions(dir, TargetV3)
	route := `type: MeshHTTPRoute
name: r
mesh: prod
spec:
  targetRef: {kind: Mesh}
  to:
    - targetRef: {kind: MeshService, labels: {kuma.io/display-name: backend}}
      rules:
        - default:
            backendRefs:
              - {kind: MeshService, labels: {kuma.io/display-name: backend}, port: 443}
`
	_, warnings, _, err := TransformDocumentWithOptions([]byte(route), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(warnings, "port is 443, which MeshService \"backend\" (mesh \"prod\") does not have — its ports are 80 (http)") {
		t.Errorf("missing Universal port warning: %v", warnings)
	}
}

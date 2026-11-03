package migrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- proxyTypes ---------------------------------------------------------------

func TestFixProxyTypes(t *testing.T) {
	k8s := func(name, kind, proxyTypes string) string {
		return `apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: ` + name + `
  namespace: kuma-system
spec:
  targetRef:
    kind: ` + kind + `
    proxyTypes: ` + proxyTypes + `
  to:
  - targetRef:
      kind: Mesh
    default:
      idleTimeout: 1h
`
	}
	uni := func(name, proxyTypes string) string {
		return `type: MeshRetry
mesh: default
name: ` + name + `
spec:
  targetRef:
    kind: Mesh
    proxyTypes: ` + proxyTypes + `
  to:
  - targetRef:
      kind: Mesh
    default:
      http:
        numRetries: 3
`
	}
	cases := []struct {
		name       string
		in         string
		target     TargetVersion
		wantKept   bool   // proxyTypes still in the output
		wantReject bool   // v3RejectionReasons flags it
		wantWarn   string // substring of a warning
	}{
		{"v3 sidecar only k8s", k8s("t", "Mesh", "[Sidecar]"), TargetV3, false, false, "now also selects the proxies that are gateways"},
		{"v3 sidecar only universal", uni("r", "[Sidecar]"), TargetV3, false, false, "Auto-removed"},
		{"v3 both values", k8s("t", "Mesh", "[Sidecar, Gateway]"), TargetV3, false, false, "scope does not change"},
		{"v3 empty list", uni("r", "[]"), TargetV3, false, false, "scope does not change"},
		{"v3 ignored on Dataplane kind", k8s("t", "Dataplane", "[Gateway]"), TargetV3, false, false, "2.x ignores it on a top-level kind Dataplane"},
		{"v3 gateway only k8s", k8s("gw-timeouts", "Mesh", "[Gateway]"), TargetV3, true, true, "restricts the policy to gateway proxies"},
		{"v3 gateway only universal", uni("gw-retry", "[Gateway]"), TargetV3, true, true, "Not converted"},
		{"v3 default gateway timeout", k8s("mesh-gateways-timeout-all-default", "Mesh", "[Gateway]"), TargetV3, true, false, "kumahq/kuma#18857"},
		{"v2 gateway only", k8s("gw-timeouts", "Mesh", "[Gateway]"), TargetV2, true, false, "selects every proxy, sidecars included"},
		{"v2 sidecar only", uni("r", "[Sidecar]"), TargetV2, true, false, "--to-latest v3 drops the field"},
		{"v2 default gateway timeout", k8s("mesh-gateways-timeout-all-default", "Mesh", "[Gateway]"), TargetV2, true, false, "skipCreatingInitialPolicies"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			obj, out, ws := scanObj(t, c.in, c.target)
			_, kept := digPath(t, obj, "spec", "targetRef").(map[string]interface{})["proxyTypes"]
			if kept != c.wantKept {
				t.Errorf("proxyTypes kept = %v, want %v:\n%s", kept, c.wantKept, out)
			}
			if !strings.Contains(joined(ws), c.wantWarn) {
				t.Errorf("expected warning containing %q, got %v", c.wantWarn, ws)
			}
			kind, _ := obj["kind"].(string)
			if kind == "" {
				kind, _ = obj["type"].(string)
			}
			rejected := false
			for _, r := range v3RejectionReasons(obj, extractNameFromObj(obj), kind) {
				if strings.Contains(r, "proxyTypes") {
					rejected = true
				}
			}
			if c.target.IsV3() && rejected != c.wantReject {
				t.Errorf("rejected = %v, want %v", rejected, c.wantReject)
			}
		})
	}
}

func TestProxyTypes_RejectedThroughTransform(t *testing.T) {
	in := `type: MeshTimeout
mesh: default
name: gw-timeouts
spec:
  targetRef:
    kind: Mesh
    proxyTypes: [Gateway]
  to:
  - targetRef:
      kind: Mesh
    default:
      idleTimeout: 1h
`
	docs, _, _, err := TransformDocumentWithOptions([]byte(in), TransformOptions{Target: TargetV3})
	rej, ok := AsRejectedByTarget(err)
	if !ok || len(docs) != 1 || !rej.IsRejected(0) {
		t.Fatalf("expected the document to be rejected under v3, got err=%v docs=%d", err, len(docs))
	}
	if _, _, _, err := TransformDocumentWithOptions([]byte(in), TransformOptions{Target: TargetV2}); err != nil {
		t.Errorf("v2 must not reject it: %v", err)
	}
}

const defaultGatewayTimeout = `apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: mesh-gateways-timeout-all-default
  namespace: kuma-system
  labels:
    kuma.io/mesh: default
spec:
  targetRef:
    kind: Mesh
    proxyTypes: [Gateway]
  from:
  - targetRef:
      kind: Mesh
    default:
      idleTimeout: 5m
      http:
        streamIdleTimeout: 5s
        requestHeadersTimeout: 500ms
  to:
  - targetRef:
      kind: Mesh
    default:
      idleTimeout: 1h
      http:
        streamIdleTimeout: 5s
`

func TestMigrate_V3DefaultGatewayTimeoutListedForDeletion(t *testing.T) {
	noUserConfig(t)
	in, out := t.TempDir(), t.TempDir()
	writeTree(t, in, map[string]string{
		"prod-zone-ctx/.kuma-migrator.json":             `{"tool":"kubectl","context":"prod","cpMode":"zone","environment":"kubernetes"}`,
		"prod-zone-ctx/mesh-default/resiliency/gw.yaml": defaultGatewayTimeout,
	})
	r, err := runMigration(in, out, true, "", TargetV3)
	if err != nil {
		t.Fatal(err)
	}
	fr, dc, ok := findChange(r, "MeshTimeout")
	if !ok {
		t.Fatal("MeshTimeout not in report")
	}
	if !dc.DeleteBeforeV3 || dc.ErrMsg != "" {
		t.Errorf("expected DeleteBeforeV3 without error, got %+v", dc)
	}
	if len(fr.OutputRelPaths) != 0 {
		t.Errorf("must not be in the apply tree, got %v", fr.OutputRelPaths)
	}
	if !strings.HasPrefix(dc.ManualRelPath, "prod-zone-ctx/"+ManualDir+"/") || len(dc.ManualRelPaths) != 1 {
		t.Errorf("expected one copy under %s/, got %v", ManualDir, dc.ManualRelPaths)
	}
	written, err := os.ReadFile(filepath.Join(out, dc.ManualRelPath))
	if err != nil || !strings.Contains(string(written), "proxyTypes") {
		t.Errorf("the manual copy must be the original, got %v:\n%s", err, written)
	}

	var b strings.Builder
	r.Mode = "apply"
	writeMarkdown(&b, r)
	md := b.String()
	i := strings.Index(md, "## Delete Before Upgrading to 3.0")
	if i < 0 {
		t.Fatalf("missing delete section:\n%s", md)
	}
	del := md[i:]
	for _, want := range []string{
		"kubectl delete MeshTimeout mesh-gateways-timeout-all-default -n kuma-system",
		"none — delete it; 3.0 applies it to every proxy",
		"skipCreatingInitialPolicies",
	} {
		if !strings.Contains(del, want) {
			t.Errorf("delete section should contain %q:\n%s", want, del)
		}
	}
	if !strings.Contains(md, "default policy to delete before upgrading") {
		t.Error("Kept Under _manual/ should give the default-policy reason")
	}

	// v2: still converted (Rules split), kept in the apply tree, with the advisory.
	out2 := t.TempDir()
	r2, err := runMigration(in, out2, true, "", TargetV2)
	if err != nil {
		t.Fatal(err)
	}
	fr2, dc2, _ := findChange(r2, "MeshTimeout")
	if dc2.DeleteBeforeV3 || len(fr2.OutputRelPaths) == 0 {
		t.Errorf("v2 keeps it in the apply tree, got %+v / %v", dc2, fr2.OutputRelPaths)
	}
	if !strings.Contains(joined(dc2.Warnings), "delete it right before the 3.0 upgrade") {
		t.Errorf("expected v2 advisory, got %v", dc2.Warnings)
	}
}

// ---- appProtocol: kafka ---------------------------------------------------------

func TestFixKafkaAppProtocol(t *testing.T) {
	ms := `apiVersion: kuma.io/v1alpha1
kind: MeshService
metadata:
  name: broker
  namespace: kafka
spec:
  selector:
    dataplaneLabels:
      matchLabels:
        app: broker
  ports:
  - name: kafka
    port: 9092
    appProtocol: kafka
  - name: admin
    port: 8080
    appProtocol: http
`
	mzs := `type: MeshMultiZoneService
mesh: default
name: broker
spec:
  selector:
    meshService:
      matchLabels:
        kuma.io/display-name: broker
  ports:
  - port: 9092
    appProtocol: Kafka
`
	cases := []struct {
		name   string
		in     string
		target TargetVersion
		want   interface{} // ports[0].appProtocol after the scan
		warn   string
	}{
		{"MeshService v3", ms, TargetV3, "tcp", "Auto-rewritten to tcp on ports[0] (kafka)"},
		{"MeshService v2", ms, TargetV2, "kafka", "--to-latest v3 rewrites it to tcp"},
		{"MeshMultiZoneService v3", mzs, TargetV3, "tcp", "Auto-rewritten to tcp on ports[0] (port 9092)"},
		{"MeshMultiZoneService v2", mzs, TargetV2, "Kafka", "kumahq/kuma#17831"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			obj, out, ws := scanObj(t, c.in, c.target)
			if got := digPath(t, obj, "spec", "ports", 0, "appProtocol"); got != c.want {
				t.Errorf("appProtocol = %v, want %v:\n%s", got, c.want, out)
			}
			if !strings.Contains(joined(ws), c.warn) {
				t.Errorf("expected warning containing %q, got %v", c.warn, ws)
			}
			if !strings.Contains(joined(ws), "kafka_broker") {
				t.Errorf("warning should explain the stats filter, got %v", ws)
			}
		})
	}
}

func TestFixDataplaneInboundKafka(t *testing.T) {
	in := `type: Dataplane
mesh: default
name: broker-1
networking:
  address: 10.0.0.5
  inbound:
  - port: 9092
    tags:
      kuma.io/service: broker
      kuma.io/protocol: kafka
`
	obj, out, ws := scanObj(t, in, TargetV3)
	if got := digPath(t, obj, "networking", "inbound", 0, "protocol"); got != "tcp" {
		t.Errorf("v3: inbound protocol = %v, want tcp:\n%s", got, out)
	}
	if !strings.Contains(joined(ws), "Auto-set protocol: tcp") {
		t.Errorf("v3: expected kafka note, got %v", ws)
	}

	obj, _, ws = scanObj(t, in, TargetV2)
	if got := digPath(t, obj, "networking", "inbound", 0, "protocol"); got != nil {
		t.Errorf("v2 must not set the protocol, got %v", got)
	}
	if !strings.Contains(joined(ws), "inbound protocol kafka") {
		t.Errorf("v2: expected kafka advisory, got %v", ws)
	}
}

// ---- directAccessServices --------------------------------------------------------

func TestFixDirectAccessServices(t *testing.T) {
	dp := func(list string) string {
		return `type: Dataplane
mesh: default
name: dp1
networking:
  address: 10.0.0.2
  inbound:
  - port: 8080
    protocol: http
  transparentProxying:
    redirectPortInbound: 15006
    redirectPortOutbound: 15001
    reachableBackends:
      refs: []
    directAccessServices: ` + list + `
`
	}
	deploy := func(value string) string {
		return `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: demo
spec:
  template:
    metadata:
      annotations:
        kuma.io/direct-access-services: "` + value + `"
    spec:
      containers:
      - name: web
        image: web
`
	}
	cases := []struct {
		name   string
		in     string
		target TargetVersion
		path   []interface{}
		want   interface{}
		warn   string
	}{
		{"Dataplane named v3", dp("[backend_demo_svc_80, redis]"), TargetV3,
			[]interface{}{"networking", "transparentProxying", "directAccessServices", 0}, "backend_demo_svc_80",
			"ignores named entries silently"},
		{"Dataplane named v2", dp("[backend_demo_svc_80]"), TargetV2,
			[]interface{}{"networking", "transparentProxying", "directAccessServices", 0}, "backend_demo_svc_80",
			"Not converted by --to-latest v3"},
		{"Dataplane wildcard plus names v3", dp(`["*", redis]`), TargetV3,
			[]interface{}{"networking", "transparentProxying", "directAccessServices", 1}, nil,
			"Auto-reduced to \"*\""},
		{"Deployment annotation named v3", deploy("backend_demo_svc_80,redis"), TargetV3,
			[]interface{}{"spec", "template", "metadata", "annotations", annotationDirectAccessServices}, "backend_demo_svc_80,redis",
			"kuma.io/direct-access-services annotation (spec.template.metadata.annotations) names services"},
		{"Deployment annotation wildcard plus names v3", deploy("*,redis"), TargetV3,
			[]interface{}{"spec", "template", "metadata", "annotations", annotationDirectAccessServices}, "*",
			"Auto-reduced"},
		{"Deployment annotation wildcard plus names v2", deploy("*,redis"), TargetV2,
			[]interface{}{"spec", "template", "metadata", "annotations", annotationDirectAccessServices}, "*,redis",
			"the names are redundant"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			obj, out, ws := scanObj(t, c.in, c.target)
			if got := digPath(t, obj, c.path...); got != c.want {
				t.Errorf("value = %v, want %v:\n%s", got, c.want, out)
			}
			if !strings.Contains(joined(ws), c.warn) {
				t.Errorf("expected warning containing %q, got %v", c.warn, ws)
			}
		})
	}

	// "*" alone is honoured on 3.0: no warning.
	_, _, ws := scanObj(t, dp(`["*"]`), TargetV3)
	if strings.Contains(joined(ws), "directAccessServices") {
		t.Errorf("\"*\" alone must not warn, got %v", ws)
	}
}

// ---- MeshRetry legacy field names ------------------------------------------------

func TestFixMeshRetryLegacyFields(t *testing.T) {
	k8s := `apiVersion: kuma.io/v1alpha1
kind: MeshRetry
metadata:
  name: backend-retry
  namespace: kuma-system
spec:
  targetRef:
    kind: Mesh
  to:
  - targetRef:
      kind: MeshService
      labels:
        kuma.io/display-name: backend
    default:
      http:
        numRetries: 3
        retryOn: [5xx, "503"]
        retriableStatusCodes: [503, 504, 999]
        retriableMethods: [GET, post]
      tcp:
        maxConnectAttempts: 4
`
	uni := `type: MeshRetry
mesh: default
name: backend-retry
spec:
  targetRef:
    kind: Mesh
  to:
  - targetRef:
      kind: Mesh
    default:
      tcp:
        maxConnectAttempts: 4
        maxConnectAttempt: 2
`
	for _, target := range []TargetVersion{TargetV2, TargetV3} {
		t.Run("kubernetes "+target.String(), func(t *testing.T) {
			obj, out, ws := scanObj(t, k8s, target)
			def := digPath(t, obj, "spec", "to", 0, "default")
			retryOn := digPath(t, def, "http", "retryOn").([]interface{})
			want := []string{"5xx", "503", "504", "HttpMethodGet", "HttpMethodPost"}
			if strings.Join(stringsOf(retryOn), ",") != strings.Join(want, ",") {
				t.Errorf("retryOn = %v, want %v:\n%s", retryOn, want, out)
			}
			for _, gone := range [][]interface{}{
				{"http", "retriableStatusCodes"}, {"http", "retriableMethods"}, {"tcp", "maxConnectAttempts"},
			} {
				if digPath(t, def, gone...) != nil {
					t.Errorf("%v should be removed:\n%s", gone, out)
				}
			}
			if got := digPath(t, def, "tcp", "maxConnectAttempt"); got != float64(4) {
				t.Errorf("tcp.maxConnectAttempt = %v, want 4", got)
			}
			for _, w := range []string{"Resources with fields that are not in the schema", "now take effect", "value 999 (not an HTTP status code)"} {
				if !strings.Contains(joined(ws), w) {
					t.Errorf("expected warning containing %q, got %v", w, ws)
				}
			}
		})
		t.Run("universal "+target.String(), func(t *testing.T) {
			obj, out, ws := scanObj(t, uni, target)
			if got := digPath(t, obj, "spec", "to", 0, "default", "tcp", "maxConnectAttempt"); got != float64(2) {
				t.Errorf("existing maxConnectAttempt must win, got %v:\n%s", got, out)
			}
			if digPath(t, obj, "spec", "to", 0, "default", "tcp", "maxConnectAttempts") != nil {
				t.Errorf("legacy field should be removed:\n%s", out)
			}
			if !strings.Contains(joined(ws), "already set and kept") {
				t.Errorf("expected conflict note, got %v", ws)
			}
		})
	}

	clean := `type: MeshRetry
mesh: default
name: ok
spec:
  targetRef:
    kind: Mesh
  to:
  - targetRef:
      kind: Mesh
    default:
      http:
        retryOn: ["503"]
`
	if out, ws := ScanForDeprecations([]byte(clean), TargetV3); string(out) != clean || len(ws) != 0 {
		t.Errorf("a MeshRetry without legacy fields must pass unchanged, got %v:\n%s", ws, out)
	}
}

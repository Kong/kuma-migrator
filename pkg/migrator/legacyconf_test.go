package migrator

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

// Legacy conf-body conversion tests.
//
// Every legacy policy's conf is structurally different from the default section
// of its Mesh* successor. These tests pin the field mappings and, just as
// importantly, pin the warnings emitted for the fields that have no equivalent —
// a silent drop is the failure mode these conversions exist to prevent.

// transformOne runs the full pipeline and asserts a single output document.
func transformOne(t *testing.T, input string) (map[string]interface{}, []string) {
	t.Helper()
	docs, warnings, _, err := TransformDocument([]byte(input), TargetV2)
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
	return out, warnings
}

// at walks a nested map by path and fails when a segment is missing.
func at(t *testing.T, obj map[string]interface{}, path ...string) interface{} {
	t.Helper()
	var cur interface{} = obj
	for i, p := range path {
		m, ok := cur.(map[string]interface{})
		if !ok {
			t.Fatalf("path %v: segment %q is not a map (at index %d)", path, p, i)
		}
		cur, ok = m[p]
		if !ok {
			t.Fatalf("path %v: segment %q missing; available: %v", path, p, keysOf(m))
		}
	}
	return cur
}

// missing asserts that a path does not resolve.
func missing(t *testing.T, obj map[string]interface{}, path ...string) {
	t.Helper()
	var cur interface{} = obj
	for _, p := range path {
		m, ok := cur.(map[string]interface{})
		if !ok {
			return
		}
		cur, ok = m[p]
		if !ok {
			return
		}
	}
	t.Fatalf("path %v unexpectedly present with value %v", path, cur)
}

func keysOf(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func hasWarning(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

// ---- Input layout ------------------------------------------------------------

func TestLegacyPolicy_KubernetesLayout(t *testing.T) {
	// On Kubernetes the legacy CRDs wrap the body in spec, name the resource
	// through metadata.name and identify it with kind. Reading only the Universal
	// layout yields an empty document with kind: "" for every legacy resource
	// `extract --kube-context` produces.
	out, warnings := transformOne(t, `
apiVersion: kuma.io/v1alpha1
kind: Timeout
mesh: prod
metadata:
  name: k8s-timeout
spec:
  sources:
    - match: {kuma.io/service: web_demo_svc_80}
  destinations:
    - match: {kuma.io/service: backend_demo_svc_3001}
  conf:
    connectTimeout: 10s
`)
	if got := out["kind"]; got != "MeshTimeout" {
		t.Fatalf("kind = %v, want MeshTimeout", got)
	}
	if got := at(t, out, "metadata", "name"); got != "k8s-timeout" {
		t.Errorf("metadata.name = %v, want k8s-timeout", got)
	}
	// The mesh association must survive: a legacy policy states it in a top-level
	// field, the Kubernetes-style output as a label. Dropping it silently moves
	// the policy to the default mesh.
	if got := at(t, out, "metadata", "labels", "kuma.io/mesh"); got != "prod" {
		t.Errorf("metadata.labels[kuma.io/mesh] = %v, want prod", got)
	}
	if got := at(t, out, "spec", "targetRef", "name"); got != "web" {
		t.Errorf("spec.targetRef.name = %v, want web (from spec.sources)", got)
	}
	to := at(t, out, "spec", "to").([]interface{})[0].(map[string]interface{})
	if got := at(t, to, "default", "connectionTimeout"); got != "10s" {
		t.Errorf("to[0].default.connectionTimeout = %v, want 10s (from spec.conf)", got)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

func TestLegacyPolicy_MeshLabelFromUniversalField(t *testing.T) {
	out, _ := transformOne(t, `
type: CircuitBreaker
name: cb
mesh: prod
destinations:
  - match: {kuma.io/service: backend_demo_svc_3001}
conf:
  interval: 5s
`)
	if got := at(t, out, "metadata", "labels", "kuma.io/mesh"); got != "prod" {
		t.Errorf("metadata.labels[kuma.io/mesh] = %v, want prod", got)
	}
}

// ---- Timeout → MeshTimeout ---------------------------------------------------

func TestLegacyConf_Timeout(t *testing.T) {
	out, warnings := transformOne(t, `
type: Timeout
name: t1
mesh: default
sources:
  - match: {kuma.io/service: web_demo_svc_80}
destinations:
  - match: {kuma.io/service: backend_demo_svc_3001}
conf:
  connectTimeout: 10s
  tcp: {idleTimeout: 1h}
  http: {requestTimeout: 5s, streamIdleTimeout: 30m, maxStreamDuration: 0s}
  grpc: {maxStreamDuration: 9s}
`)

	def := at(t, out, "spec", "to").([]interface{})[0].(map[string]interface{})["default"].(map[string]interface{})

	// connectTimeout is connectionTimeout on MeshTimeout — the rename that makes a
	// verbatim copy silently ineffective.
	if got := def["connectionTimeout"]; got != "10s" {
		t.Errorf("connectionTimeout = %v, want 10s", got)
	}
	missing(t, def, "connectTimeout")

	// tcp.idleTimeout is hoisted to the top level; there is no tcp section.
	if got := def["idleTimeout"]; got != "1h" {
		t.Errorf("idleTimeout = %v, want 1h", got)
	}
	missing(t, def, "tcp")

	if got := at(t, def, "http", "requestTimeout"); got != "5s" {
		t.Errorf("http.requestTimeout = %v, want 5s", got)
	}
	if got := at(t, def, "http", "streamIdleTimeout"); got != "30m" {
		t.Errorf("http.streamIdleTimeout = %v, want 30m", got)
	}
	// grpc has no MeshTimeout section — gRPC runs over HTTP/2, so it folds into http.
	// http.maxStreamDuration was set explicitly, so it wins over the grpc value.
	if got := at(t, def, "http", "maxStreamDuration"); got != "0s" {
		t.Errorf("http.maxStreamDuration = %v, want 0s (explicit http value wins)", got)
	}
	missing(t, def, "grpc")

	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

func TestLegacyConf_Timeout_GrpcFoldsIntoHTTP(t *testing.T) {
	out, _ := transformOne(t, `
type: Timeout
name: t2
mesh: default
destinations:
  - match: {kuma.io/service: backend_demo_svc_3001}
conf:
  grpc: {streamIdleTimeout: 7s, maxStreamDuration: 9s}
`)
	def := at(t, out, "spec", "to").([]interface{})[0].(map[string]interface{})["default"].(map[string]interface{})
	if got := at(t, def, "http", "streamIdleTimeout"); got != "7s" {
		t.Errorf("http.streamIdleTimeout = %v, want 7s (folded from grpc)", got)
	}
	if got := at(t, def, "http", "maxStreamDuration"); got != "9s" {
		t.Errorf("http.maxStreamDuration = %v, want 9s (folded from grpc)", got)
	}
}

func TestLegacyConf_Timeout_IdleTimeoutCollisionWarns(t *testing.T) {
	_, warnings := transformOne(t, `
type: Timeout
name: t3
mesh: default
destinations:
  - match: {kuma.io/service: backend_demo_svc_3001}
conf:
  tcp: {idleTimeout: 1h}
  http: {idleTimeout: 30s}
`)
	// Both legacy fields map onto the single MeshTimeout idleTimeout; one has to go.
	if !hasWarning(warnings, "both map to MeshTimeout default.idleTimeout") {
		t.Errorf("expected idleTimeout collision warning, got: %v", warnings)
	}
}

func TestLegacyConf_Timeout_UnknownFieldWarns(t *testing.T) {
	_, warnings := transformOne(t, `
type: Timeout
name: t4
mesh: default
destinations:
  - match: {kuma.io/service: backend_demo_svc_3001}
conf:
  connectTimeout: 10s
  somethingElse: 5s
`)
	if !hasWarning(warnings, `"conf.somethingElse"`) {
		t.Errorf("expected unmapped-field warning for conf.somethingElse, got: %v", warnings)
	}
}

// ---- CircuitBreaker → MeshCircuitBreaker -------------------------------------

func TestLegacyConf_CircuitBreaker(t *testing.T) {
	out, warnings := transformOne(t, `
type: CircuitBreaker
name: cb
mesh: default
destinations:
  - match: {kuma.io/service: backend_demo_svc_3001}
conf:
  interval: 5s
  baseEjectionTime: 30s
  maxEjectionPercent: 20
  splitExternalAndLocalErrors: true
  detectors:
    totalErrors: {consecutive: 20}
    gatewayErrors: {consecutive: 10}
    localErrors: {consecutive: 7}
    standardDeviation: {requestVolume: 10, minimumHosts: 5, factor: 1.9}
    failure: {requestVolume: 10, minimumHosts: 5, threshold: 85}
  thresholds:
    maxConnections: 2
    maxPendingRequests: 3
`)
	def := at(t, out, "spec", "to").([]interface{})[0].(map[string]interface{})["default"].(map[string]interface{})

	// The flat legacy conf splits into connectionLimits + outlierDetection.
	if got := at(t, def, "connectionLimits", "maxConnections"); got != float64(2) {
		t.Errorf("connectionLimits.maxConnections = %v, want 2", got)
	}
	if got := at(t, def, "connectionLimits", "maxPendingRequests"); got != float64(3) {
		t.Errorf("connectionLimits.maxPendingRequests = %v, want 3", got)
	}
	missing(t, def, "thresholds")

	od := at(t, def, "outlierDetection").(map[string]interface{})
	if od["interval"] != "5s" || od["baseEjectionTime"] != "30s" {
		t.Errorf("outlierDetection interval/baseEjectionTime = %v/%v", od["interval"], od["baseEjectionTime"])
	}

	// Every detector was renamed.
	if got := at(t, def, "outlierDetection", "detectors", "totalFailures", "consecutive"); got != float64(20) {
		t.Errorf("totalFailures.consecutive = %v, want 20", got)
	}
	if got := at(t, def, "outlierDetection", "detectors", "gatewayFailures", "consecutive"); got != float64(10) {
		t.Errorf("gatewayFailures.consecutive = %v, want 10", got)
	}
	if got := at(t, def, "outlierDetection", "detectors", "localOriginFailures", "consecutive"); got != float64(7) {
		t.Errorf("localOriginFailures.consecutive = %v, want 7", got)
	}
	if got := at(t, def, "outlierDetection", "detectors", "successRate", "standardDeviationFactor"); got != "1.9" {
		t.Errorf("successRate.standardDeviationFactor = %v, want \"1.9\"", got)
	}
	if got := at(t, def, "outlierDetection", "detectors", "failurePercentage", "threshold"); got != float64(85) {
		t.Errorf("failurePercentage.threshold = %v, want 85", got)
	}
	missing(t, def, "outlierDetection", "detectors", "totalErrors")

	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

// ---- Retry → MeshRetry -------------------------------------------------------

func TestLegacyConf_Retry(t *testing.T) {
	out, warnings := transformOne(t, `
type: Retry
name: r1
mesh: default
destinations:
  - match: {kuma.io/service: backend_demo_svc_3001}
conf:
  http:
    numRetries: 5
    perTryTimeout: 200ms
    backOff: {baseInterval: 20ms, maxInterval: 1s}
    retryOn: [all_5xx, gateway_error, retriable_status_codes]
    retriableMethods: [GET, POST]
    retriableStatusCodes: [500, 502]
  tcp:
    maxConnectAttempts: 5
  grpc:
    retryOn: [cancelled, deadline_exceeded]
    numRetries: 3
`)
	def := at(t, out, "spec", "to").([]interface{})[0].(map[string]interface{})["default"].(map[string]interface{})

	// Singular on MeshRetry.
	if got := at(t, def, "tcp", "maxConnectAttempt"); got != float64(5) {
		t.Errorf("tcp.maxConnectAttempt = %v, want 5", got)
	}
	missing(t, def, "tcp", "maxConnectAttempts")

	retryOn := at(t, def, "http", "retryOn").([]interface{})
	want := []string{"5xx", "GatewayError", "HttpMethodGet", "HttpMethodPost"}
	if len(retryOn) != len(want) {
		t.Fatalf("http.retryOn = %v, want %v", retryOn, want)
	}
	for i, w := range want {
		if retryOn[i] != w {
			t.Errorf("http.retryOn[%d] = %v, want %v", i, retryOn[i], w)
		}
	}
	missing(t, def, "http", "retriableMethods")

	// "cancelled" is spelled "Canceled" on MeshRetry.
	grpcRetryOn := at(t, def, "grpc", "retryOn").([]interface{})
	if grpcRetryOn[0] != "Canceled" || grpcRetryOn[1] != "DeadlineExceeded" {
		t.Errorf("grpc.retryOn = %v, want [Canceled DeadlineExceeded]", grpcRetryOn)
	}

	// Both status-code paths are removals, not renames — they must be reported.
	if !hasWarning(warnings, "retriableStatusCodes") {
		t.Errorf("expected retriableStatusCodes drop warning, got: %v", warnings)
	}
	if !hasWarning(warnings, `retryOn value "retriable_status_codes"`) {
		t.Errorf("expected retryOn value drop warning, got: %v", warnings)
	}
}

// ---- HealthCheck → MeshHealthCheck -------------------------------------------

func TestLegacyConf_HealthCheck_RequestHeadersSplit(t *testing.T) {
	out, warnings := transformOne(t, `
type: HealthCheck
name: hc
mesh: default
destinations:
  - match: {kuma.io/service: backend_demo_svc_3001}
conf:
  interval: 10s
  timeout: 2s
  healthyPanicThreshold: 61.5
  http:
    path: /health
    requestHeadersToAdd:
      - header: {key: x-set, value: "1"}
        append: false
      - header: {key: x-add, value: "2"}
        append: true
      - header: {key: x-default, value: "3"}
  tcp:
    send: Zm9v
    receive: [YmFy]
`)
	def := at(t, out, "spec", "to").([]interface{})[0].(map[string]interface{})["default"].(map[string]interface{})

	// {header:{key,value}, append} becomes the Gateway-API-style add/set split.
	add := at(t, def, "http", "requestHeadersToAdd", "add").([]interface{})
	set := at(t, def, "http", "requestHeadersToAdd", "set").([]interface{})
	if len(set) != 1 || set[0].(map[string]interface{})["name"] != "x-set" {
		t.Errorf("set = %v, want one entry named x-set", set)
	}
	// append defaults to true when unset (Envoy appends), so x-default joins add.
	if len(add) != 2 {
		t.Fatalf("add = %v, want 2 entries (x-add, x-default)", add)
	}
	if add[0].(map[string]interface{})["name"] != "x-add" || add[1].(map[string]interface{})["name"] != "x-default" {
		t.Errorf("add = %v, want [x-add x-default]", add)
	}

	// Base64 in both APIs — copied through unchanged.
	if got := at(t, def, "tcp", "send"); got != "Zm9v" {
		t.Errorf("tcp.send = %v, want Zm9v", got)
	}
	// Fractional percentages must be strings for the int-or-string schema.
	if got := def["healthyPanicThreshold"]; got != "61.5" {
		t.Errorf("healthyPanicThreshold = %v (%T), want string \"61.5\"", got, got)
	}

	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

// ---- FaultInjection → MeshFaultInjection -------------------------------------

func TestLegacyConf_FaultInjection_IsInbound(t *testing.T) {
	out, _ := transformOne(t, `
type: FaultInjection
name: fi
mesh: default
sources:
  - match: {kuma.io/service: web_demo_svc_80}
destinations:
  - match: {kuma.io/service: backend_demo_svc_3001}
conf:
  abort: {percentage: 50, httpStatus: 500}
  delay: {percentage: 50.5, value: 5s}
`)
	// MeshFaultInjection is enforced by the destination's sidecar: destinations
	// become spec.targetRef and sources become from[], not the other way round.
	spec := at(t, out, "spec").(map[string]interface{})
	if _, hasTo := spec["to"]; hasTo {
		t.Errorf("MeshFaultInjection must not use to[]: %v", spec)
	}
	if got := at(t, out, "spec", "targetRef", "name"); got != "backend" {
		t.Errorf("spec.targetRef.name = %v, want backend (the destination)", got)
	}
	from := at(t, out, "spec", "from").([]interface{})[0].(map[string]interface{})
	if got := from["targetRef"].(map[string]interface{})["name"]; got != "web" {
		t.Errorf("from[0].targetRef.name = %v, want web (the source)", got)
	}

	// The single legacy fault becomes a one-element http[] list.
	faults := at(t, from, "default", "http").([]interface{})
	if len(faults) != 1 {
		t.Fatalf("default.http = %v, want 1 entry", faults)
	}
	fault := faults[0].(map[string]interface{})
	if got := at(t, fault, "abort", "httpStatus"); got != float64(500) {
		t.Errorf("abort.httpStatus = %v, want 500", got)
	}
	if got := at(t, fault, "delay", "percentage"); got != "50.5" {
		t.Errorf("delay.percentage = %v, want \"50.5\"", got)
	}
	if got := at(t, fault, "abort", "percentage"); got != float64(50) {
		t.Errorf("abort.percentage = %v, want 50 (whole numbers stay numeric)", got)
	}
}

// ---- RateLimit → MeshRateLimit -----------------------------------------------

func TestLegacyConf_RateLimit(t *testing.T) {
	out, warnings := transformOne(t, `
type: RateLimit
name: rl
mesh: default
sources:
  - match: {kuma.io/service: web_demo_svc_80}
destinations:
  - match: {kuma.io/service: backend_demo_svc_3001}
conf:
  http:
    requests: 5
    interval: 10s
    onRateLimit:
      status: 423
      headers:
        - {key: x-limited, value: "true", append: true}
`)
	spec := at(t, out, "spec").(map[string]interface{})
	if _, hasTo := spec["to"]; hasTo {
		t.Errorf("MeshRateLimit must not use to[]: %v", spec)
	}
	if got := at(t, out, "spec", "targetRef", "name"); got != "backend" {
		t.Errorf("spec.targetRef.name = %v, want backend (the destination)", got)
	}

	// from[] accepts only kind: Mesh and is removed in 3.0 with a mechanical
	// rules[] equivalent, so the conversion emits rules[] straight away.
	if _, hasFrom := spec["from"]; hasFrom {
		t.Errorf("MeshRateLimit should use rules[], not from[]: %v", spec)
	}
	from := at(t, out, "spec", "rules").([]interface{})[0].(map[string]interface{})
	if !hasWarning(warnings, "enforced for traffic from all clients") {
		t.Errorf("expected scope-widening warning, got: %v", warnings)
	}

	// requests/interval become a requestRate object under local.http.
	if got := at(t, from, "default", "local", "http", "requestRate", "num"); got != float64(5) {
		t.Errorf("requestRate.num = %v, want 5", got)
	}
	if got := at(t, from, "default", "local", "http", "requestRate", "interval"); got != "10s" {
		t.Errorf("requestRate.interval = %v, want 10s", got)
	}
	if got := at(t, from, "default", "local", "http", "onRateLimit", "status"); got != float64(423) {
		t.Errorf("onRateLimit.status = %v, want 423", got)
	}
	hdr := at(t, from, "default", "local", "http", "onRateLimit", "headers", "add").([]interface{})
	if hdr[0].(map[string]interface{})["name"] != "x-limited" {
		t.Errorf("onRateLimit.headers.add = %v, want name x-limited", hdr)
	}
}

// ---- TrafficPermission → MeshTrafficPermission -------------------------------

func TestLegacyConf_TrafficPermission_SynthesisesAllow(t *testing.T) {
	// The legacy resource has no conf — its existence is the permission. Every
	// from[] entry of the successor needs an explicit action or the policy is
	// meaningless, so the conversion must synthesise it.
	out, _ := transformOne(t, `
type: TrafficPermission
name: allow
mesh: default
sources:
  - match: {kuma.io/service: web_demo_svc_80}
destinations:
  - match: {kuma.io/service: backend_demo_svc_3001}
`)
	if got := at(t, out, "spec", "targetRef", "name"); got != "backend" {
		t.Errorf("spec.targetRef.name = %v, want backend (the destination)", got)
	}
	from := at(t, out, "spec", "from").([]interface{})[0].(map[string]interface{})
	if got := at(t, from, "default", "action"); got != "Allow" {
		t.Errorf("from[0].default.action = %v, want Allow", got)
	}
}

// ---- ProxyTemplate → MeshProxyPatch ------------------------------------------

func TestLegacyConf_ProxyTemplate(t *testing.T) {
	out, warnings := transformOne(t, `
type: ProxyTemplate
name: pt
mesh: default
selectors:
  - match: {kuma.io/service: web_demo_svc_80}
conf:
  imports:
    - default-proxy
  modifications:
    - cluster:
        operation: add
        value: |
          name: test-cluster
    - httpFilter:
        operation: add
        match: {name: envoy.filters.http.router}
        value: 'name: custom'
`)
	// selectors[] scopes the policy: it must land on spec.targetRef, not be dropped.
	if got := at(t, out, "spec", "targetRef", "name"); got != "web" {
		t.Errorf("spec.targetRef.name = %v, want web (from selectors)", got)
	}
	mods := at(t, out, "spec", "default", "appendModifications").([]interface{})
	if len(mods) != 2 {
		t.Fatalf("appendModifications = %v, want 2", mods)
	}
	// Operations are capitalised on MeshProxyPatch.
	if got := at(t, mods[0].(map[string]interface{}), "cluster", "operation"); got != "Add" {
		t.Errorf("cluster.operation = %v, want Add", got)
	}
	// Filters have no plain Add — the positional variants replaced it.
	if got := at(t, mods[1].(map[string]interface{}), "httpFilter", "operation"); got != "AddLast" {
		t.Errorf("httpFilter.operation = %v, want AddLast", got)
	}
	if !hasWarning(warnings, "AddFirst/AddBefore/AddAfter/AddLast") {
		t.Errorf("expected filter-position warning, got: %v", warnings)
	}
	if !hasWarning(warnings, "conf.imports") {
		t.Errorf("expected imports warning, got: %v", warnings)
	}
}

// ---- TrafficTrace / TrafficLog backend resolution ----------------------------

const meshWithBackends = `
type: Mesh
name: default
logging:
  defaultBackend: file
  backends:
    - name: file
      type: file
      conf: {path: /tmp/access.log}
tracing:
  defaultBackend: jaeger
  backends:
    - name: jaeger
      type: zipkin
      sampling: 80
      conf: {url: http://jaeger:9411/api/v2/spans}
`

// transformWithMesh runs a document with the Mesh backends of meshWithBackends
// available, the way runMigration does after its pre-pass.
func transformWithMesh(t *testing.T, input string) (map[string]interface{}, []string) {
	t.Helper()
	name, backends := parseMeshBackends([]byte(meshWithBackends))
	if name == "" {
		t.Fatal("failed to parse the Mesh fixture")
	}
	opts := TransformOptions{Target: TargetV2, MeshBackends: MeshBackendIndex{name: backends}}
	docs, warnings, _, err := TransformDocumentWithOptions([]byte(input), opts)
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
	return out, warnings
}

func TestLegacyConf_TrafficTrace_ResolvesBackend(t *testing.T) {
	out, warnings := transformWithMesh(t, `
type: TrafficTrace
name: tt
mesh: default
selectors:
  - match: {kuma.io/service: backend_demo_svc_3001}
conf:
  backend: jaeger
`)
	// selectors[] must reach spec.targetRef.
	if got := at(t, out, "spec", "targetRef", "name"); got != "backend" {
		t.Errorf("spec.targetRef.name = %v, want backend", got)
	}
	// MeshTrace inlines the backend the legacy policy only referenced by name.
	backends := at(t, out, "spec", "default", "backends").([]interface{})
	b := backends[0].(map[string]interface{})
	if b["type"] != "Zipkin" {
		t.Errorf("backend type = %v, want Zipkin", b["type"])
	}
	if got := at(t, b, "zipkin", "url"); got != "http://jaeger:9411/api/v2/spans" {
		t.Errorf("zipkin.url = %v", got)
	}
	if got := at(t, out, "spec", "default", "sampling", "overall"); got != float64(80) {
		t.Errorf("sampling.overall = %v, want 80", got)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

func TestLegacyConf_TrafficTrace_UnresolvableBackendWarns(t *testing.T) {
	// No Mesh in the input set: the backend cannot be inlined, and that must be
	// said out loud rather than producing an empty MeshTrace.
	out, warnings := transformOne(t, `
type: TrafficTrace
name: tt
mesh: default
selectors:
  - match: {kuma.io/service: '*'}
conf:
  backend: jaeger
`)
	if !hasWarning(warnings, "not in the input set") {
		t.Errorf("expected unresolved-backend warning, got: %v", warnings)
	}
	missing(t, out, "spec", "default")
}

func TestLegacyConf_TrafficTrace_DanglingBackendWarns(t *testing.T) {
	_, warnings := transformWithMesh(t, `
type: TrafficTrace
name: tt
mesh: default
selectors:
  - match: {kuma.io/service: '*'}
conf:
  backend: does-not-exist
`)
	if !hasWarning(warnings, "dangling") {
		t.Errorf("expected dangling-reference warning, got: %v", warnings)
	}
}

func TestLegacyConf_TrafficLog_ResolvesBackend(t *testing.T) {
	out, warnings := transformWithMesh(t, `
type: TrafficLog
name: tl
mesh: default
sources:
  - match: {kuma.io/service: web_demo_svc_80}
destinations:
  - match: {kuma.io/service: backend_demo_svc_3001}
conf:
  backend: file
`)
	to := at(t, out, "spec", "to").([]interface{})[0].(map[string]interface{})
	backends := at(t, to, "default", "backends").([]interface{})
	b := backends[0].(map[string]interface{})
	if b["type"] != "File" {
		t.Errorf("backend type = %v, want File", b["type"])
	}
	if got := at(t, b, "file", "path"); got != "/tmp/access.log" {
		t.Errorf("file.path = %v", got)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

func TestLegacyConf_TrafficLog_DefaultBackend(t *testing.T) {
	// No conf.backend at all: the Mesh's defaultBackend applies.
	out, _ := transformWithMesh(t, `
type: TrafficLog
name: tl
mesh: default
destinations:
  - match: {kuma.io/service: backend_demo_svc_3001}
`)
	to := at(t, out, "spec", "to").([]interface{})[0].(map[string]interface{})
	backends := at(t, to, "default", "backends").([]interface{})
	if backends[0].(map[string]interface{})["type"] != "File" {
		t.Errorf("expected the mesh default logging backend, got %v", backends)
	}
}

// ---- VirtualOutbound ---------------------------------------------------------

func TestVirtualOutbound_RequiresManualMigration(t *testing.T) {
	input := `
type: VirtualOutbound
name: vo
mesh: default
selectors:
  - match: {kuma.io/service: '*'}
conf:
  host: "{{.svc}}.mesh"
  port: "8080"
  parameters:
    - {name: svc, tagKey: kuma.io/service}
`
	// Detected (not silently skipped), and reported as needing manual work rather
	// than converted into something that would not reproduce the templates.
	scenario, err := DetectScenario([]byte(input))
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if scenario != ScenarioLegacy {
		t.Fatalf("scenario = %s, want ScenarioLegacy", scenario)
	}

	_, _, _, err = TransformDocument([]byte(input), TargetV2)
	if err == nil {
		t.Fatal("expected an error for VirtualOutbound")
	}
	for _, want := range []string{"HostnameGenerator", "MeshHTTPRoute"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %s: %v", want, err)
		}
	}
}

// ---- ContainerPatch ----------------------------------------------------------

func TestContainerPatch_PassesThrough(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: ContainerPatch
metadata:
  name: cp
  namespace: kuma-system
spec:
  sidecarPatch:
    - op: add
      path: /resources/limits/cpu
      value: '"1"'
`
	docs, warnings, scenario, err := TransformDocument([]byte(input), TargetV2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// ContainerPatch is not a policy and has no successor — it survives Kuma 3.0
	// unchanged, so it must pass through rather than be reported as unrecognised.
	if scenario != ScenarioPassthrough {
		t.Errorf("scenario = %s, want ScenarioPassthrough", scenario)
	}
	if len(docs) != 1 {
		t.Fatalf("expected the document to be preserved, got %d docs", len(docs))
	}
	if !strings.Contains(string(docs[0]), "sidecarPatch") {
		t.Errorf("ContainerPatch body was altered: %s", docs[0])
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
}

// ---- Mesh backend index ------------------------------------------------------

func TestParseMeshBackends_KubernetesAndUniversal(t *testing.T) {
	// Universal (flat) form.
	name, backends := parseMeshBackends([]byte(meshWithBackends))
	if name != "default" {
		t.Fatalf("name = %q, want default", name)
	}
	if _, ok := backends.Logging["file"]; !ok {
		t.Errorf("logging backend %q not indexed: %v", "file", backends.Logging)
	}
	if backends.DefaultTracingBackend != "jaeger" {
		t.Errorf("DefaultTracingBackend = %q, want jaeger", backends.DefaultTracingBackend)
	}

	// Kubernetes (spec-nested) form.
	name, backends = parseMeshBackends([]byte(`
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: prod
spec:
  logging:
    backends:
      - name: tcp-log
        type: tcp
        conf: {address: 127.0.0.1:5000}
`))
	if name != "prod" {
		t.Fatalf("name = %q, want prod", name)
	}
	if _, ok := backends.Logging["tcp-log"]; !ok {
		t.Errorf("logging backend not indexed: %v", backends.Logging)
	}

	// A Mesh with no observability sections is still indexed, so that a backend
	// reference into it is reported as dangling rather than as a missing Mesh.
	n, b := parseMeshBackends([]byte("type: Mesh\nname: empty\n"))
	if n != "empty" || b == nil {
		t.Fatalf("expected a bare Mesh to be indexed, got %q/%v", n, b)
	}
	if len(b.Logging) != 0 || len(b.Tracing) != 0 {
		t.Errorf("bare Mesh should declare no backends, got %v/%v", b.Logging, b.Tracing)
	}

	// A non-Mesh document is never indexed.
	if n, b := parseMeshBackends([]byte("type: MeshTimeout\nname: t\n")); n != "" || b != nil {
		t.Errorf("expected no index entry for a non-Mesh document, got %q/%v", n, b)
	}
}

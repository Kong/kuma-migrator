package migrator

import (
	"errors"
	"testing"

	"sigs.k8s.io/yaml"
)

// Universal input must yield Universal output (type/name/mesh/labels/spec) for
// every scenario that can carry it, including every companion a conversion
// generates. The Gateway scenarios are absent on purpose: their output is
// Gateway API, which exists only on Kubernetes.
func TestUniversalInputYieldsUniversalOutput(t *testing.T) {
	cases := []struct {
		name  string
		input string
		// kinds lists, per target, the output types expected (in any order).
		// A nil entry means the target errors on this input (manual migration).
		v2, v3 []string
	}{
		{"Legacy/Timeout", `type: Timeout
name: t
mesh: prod
labels: {team: payments}
sources: [{match: {kuma.io/service: web}}]
destinations: [{match: {kuma.io/service: backend}}]
conf: {connectTimeout: 5s}
`, []string{"MeshTimeout"}, []string{"MeshTimeout"}},
		{"Legacy/CircuitBreaker", `type: CircuitBreaker
name: cb
mesh: prod
sources: [{match: {kuma.io/service: '*'}}]
destinations: [{match: {kuma.io/service: backend}}]
conf: {interval: 5s, detectors: {totalErrors: {consecutive: 3}}}
`, []string{"MeshCircuitBreaker"}, []string{"MeshCircuitBreaker"}},
		{"Legacy/Retry", `type: Retry
name: r
mesh: prod
sources: [{match: {kuma.io/service: '*'}}]
destinations: [{match: {kuma.io/service: backend}}]
conf: {http: {numRetries: 3}}
`, []string{"MeshRetry"}, []string{"MeshRetry"}},
		{"Legacy/HealthCheck with companion", `type: HealthCheck
name: hc
mesh: prod
sources: [{match: {kuma.io/service: '*'}}]
destinations: [{match: {kuma.io/service: backend}}]
conf: {interval: 10s, healthyPanicThreshold: 40}
`, []string{"MeshHealthCheck"}, []string{"MeshHealthCheck", "MeshCircuitBreaker"}},
		{"Legacy/TrafficLog", `type: TrafficLog
name: tl
mesh: prod
sources: [{match: {kuma.io/service: '*'}}]
destinations: [{match: {kuma.io/service: '*'}}]
`, []string{"MeshAccessLog"}, []string{"MeshAccessLog"}},
		{"Legacy/TrafficPermission", `type: TrafficPermission
name: tp
mesh: prod
sources: [{match: {kuma.io/service: web}}]
destinations: [{match: {kuma.io/service: backend}}]
`, []string{"MeshTrafficPermission"}, nil},
		{"Legacy/FaultInjection rules", `type: FaultInjection
name: fi
mesh: prod
sources: [{match: {kuma.io/service: '*'}}]
destinations: [{match: {kuma.io/service: backend}}]
conf: {abort: {httpStatus: 500, percentage: 50}}
`, []string{"MeshFaultInjection"}, []string{"MeshFaultInjection"}},
		{"Legacy/RateLimit", `type: RateLimit
name: rl
mesh: prod
sources: [{match: {kuma.io/service: '*'}}]
destinations: [{match: {kuma.io/service: backend}}]
conf: {http: {requests: 10, interval: 1s}}
`, []string{"MeshRateLimit"}, []string{"MeshRateLimit"}},
		{"Legacy/TrafficTrace", `type: TrafficTrace
name: tt
mesh: prod
selectors: [{match: {kuma.io/service: '*'}}]
`, []string{"MeshTrace"}, []string{"MeshTrace"}},
		{"Legacy/ProxyTemplate", `type: ProxyTemplate
name: pt
mesh: prod
selectors: [{match: {kuma.io/service: '*'}}]
conf:
  modifications:
    - cluster: {operation: patch, value: "connectTimeout: 5s"}
`, []string{"MeshProxyPatch"}, []string{"MeshProxyPatch"}},
		{"Subset", `type: MeshTimeout
name: st
mesh: prod
spec:
  targetRef: {kind: Mesh}
  to:
    - targetRef: {kind: MeshSubset, tags: {kuma.io/service: backend_demo_svc_80}}
      default: {connectionTimeout: 5s}
`, []string{"MeshTimeout"}, []string{"MeshTimeout"}},
		{"Rules split", `type: MeshTimeout
name: rt
mesh: prod
spec:
  targetRef: {kind: Mesh}
  from:
    - targetRef: {kind: Mesh}
      default: {idleTimeout: 10s}
  to:
    - targetRef: {kind: Mesh}
      default: {idleTimeout: 10s}
`, []string{"MeshTimeout", "MeshTimeout"}, []string{"MeshTimeout", "MeshTimeout"}},
		{"Passthrough/MeshTrace inline OTel", `type: MeshTrace
name: otel
mesh: prod
spec:
  targetRef: {kind: Mesh}
  default:
    backends:
      - type: OpenTelemetry
        openTelemetry: {endpoint: "otel-collector:4317"}
`, []string{"MeshTrace"}, []string{"MeshTrace", "MeshOpenTelemetryBackend"}},
		{"Mesh with companions", `type: Mesh
name: prod
mtls:
  enabledBackend: ca
  backends: [{name: ca, type: builtin, mode: PERMISSIVE}]
networking:
  outbound: {passthrough: true}
metrics:
  enabledBackend: prom
  backends: [{name: prom, type: prometheus, conf: {port: 5670, path: /metrics}}]
`, nil, nil},
		{"ExternalService", `type: ExternalService
name: httpbin
mesh: prod
tags: {kuma.io/service: httpbin}
networking: {address: "httpbin.org:443", tls: {enabled: true}}
`, []string{"MeshExternalService"}, []string{"MeshExternalService"}},
		{"OPAPolicy", `type: OPAPolicy
name: opa
mesh: prod
selectors: [{match: {kuma.io/service: '*'}}]
conf:
  policies:
    - inlineString: "package envoy.authz"
`, []string{"MeshOPA"}, []string{"MeshOPA"}},
	}

	for _, tc := range cases {
		for _, target := range []TargetVersion{TargetV2, TargetV3} {
			want := tc.v2
			if target.IsV3() {
				want = tc.v3
			}
			t.Run(tc.name+"/"+targetLine(target), func(t *testing.T) {
				docs, _, _, err := TransformDocumentWithOptions([]byte(tc.input), TransformOptions{Target: target})
				var rejected *RejectedByTargetError
				if err != nil && !errors.As(err, &rejected) {
					if want == nil && tc.name != "Mesh with companions" {
						return // manual migration on this target
					}
					t.Fatalf("unexpected error: %v", err)
				}
				var got []string
				for _, d := range docs {
					var obj map[string]interface{}
					if err := yaml.Unmarshal(d, &obj); err != nil {
						t.Fatalf("unmarshal: %v\n%s", err, d)
					}
					for _, k := range []string{"apiVersion", "kind", "metadata"} {
						if _, ok := obj[k]; ok {
							t.Errorf("Universal output has Kubernetes key %q:\n%s", k, d)
						}
					}
					typ, _ := obj["type"].(string)
					if typ == "" || obj["name"] == nil {
						t.Errorf("Universal output lacks type/name:\n%s", d)
					}
					if typ != "Mesh" && obj["mesh"] != "prod" {
						t.Errorf("Universal output lacks mesh: prod:\n%s", d)
					}
					if typ != "Mesh" && obj["spec"] == nil {
						t.Errorf("Universal %s output has no spec wrapper:\n%s", typ, d)
					}
					got = append(got, typ)
				}
				if want != nil && !sameMultiset(got, want) {
					t.Errorf("output types = %v, want %v", got, want)
				}
			})
		}
	}
}

func TestUniversalLegacyOutputCarriesLabels(t *testing.T) {
	docs, _, _, err := TransformDocument([]byte(`type: Timeout
name: t
mesh: prod
labels: {team: payments, kuma.io/mesh: prod}
sources: [{match: {kuma.io/service: '*'}}]
destinations: [{match: {kuma.io/service: backend}}]
conf: {connectTimeout: 5s}
`), TargetV2)
	if err != nil {
		t.Fatal(err)
	}
	out := unmarshalDoc(t, docs[0])
	labels, _ := out["labels"].(map[string]interface{})
	if labels["team"] != "payments" {
		t.Errorf("labels = %v, want team: payments carried over", labels)
	}
	if _, ok := labels[meshLabel]; ok {
		t.Errorf("kuma.io/mesh must not be a label on Universal output (mesh is top-level): %v", labels)
	}
}

func sameMultiset(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	count := map[string]int{}
	for _, s := range a {
		count[s]++
	}
	for _, s := range b {
		count[s]--
	}
	for _, n := range count {
		if n != 0 {
			return false
		}
	}
	return true
}

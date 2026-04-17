package resource

import "testing"

func TestKindSubfolder(t *testing.T) {
	cases := []struct {
		kind string
		want string
	}{
		// resiliency
		{"MeshCircuitBreaker", "resiliency"},
		{"MeshRetry", "resiliency"},
		{"MeshTimeout", "resiliency"},
		{"MeshHealthCheck", "resiliency"},
		{"MeshRateLimit", "resiliency"},
		{"MeshFaultInjection", "resiliency"},
		{"CircuitBreaker", "resiliency"},
		{"Retry", "resiliency"},
		{"Timeout", "resiliency"},
		// routing
		{"MeshHTTPRoute", "routing"},
		{"MeshTCPRoute", "routing"},
		{"MeshGateway", "routing"},
		{"MeshGatewayInstance", "routing"},
		{"MeshGatewayRoute", "routing"},
		{"Gateway", "routing"},
		{"GatewayClass", "routing"},
		{"HTTPRoute", "routing"},
		{"TCPRoute", "routing"},
		{"MeshLoadBalancingStrategy", "routing"},
		{"MeshPassthrough", "routing"},
		{"MeshExternalService", "routing"},
		{"ExternalService", "routing"},
		{"MeshProxyPatch", "mesh"},
		// zero-trust
		{"MeshTrafficPermission", "zero-trust"},
		{"MeshTLS", "zero-trust"},
		{"MeshIdentity", "zero-trust"},
		{"MeshTrust", "zero-trust"},
		{"MeshOPA", "zero-trust"},
		{"TrafficPermission", "zero-trust"},
		{"Secret", "zero-trust"},
		// mesh
		{"Mesh", "mesh"},
		// observability
		{"MeshMetric", "observability"},
		{"MeshTrace", "observability"},
		{"MeshAccessLog", "observability"},
		{"TrafficLog", "observability"},
		// unknown → other
		{"SomeUnknownKind", "other"},
		{"", "other"},
	}

	for _, tc := range cases {
		got := KindSubfolder(tc.kind)
		if got != tc.want {
			t.Errorf("KindSubfolder(%q) = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

package resource

// KindSubfolder maps a Kuma resource kind to its output subdirectory.
// Unknown kinds fall back to "other".
func KindSubfolder(kind string) string {
	switch kind {

	// ── Resiliency ────────────────────────────────────────────────────────────
	case "MeshCircuitBreaker", "MeshRetry", "MeshTimeout",
		"MeshHealthCheck", "MeshRateLimit", "MeshFaultInjection",
		// Legacy equivalents
		"CircuitBreaker", "Retry", "Timeout", "HealthCheck", "RateLimit", "FaultInjection":
		return "resiliency"

	// ── Routing ───────────────────────────────────────────────────────────────
	case
		// Gateway API outputs
		"Gateway", "GatewayClass", "HTTPRoute", "TCPRoute", "MeshGatewayConfig",
		// Kuma gateway kinds
		"MeshGateway", "MeshGatewayInstance", "MeshGatewayRoute",
		"MeshHTTPRoute", "MeshTCPRoute",
		// Load balancing
		"MeshLoadBalancingStrategy",
		// Passthrough and reachability
		"MeshPassthrough",
		// External and multi-zone services
		"MeshExternalService", "ExternalService",
		"MeshService", "MeshMultiZoneService",
		// Legacy routing
		"VirtualOutbound", "TrafficRoute":
		return "routing"

	// ── Zero-trust / Security ─────────────────────────────────────────────────
	case "MeshTrafficPermission", "MeshTLS", "MeshIdentity", "MeshTrust",
		"MeshOPA",
		// Legacy
		"TrafficPermission", "OPAPolicy",
		"Secret", "GlobalSecret":
		return "zero-trust"

	// ── Mesh CRs ──────────────────────────────────────────────────────────────
	case "Mesh", "MeshProxyPatch", "ProxyTemplate":
		return "mesh"

	// ── Observability ─────────────────────────────────────────────────────────
	case "MeshMetric", "MeshTrace", "MeshAccessLog",
		// Legacy
		"TrafficLog", "TrafficTrace":
		return "observability"

	default:
		return "other"
	}
}

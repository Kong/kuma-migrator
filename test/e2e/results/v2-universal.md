# v2 output on universal

Kong Mesh 2.14.7, universal, platform `marina`.
Applied for real with `kumactl apply` against a memory-store CP.

| Phase | File | Result | Server message |
|---|---|---|---|
| input | `uni-zone-ctx/mesh-default/mesh/Mesh-default.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-payments/mesh/Mesh-payments.yaml` | accepted |   |
| input | `uni-zone-ctx/global-scoped-resources/other/AccessAudit-default.yaml` | accepted |   |
| input | `uni-zone-ctx/global-scoped-resources/other/AccessRole-mesh-admin.yaml` | accepted |   |
| input | `uni-zone-ctx/global-scoped-resources/other/HostnameGenerator-local.yaml` | accepted |   |
| input | `uni-zone-ctx/global-scoped-resources/other/Zone-zone-2.yaml` | accepted |   |
| input | `uni-zone-ctx/global-scoped-resources/other/ZoneEgress-egress-1.yaml` | accepted |   |
| input | `uni-zone-ctx/global-scoped-resources/other/ZoneIngress-ingress-1.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/mesh/MeshProxyPatch-cluster-timeout.yaml` | accepted | Warning: MeshSubset value for 'targetRef.kind' is deprecated, use Dataplane with labels instead  |
| input | `uni-zone-ctx/mesh-default/mesh/ProxyTemplate-custom.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/observability/MeshAccessLog-from.yaml` | accepted | Warning: 'from' field is deprecated, use 'rules' instead  |
| input | `uni-zone-ctx/mesh-default/observability/MeshAccessLog-otel.yaml` | accepted | Warning: openTelemetry.endpoint is deprecated, use openTelemetry.backendRef with a MeshOpenTelemetryBackend resource instead  |
| input | `uni-zone-ctx/mesh-default/observability/MeshMetric-profiles.yaml` | accepted | Warning: openTelemetry.endpoint is deprecated, use openTelemetry.backendRef with a MeshOpenTelemetryBackend resource instead  |
| input | `uni-zone-ctx/mesh-default/observability/MeshTrace-otel.yaml` | accepted | Warning: openTelemetry.endpoint is deprecated, use openTelemetry.backendRef with a MeshOpenTelemetryBackend resource instead  |
| input | `uni-zone-ctx/mesh-default/observability/TrafficLog-all.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/observability/TrafficLog-default-backend.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/observability/TrafficTrace-all.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/other/Dataplane-backend-1.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/other/Dataplane-edge-gateway-1.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/other/Dataplane-legacy-db-1.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/other/MeshGlobalRateLimit-kong-mesh-system.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/other/MeshOpenTelemetryBackend-collector.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/resiliency/CircuitBreaker-redis.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/resiliency/FaultInjection-backend.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/resiliency/HealthCheck-web.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/resiliency/MeshCircuitBreaker-svc-name.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/resiliency/MeshFaultInjection-from-meshservice.yaml` | accepted | Warning: 'from' field is deprecated, use 'rules' with SPIFFE-based 'matches' instead  |
| input | `uni-zone-ctx/mesh-default/resiliency/MeshHealthCheck-panic.yaml` | accepted | Warning: healthyPanicThreshold for 'to[].default' is deprecated. The setting has been moved to MeshCircuitBreaker policy, please use MeshCircuitBreaker policy instead.  |
| input | `uni-zone-ctx/mesh-default/resiliency/MeshRateLimit-from.yaml` | accepted | Warning: 'from' field is deprecated, use 'rules' instead  |
| input | `uni-zone-ctx/mesh-default/resiliency/MeshRetry-default.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/resiliency/MeshTimeout-subset.yaml` | accepted | Warning: MeshSubset value for 'targetRef.kind' is deprecated, use Dataplane with labels instead  |
| input | `uni-zone-ctx/mesh-default/resiliency/RateLimit-backend.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/resiliency/Retry-all.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/resiliency/Timeout-backend.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/routing/ExternalService-httpbin.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/routing/ExternalService-postgres.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/routing/MeshExternalService-api.yaml` | accepted | Warning: 'spec.tls.verification.caCert' uses 'secret', 'inline' or 'inlineString', which are deprecated and no longer read in 3.0. Use 'type: Secret' with 'secretRef', or 'type: InsecureInline' with 'insecureInline.value' (plain text, not base64).  |
| input | `uni-zone-ctx/mesh-default/routing/MeshGateway-edge.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/routing/MeshGatewayRoute-edge-http.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/routing/MeshGatewayRoute-edge-tcp.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/routing/MeshHTTPRoute-backend.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/routing/MeshHTTPRoute-subset.yaml` | accepted | Warning: MeshSubset value for 'targetRef.kind' is deprecated, use Dataplane with labels instead  |
| input | `uni-zone-ctx/mesh-default/routing/MeshLoadBalancingStrategy-ringhash.yaml` | accepted | Warning: configuration 'spec.to[0].default.loadBalancer.ringHash.hashPolicies' is deprecated, use 'spec.to[0].default.hashPolicies' instead Warning: SourceIP type for 'spec.to[0].default.loadBalancer.ringHash.hashPolicies[1].type' is deprecated, use Connection instead  |
| input | `uni-zone-ctx/mesh-default/routing/MeshMultiZoneService-backend.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/routing/MeshPassthrough-egress.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/routing/MeshService-legacy-db.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/routing/MeshTCPRoute-redis.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/routing/TrafficRoute-split.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/routing/VirtualOutbound-instances.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/zero-trust/MeshIdentity-bundled.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/zero-trust/MeshOPA-legacy.yaml` | accepted | Warning: MeshService value for 'targetRef.kind' is deprecated, use Dataplane with labels instead Warning: 'spec.default.agentConfig' uses 'secret', 'inline' or 'inlineString', which are deprecated and no longer read in 3.0. Use 'type: Secret' with 'secretRef', or 'type: InsecureInline' with 'insecureInline.value' (plain text, not base64). Warning: 'spec.default.appendPolicies[0].rego' uses 'secret', 'inline' or 'inlineString', which are deprecated and no longer read in 3.0. Use 'type: Secret' with 'secretRef', or 'type: InsecureInline' with 'insecureInline.value' (plain text, not base64). Warn |
| input | `uni-zone-ctx/mesh-default/zero-trust/MeshTLS-strict.yaml` | accepted | Warning: 'from' field is deprecated, use 'rules' instead  |
| input | `uni-zone-ctx/mesh-default/zero-trust/MeshTrafficPermission-from.yaml` | accepted | Warning: 'from' field is deprecated and will be removed in 3.0, use 'rules' with MeshIdentity (spiffeId) instead Warning: MeshService value for 'from[].targetRef.kind' is deprecated, use MeshSubset with 'kuma.io/service' instead  |
| input | `uni-zone-ctx/mesh-default/zero-trust/MeshTrafficPermission-mesh.yaml` | accepted | Warning: 'from' field is deprecated and will be removed in 3.0, use 'rules' with MeshIdentity (spiffeId) instead  |
| input | `uni-zone-ctx/mesh-default/zero-trust/MeshTrust-partner.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/zero-trust/OPAPolicy-default.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/zero-trust/TrafficPermission-allow-all.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-default/zero-trust/TrafficPermission-frontend-to-backend.yaml` | accepted |   |
| input | `uni-zone-ctx/mesh-payments/zero-trust/TrafficPermission-payments-all.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/backends/MeshOpenTelemetryBackend-collector.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshCircuitBreaker-cb-redis.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshFaultInjection-fi-backend.yaml` | accepted | Warning: 'from' field is deprecated, use 'rules' with SPIFFE-based 'matches' instead  |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshHealthCheck-hc-web.yaml` | accepted | Warning: healthyPanicThreshold for 'to[].default' is deprecated. The setting has been moved to MeshCircuitBreaker policy, please use MeshCircuitBreaker policy instead.  |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshCircuitBreaker-cb-to-redis.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshFaultInjection-backend-faults.yaml` | accepted | Warning: 'from' field is deprecated, use 'rules' with SPIFFE-based 'matches' instead  |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshHealthCheck-hc-panic.yaml` | accepted | Warning: healthyPanicThreshold for 'to[].default' is deprecated. The setting has been moved to MeshCircuitBreaker policy, please use MeshCircuitBreaker policy instead.  |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshRateLimit-backend-rate-limit.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshRetry-retry-default.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshTimeout-frontend-to-backend.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshRateLimit-rl-backend.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshRetry-retry-all.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshTimeout-timeout-backend.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/routing/MeshPassthrough-default-passthrough.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/routing/MeshExternalService-httpbin.yaml` | accepted | Warning: 'spec.tls.verification.caCert' uses 'secret', 'inline' or 'inlineString', which are deprecated and no longer read in 3.0. Use 'type: Secret' with 'secretRef', or 'type: InsecureInline' with 'insecureInline.value' (plain text, not base64).  |
| apply | `uni-zone-ctx/mesh-default/routing/MeshExternalService-postgres.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/routing/MeshExternalService-example-api.yaml` | accepted | Warning: 'spec.tls.verification.caCert' uses 'secret', 'inline' or 'inlineString', which are deprecated and no longer read in 3.0. Use 'type: Secret' with 'secretRef', or 'type: InsecureInline' with 'insecureInline.value' (plain text, not base64).  |
| apply | `uni-zone-ctx/mesh-default/routing/MeshGateway-edge-gateway.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/routing/MeshGatewayRoute-edge-http.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/routing/MeshGatewayRoute-edge-tcp.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/routing/MeshHTTPRoute-backend-canary.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/routing/MeshHTTPRoute-frontend-routes.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/routing/MeshLoadBalancingStrategy-ring-hash.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/routing/MeshMultiZoneService-backend-mz.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/routing/MeshPassthrough-allow-egress.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/routing/MeshService-legacy-db.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/routing/MeshTCPRoute-redis-route.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/zero-trust/MeshIdentity-identity.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/zero-trust/MeshOPA-opa-frontend.yaml` | accepted | Warning: MeshService value for 'targetRef.kind' is deprecated, use Dataplane with labels instead Warning: 'spec.default.agentConfig' uses 'secret', 'inline' or 'inlineString', which are deprecated and no longer read in 3.0. Use 'type: Secret' with 'secretRef', or 'type: InsecureInline' with 'insecureInline.value' (plain text, not base64). Warning: 'spec.default.appendPolicies[0].rego' uses 'secret', 'inline' or 'inlineString', which are deprecated and no longer read in 3.0. Use 'type: Secret' with 'secretRef', or 'type: InsecureInline' with 'insecureInline.value' (plain text, not base64). Warn |
| apply | `uni-zone-ctx/mesh-default/zero-trust/MeshTLS-strict-tls.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/zero-trust/MeshTrafficPermission-backend-allow.yaml` | accepted | Warning: 'from' field is deprecated and will be removed in 3.0, use 'rules' with MeshIdentity (spiffeId) instead Warning: MeshService value for 'from[].targetRef.kind' is deprecated, use MeshSubset with 'kuma.io/service' instead  |
| apply | `uni-zone-ctx/mesh-default/zero-trust/MeshTrafficPermission-allow-all.yaml` | accepted | Warning: 'from' field is deprecated and will be removed in 3.0, use 'rules' with MeshIdentity (spiffeId) instead  |
| apply | `uni-zone-ctx/mesh-default/zero-trust/MeshTrust-partner-trust.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/zero-trust/MeshOPA-opa-backend.yaml` | accepted | Warning: 'spec.default.agentConfig' uses 'secret', 'inline' or 'inlineString', which are deprecated and no longer read in 3.0. Use 'type: Secret' with 'secretRef', or 'type: InsecureInline' with 'insecureInline.value' (plain text, not base64). Warning: 'spec.default.appendPolicies[0].rego' uses 'secret', 'inline' or 'inlineString', which are deprecated and no longer read in 3.0. Use 'type: Secret' with 'secretRef', or 'type: InsecureInline' with 'insecureInline.value' (plain text, not base64).  |
| apply | `uni-zone-ctx/mesh-default/zero-trust/MeshTrafficPermission-allow-all-default.yaml` | accepted | Warning: 'from' field is deprecated and will be removed in 3.0, use 'rules' with MeshIdentity (spiffeId) instead  |
| apply | `uni-zone-ctx/mesh-default/zero-trust/MeshTrafficPermission-frontend-to-backend.yaml` | accepted | Warning: 'from' field is deprecated and will be removed in 3.0, use 'rules' with MeshIdentity (spiffeId) instead  |
| apply | `uni-zone-ctx/mesh-payments/zero-trust/MeshTrafficPermission-payments-all.yaml` | accepted | Warning: 'from' field is deprecated and will be removed in 3.0, use 'rules' with MeshIdentity (spiffeId) instead  |
| apply | `uni-zone-ctx/mesh-default/observability/MeshMetric-default-metrics.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/observability/MeshAccessLog-backend-inbound-logs.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/observability/MeshAccessLog-otel-logs.yaml` | accepted | Warning: openTelemetry.endpoint is deprecated, use openTelemetry.backendRef with a MeshOpenTelemetryBackend resource instead  |
| apply | `uni-zone-ctx/mesh-default/observability/MeshMetric-metrics-default.yaml` | accepted | Warning: openTelemetry.endpoint is deprecated, use openTelemetry.backendRef with a MeshOpenTelemetryBackend resource instead  |
| apply | `uni-zone-ctx/mesh-default/observability/MeshTrace-otel-trace.yaml` | accepted | Warning: openTelemetry.endpoint is deprecated, use openTelemetry.backendRef with a MeshOpenTelemetryBackend resource instead  |
| apply | `uni-zone-ctx/mesh-default/observability/MeshAccessLog-log-all.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/observability/MeshAccessLog-log-backend.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/observability/MeshTrace-trace-all.yaml` | accepted |   |
| apply | `uni-zone-ctx/global-scoped-resources/other/AccessAudit-audit.yaml` | accepted |   |
| apply | `uni-zone-ctx/global-scoped-resources/other/AccessRole-mesh-operator.yaml` | accepted |   |
| apply | `uni-zone-ctx/global-scoped-resources/other/HostnameGenerator-local-mesh-external-services.yaml` | accepted |   |
| apply | `uni-zone-ctx/global-scoped-resources/other/ZoneEgress-zone-egress-1.yaml` | accepted |   |
| apply | `uni-zone-ctx/global-scoped-resources/other/ZoneIngress-zone-ingress-1.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/other/Dataplane-backend-1.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/other/Dataplane-edge-gateway-1.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/other/Dataplane-legacy-db-1.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/other/MeshGlobalRateLimit-global-rl.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/mesh/Mesh-default.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/mesh/MeshProxyPatch-cluster-timeout.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/mesh/MeshProxyPatch-custom-template.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-payments/mesh/Mesh-payments.yaml` | accepted |   |

## Rejections

None.

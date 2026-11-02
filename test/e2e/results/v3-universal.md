# v3 output on universal

Kong Mesh 3.0.1, universal, platform `marina`.
Applied for real with `kumactl apply` against a memory-store CP.

| Phase | File | Result | Server message |
|---|---|---|---|
| apply | `uni-zone-ctx/mesh-default/backends/MeshOpenTelemetryBackend-otel-logs-otel.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/backends/MeshOpenTelemetryBackend-metrics-default-otel.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/backends/MeshOpenTelemetryBackend-otel-trace-otel.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/backends/MeshOpenTelemetryBackend-collector.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshCircuitBreaker-cb-redis.yaml` | **REJECTED** | resource[0]: failed to parse resource: invalid resource type "" Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshHealthCheck-hc-web.yaml` | **REJECTED** | resource[0]: failed to parse resource: invalid resource type "" Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshCircuitBreaker-hc-web-panic-threshold.yaml` | **REJECTED** | resource[0]: failed to parse resource: invalid resource type "" Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshCircuitBreaker-cb-to-redis.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshHealthCheck-hc-panic.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshRateLimit-backend-rate-limit.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshRetry-retry-default.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshTimeout-frontend-to-backend.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshRateLimit-rl-backend.yaml` | **REJECTED** | resource[0]: failed to parse resource: invalid resource type "" Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshRetry-retry-all.yaml` | **REJECTED** | resource[0]: failed to parse resource: invalid resource type "" Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshTimeout-timeout-backend.yaml` | **REJECTED** | resource[0]: failed to parse resource: invalid resource type "" Error: failed to validate some resources  |
| apply | `uni-zone-ctx/global-scoped-resources/routing/Gateway-edge-gateway.yaml` | **REJECTED** | resource[0]: failed to parse resource: invalid resource type "" Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/routing/MeshPassthrough-default-passthrough.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/routing/MeshExternalService-httpbin.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/routing/MeshExternalService-postgres.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/routing/MeshExternalService-example-api.yaml` | **REJECTED** | resource[0]: failed to parse resource: spec.tls.verification.caCert.inline: unknown field Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/routing/MeshHTTPRoute-backend-canary.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/routing/MeshHTTPRoute-frontend-routes.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/routing/MeshLoadBalancingStrategy-ring-hash.yaml` | **REJECTED** | resource[0]: failed to parse resource: spec.to[0].default.hashPolicies[1].type: in body should be one of [Header Cookie Connection QueryParameter FilterState] Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/routing/MeshMultiZoneService-backend-mz.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/routing/MeshPassthrough-allow-egress.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/routing/MeshService-legacy-db.yaml` | **REJECTED** | resource[0]: failed to parse resource: spec.selector.dataplaneTags: unknown field Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/routing/MeshTCPRoute-redis-route.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-payments/routing/MeshPassthrough-payments-passthrough.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/zero-trust/MeshIdentity-identity.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/zero-trust/MeshOPA-opa-frontend.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/zero-trust/MeshTLS-strict-tls.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/zero-trust/MeshTrust-partner-trust.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/zero-trust/MeshOPA-opa-backend.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/observability/MeshMetric-default-metrics.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/observability/MeshAccessLog-backend-inbound-logs.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/observability/MeshAccessLog-otel-logs.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/observability/MeshMetric-metrics-default.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/observability/MeshTrace-otel-trace.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/observability/MeshAccessLog-log-all.yaml` | **REJECTED** | resource[0]: failed to parse resource: invalid resource type "" Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/observability/MeshAccessLog-log-backend.yaml` | **REJECTED** | resource[0]: failed to parse resource: invalid resource type "" Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/observability/MeshTrace-trace-all.yaml` | **REJECTED** | resource[0]: failed to parse resource: invalid resource type "" Error: failed to validate some resources  |
| apply | `uni-zone-ctx/global-scoped-resources/other/AccessAudit-audit.yaml` | accepted |   |
| apply | `uni-zone-ctx/global-scoped-resources/other/AccessRole-mesh-operator.yaml` | accepted |   |
| apply | `uni-zone-ctx/global-scoped-resources/other/HostnameGenerator-local-mesh-external-services.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/other/Dataplane-backend-1.yaml` | **REJECTED** | resource[0]: failed to parse resource: networking.outbound[0].backendRef: must be defined Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/other/Dataplane-edge-gateway-1.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/other/Dataplane-legacy-db-1.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/mesh/Mesh-default.yaml` | accepted | Warning: meshServices was removed in 3.0 and is ignored: every mesh behaves as Exclusive. Drop the field before it is reserved again in a future release.  |
| apply | `uni-zone-ctx/mesh-default/mesh/MeshProxyPatch-cluster-timeout.yaml` | accepted |   |
| apply | `uni-zone-ctx/mesh-default/mesh/MeshProxyPatch-custom-template.yaml` | **REJECTED** | resource[0]: failed to parse resource: invalid resource type "" Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-payments/mesh/Mesh-payments.yaml` | accepted | Warning: meshServices was removed in 3.0 and is ignored: every mesh behaves as Exclusive. Drop the field before it is reserved again in a future release.  |
| _manual | `uni-zone-ctx/_manual/global-scoped-resources/other/ZoneEgress-zone-egress-1.yaml` | rejected (expected) | resource[0]: failed to parse resource: invalid resource type "ZoneEgress" Error: failed to validate some resources  |
| _manual | `uni-zone-ctx/_manual/global-scoped-resources/other/ZoneIngress-zone-ingress-1.yaml` | rejected (expected) | resource[0]: failed to parse resource: invalid resource type "ZoneIngress" Error: failed to validate some resources  |
| _manual | `uni-zone-ctx/_manual/mesh-default/other/MeshGlobalRateLimit-global-rl.yaml` | rejected (expected) | resource[0]: failed to parse resource: invalid resource type "MeshGlobalRateLimit" Error: failed to validate some resources  |
| _manual | `uni-zone-ctx/_manual/mesh-default/resiliency/FaultInjection-fi-backend.yaml` | rejected (expected) | resource[0]: failed to parse resource: invalid resource type "FaultInjection" Error: failed to validate some resources  |
| _manual | `uni-zone-ctx/_manual/mesh-default/resiliency/MeshFaultInjection-backend-faults.yaml` | rejected (expected) | resource[0]: failed to parse resource: spec.from: unknown field Error: failed to validate some resources  |
| _manual | `uni-zone-ctx/_manual/mesh-default/routing/TrafficRoute-backend-split.yaml` | rejected (expected) | resource[0]: failed to parse resource: invalid resource type "TrafficRoute" Error: failed to validate some resources  |
| _manual | `uni-zone-ctx/_manual/mesh-default/routing/VirtualOutbound-instances.yaml` | rejected (expected) | resource[0]: failed to parse resource: invalid resource type "VirtualOutbound" Error: failed to validate some resources  |
| _manual | `uni-zone-ctx/_manual/mesh-default/zero-trust/MeshTrafficPermission-allow-all.yaml` | rejected (expected) | resource[0]: failed to parse resource: spec.from: unknown field Error: failed to validate some resources  |
| _manual | `uni-zone-ctx/_manual/mesh-default/zero-trust/MeshTrafficPermission-backend-allow.yaml` | rejected (expected) | resource[0]: failed to parse resource: spec.from: unknown field Error: failed to validate some resources  |
| _manual | `uni-zone-ctx/_manual/mesh-default/zero-trust/TrafficPermission-allow-all-default.yaml` | rejected (expected) | resource[0]: failed to parse resource: invalid resource type "TrafficPermission" Error: failed to validate some resources  |
| _manual | `uni-zone-ctx/_manual/mesh-default/zero-trust/TrafficPermission-frontend-to-backend.yaml` | rejected (expected) | resource[0]: failed to parse resource: invalid resource type "TrafficPermission" Error: failed to validate some resources  |
| _manual | `uni-zone-ctx/_manual/mesh-payments/zero-trust/TrafficPermission-payments-all.yaml` | rejected (expected) | resource[0]: failed to parse resource: invalid resource type "TrafficPermission" Error: failed to validate some resources  |

## Rejections

| apply | `uni-zone-ctx/mesh-default/resiliency/MeshCircuitBreaker-cb-redis.yaml` | **REJECTED** | resource[0]: failed to parse resource: invalid resource type "" Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshHealthCheck-hc-web.yaml` | **REJECTED** | resource[0]: failed to parse resource: invalid resource type "" Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshCircuitBreaker-hc-web-panic-threshold.yaml` | **REJECTED** | resource[0]: failed to parse resource: invalid resource type "" Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshRateLimit-rl-backend.yaml` | **REJECTED** | resource[0]: failed to parse resource: invalid resource type "" Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshRetry-retry-all.yaml` | **REJECTED** | resource[0]: failed to parse resource: invalid resource type "" Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/resiliency/MeshTimeout-timeout-backend.yaml` | **REJECTED** | resource[0]: failed to parse resource: invalid resource type "" Error: failed to validate some resources  |
| apply | `uni-zone-ctx/global-scoped-resources/routing/Gateway-edge-gateway.yaml` | **REJECTED** | resource[0]: failed to parse resource: invalid resource type "" Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/routing/MeshExternalService-example-api.yaml` | **REJECTED** | resource[0]: failed to parse resource: spec.tls.verification.caCert.inline: unknown field Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/routing/MeshLoadBalancingStrategy-ring-hash.yaml` | **REJECTED** | resource[0]: failed to parse resource: spec.to[0].default.hashPolicies[1].type: in body should be one of [Header Cookie Connection QueryParameter FilterState] Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/routing/MeshService-legacy-db.yaml` | **REJECTED** | resource[0]: failed to parse resource: spec.selector.dataplaneTags: unknown field Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/observability/MeshAccessLog-log-all.yaml` | **REJECTED** | resource[0]: failed to parse resource: invalid resource type "" Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/observability/MeshAccessLog-log-backend.yaml` | **REJECTED** | resource[0]: failed to parse resource: invalid resource type "" Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/observability/MeshTrace-trace-all.yaml` | **REJECTED** | resource[0]: failed to parse resource: invalid resource type "" Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/other/Dataplane-backend-1.yaml` | **REJECTED** | resource[0]: failed to parse resource: networking.outbound[0].backendRef: must be defined Error: failed to validate some resources  |
| apply | `uni-zone-ctx/mesh-default/mesh/MeshProxyPatch-custom-template.yaml` | **REJECTED** | resource[0]: failed to parse resource: invalid resource type "" Error: failed to validate some resources  |

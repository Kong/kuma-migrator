# v3 output on kubernetes

Kong Mesh 3.0.1, kubernetes, platform `marina`.
Applied with `kubectl apply --dry-run=server` (runs the validating webhooks).

| Phase | File | Result | Server message |
|---|---|---|---|
| apply | `k8s-zone-ctx/mesh-default/backends/MeshOpenTelemetryBackend-kong-mesh-system-collector.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/backends/MeshOpenTelemetryBackend-kong-mesh-system-otel-logs-otel.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/backends/MeshOpenTelemetryBackend-kong-mesh-system-metrics-default-otel.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/backends/MeshOpenTelemetryBackend-kong-mesh-system-otel-trace-otel.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshCircuitBreaker-kong-mesh-system-cb-to-redis.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshHealthCheck-kong-mesh-system-hc-panic.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshRateLimit-kuma-demo-backend-rate-limit.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshRetry-kong-mesh-system-retry-default.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshTimeout-kong-mesh-system-frontend-to-backend.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshCircuitBreaker-kong-mesh-system-cb-redis.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshHealthCheck-kong-mesh-system-hc-web.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshCircuitBreaker-kong-mesh-system-hc-web-panic-threshold.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshCircuitBreaker-kong-mesh-system-hc-panic-panic-threshold.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshRateLimit-kong-mesh-system-rl-backend.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshRetry-kong-mesh-system-retry-all.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshTimeout-kong-mesh-system-timeout-backend.yaml` | accepted |   |
| apply | `k8s-zone-ctx/global-scoped-resources/routing/Gateway-kuma-demo-edge-gateway.yaml` | accepted |   |
| apply | `k8s-zone-ctx/global-scoped-resources/routing/HTTPRoute-kuma-demo-edge-http.yaml` | accepted |   |
| apply | `k8s-zone-ctx/global-scoped-resources/routing/TCPRoute-kuma-demo-edge-tcp.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/routing/MeshHTTPRoute-kong-mesh-system-frontend-routes.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/routing/MeshHTTPRoute-kuma-demo-backend-canary.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/routing/MeshLoadBalancingStrategy-kong-mesh-system-ring-hash.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/routing/MeshMultiZoneService-kong-mesh-system-backend-mz.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/routing/MeshPassthrough-kong-mesh-system-allow-egress.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/routing/MeshTCPRoute-kuma-demo-redis-route.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/routing/MeshPassthrough-kong-mesh-system-default-passthrough.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/routing/MeshExternalService-kong-mesh-system-httpbin.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/routing/MeshExternalService-kong-mesh-system-postgres.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-payments/routing/MeshPassthrough-kong-mesh-system-payments-passthrough.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/zero-trust/MeshIdentity-kong-mesh-system-identity.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/zero-trust/MeshOPA-kong-mesh-system-opa-frontend.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/zero-trust/MeshTLS-kong-mesh-system-strict-tls.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/zero-trust/MeshTrust-kong-mesh-system-partner-trust.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/zero-trust/MeshOPA-kong-mesh-system-opa-backend.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/observability/MeshAccessLog-kong-mesh-system-otel-logs.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/observability/MeshAccessLog-kuma-demo-backend-inbound-logs.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/observability/MeshMetric-kong-mesh-system-metrics-default.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/observability/MeshTrace-kong-mesh-system-otel-trace.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/observability/MeshMetric-kong-mesh-system-default-metrics.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/observability/MeshAccessLog-kong-mesh-system-log-all.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/observability/MeshAccessLog-kong-mesh-system-log-backend.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/observability/MeshTrace-kong-mesh-system-trace-all.yaml` | accepted |   |
| apply | `k8s-zone-ctx/global-scoped-resources/other/AccessAudit-audit.yaml` | accepted |   |
| apply | `k8s-zone-ctx/global-scoped-resources/other/AccessRole-mesh-operator.yaml` | accepted |   |
| apply | `k8s-zone-ctx/global-scoped-resources/other/HostnameGenerator-kong-mesh-system-local-mesh-external-services.yaml` | accepted |   |
| apply | `k8s-zone-ctx/global-scoped-resources/other/ContainerPatch-kong-mesh-system-sidecar-resources.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/mesh/Mesh-default.yaml` | accepted | Warning: meshServices was removed in 3.0 and is ignored: every mesh behaves as Exclusive. Drop the field before it is reserved again in a future release.  |
| apply | `k8s-zone-ctx/mesh-default/mesh/MeshProxyPatch-kong-mesh-system-cluster-timeout.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/mesh/MeshProxyPatch-kong-mesh-system-custom-template.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-payments/mesh/Mesh-payments.yaml` | accepted | Warning: meshServices was removed in 3.0 and is ignored: every mesh behaves as Exclusive. Drop the field before it is reserved again in a future release.  |
| _manual | `k8s-zone-ctx/_manual/global-scoped-resources/routing/MeshGatewayConfig-edge-config.yaml` | rejected (expected) | error: resource mapping not found for name: "edge-config" namespace: "" from "$E2E_WORK/out/v3-kubernetes/k8s-zone-ctx/_manual/global-scoped-resources/routing/MeshGatewayConfig-edge-config.yaml": no matches for kind "MeshGatewayConfig" in version "kuma.io/v1alpha1" ensure CRDs are installed first  |
| _manual | `k8s-zone-ctx/_manual/mesh-default/other/MeshGlobalRateLimit-kong-mesh-system-global-rl.yaml` | rejected (expected) | error: resource mapping not found for name: "global-rl" namespace: "kong-mesh-system" from "$E2E_WORK/out/v3-kubernetes/k8s-zone-ctx/_manual/mesh-default/other/MeshGlobalRateLimit-kong-mesh-system-global-rl.yaml": no matches for kind "MeshGlobalRateLimit" in version "kuma.io/v1alpha1" ensure CRDs are installed first  |
| _manual | `k8s-zone-ctx/_manual/mesh-default/resiliency/FaultInjection-fi-backend.yaml` | rejected (expected) | error: resource mapping not found for name: "fi-backend" namespace: "" from "$E2E_WORK/out/v3-kubernetes/k8s-zone-ctx/_manual/mesh-default/resiliency/FaultInjection-fi-backend.yaml": no matches for kind "FaultInjection" in version "kuma.io/v1alpha1" ensure CRDs are installed first  |
| _manual | `k8s-zone-ctx/_manual/mesh-default/resiliency/MeshFaultInjection-kuma-demo-backend-faults.yaml` | rejected (expected) | Error from server (BadRequest): error when creating "$E2E_WORK/out/v3-kubernetes/k8s-zone-ctx/_manual/mesh-default/resiliency/MeshFaultInjection-kuma-demo-backend-faults.yaml": MeshFaultInjection in version "v1alpha1" cannot be handled as a MeshFaultInjection: strict decoding error: unknown field "spec.from"  |
| _manual | `k8s-zone-ctx/_manual/mesh-default/routing/MeshExternalService-kong-mesh-system-example-api.yaml` | rejected (expected) | Error from server (BadRequest): error when creating "$E2E_WORK/out/v3-kubernetes/k8s-zone-ctx/_manual/mesh-default/routing/MeshExternalService-kong-mesh-system-example-api.yaml": MeshExternalService in version "v1alpha1" cannot be handled as a MeshExternalService: strict decoding error: unknown field "spec.tls.verification.caCert.inline"  |
| _manual | `k8s-zone-ctx/_manual/mesh-default/routing/MeshGatewayInstance-kuma-demo-edge-gateway.yaml` | rejected (expected) | error: resource mapping not found for name: "edge-gateway" namespace: "kuma-demo" from "$E2E_WORK/out/v3-kubernetes/k8s-zone-ctx/_manual/mesh-default/routing/MeshGatewayInstance-kuma-demo-edge-gateway.yaml": no matches for kind "MeshGatewayInstance" in version "kuma.io/v1alpha1" ensure CRDs are installed first  |
| _manual | `k8s-zone-ctx/_manual/mesh-default/routing/MeshService-kuma-demo-legacy-db.yaml` | rejected (expected) | Error from server (BadRequest): error when creating "$E2E_WORK/out/v3-kubernetes/k8s-zone-ctx/_manual/mesh-default/routing/MeshService-kuma-demo-legacy-db.yaml": MeshService in version "v1alpha1" cannot be handled as a MeshService: strict decoding error: unknown field "spec.selector.dataplaneTags"  |
| _manual | `k8s-zone-ctx/_manual/mesh-default/routing/TrafficRoute-backend-split.yaml` | rejected (expected) | error: resource mapping not found for name: "backend-split" namespace: "" from "$E2E_WORK/out/v3-kubernetes/k8s-zone-ctx/_manual/mesh-default/routing/TrafficRoute-backend-split.yaml": no matches for kind "TrafficRoute" in version "kuma.io/v1alpha1" ensure CRDs are installed first  |
| _manual | `k8s-zone-ctx/_manual/mesh-default/routing/VirtualOutbound-instances.yaml` | rejected (expected) | error: resource mapping not found for name: "instances" namespace: "" from "$E2E_WORK/out/v3-kubernetes/k8s-zone-ctx/_manual/mesh-default/routing/VirtualOutbound-instances.yaml": no matches for kind "VirtualOutbound" in version "kuma.io/v1alpha1" ensure CRDs are installed first  |
| _manual | `k8s-zone-ctx/_manual/mesh-default/zero-trust/MeshTrafficPermission-kong-mesh-system-allow-all.yaml` | rejected (expected) | Error from server (BadRequest): error when creating "$E2E_WORK/out/v3-kubernetes/k8s-zone-ctx/_manual/mesh-default/zero-trust/MeshTrafficPermission-kong-mesh-system-allow-all.yaml": MeshTrafficPermission in version "v1alpha1" cannot be handled as a MeshTrafficPermission: strict decoding error: unknown field "spec.from"  |
| _manual | `k8s-zone-ctx/_manual/mesh-default/zero-trust/MeshTrafficPermission-kuma-demo-backend-allow.yaml` | rejected (expected) | Error from server (BadRequest): error when creating "$E2E_WORK/out/v3-kubernetes/k8s-zone-ctx/_manual/mesh-default/zero-trust/MeshTrafficPermission-kuma-demo-backend-allow.yaml": MeshTrafficPermission in version "v1alpha1" cannot be handled as a MeshTrafficPermission: strict decoding error: unknown field "spec.from"  |
| _manual | `k8s-zone-ctx/_manual/mesh-default/zero-trust/TrafficPermission-allow-all-default.yaml` | rejected (expected) | error: resource mapping not found for name: "allow-all-default" namespace: "" from "$E2E_WORK/out/v3-kubernetes/k8s-zone-ctx/_manual/mesh-default/zero-trust/TrafficPermission-allow-all-default.yaml": no matches for kind "TrafficPermission" in version "kuma.io/v1alpha1" ensure CRDs are installed first  |
| _manual | `k8s-zone-ctx/_manual/mesh-default/zero-trust/TrafficPermission-frontend-to-backend.yaml` | rejected (expected) | error: resource mapping not found for name: "frontend-to-backend" namespace: "" from "$E2E_WORK/out/v3-kubernetes/k8s-zone-ctx/_manual/mesh-default/zero-trust/TrafficPermission-frontend-to-backend.yaml": no matches for kind "TrafficPermission" in version "kuma.io/v1alpha1" ensure CRDs are installed first  |
| _manual | `k8s-zone-ctx/_manual/mesh-payments/zero-trust/TrafficPermission-payments-all.yaml` | rejected (expected) | error: resource mapping not found for name: "payments-all" namespace: "" from "$E2E_WORK/out/v3-kubernetes/k8s-zone-ctx/_manual/mesh-payments/zero-trust/TrafficPermission-payments-all.yaml": no matches for kind "TrafficPermission" in version "kuma.io/v1alpha1" ensure CRDs are installed first  |

## Rejections

None.

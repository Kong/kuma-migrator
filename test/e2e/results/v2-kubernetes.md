# v2 output on kubernetes

Kong Mesh 2.14.7, kubernetes, platform `marina`.
Applied with `kubectl apply --dry-run=server` (runs the validating webhooks).

| Phase | File | Result | Server message |
|---|---|---|---|
| input | `k8s-zone-ctx/mesh-default/mesh/Mesh-default.yaml` | accepted | Warning: resource meshes/default is missing the kubectl.kubernetes.io/last-applied-configuration annotation which is required by kubectl apply. kubectl apply should only be used on resources created declaratively by either kubectl create --save-config or kubectl apply. The missing annotation will be patched automatically.  |
| input | `k8s-zone-ctx/mesh-payments/mesh/Mesh-payments.yaml` | accepted |   |
| input | `k8s-zone-ctx/global-scoped-resources/other/AccessAudit-default.yaml` | accepted |   |
| input | `k8s-zone-ctx/global-scoped-resources/other/AccessRole-mesh-admin.yaml` | accepted |   |
| input | `k8s-zone-ctx/global-scoped-resources/other/ContainerPatch-kong-mesh-system-sidecar.yaml` | accepted |   |
| input | `k8s-zone-ctx/global-scoped-resources/other/HostnameGenerator-kong-mesh-system-local.yaml` | accepted |   |
| input | `k8s-zone-ctx/global-scoped-resources/other/MeshGatewayConfig-edge.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/mesh/MeshProxyPatch-kuma-demo-cluster-timeout.yaml` | accepted | Warning: MeshSubset value for 'targetRef.kind' is deprecated, use Dataplane with labels instead  |
| input | `k8s-zone-ctx/mesh-default/mesh/ProxyTemplate-custom.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/observability/MeshAccessLog-kong-mesh-system-otel.yaml` | accepted | Warning: openTelemetry.endpoint is deprecated, use openTelemetry.backendRef with a MeshOpenTelemetryBackend resource instead  |
| input | `k8s-zone-ctx/mesh-default/observability/MeshAccessLog-kuma-demo-from.yaml` | accepted | Warning: 'from' field is deprecated, use 'rules' instead  |
| input | `k8s-zone-ctx/mesh-default/observability/MeshMetric-kong-mesh-system-profiles.yaml` | accepted | Warning: openTelemetry.endpoint is deprecated, use openTelemetry.backendRef with a MeshOpenTelemetryBackend resource instead  |
| input | `k8s-zone-ctx/mesh-default/observability/MeshTrace-kong-mesh-system-otel.yaml` | accepted | Warning: openTelemetry.endpoint is deprecated, use openTelemetry.backendRef with a MeshOpenTelemetryBackend resource instead  |
| input | `k8s-zone-ctx/mesh-default/observability/TrafficLog-all.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/observability/TrafficLog-default-backend.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/observability/TrafficTrace-all.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/other/MeshGlobalRateLimit-kong-mesh-system.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/other/MeshOpenTelemetryBackend-kong-mesh-system-collector.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/resiliency/CircuitBreaker-redis.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/resiliency/FaultInjection-backend.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/resiliency/HealthCheck-web.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/resiliency/MeshCircuitBreaker-kong-mesh-system-svc-name.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/resiliency/MeshFaultInjection-kuma-demo-from-meshservice.yaml` | accepted | Warning: 'from' field is deprecated, use 'rules' with SPIFFE-based 'matches' instead  |
| input | `k8s-zone-ctx/mesh-default/resiliency/MeshHealthCheck-kong-mesh-system-panic.yaml` | accepted | Warning: healthyPanicThreshold for 'to[].default' is deprecated. The setting has been moved to MeshCircuitBreaker policy, please use MeshCircuitBreaker policy instead.  |
| input | `k8s-zone-ctx/mesh-default/resiliency/MeshRateLimit-kuma-demo-from.yaml` | accepted | Warning: 'from' field is deprecated, use 'rules' instead  |
| input | `k8s-zone-ctx/mesh-default/resiliency/MeshRetry-kong-mesh-system-default.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/resiliency/MeshTimeout-kong-mesh-system-subset.yaml` | accepted | Warning: MeshSubset value for 'targetRef.kind' is deprecated, use Dataplane with labels instead  |
| input | `k8s-zone-ctx/mesh-default/resiliency/RateLimit-backend.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/resiliency/Retry-all.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/resiliency/Timeout-backend.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/routing/ExternalService-httpbin.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/routing/ExternalService-postgres.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/routing/MeshExternalService-kong-mesh-system-api.yaml` | accepted | Warning: 'spec.tls.verification.caCert' uses 'secret', 'inline' or 'inlineString', which are deprecated and no longer read in 3.0. Use 'type: Secret' with 'secretRef', or 'type: InsecureInline' with 'insecureInline.value' (plain text, not base64).  |
| input | `k8s-zone-ctx/mesh-default/routing/MeshGateway-edge.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/routing/MeshGatewayInstance-kuma-demo-edge-gateway.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/routing/MeshGatewayRoute-edge-http.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/routing/MeshGatewayRoute-edge-tcp.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/routing/MeshHTTPRoute-kong-mesh-system-subset.yaml` | accepted | Warning: MeshSubset value for 'targetRef.kind' is deprecated, use Dataplane with labels instead  |
| input | `k8s-zone-ctx/mesh-default/routing/MeshHTTPRoute-kuma-demo-backend.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/routing/MeshLoadBalancingStrategy-kong-mesh-system-ringhash.yaml` | accepted | Warning: configuration 'spec.to[0].default.loadBalancer.ringHash.hashPolicies' is deprecated, use 'spec.to[0].default.hashPolicies' instead Warning: SourceIP type for 'spec.to[0].default.loadBalancer.ringHash.hashPolicies[1].type' is deprecated, use Connection instead  |
| input | `k8s-zone-ctx/mesh-default/routing/MeshMultiZoneService-kong-mesh-system-backend.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/routing/MeshPassthrough-kong-mesh-system-egress.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/routing/MeshService-kuma-demo-legacy-db.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/routing/MeshTCPRoute-kuma-demo-redis.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/routing/TrafficRoute-split.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/routing/VirtualOutbound-instances.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/zero-trust/MeshIdentity-kong-mesh-system-bundled.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/zero-trust/MeshOPA-kong-mesh-system-legacy.yaml` | accepted | Warning: MeshService value for 'targetRef.kind' is deprecated, use Dataplane with labels instead Warning: 'spec.default.agentConfig' uses 'secret', 'inline' or 'inlineString', which are deprecated and no longer read in 3.0. Use 'type: Secret' with 'secretRef', or 'type: InsecureInline' with 'insecureInline.value' (plain text, not base64). Warning: 'spec.default.appendPolicies[0].rego' uses 'secret', 'inline' or 'inlineString', which are deprecated and no longer read in 3.0. Use 'type: Secret' with 'secretRef', or 'type: InsecureInline' with 'insecureInline.value' (plain text, not base64). Warn |
| input | `k8s-zone-ctx/mesh-default/zero-trust/MeshTLS-kong-mesh-system-strict.yaml` | accepted | Warning: 'from' field is deprecated, use 'rules' instead  |
| input | `k8s-zone-ctx/mesh-default/zero-trust/MeshTrafficPermission-kong-mesh-system-mesh.yaml` | accepted | Warning: 'from' field is deprecated and will be removed in 3.0, use 'rules' with MeshIdentity (spiffeId) instead  |
| input | `k8s-zone-ctx/mesh-default/zero-trust/MeshTrafficPermission-kuma-demo-from.yaml` | accepted | Warning: 'from' field is deprecated and will be removed in 3.0, use 'rules' with MeshIdentity (spiffeId) instead Warning: MeshService value for 'from[].targetRef.kind' is deprecated, use MeshSubset with 'kuma.io/service' instead  |
| input | `k8s-zone-ctx/mesh-default/zero-trust/MeshTrust-kong-mesh-system-partner.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/zero-trust/OPAPolicy-default.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/zero-trust/TrafficPermission-allow-all.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-default/zero-trust/TrafficPermission-frontend-to-backend.yaml` | accepted |   |
| input | `k8s-zone-ctx/mesh-payments/zero-trust/TrafficPermission-payments-all.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/backends/MeshOpenTelemetryBackend-kong-mesh-system-collector.yaml` | accepted |   |
| namespace | `k8s-zone-ctx/mesh-default/resiliency/MeshCircuitBreaker-cb-redis.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshCircuitBreaker-cb-redis.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshCircuitBreaker-kong-mesh-system-cb-to-redis.yaml` | accepted |   |
| namespace | `k8s-zone-ctx/mesh-default/resiliency/MeshFaultInjection-fi-backend.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshFaultInjection-fi-backend.yaml` | **REJECTED** | The MeshFaultInjection "fi-backend" is invalid: spec.from[0].default.http.responseBandwidth[0].limit: must be in kbps/Mbps/Gbps units  |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshFaultInjection-kuma-demo-backend-faults.yaml` | accepted | Warning: 'from' field is deprecated, use 'rules' with SPIFFE-based 'matches' instead  |
| namespace | `k8s-zone-ctx/mesh-default/resiliency/MeshHealthCheck-hc-web.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshHealthCheck-hc-web.yaml` | **REJECTED** | The MeshHealthCheck "hc-web" is invalid: * spec.to[0].default.http.requestHeadersToAdd.add[0].name: Invalid value: "Accept": spec.to[0].default.http.requestHeadersToAdd.add[0].name in body should match '^[a-z0-9!#$%&'*+\-.^_\x60\|~]+$' * spec.to[0].default.http.requestHeadersToAdd.set[0].name: Invalid value: "Content-Type": spec.to[0].default.http.requestHeadersToAdd.set[0].name in body should match '^[a-z0-9!#$%&'*+\-.^_\x60\|~]+$'  |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshHealthCheck-kong-mesh-system-hc-panic.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshRateLimit-kuma-demo-backend-rate-limit.yaml` | accepted |   |
| namespace | `k8s-zone-ctx/mesh-default/resiliency/MeshRateLimit-rl-backend.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshRateLimit-rl-backend.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshRetry-kong-mesh-system-retry-default.yaml` | accepted |   |
| namespace | `k8s-zone-ctx/mesh-default/resiliency/MeshRetry-retry-all.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshRetry-retry-all.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshTimeout-kong-mesh-system-frontend-to-backend.yaml` | accepted |   |
| namespace | `k8s-zone-ctx/mesh-default/resiliency/MeshTimeout-timeout-backend.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshTimeout-timeout-backend.yaml` | accepted |   |
| apply | `k8s-zone-ctx/global-scoped-resources/routing/Gateway-kuma-demo-edge-gateway.yaml` | accepted |   |
| apply | `k8s-zone-ctx/global-scoped-resources/routing/GatewayClass-edge-gateway.yaml` | accepted |   |
| apply | `k8s-zone-ctx/global-scoped-resources/routing/HTTPRoute-kuma-demo-edge-http.yaml` | accepted |   |
| apply | `k8s-zone-ctx/global-scoped-resources/routing/MeshGatewayConfig-edge-config.yaml` | accepted |   |
| apply | `k8s-zone-ctx/global-scoped-resources/routing/TCPRoute-kuma-demo-edge-tcp.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/routing/MeshExternalService-kong-mesh-system-example-api.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/routing/MeshExternalService-kong-mesh-system-httpbin.yaml` | accepted | Warning: 'spec.tls.verification.caCert' uses 'secret', 'inline' or 'inlineString', which are deprecated and no longer read in 3.0. Use 'type: Secret' with 'secretRef', or 'type: InsecureInline' with 'insecureInline.value' (plain text, not base64).  |
| apply | `k8s-zone-ctx/mesh-default/routing/MeshExternalService-kong-mesh-system-postgres.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/routing/MeshGatewayConfig-edge-gateway.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/routing/MeshHTTPRoute-kong-mesh-system-frontend-routes.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/routing/MeshHTTPRoute-kuma-demo-backend-canary.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/routing/MeshLoadBalancingStrategy-kong-mesh-system-ring-hash.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/routing/MeshMultiZoneService-kong-mesh-system-backend-mz.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/routing/MeshPassthrough-kong-mesh-system-allow-egress.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/routing/MeshPassthrough-kong-mesh-system-default-passthrough.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/routing/MeshService-kuma-demo-legacy-db.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/routing/MeshTCPRoute-kuma-demo-redis-route.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/routing/TrafficRoute-backend-split.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/routing/VirtualOutbound-instances.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/zero-trust/MeshIdentity-kong-mesh-system-identity.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/zero-trust/MeshOPA-kong-mesh-system-opa-frontend.yaml` | accepted | Warning: 'spec.default.agentConfig' uses 'secret', 'inline' or 'inlineString', which are deprecated and no longer read in 3.0. Use 'type: Secret' with 'secretRef', or 'type: InsecureInline' with 'insecureInline.value' (plain text, not base64). Warning: 'spec.default.appendPolicies[0].rego' uses 'secret', 'inline' or 'inlineString', which are deprecated and no longer read in 3.0. Use 'type: Secret' with 'secretRef', or 'type: InsecureInline' with 'insecureInline.value' (plain text, not base64). Warning: 'spec.default.appendPolicies[1].rego' uses 'secret', 'inline' or 'inlineString', which are d |
| namespace | `k8s-zone-ctx/mesh-default/zero-trust/MeshOPA-opa-backend.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| apply | `k8s-zone-ctx/mesh-default/zero-trust/MeshOPA-opa-backend.yaml` | accepted | Warning: 'spec.default.agentConfig' uses 'secret', 'inline' or 'inlineString', which are deprecated and no longer read in 3.0. Use 'type: Secret' with 'secretRef', or 'type: InsecureInline' with 'insecureInline.value' (plain text, not base64). Warning: 'spec.default.appendPolicies[0].rego' uses 'secret', 'inline' or 'inlineString', which are deprecated and no longer read in 3.0. Use 'type: Secret' with 'secretRef', or 'type: InsecureInline' with 'insecureInline.value' (plain text, not base64).  |
| apply | `k8s-zone-ctx/mesh-default/zero-trust/MeshTLS-kong-mesh-system-strict-tls.yaml` | accepted |   |
| namespace | `k8s-zone-ctx/mesh-default/zero-trust/MeshTrafficPermission-allow-all-default.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| apply | `k8s-zone-ctx/mesh-default/zero-trust/MeshTrafficPermission-allow-all-default.yaml` | accepted | Warning: 'from' field is deprecated and will be removed in 3.0, use 'rules' with MeshIdentity (spiffeId) instead  |
| namespace | `k8s-zone-ctx/mesh-default/zero-trust/MeshTrafficPermission-frontend-to-backend.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| apply | `k8s-zone-ctx/mesh-default/zero-trust/MeshTrafficPermission-frontend-to-backend.yaml` | accepted | Warning: 'from' field is deprecated and will be removed in 3.0, use 'rules' with MeshIdentity (spiffeId) instead  |
| apply | `k8s-zone-ctx/mesh-default/zero-trust/MeshTrafficPermission-kong-mesh-system-allow-all.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/zero-trust/MeshTrafficPermission-kuma-demo-backend-allow.yaml` | accepted | Warning: 'from' field is deprecated and will be removed in 3.0, use 'rules' with MeshIdentity (spiffeId) instead  |
| apply | `k8s-zone-ctx/mesh-default/zero-trust/MeshTrust-kong-mesh-system-partner-trust.yaml` | accepted |   |
| namespace | `k8s-zone-ctx/mesh-payments/zero-trust/MeshTrafficPermission-payments-all.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| apply | `k8s-zone-ctx/mesh-payments/zero-trust/MeshTrafficPermission-payments-all.yaml` | accepted | Warning: 'from' field is deprecated and will be removed in 3.0, use 'rules' with MeshIdentity (spiffeId) instead  |
| apply | `k8s-zone-ctx/mesh-default/observability/MeshAccessLog-kong-mesh-system-otel-logs.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/observability/MeshAccessLog-kuma-demo-backend-inbound-logs.yaml` | accepted |   |
| namespace | `k8s-zone-ctx/mesh-default/observability/MeshAccessLog-log-all.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| apply | `k8s-zone-ctx/mesh-default/observability/MeshAccessLog-log-all.yaml` | accepted |   |
| namespace | `k8s-zone-ctx/mesh-default/observability/MeshAccessLog-log-backend.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| apply | `k8s-zone-ctx/mesh-default/observability/MeshAccessLog-log-backend.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/observability/MeshMetric-kong-mesh-system-default-metrics.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/observability/MeshMetric-kong-mesh-system-metrics-default.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/observability/MeshTrace-kong-mesh-system-otel-trace.yaml` | accepted |   |
| namespace | `k8s-zone-ctx/mesh-default/observability/MeshTrace-trace-all.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| apply | `k8s-zone-ctx/mesh-default/observability/MeshTrace-trace-all.yaml` | accepted |   |
| apply | `k8s-zone-ctx/global-scoped-resources/other/AccessAudit-audit.yaml` | accepted |   |
| apply | `k8s-zone-ctx/global-scoped-resources/other/AccessRole-mesh-operator.yaml` | accepted |   |
| apply | `k8s-zone-ctx/global-scoped-resources/other/ContainerPatch-kong-mesh-system-sidecar-resources.yaml` | accepted |   |
| apply | `k8s-zone-ctx/global-scoped-resources/other/HostnameGenerator-kong-mesh-system-local-mesh-external-services.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/other/MeshGlobalRateLimit-kong-mesh-system-global-rl.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-default/mesh/Mesh-default.yaml` | accepted |   |
| namespace | `k8s-zone-ctx/mesh-default/mesh/MeshProxyPatch-custom-template.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| apply | `k8s-zone-ctx/mesh-default/mesh/MeshProxyPatch-custom-template.yaml` | **REJECTED** | The MeshProxyPatch "custom-template" is invalid: spec.default.appendModifications[1].httpFilter.operation: Unsupported value: "addLast": supported values: "Remove", "Patch", "AddFirst", "AddBefore", "AddAfter", "AddLast"  |
| apply | `k8s-zone-ctx/mesh-default/mesh/MeshProxyPatch-kong-mesh-system-cluster-timeout.yaml` | accepted |   |
| apply | `k8s-zone-ctx/mesh-payments/mesh/Mesh-payments.yaml` | accepted |   |

## Rejections

| namespace | `k8s-zone-ctx/mesh-default/resiliency/MeshCircuitBreaker-cb-redis.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| namespace | `k8s-zone-ctx/mesh-default/resiliency/MeshFaultInjection-fi-backend.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshFaultInjection-fi-backend.yaml` | **REJECTED** | The MeshFaultInjection "fi-backend" is invalid: spec.from[0].default.http.responseBandwidth[0].limit: must be in kbps/Mbps/Gbps units  |
| namespace | `k8s-zone-ctx/mesh-default/resiliency/MeshHealthCheck-hc-web.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| apply | `k8s-zone-ctx/mesh-default/resiliency/MeshHealthCheck-hc-web.yaml` | **REJECTED** | The MeshHealthCheck "hc-web" is invalid: * spec.to[0].default.http.requestHeadersToAdd.add[0].name: Invalid value: "Accept": spec.to[0].default.http.requestHeadersToAdd.add[0].name in body should match '^[a-z0-9!#$%&'*+\-.^_\x60\|~]+$' * spec.to[0].default.http.requestHeadersToAdd.set[0].name: Invalid value: "Content-Type": spec.to[0].default.http.requestHeadersToAdd.set[0].name in body should match '^[a-z0-9!#$%&'*+\-.^_\x60\|~]+$'  |
| namespace | `k8s-zone-ctx/mesh-default/resiliency/MeshRateLimit-rl-backend.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| namespace | `k8s-zone-ctx/mesh-default/resiliency/MeshRetry-retry-all.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| namespace | `k8s-zone-ctx/mesh-default/resiliency/MeshTimeout-timeout-backend.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| namespace | `k8s-zone-ctx/mesh-default/zero-trust/MeshOPA-opa-backend.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| namespace | `k8s-zone-ctx/mesh-default/zero-trust/MeshTrafficPermission-allow-all-default.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| namespace | `k8s-zone-ctx/mesh-default/zero-trust/MeshTrafficPermission-frontend-to-backend.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| namespace | `k8s-zone-ctx/mesh-payments/zero-trust/MeshTrafficPermission-payments-all.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| namespace | `k8s-zone-ctx/mesh-default/observability/MeshAccessLog-log-all.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| namespace | `k8s-zone-ctx/mesh-default/observability/MeshAccessLog-log-backend.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| namespace | `k8s-zone-ctx/mesh-default/observability/MeshTrace-trace-all.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| namespace | `k8s-zone-ctx/mesh-default/mesh/MeshProxyPatch-custom-template.yaml` | **NO NAMESPACE** | namespaced kind without metadata.namespace; validated below with -n kong-mesh-system  |
| apply | `k8s-zone-ctx/mesh-default/mesh/MeshProxyPatch-custom-template.yaml` | **REJECTED** | The MeshProxyPatch "custom-template" is invalid: spec.default.appendModifications[1].httpFilter.operation: Unsupported value: "addLast": supported values: "Remove", "Patch", "AddFirst", "AddBefore", "AddAfter", "AddLast"  |

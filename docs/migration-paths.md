# Migration paths and deprecation warnings

Every transformation `kuma-migrator` performs, and every deprecated field it detects but deliberately leaves for you to handle.

[← Back to the README](../README.md)

---

## Supported migration paths

| Scenario | Description |
|---|---|
| **Legacy** | Old-style `sources`/`destinations` policies (e.g. `Timeout`, `TrafficPermission`) → `targetRef`/`to`/`from` |
| **Subset** | New `Mesh*` policy types still using `MeshSubset` with `kuma.io/service` or `k8s.kuma.io/service-name` tags → `Dataplane`/`MeshService` |
| **Passthrough** | Already using `MeshService` kind throughout — passed through unchanged |
| **Rules** | New-style `Mesh*` policies with deprecated `from[]` → `rules[]` (Kuma 2.10+) |
| **Mesh** | `Mesh` CRD with embedded observability/passthrough → standalone `MeshMetric`, `MeshTrace`, `MeshAccessLog`, `MeshPassthrough` CRDs |
| **ExternalService** | `ExternalService` → `MeshExternalService` |
| **GW** | `MeshGateway` → `Gateway`, `MeshGatewayInstance` → `GatewayClass`+`MeshGatewayConfig`, `MeshGatewayRoute`/`MeshHTTPRoute`/`MeshTCPRoute` → Gateway API `HTTPRoute`/`TCPRoute` |
| **OPAPolicy** | Kong Mesh `OPAPolicy` → `MeshOPA` (Kong Mesh 2.5+) |

## Deprecated-field warnings (auto-detected, not auto-transformed)

The tool also emits warnings for deprecated fields that require manual action:

- `MeshMetric` `spec.default.sidecar.regex` → `sidecar.profiles.exclude` *(auto-fixed, Kuma 2.7)*
- `MeshService` `spec.ports[].protocol` → `appProtocol` *(auto-fixed, Kuma 2.8)*
- `MeshHealthCheck` `healthyPanicThreshold` moved to `MeshCircuitBreaker` *(warn, Kuma 2.10)*
- `MeshTrafficPermission` `spec.*.spiffeId` → `spiffeID` casing *(auto-fixed, Kuma 2.12)*
- `MeshLoadBalancingStrategy` `loadBalancer.{ringHash,maglev}.hashPolicies` → `to[].default.hashPolicies` *(auto-fixed, Kuma 2.12)*
- `MeshTrust` `spec.origin` deprecated → `status.origin` *(warn, Kuma 2.13)*
- `MeshTrafficPermission`/`MeshFaultInjection` `from[].targetRef.kind: MeshService` deprecated *(warn, Kuma 2.7)*
- `MeshTrafficPermission` `action: ALLOW/DENY` uppercase casing → `Allow`/`Deny` *(warn, Kong Mesh 2.1)*
- `MeshLoadBalancingStrategy` `hashPolicies[].type: SourceIP` → `Connection` *(warn, Kuma 2.10)*
- `Dataplane` `transparentProxying.redirectPortInboundV6` removed *(warn, Kuma 2.9)*
- `Dataplane` `transparentProxying.reachableServices` → MeshService display names / `reachableBackends` *(warn, Kuma 2.10)*
- `MeshMetric`/`MeshTrace`/`MeshAccessLog` inline `openTelemetry.endpoint` → `MeshOpenTelemetryBackend` + `backendRef` *(warn, deprecated 2.14, removed 3.0)*
- `MeshAccessLog` `openTelemetry.attributes[].key` stricter validation (reserved `otel.` prefix, casing, placeholders) *(warn, Kuma 2.14)*
- `Mesh` `spec.routing.defaultForbidMeshExternalServiceAccess` removed *(warn, Kuma 3.0)*
- `Mesh` `spec.mtls.backends` → `MeshIdentity` + `MeshTrust` successor model *(advisory only — guided CA cutover, not a transform; `spec.mtls` is not deprecated)*
- `Mesh` with no `spec.meshServices` block → 3.0 defaults `meshServices.mode` to `Exclusive` (restricts outbound reachability) *(advisory only — set the mode explicitly before upgrading to 3.0)*
- `MeshTrafficPermission`/`MeshFaultInjection` `from[]` deprecated → `rules[]` API *(warn — manual, MFI 2.13 / MTP 2.14)*
- Deprecated top-level `spec.targetRef.kind`: `MeshSubset`/`MeshService`/`MeshServiceSubset` → `Dataplane`; `MeshHTTPRoute` → `spec.to[].targetRef` *(warn, Kuma 2.10/2.11)*
- `kuma.io/*` annotation values `"yes"`/`"no"` → `"true"`/`"false"` *(scanner, Kuma 2.9)*
- Legacy `kuma.io/service`-encoded addresses in Deployment/StatefulSet env vars *(scanner)*
- RFC 1035/1123 name validation for `Mesh*Service` resources — hard error in 3.0 *(warn)*

For why `MeshTrafficPermission`'s `from[]` is never auto-converted, see [MeshTrafficPermission modes](meshtrafficpermission-modes.md).

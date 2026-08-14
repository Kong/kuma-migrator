# Migration paths and deprecation warnings

Every transformation `kuma-migrator` performs, and every deprecated field it detects but deliberately leaves for you to handle.

[← Back to the README](../README.md)

---

## Supported migration paths

| Scenario | Description |
|---|---|
| **Legacy** | Old-style `sources`/`destinations`/`selectors` policies (e.g. `Timeout`, `TrafficPermission`) → `targetRef`/`to`/`from`/`rules`/`default` — see [below](#legacy-policies-in-detail) |
| **Subset** | New `Mesh*` policy types still using `MeshSubset` with `kuma.io/service` or `k8s.kuma.io/service-name` tags → `Dataplane`/`MeshService` |
| **Passthrough** | Already using `MeshService` kind throughout — passed through unchanged |
| **Rules** | New-style `Mesh*` policies with deprecated `from[]` → `rules[]` (Kuma 2.10+) |
| **Mesh** | `Mesh` CRD with embedded observability/passthrough → standalone `MeshMetric`, `MeshTrace`, `MeshAccessLog`, `MeshPassthrough` CRDs |
| **ExternalService** | `ExternalService` → `MeshExternalService` |
| **GW** | `MeshGateway` → `Gateway`, `MeshGatewayInstance` → `GatewayClass`+`MeshGatewayConfig` *(v2 only — see below)*, `MeshGatewayRoute`/`MeshHTTPRoute`/`MeshTCPRoute` → Gateway API `HTTPRoute`/`TCPRoute` |
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
- `Mesh` with no `spec.meshServices` block → 3.0 removes the `meshServices` field entirely and behaves as `Exclusive` unconditionally (restricts outbound reachability) *(advisory only — set the mode explicitly before upgrading to 3.0)*
- `MeshTrafficPermission`/`MeshFaultInjection` `from[]` deprecated → `rules[]` API *(warn — manual, MFI 2.13 / MTP 2.14)*
- Deprecated top-level `spec.targetRef.kind`: `MeshSubset`/`MeshService`/`MeshServiceSubset` → `Dataplane`; `MeshHTTPRoute` → `spec.to[].targetRef` *(warn, Kuma 2.10/2.11)*
- `kuma.io/*` annotation values `"yes"`/`"no"` → `"true"`/`"false"` *(scanner, Kuma 2.9)*
- Legacy `kuma.io/service`-encoded addresses in Deployment/StatefulSet env vars *(scanner)*
- RFC 1035/1123 name validation for `Mesh*Service` resources — hard error in 3.0 *(warn)*

For why `MeshTrafficPermission`'s `from[]` is never auto-converted, see [MeshTrafficPermission modes](meshtrafficpermission-modes.md).

---

## Legacy policies in detail

The 12 legacy (non-`Mesh*`) policy resources are the set Kuma 3.0 removes outright.
Applying any of them to a 3.0 control plane fails — the CRDs are no longer installed.

| Legacy kind | Becomes | Notes |
|---|---|---|
| `Timeout` | `MeshTimeout` | `connectTimeout` → `connectionTimeout`; the `grpc` section folds into `http` |
| `CircuitBreaker` | `MeshCircuitBreaker` | `thresholds` → `connectionLimits`; the rest moves under `outlierDetection` and every detector is renamed |
| `Retry` | `MeshRetry` | `retryOn` values are recased; `retriableMethods` folds into `retryOn`; **`retriableStatusCodes` has no equivalent and is dropped** |
| `HealthCheck` | `MeshHealthCheck` | `http.requestHeadersToAdd` splits into `{add, set}` |
| `FaultInjection` | `MeshFaultInjection` | **inbound policy** — `destinations` become `spec.targetRef`, `sources` become `from[]` |
| `RateLimit` | `MeshRateLimit` | **inbound policy**, emitted as `rules[]`. A specific `sources` selector cannot be preserved — the limit applies to every client |
| `TrafficPermission` | `MeshTrafficPermission` | **inbound policy** |
| `TrafficLog` | `MeshAccessLog` | `conf.backend` names a backend on the `Mesh`; it is resolved and inlined |
| `TrafficTrace` | `MeshTrace` | scoped by `selectors[]`; `conf.backend` resolved and inlined, along with its sampling rate |
| `ProxyTemplate` | `MeshProxyPatch` | scoped by `selectors[]`; `imports` and `resources` have no equivalent |
| `TrafficRoute` | — | **manual**: ambiguous between `MeshHTTPRoute` and `MeshTCPRoute` |
| `VirtualOutbound` | — | **manual**: see below |

Two things to know about these conversions:

- **`conf` bodies are rewritten, not copied.** No legacy `conf` is structurally
  compatible with the `default` section of its successor. Any field with no
  equivalent is reported rather than dropped quietly.
- **`TrafficLog` and `TrafficTrace` need the `Mesh` resource.** They reference a
  backend by name that their successors declare inline. Keep the `Mesh` document in
  the input directory; without it the tool warns and names the backend you have to
  write by hand.

### `VirtualOutbound`

`VirtualOutbound` renders a hostname *and* a port from arbitrary Dataplane tags, and
allocates a VIP per result. Nothing reproduces that in one resource:

- `HostnameGenerator` covers the hostname, but selects `MeshService`/
  `MeshExternalService`/`MeshMultiZoneService` by label (not Dataplanes by tag), its
  template sees only `.Name`, `.DisplayName`, `.Namespace`, `.Mesh`, `.Zone` and a
  `label` function, and **it cannot template a port**.
- `MeshHTTPRoute`/`MeshTCPRoute` cover the routing half, which is what upstream's
  3.0 upgrade notes point to.

The tool reports it as needing manual migration and leaves the original document in
the output directory.

### Built-in gateways and `--to-latest v3`

Kuma 3.0 removes the built-in gateway API in full — `MeshGateway`, `MeshGatewayRoute`,
`MeshGatewayInstance` and `MeshGatewayConfig`, including their CRDs — and reduces the
Gateway API integration to `HTTPRoute` alone. There is no `Gateway` or `GatewayClass`
reconciler left, and the control plane strips finalizers from Kuma-controlled
`GatewayClass` objects on startup.

`MeshGatewayInstance` therefore has **no successor on 3.0**. Under `--to-latest v3` the tool
reports it instead of converting it, naming the settings to carry over (`replicas`,
`serviceType`, `tags`). The replacement is a *delegated* gateway: a `Deployment` and
`Service` you manage, with the pod labelled `kuma.io/gateway: enabled` so Kuma injects a
sidecar. That needs a container image and pod spec the original manifest does not carry,
so it cannot be generated for you.

Under `--to-latest v2` the 2.x `GatewayClass` + `MeshGatewayConfig` output is unchanged.

Route conversion (`MeshGatewayRoute`/`MeshHTTPRoute` → `HTTPRoute`) stays correct on 3.0.

### `ContainerPatch`

`ContainerPatch` is **not** a legacy policy: it is a Kubernetes-only JSON patch for
the injected sidecar and init containers, it has no `targetRef` successor, and Kuma
3.0 still serves it. The tool passes it through unchanged — no action needed.

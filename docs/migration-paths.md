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
| **Rules** | New-style `Mesh*` policies with deprecated `from[]` → `rules[]` (Kuma 2.10+) — only for `MeshTimeout`/`MeshCircuitBreaker`/`MeshRateLimit`/`MeshAccessLog`/`MeshTLS`. `MeshTrafficPermission`/`MeshFaultInjection` use a different, SPIFFE-identity-based `rules[]` shape and are **not** auto-converted — see [MeshTrafficPermission modes](meshtrafficpermission-modes.md) |
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
- `HostnameGenerator` `spec.template` rendering to an invalid RFC 1123 DNS subdomain (leading/trailing/consecutive dots, uppercase) — accepted silently before Kuma 2.14 *(warn, Kuma 2.14)*
- `MeshPassthrough` `spec.default.appendMatch[]` partial wildcards, `type: Domain` on `tcp`/`mysql`, and wildcard domains on an L7 protocol with no port *(warn, Kuma 2.14)*
- Deprecated Pod annotations, exact-match: `prometheus.metrics.kuma.io/port`/`/path` (use `MeshMetric`); `kuma.io/virtual-probes`/`-port` (replaced by the Application Probe Proxy); `kuma.io/builtindns`/`builtindnsport` (**silently ignored**, not just deprecated — use `kuma.io/builtin-dns`/`-port`); `kuma.io/sidecar-injection` (must be a **label**, has no effect as an annotation) *(warn)*
- `MeshExternalService` `spec.tls.verification.{caCert,clientCert,clientKey}.{inline,inlineString,secret}` → `SecureDataSource` (`type` + `insecureInline.value`/`secretRef`) — **not auto-converted even under v3**, since `inline` is base64 credential material and rewriting it means decoding and re-emitting it in the clear *(warn, removed 3.0)*
- Kong Mesh `MeshOPA` `spec.targetRef.{name,namespace,mesh}` — removed in 3.0; `name` is **auto-converted** to a display-name label under `--to-latest v3` (dropping it instead would silently widen the policy to every service matching `kind`) *(warn under v2, auto-fixed under v3)*
- Kong Mesh `MeshOPA` `spec.targetRef.kind: MeshService` — no longer valid in 3.0 (only `Mesh`/`Dataplane` remain); **auto-converted** to `kind: Dataplane` with the display-name label renamed to `labels["app"]` under `--to-latest v3` *(warn under v2, auto-fixed under v3)*
- Kong Mesh `MeshOPA` `spec.default.agentConfig`/`appendPolicies[].rego` legacy flat `DataSource` (`secret`/`inline`/`inlineString`) → `SecureDataSource` — rejected at write time on 3.0; **auto-converted** under `--to-latest v3` (unlike the `MeshExternalService` case above, rego source and OPA agent config are not credential material, so decoding `inline` here is safe) *(warn under v2, auto-fixed under v3)*
- Kong Mesh `MeshGlobalRateLimit` — removed in 3.0 with no in-mesh replacement; leftover objects go **inert** rather than being rejected *(warn, removed 3.0)*
- `Dataplane` `networking.inbound[].tags` — removed **silently** in 3.0 (the field is `reserved` in the proto), so a manifest still setting it applies cleanly and simply loses the tags *(warn under `--to-latest v3` only — inbound tags are mandatory in 2.x, so a v2 advisory would fire on every Dataplane with nothing actionable)*
- `Dataplane` `networking.gateway.type: BUILTIN` — the `GatewayType` ordinal is `reserved` in the proto on 3.0, rejected at parse time, not just admission *(warn under `--to-latest v3` only — `DELEGATED`/`BUILTIN` are both valid in 2.x)*
- `MeshLoadBalancingStrategy` `to[].default.localityAwareness.crossZone` set on a `to[]` entry whose `targetRef.kind` is not `MeshMultiZoneService` — new 3.0-dev validator restriction *(warn under `--to-latest v3` only — not yet enforced in the 2.14 line)*
- `MeshHTTPRoute`/`HTTPRoute` with no catch-all rule (empty `matches[]`, or an unconditional `PathPrefix: "/"`) — unmatched requests now block instead of falling through on 3.0, easy to hit by accident when a route exists only to anchor another policy *(advisory under `--to-latest v3` only, surfaced on the converted `HTTPRoute` output)*

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
Gateway API integration to `HTTPRoute` and, since kuma#18280, `GRPCRoute`. There is no
`Gateway` or `GatewayClass` reconciler left, and the control plane strips finalizers from
Kuma-controlled `GatewayClass` objects on startup.

`MeshGatewayInstance` therefore has **no successor on 3.0**. Under `--to-latest v3` the tool
reports it instead of converting it, naming the settings to carry over (`replicas`,
`serviceType`, `tags`). The replacement is a *delegated* gateway: a `Deployment` and
`Service` you manage, with the pod labelled `kuma.io/gateway: enabled` so Kuma injects a
sidecar. That needs a container image and pod spec the original manifest does not carry,
so it cannot be generated for you.

Under `--to-latest v2` the 2.x `GatewayClass` + `MeshGatewayConfig` output is unchanged.

`MeshGateway` itself **is** still converted on both targets — its listener block (ports,
protocols, hostnames, TLS certificate references) is valid Gateway API on 3.0 and is the
part worth automating. Only `spec.gatewayClassName` differs:

- **v2** — resolved to the `GatewayClass` generated from the companion `MeshGatewayInstance`,
  matched through the `kuma.io/service` tag the two share. Keep both documents in the input
  directory so the link can be made.
- **v3** — no Kuma `GatewayClass` exists, so it is left as
  `REPLACE-WITH-YOUR-GATEWAYCLASS` with a warning. Point it at the gateway implementation you
  adopt.

If the class cannot be determined on either target you get the same placeholder and a warning,
rather than a plausible-looking value that would leave the `Gateway` sitting unreconciled.

Route conversion (`MeshGatewayRoute`/`MeshHTTPRoute` → `HTTPRoute`) stays correct on 3.0.

### `ContainerPatch`

`ContainerPatch` is **not** a legacy policy: it is a Kubernetes-only JSON patch for
the injected sidecar and init containers, it has no `targetRef` successor, and Kuma
3.0 still serves it. The tool passes it through unchanged — no action needed.

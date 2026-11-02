---
title: Legacy policies
description: The twelve source/destination policies Kuma 3.0 removes, how kuma-migrator maps their selectors and conf to the Mesh* successors, and the two it leaves to you.
order: 120
---

Before the targetRef policies, Kuma configured traffic with twelve policies that select proxies
by tag with `sources`, `destinations` or `selectors`, and carry their settings in `conf`. They
stopped affecting Envoy configuration during the 2.x line, and 3.0 removes them from the resource
registry: CRDs, API, `kumactl` and KDS sync
([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#legacy-policy-resources-removed)).
Stored objects of these types are not migrated, so they must be converted and deleted before the
upgrade.

| Legacy | Successor |
|---|---|
| [Timeout](policy:Timeout) | [MeshTimeout](policy:MeshTimeout) |
| [CircuitBreaker](policy:CircuitBreaker) | [MeshCircuitBreaker](policy:MeshCircuitBreaker) |
| [Retry](policy:Retry) | [MeshRetry](policy:MeshRetry) |
| [HealthCheck](policy:HealthCheck) | [MeshHealthCheck](policy:MeshHealthCheck) |
| [TrafficLog](policy:TrafficLog) | [MeshAccessLog](policy:MeshAccessLog) |
| [TrafficPermission](policy:TrafficPermission) | [MeshTrafficPermission](policy:MeshTrafficPermission) |
| [FaultInjection](policy:FaultInjection) | [MeshFaultInjection](policy:MeshFaultInjection) |
| [RateLimit](policy:RateLimit) | [MeshRateLimit](policy:MeshRateLimit) |
| [TrafficTrace](policy:TrafficTrace) | [MeshTrace](policy:MeshTrace) |
| [ProxyTemplate](policy:ProxyTemplate) | [MeshProxyPatch](policy:MeshProxyPatch) |
| [TrafficRoute](policy:TrafficRoute) | [MeshHTTPRoute](policy:MeshHTTPRoute) or [MeshTCPRoute](policy:MeshTCPRoute) — manual |
| [VirtualOutbound](policy:VirtualOutbound) | No single successor — manual |

[ExternalService](policy:ExternalService) is also removed in 3.0 and has its own page:
[External services](external-services.md). `ContainerPatch` is not a legacy policy: it has no
targetRef successor, 3.0 still serves it, and the tool copies it unchanged.

The upstream guide is [Migration to the new policies](https://kuma.io/docs/latest/guides/migration-to-the-new-policies/).

## Selector shapes

The legacy selectors do not map the same way for every kind. Getting the direction wrong attaches
the policy to the wrong proxy.

| Shape | Kinds | Mapping |
|---|---|---|
| Outbound | `Timeout`, `CircuitBreaker`, `Retry`, `HealthCheck`, `TrafficLog` | `sources` → `spec.targetRef`; `destinations` → `to[].targetRef` |
| Inbound | `TrafficPermission`, `FaultInjection`, `RateLimit` | `destinations` → `spec.targetRef`; `sources` → `from[]` (or `rules[]`) |
| Selector | `TrafficTrace`, `ProxyTemplate` | `selectors` → `spec.targetRef`; `conf` → `spec.default` (no `to[]`/`from[]`) |

`FaultInjection` and `RateLimit` are enforced by the destination's sidecar, like
`TrafficPermission`.

Selector values:

| Legacy match | New reference |
|---|---|
| `kuma.io/service: '*'` | `kind: Mesh` |
| `kuma.io/service: backend_demo_svc_3001` (top level) | `kind: Dataplane` (see below) |
| `kuma.io/service: backend_demo_svc_3001` (`to[]`/`from[]`) | `kind: MeshService`, `name: backend`, `namespace: demo` (v2) or `labels` (v3) |
| Service tag plus other tags | Other tags kept as `labels` |
| Empty `destinations` | Wildcard, so the conf keeps a `to[]` entry |
| Several specific `sources` | One output policy per source, named `<name>-0`, `<name>-1`, … |

## Conf mapping

No legacy `conf` is structurally compatible with the successor's `default`. A verbatim copy
applies cleanly and does nothing. Each kind has its own converter
([`legacyconf.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/legacyconf.go)),
and any input field no converter consumes is reported.

| Legacy | Notable changes |
|---|---|
| `Timeout` | `connectTimeout` → `connectionTimeout`; `tcp.idleTimeout` → `idleTimeout`; no `grpc` section (folds into `http`) |
| `CircuitBreaker` | `thresholds.*` → `connectionLimits.*`; the rest under `outlierDetection`; detectors renamed (`totalErrors` → `totalFailures`, `gatewayErrors` → `gatewayFailures`, `localErrors` → `localOriginFailures`, `standardDeviation` → `successRate`, `failure` → `failurePercentage`) |
| `Retry` | `tcp.maxConnectAttempts` → `maxConnectAttempt`; `retryOn` values recased (`gateway_error` → `GatewayError`); `retriableMethods` → `retryOn: [HttpMethodGet, …]`; gRPC `cancelled` → `Canceled`; `retriableStatusCodes` has no equivalent (dropped, warned) |
| `HealthCheck` | `http.requestHeadersToAdd` → `{add, set}` (`append` unset means `add`); `tcp.send`/`receive` stay base64 |
| `FaultInjection` | The single conf becomes a one-element `default.http[]` |
| `RateLimit` | `http.{requests, interval}` → `local.http.requestRate.{num, interval}`; emitted as `rules[]` (a specific `sources` selector is lost and warned) |
| `TrafficLog` | `conf.backend` resolved against `Mesh.logging.backends` and inlined as `default.backends[]` |
| `TrafficTrace` | Backend resolved against `Mesh.tracing.backends`; its `sampling` → `default.sampling.overall` |
| `ProxyTemplate` | `modifications` → `appendModifications`; operations recased; `httpFilter`/`networkFilter`/`virtualHost` `add` → `AddLast` (warned); `imports` and `resources` have no equivalent |
| `TrafficPermission` | No conf; one `from[]` entry with `action: Allow` per source |

Percentages stay numbers when whole and become strings when fractional, as the new schemas type
them `anyOf: [integer, string]`.

## What kuma-migrator does

| Input | `--to-latest v2` | `--to-latest v3` |
|---|---|---|
| Outbound-shape kinds | Converted. Top-level specific service → `Dataplane` `name`/`namespace`; destinations → `MeshService` `name`/`namespace` | Converted. Top-level → `Dataplane` `labels: {app, k8s.kuma.io/namespace}` (warned as a best guess); destinations → `MeshService` labels. Delete-before-upgrade advisory |
| `HealthCheck` with `healthyPanicThreshold` | Kept on `MeshHealthCheck`, warned | Moved to a companion `MeshCircuitBreaker` `<name>-panic-threshold` with `outlierDetection.healthyPanicThreshold` and `detectors.totalFailures.consecutive: 5` |
| `TrafficPermission` | `MeshTrafficPermission` with `from[]` | **Error**, original to `_manual/`. See [Access control](access-control.md) |
| `FaultInjection` | `MeshFaultInjection` with `from[]` | `rules[]` when every source is `kuma.io/service: '*'`; error otherwise |
| `RateLimit` | `MeshRateLimit` with `rules[]` | Same |
| `TrafficLog`, `TrafficTrace` | Converted with the backend from the `Mesh` in the input; warning naming the backend if the `Mesh` is missing | Same |
| `ProxyTemplate` | `MeshProxyPatch` | Same; warning when it patches cluster `circuitBreakers` (on 3.0 it overrides `MeshCircuitBreaker`) |
| Selector with no service tag in a destination | `to[].targetRef` `kind: MeshSubset` with the tags | Error: 3.0 accepts no tag selector in `to[]` |
| `TrafficRoute` | Error; original copied to the output tree | Error; original to `_manual/` |
| `VirtualOutbound` | Error; original copied to the output tree | Error; original to `_manual/` |

Code: [`transform.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/transform.go)
(`legacyShapes`), [`legacyconf.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/legacyconf.go),
[`legacybackend.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/legacybackend.go).

> **Warning:** Under `v2`, a top-level `kuma.io/service` selector becomes
> `kind: Dataplane` with `name` and `namespace`. A `Dataplane` `name` selects one `Dataplane`
> resource by name, and on Kubernetes a `Dataplane` is named after its pod
> (`web-6d4f8c-xk2pq`), not after the service. Check what it selects, or replace it with `labels`
> before applying. The `v3` output already uses labels.

## What it cannot do

### TrafficRoute

A `TrafficRoute` splits or redirects traffic by tag. Whether its successor is a `MeshHTTPRoute`
or a `MeshTCPRoute` depends on the protocol of the destination, which the policy does not state.
Write the route by hand, targeting `MeshService` backends. Default route-all `TrafficRoute`s
created by older control planes can be deleted; 3.0 no longer creates them
([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#default-trafficpermissiontrafficroute-creation-removed)).

### VirtualOutbound

`VirtualOutbound` renders a hostname **and** a port from arbitrary `Dataplane` tags, and allocates
a VIP per result. No single resource does that:

| | `VirtualOutbound` | `HostnameGenerator` |
|---|---|---|
| Selects | `Dataplane`s by tags | `MeshService`/`MeshExternalService`/`MeshMultiZoneService` by label selector |
| Template variables | Any `Dataplane` tag via `parameters[]` | `.Name`, `.DisplayName`, `.Namespace`, `.Mesh`, `.Zone` and a `label` function |
| Port | Templatable (`conf.port`) | Not templatable; ports come from the `MeshService` |
| Result | VIP + hostname | Hostname in the resource's `status.addresses` |

`HostnameGenerator` covers the hostname half
([docs](https://kuma.io/docs/latest/networking/hostnamegenerator/)). For the routing half,
`UPGRADE.md` points to `MeshHTTPRoute`/`MeshTCPRoute`
([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#virtualoutbound-no-longer-affects-generated-envoy-config)).

### Other gaps

- **Source scope on inbound policies.** `MeshRateLimit` `rules[]` and, on 3.0,
  `MeshFaultInjection` and `MeshTrafficPermission` match clients by SPIFFE ID or not at all. A
  specific legacy `sources` selector cannot be carried over.
- **Retry on status codes.** `MeshRetry` cannot retry on specific status codes.
- **Pod labels.** The top-level `app: <name>` label under `v3` is a guess.
- **`ProxyTemplate` `imports` and `resources`.** No equivalent in `MeshProxyPatch`.

## Manual steps

1. Put the `Mesh` resources in the input directory (needed for `TrafficLog`/`TrafficTrace`).
2. Run `migrate --dry-run --to-latest v2` and read every unmapped-field warning.
3. Apply the converted policies on 2.x and check proxy configuration
   (`kumactl inspect dataplane <name> --type=config`).
4. Write `TrafficRoute` and `VirtualOutbound` replacements by hand.
5. Delete the legacy objects. 3.0 does not migrate stored objects of these types.
6. Before the 3.0 upgrade, re-run with `--to-latest v3`: legacy `TrafficPermission` needs the
   SPIFFE `rules[]` work in [Access control](access-control.md), and `HealthCheck` panic
   thresholds move to `MeshCircuitBreaker`.

## Example

Input — three Universal files:

```yaml
type: Timeout
mesh: default
name: backend-timeout
sources:
  - match:
      kuma.io/service: '*'
destinations:
  - match:
      kuma.io/service: backend_demo_svc_3001
conf:
  connectTimeout: 5s
  tcp:
    idleTimeout: 1h
  http:
    requestTimeout: 15s
---
type: Retry
mesh: default
name: web-retry
sources:
  - match:
      kuma.io/service: web_demo_svc_8080
destinations:
  - match:
      kuma.io/service: backend_demo_svc_3001
conf:
  http:
    numRetries: 3
    retryOn: [gateway_error, connect_failure]
    retriableStatusCodes: [503]
  tcp:
    maxConnectAttempts: 4
---
type: TrafficRoute
mesh: default
name: route-all
sources:
  - match:
      kuma.io/service: '*'
destinations:
  - match:
      kuma.io/service: '*'
conf:
  destination:
    kuma.io/service: '*'
```

Output with `--to-latest v2`:

```yaml
# resiliency/MeshTimeout-backend-timeout.yaml
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  labels:
    kuma.io/mesh: default
  name: backend-timeout
spec:
  targetRef:
    kind: Mesh
  to:
  - default:
      connectionTimeout: 5s
      http:
        requestTimeout: 15s
      idleTimeout: 1h
    targetRef:
      kind: MeshService
      name: backend
      namespace: demo
```

```yaml
# resiliency/MeshRetry-web-retry.yaml
apiVersion: kuma.io/v1alpha1
kind: MeshRetry
metadata:
  labels:
    kuma.io/mesh: default
  name: web-retry
spec:
  targetRef:
    kind: Dataplane
    name: web
    namespace: demo
  to:
  - default:
      http:
        numRetries: 3
        retryOn:
        - GatewayError
        - ConnectFailure
      tcp:
        maxConnectAttempt: 4
    targetRef:
      kind: MeshService
      name: backend
      namespace: demo
```

The `TrafficRoute` is copied to `routing/TrafficRoute-route-all.yaml` unchanged, with an error:

```
⚠  Retry "web-retry": conf.http.retriableStatusCodes ([503]) has no MeshRetry equivalent and was
   dropped — MeshRetry cannot retry on specific status codes. …
✗  TrafficRoute requires manual migration to MeshHTTPRoute or MeshTCPRoute
```

With `--to-latest v3` the selectors use labels, the `TrafficRoute` goes to `_manual/`, and every
converted source gets a delete-before-upgrade advisory:

```yaml
# resiliency/MeshRetry-web-retry.yaml (v3)
apiVersion: kuma.io/v1alpha1
kind: MeshRetry
metadata:
  labels:
    kuma.io/mesh: default
  name: web-retry
spec:
  targetRef:
    kind: Dataplane
    labels:
      app: web
      k8s.kuma.io/namespace: demo
  to:
  - default:
      http:
        numRetries: 3
        retryOn:
        - GatewayError
        - ConnectFailure
      tcp:
        maxConnectAttempt: 4
    targetRef:
      kind: MeshService
      labels:
        k8s.kuma.io/namespace: demo
        kuma.io/display-name: backend
```

A `HealthCheck` with `healthyPanicThreshold: 60` under `--to-latest v3` produces the
`MeshHealthCheck` without the field and this companion:

```yaml
# resiliency/MeshCircuitBreaker-backend-hc-panic-threshold.yaml
apiVersion: kuma.io/v1alpha1
kind: MeshCircuitBreaker
metadata:
  labels:
    kuma.io/mesh: default
  name: backend-hc-panic-threshold
spec:
  targetRef:
    kind: Mesh
  to:
  - default:
      outlierDetection:
        detectors:
          totalFailures:
            consecutive: 5
        healthyPanicThreshold: 60
    targetRef:
      kind: MeshService
      labels:
        k8s.kuma.io/namespace: demo
        kuma.io/display-name: backend
```

3.0 reads the threshold only from an enabled outlier detection, which needs a detector, so the
companion turns on outlier ejection for these destinations. If a `MeshCircuitBreaker` already
covers them, move the threshold into it instead
([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#healthypanicthreshold-removed-from-meshhealthcheck)).

## Related

- [Policy targeting](policy-targeting.md)
- [Access control](access-control.md) — `TrafficPermission`, `FaultInjection`
- [Observability](observability.md) — `TrafficLog`, `TrafficTrace`
- [Choosing a target version](../guides/target-version.md)

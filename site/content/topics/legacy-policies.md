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
| `kuma.io/service: backend_demo_svc_3001` (top level) | `kind: Dataplane`, `labels: {app: backend, k8s.kuma.io/namespace: demo}` (see below) |
| `kuma.io/service: backend_demo_svc_3001` (`to[]`) | `kind: MeshService`, `name: backend`, `namespace: demo` (v2) or `labels` (v3) |
| `kuma.io/service: web_demo_svc_8080` (`from[]` of `TrafficPermission`/`FaultInjection`, v2) | `kind: MeshSubset`, `tags: {kuma.io/service: web_demo_svc_8080}` |
| Service tag plus other tags | Other tags kept as `labels`, except Kuma-reserved tags that are not labels (`kuma.io/protocol`, `kuma.io/instance`, `k8s.kuma.io/service-port`, …), which are dropped with a warning |
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
| `HealthCheck` | `http.requestHeadersToAdd` → `{add, set}` (`append` unset means `add`); header names lower-cased (warned); `tcp.send`/`receive` stay base64 |
| `FaultInjection` | The single conf becomes a one-element `default.http[]`; `responseBandwidth.limit` units rewritten to `kbps`/`Mbps`/`Gbps` (`50 mbps` → `50Mbps`, warned) |
| `RateLimit` | `http.{requests, interval}` → `local.http.requestRate.{num, interval}`; `onRateLimit.headers` names lower-cased (warned); emitted as `rules[]` (a specific `sources` selector is lost and warned) |
| `TrafficLog` | `conf.backend` resolved against `Mesh.logging.backends` and inlined as `default.backends[]` |
| `TrafficTrace` | Backend resolved against `Mesh.tracing.backends`; its `sampling` → `default.sampling.overall` |
| `ProxyTemplate` | `modifications` → `appendModifications`; operations recased (`addLast` → `AddLast`, `patch` → `Patch`, …); `httpFilter`/`networkFilter` `add` → `AddLast` (warned); `imports` and `resources` have no equivalent |
| `TrafficPermission` | No conf; one `from[]` entry with `action: Allow` per source |

Percentages stay numbers when whole and become strings when fractional, as the new schemas type
them `anyOf: [integer, string]`.

Three value formats are stricter on the new kinds, and the converters rewrite them:

| Field | Legacy accepts | New kind requires | Rewrite |
|---|---|---|---|
| `ProxyTemplate` `operation` | `add`, `addFirst`, `addLast`, `addBefore`, `addAfter`, `patch`, `remove` | PascalCase enum: `Add`/`Remove`/`Patch` on `cluster`, `listener`, `virtualHost`; `AddFirst`/`AddBefore`/`AddAfter`/`AddLast`/`Remove`/`Patch` on `httpFilter`, `networkFilter` | Recased. A filter `add` becomes `AddLast` (warned) |
| Header names (`HealthCheck`, `RateLimit`) | any case | Lower-case RFC 7230 token characters only ([`header.go`](https://github.com/kumahq/kuma/blob/master/api/common/v1alpha1/header.go)) | Lower-cased (HTTP header names are case-insensitive), warned. A name with a character no header can have is warned as rejected |
| `FaultInjection` `responseBandwidth.limit` | `50 mbps` (lower-case `kbps`/`mbps`/`gbps`) | `kbps`, `Mbps` or `Gbps` | `50 mbps` → `50Mbps`, warned. A value that is not `<integer><unit>` is copied and warned |

## Namespace on Kubernetes

The legacy CRDs are cluster-scoped; their successors are namespaced. A converted policy is written
with `metadata.namespace` set to the control-plane namespace: the `systemNamespace` config value,
else the namespace other input documents show (`kong-mesh-system` or `kuma-system`), else
`kuma-system` with a warning. In that namespace the control plane always computes
`kuma.io/policy-role: system`, so the policy applies mesh-wide as the legacy one did. Without a
namespace, `kubectl apply` stores it in the current namespace, where it gets a
`workload-owner`/`consumer` role and only selects that namespace's proxies
([`compute.go`](https://github.com/kumahq/kuma/blob/master/pkg/core/resources/labels/compute.go)).
The same applies to `OPAPolicy` → `MeshOPA` and to a `MeshSubset` policy written without a
namespace.

## What kuma-migrator does

| Input | `--to-latest v2` | `--to-latest v3` |
|---|---|---|
| Outbound-shape kinds | Converted. Top-level specific service → `Dataplane` `labels: {app, k8s.kuma.io/namespace}` (warned as a best guess); destinations → `MeshService` `name`/`namespace` | Converted. Top-level → `Dataplane` labels, as for v2; destinations → `MeshService` labels. Delete-before-upgrade advisory |
| `HealthCheck` with `healthyPanicThreshold` | Kept on `MeshHealthCheck`, warned | Moved to a companion `MeshCircuitBreaker` `<name>-panic-threshold` with `outlierDetection.healthyPanicThreshold` and `detectors.totalFailures.consecutive: 5` |
| `TrafficPermission` | `MeshTrafficPermission` with `from[]` of `MeshSubset` source tags | **Error**, original to `_manual/`. See [Access control](access-control.md) |
| `FaultInjection` | `MeshFaultInjection` with `from[]` of `MeshSubset` source tags | `rules[]` when every source is `kuma.io/service: '*'`; error otherwise |
| `RateLimit` | `MeshRateLimit` with `rules[]` | Same |
| `TrafficLog`, `TrafficTrace` | Converted with the backend from the `Mesh` in the input; warning naming the backend if the `Mesh` is missing | Same |
| `ProxyTemplate` | `MeshProxyPatch` | Same; warning when it patches cluster `circuitBreakers` (on 3.0 it overrides `MeshCircuitBreaker`) |
| Selector with no service tag in a destination | `to[].targetRef` `kind: MeshSubset` with the tags | Error: 3.0 accepts no tag selector in `to[]` |
| `TrafficRoute` | Error; original copied to the output tree | Error; original to `_manual/` |
| `VirtualOutbound` | Error; original copied to the output tree | Error; original to `_manual/` |

Code: [`transform.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/transform.go)
(`legacyShapes`), [`legacyconf.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/legacyconf.go),
[`legacybackend.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/legacybackend.go).

A top-level `kuma.io/service` selector never becomes `Dataplane` `name`/`namespace`: a `Dataplane`
`name` selects one `Dataplane` by name, and on Kubernetes a `Dataplane` is named after its pod
(`web-6d4f8c-xk2pq`), not after the service. Both targets emit `labels`. On Kubernetes a
`Dataplane` carries its pod's labels plus `k8s.kuma.io/namespace`, which the control plane sets;
`app: <service>` is a guess at the pod label, so each conversion warns. On Universal a `Dataplane`
carries only the labels in its own `labels` field. `kind: Dataplane` with `labels` at the top level
is accepted since 2.10
([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#new-kind-dataplane-for-targetref-in-policies)).

`TrafficPermission` and `FaultInjection` sources become `from[]` `kind: MeshSubset` with the
source's tags: the 2.7 upgrade notes deprecate `from[].targetRef.kind: MeshService` for these two
policies and give that form instead
([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#deprecation-of-fromtargetrefkind-meshservice)).

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

Output with `--to-latest v2`. Universal input gives Universal output (`type`, `name`, `mesh`,
`spec`); Kubernetes input gives Kubernetes resources:

```yaml
# resiliency/MeshTimeout-backend-timeout.yaml
mesh: default
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
type: MeshTimeout
```

```yaml
# resiliency/MeshRetry-web-retry.yaml
mesh: default
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
      name: backend
      namespace: demo
type: MeshRetry
```

The `TrafficRoute` is copied unchanged to `_manual/routing/TrafficRoute-route-all.yaml`, which no apply step uses, with an error:

```text
⚠  Retry "web-retry": conf.http.retriableStatusCodes ([503]) has no MeshRetry equivalent and was
   dropped — MeshRetry cannot retry on specific status codes. …
⚠  spec.targetRef Dataplane labels use 'app: web' as a best-guess pod label — verify this matches
   your actual pod labels before applying
✗  TrafficRoute requires manual migration to MeshHTTPRoute or MeshTCPRoute
```

With `--to-latest v3` the `to[]` selectors use labels too, and every
converted source gets a delete-before-upgrade advisory:

```yaml
# resiliency/MeshRetry-web-retry.yaml (v3)
mesh: default
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
type: MeshRetry
```

A `HealthCheck` with `healthyPanicThreshold: 60` under `--to-latest v3` produces the
`MeshHealthCheck` without the field and this companion:

```yaml
# resiliency/MeshCircuitBreaker-backend-hc-panic-threshold.yaml
mesh: default
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
type: MeshCircuitBreaker
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

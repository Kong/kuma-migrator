---
title: Policy targeting
description: Which targetRef kinds each policy accepts at each position on 2.14 and 3.0, labels-only selection, policy roles, and control-plane-owned labels.
order: 100
---

A targetRef policy selects proxies in `spec.targetRef` (top level), destinations in
`spec.to[].targetRef`, and clients in `spec.from[].targetRef` or `spec.rules[]`. Each position
accepts a different set of kinds, and 3.0 narrows all three. It also changes how a real resource
is selected (labels only) and which labels you may set on the policy itself.

## What changed upstream

| Version | Change |
|---|---|
| 2.7 | `from[].targetRef.kind: MeshService` deprecated on `MeshTrafficPermission`/`MeshFaultInjection` ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md)) |
| 2.10 / 2.11 | Top-level `MeshSubset`, `MeshService`, `MeshServiceSubset` and `MeshHTTPRoute` deprecated (`validators.TopLevelTargetRefDeprecations`) |
| 2.10 | `rules[]` API for inbound policies (`MeshTimeout`, `MeshCircuitBreaker`, `MeshRateLimit`, `MeshAccessLog`, `MeshTLS`) |
| 3.0 | Top-level `MeshSubset`, `MeshService`, `MeshServiceSubset` rejected. Use `Mesh` or `Dataplane` + `labels`. Applies to KDS sync too: a policy with a legacy kind can fail to sync into an upgraded zone ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#legacy-top-level-policy-targetref-selector-kinds-removed)) |
| 3.0 | `MeshGateway` is no longer a valid `targetRef.kind` ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#built-in-gateway-api-and-crds-removed)) |
| 3.0 | `from[]` removed from `MeshTimeout`, `MeshCircuitBreaker`, `MeshAccessLog`, `MeshRateLimit`, `MeshTLS`, `MeshTrafficPermission` ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#from-removed-from-meshtimeout)) |
| 3.0 | `Dataplane`, `MeshService`, `MeshExternalService`, `MeshMultiZoneService` and `MeshHTTPRoute` are selected by `labels` only in `spec.targetRef` and `spec.to[].targetRef`; `sectionName` stays ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#real-resource-policy-selection-now-uses-labels-only)) |
| 3.0 | Fields not in a policy's schema are rejected with `400` (`spec.from: unknown field`) by the API server and `kumactl apply`; `kubectl apply` prunes them with a warning ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#resources-with-fields-that-are-not-in-the-schema-are-rejected)) |
| 3.0 | Control-plane-owned labels rejected when they differ from the computed value ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#control-plane-owned-labels-are-rejected-when-they-differ-from-the-computed-value)) |
| 3.0 | `kuma.io/` and `k8s.kuma.io/` are reserved label prefixes; unknown labels under them are rejected ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#reserved-label-prefixes)) |

## Kinds accepted per policy

Extracted from every `pkg/plugins/policies/*/api/v1alpha1/valid*.go` validator on Kuma `master`
(3.0) and, for the last column, at `v2.14.4`. Kong Mesh adds `MeshOPA`.

| Policy | top-level `targetRef` (3.0) | `to[]` (3.0) | additional top-level kinds in 2.14.4 |
|---|---|---|---|
| [MeshAccessLog](policy:MeshAccessLog) | Mesh, Dataplane | Mesh, MeshService, MeshExternalService, MeshMultiZoneService, MeshHTTPRoute | MeshSubset, MeshGateway, MeshService, MeshServiceSubset |
| [MeshCircuitBreaker](policy:MeshCircuitBreaker) | Mesh, Dataplane | Mesh, MeshService, MeshExternalService, MeshMultiZoneService | MeshSubset, MeshService, MeshGateway, MeshServiceSubset |
| [MeshFaultInjection](policy:MeshFaultInjection) | Mesh, Dataplane | **Mesh only** | MeshSubset, MeshGateway, MeshService, MeshServiceSubset |
| [MeshHealthCheck](policy:MeshHealthCheck) | Mesh, Dataplane | Mesh, MeshService, MeshExternalService, MeshMultiZoneService | MeshSubset, MeshGateway, MeshService, MeshServiceSubset |
| [MeshHTTPRoute](policy:MeshHTTPRoute) | Mesh, Dataplane | MeshService, MeshExternalService, MeshMultiZoneService (no Mesh) | MeshGateway, MeshSubset, MeshService, MeshServiceSubset |
| [MeshLoadBalancingStrategy](policy:MeshLoadBalancingStrategy) | Mesh, Dataplane | Mesh, MeshService, MeshExternalService, MeshMultiZoneService, MeshHTTPRoute | MeshSubset, MeshGateway, MeshService, MeshServiceSubset |
| [MeshMetric](policy:MeshMetric) | Mesh, Dataplane | — | MeshSubset, MeshService, MeshServiceSubset, MeshGateway |
| [MeshPassthrough](policy:MeshPassthrough) | Mesh, Dataplane | — | MeshSubset |
| [MeshProxyPatch](policy:MeshProxyPatch) | Mesh, Dataplane | — | MeshSubset, MeshService, MeshServiceSubset, MeshGateway |
| [MeshRateLimit](policy:MeshRateLimit) | Mesh, Dataplane | **Mesh only** | MeshSubset, MeshGateway, MeshService, MeshServiceSubset, MeshHTTPRoute |
| [MeshRetry](policy:MeshRetry) | Mesh, Dataplane | Mesh, MeshService, MeshExternalService, MeshMultiZoneService, MeshHTTPRoute | MeshSubset, MeshGateway, MeshService, MeshServiceSubset, MeshHTTPRoute |
| [MeshTCPRoute](policy:MeshTCPRoute) | Mesh, Dataplane | MeshService, MeshExternalService, MeshMultiZoneService (no Mesh) | MeshSubset, MeshGateway, MeshService, MeshServiceSubset |
| [MeshTimeout](policy:MeshTimeout) | Mesh, Dataplane | Mesh, MeshService, MeshExternalService, MeshMultiZoneService, MeshHTTPRoute | MeshSubset, MeshGateway, MeshService, MeshServiceSubset, MeshHTTPRoute |
| [MeshTLS](policy:MeshTLS) | Mesh, Dataplane | — | MeshSubset |
| [MeshTrace](policy:MeshTrace) | Mesh, Dataplane | — | MeshSubset, MeshGateway, MeshService, MeshServiceSubset |
| [MeshTrafficPermission](policy:MeshTrafficPermission) | Mesh, Dataplane | — (`rules[]`) | MeshSubset, MeshService, MeshServiceSubset |
| [MeshOPA](policy:MeshOPA) (Kong Mesh) | Mesh, Dataplane | — | MeshService |

The top-level rule on 3.0 is uniform: every policy accepts `Mesh` and `Dataplane`, with no
per-policy and no system-role exception. `to[]` has four shapes:

| Shape | Kinds | Policies |
|---|---|---|
| Outbound and route anchor | Mesh, the three service kinds, MeshHTTPRoute | MeshAccessLog, MeshLoadBalancingStrategy, MeshRetry, MeshTimeout |
| Outbound | Mesh, the three service kinds | MeshCircuitBreaker, MeshHealthCheck |
| Mesh only | Mesh | MeshFaultInjection, MeshRateLimit (inbound policies) |
| Route backends | The three service kinds, no Mesh | MeshHTTPRoute, MeshTCPRoute |

No policy accepts `MeshSubset`, `MeshServiceSubset` or `MeshGateway` in `to[]` on 3.0. Route
`backendRefs` accept `MeshService`, `MeshExternalService` and `MeshMultiZoneService`
([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#route-backendref-no-longer-accepts-meshservicesubset)).
`MeshTimeout` and `MeshRetry` still contain a validator branch for a top-level `MeshHTTPRoute`
that no input can reach on 3.0.

### Labels only

On 3.0 a reference to a real resource carries `kind`, `labels` and optionally `sectionName`:

| 2.x | 3.0 |
|---|---|
| `name: backend` | `labels: {kuma.io/display-name: backend}` |
| `namespace: demo` | `labels: {k8s.kuma.io/namespace: demo}` |
| `mesh: default` | `labels: {kuma.io/mesh: default}` |
| `sectionName: http` | `sectionName: http` (unchanged) |

A label selector matches every resource that carries the labels. On 2.14 a selector with only
`kuma.io/display-name` and `k8s.kuma.io/namespace` also matches same-named services synced from
other zones; add `kuma.io/zone` to pin one.

## Policy roles

The control plane sets `kuma.io/policy-role` on every policy. Precedence, low to high:
`system` → `producer` → `consumer` → `workload-owner`
([docs](https://kuma.io/docs/latest/policies/introduction/)).

| Role | When (2.x docs) |
|---|---|
| `system` | Policy in the system namespace (`kuma-system`, `kong-mesh-system`) |
| `workload-owner` | No `spec.to` (only `spec.from` or `rules[]`, or neither) |
| `producer` | `spec.to[].targetRef` names services in the policy's own namespace |
| `consumer` | `spec.to[]` selects services by labels or in another namespace |

A producer policy applies to every client of the service and is synced to other zones; a
consumer policy applies to clients in its own namespace and wins over a producer.

> **v3:** On 3.0 the role is computed from labels. A `to[]` item counts as producer only when it
> selects a `MeshService` or `MeshHTTPRoute` with **exactly three** labels:
> `kuma.io/display-name`, `k8s.kuma.io/namespace` equal to the policy's namespace, and
> `kuma.io/zone` equal to the local zone. A policy mixing producer and consumer items is
> rejected ([`compute.go`](https://github.com/kumahq/kuma/blob/master/pkg/core/resources/labels/compute.go)).
> The `v3` conversion of `{name, namespace}` writes `kuma.io/zone` only when the input policy
> carries a `kuma.io/zone` label (policies extracted from a Zone CP do). For a hand-written
> producer policy, add `kuma.io/zone` to every `to[]` selector or it becomes a consumer policy
> that only applies to clients in its own namespace.

## Control-plane-owned labels

The control plane computes these labels on every write: `kuma.io/origin`, `kuma.io/zone`,
`kuma.io/env`, `kuma.io/mesh`, `kuma.io/display-name`, `kuma.io/policy-role`,
`k8s.kuma.io/namespace`, `k8s.kuma.io/service-account`, `kuma.io/listener-zoneingress`,
`kuma.io/listener-zoneegress`. On 3.0 a supplied value that differs from the computed one is
rejected. Extracted manifests carry the stored values, which become wrong as soon as the migration
renames, splits or moves a policy (a split policy changes role, for example).

## What kuma-migrator does

| Input | `--to-latest v2` | `--to-latest v3` |
|---|---|---|
| Top-level `MeshSubset` with a service tag | `Dataplane` with labels (see [MeshService](meshservice.md)) | Same |
| Top-level `MeshSubset` without a service tag | Warning | Warning: rejected by 3.0 |
| Top-level `MeshService` / `MeshServiceSubset` | Warning | Warning: rejected by 3.0. `name` → labels, kind unchanged |
| Top-level `MeshHTTPRoute` | Warning: move to `to[].targetRef` | Warning |
| Top-level `MeshGateway` | Nothing (valid on 2.14 for system-role policies) | Warning: rejected by 3.0 |
| Any other top-level kind | — | Generic warning |
| `to[]` kind not accepted by that policy on 3.0 (matrix above) | — | Warning naming the accepted kinds |
| `targetRef`, `to[].targetRef`, route `backendRefs` with `name`/`namespace`/`mesh` | Kept | Rewritten to labels; `Dataplane{name}` warned (a Dataplane name is not a label) |
| `from[]` on `MeshTimeout`/`MeshCircuitBreaker`/`MeshRateLimit`/`MeshAccessLog`/`MeshTLS` | `rules[]` (Rules scenario). A source that is not `kind: Mesh` is collapsed, with a warning | Same |
| `rules[]` and `to[]` in one policy | Split into `<name>` and `<name>-outbound` | Same |
| Control-plane-owned labels on the resource's own `metadata.labels` | Kept | Removed (`kuma.io/mesh` stays). Selectors are not touched |

Code: [`v3_policy.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/v3_policy.go)
(`stripControlPlaneLabels`, `fixRealResourceTargetRefsForV3`, `warnV3ToTargetRefKinds`),
[`deprecation.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/deprecation.go)
(`warnDeprecatedTopLevelTargetRef`),
[`rulesapi.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/rulesapi.go).

## What it cannot do

- **Expand a `MeshService` selector into `Dataplane` labels.** A top-level `MeshService` names a
  service; the equivalent `Dataplane` selector is the set of pod labels behind it, which is not in
  the policy. Only Kuma-internal `_svc_` names are expanded (to `app: <name>`, warned).
- **Re-target `MeshGateway` policies.** The delegated gateway's labels depend on how you deploy
  it. See [Gateways](gateways.md).
- **Prove that a label selector selects one resource.** Labels match every resource that
  carries them. Each rewrite is warned.

## Manual steps

1. Run `migrate --dry-run --to-latest v3` and list every top-level `targetRef` warning.
2. On 2.14, rewrite top-level `MeshService`/`MeshServiceSubset`/`MeshSubset` to `Mesh` or
   `Dataplane` + `labels`, and top-level `MeshHTTPRoute` to `to[].targetRef`. Verify coverage in
   every zone before upgrading zone control planes.
3. Fix `to[]` entries the matrix rejects.
4. For producer policies, add `kuma.io/zone` to the generated label selectors.
5. Apply the `v3` output, which strips control-plane-owned labels.

## Example

Input — two files:

```yaml
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: backend-timeouts
  namespace: demo
  labels:
    kuma.io/mesh: default
    kuma.io/origin: zone
    kuma.io/zone: zone-1
    kuma.io/policy-role: producer
    k8s.kuma.io/namespace: demo
spec:
  targetRef:
    kind: MeshService
    name: backend
  to:
    - targetRef:
        kind: MeshService
        name: backend
        sectionName: http
      default:
        http:
          requestTimeout: 10s
---
apiVersion: kuma.io/v1alpha1
kind: MeshCircuitBreaker
metadata:
  name: cb
  namespace: kong-mesh-system
  labels:
    kuma.io/mesh: default
spec:
  targetRef:
    kind: MeshGateway
    name: edge-gateway
  to:
    - targetRef:
        kind: MeshHTTPRoute
        name: some-route
      default:
        connectionLimits:
          maxConnections: 100
```

`--to-latest v2` copies both unchanged; the `MeshTimeout` gets one warning:

```
⚠  MeshTimeout "backend-timeouts": spec.targetRef.kind MeshService is deprecated as a top-level
   target in Kuma 2.10+ — use kind: Dataplane with labels instead.
```

`--to-latest v3`:

```yaml
# resiliency/MeshTimeout-demo-backend-timeouts.yaml
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  labels:
    kuma.io/mesh: default
  name: backend-timeouts
  namespace: demo
spec:
  targetRef:
    kind: MeshService
    labels:
      k8s.kuma.io/namespace: demo
      kuma.io/display-name: backend
      kuma.io/zone: zone-1
  to:
  - default:
      http:
        requestTimeout: 10s
    targetRef:
      kind: MeshService
      labels:
        k8s.kuma.io/namespace: demo
        kuma.io/display-name: backend
        kuma.io/zone: zone-1
      sectionName: http
```

```yaml
# resiliency/MeshCircuitBreaker-kong-mesh-system-cb.yaml
apiVersion: kuma.io/v1alpha1
kind: MeshCircuitBreaker
metadata:
  labels:
    kuma.io/mesh: default
  name: cb
  namespace: kong-mesh-system
spec:
  targetRef:
    kind: MeshGateway
    name: edge-gateway
  to:
  - default:
      connectionLimits:
        maxConnections: 100
    targetRef:
      kind: MeshHTTPRoute
      labels:
        k8s.kuma.io/namespace: kong-mesh-system
        kuma.io/display-name: some-route
```

The control-plane-owned labels are gone and selectors use labels. Both policies are still invalid
on 3.0, and the warnings say why (shortened):

```
⚠  MeshTimeout "backend-timeouts": spec.targetRef.kind MeshService is deprecated as a top-level
   target … Kuma 3.0 rejects it outright: Mesh and Dataplane are the only top-level targetRef kinds
   any policy accepts.
⚠  MeshTimeout "backend-timeouts": removed control-plane-owned labels kuma.io/origin, kuma.io/zone,
   kuma.io/policy-role, k8s.kuma.io/namespace. …
⚠  MeshCircuitBreaker "cb": spec.targetRef.kind MeshGateway is removed in Kuma 3.0 … Re-target the
   policy at the delegated gateway's own proxies (kind: Dataplane with labels).
⚠  MeshCircuitBreaker "cb": spec.targetRef (kind MeshGateway) sets name/namespace/mesh, which Kuma
   3.0 removes from targetRef. Remove them.
⚠  MeshCircuitBreaker "cb": spec.to[0].targetRef.kind MeshHTTPRoute is rejected by Kuma 3.0.
   MeshCircuitBreaker accepts Mesh, MeshExternalService, MeshMultiZoneService, MeshService in to[] …
```

## Related

- [MeshService](meshservice.md) — `kuma.io/service` selectors
- [Gateways, Gateway API and GAMMA](gateways.md)
- [Legacy policies](legacy-policies.md) — `sources`/`destinations` → `targetRef`
- [Access control](access-control.md) — `from[]` vs `rules[]` on `MeshTrafficPermission`

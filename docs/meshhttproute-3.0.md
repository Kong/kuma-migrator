# MeshHTTPRoute and the 3.0 routing changes

Where `MeshHTTPRoute` is heading in Kuma 3.0, what breaks on the way, and what `kuma-migrator` should do about it.

[← Back to the README](../README.md)

---

**Sources.** Everything below was verified against the kong-ama KB clone of `kumahq/kuma`
at `master` (3.0-dev), HEAD `2bdad1995`, 2026-09-18 — `UPGRADE.md` §`Upgrade to 3.0.0`,
the Gateway API controllers under `pkg/plugins/runtime/k8s/controllers/gatewayapi/`, and the
policy validators at `v2.14.4` vs `master`. Anything not verified is marked **unverified**.

## TL;DR

| | Status in 3.0 |
|---|---|
| `MeshHTTPRoute` | **Kept.** It is what Gateway API `HTTPRoute`/`GRPCRoute` compile into |
| `MeshTCPRoute` | **Kept** (policy package present on master) |
| `MeshGateway`, `MeshGatewayRoute`, `MeshGatewayInstance`, `MeshGatewayConfig` | **Removed entirely** — Go/proto types, CRDs and KDS sync |
| Built-in gateway | **Retired.** Delegated gateways you run yourself are the only supported shape |
| Unmatched request on a routed destination | **`404`** instead of falling through |

## 1. MeshHTTPRoute is the compile target, not the casualty

`pkg/plugins/policies/meshhttproute` and `meshtcproute` both exist on master. The Gateway API
reconcilers translate *inward*: `plugin_gateway.go` registers an `HTTPRouteReconciler` and a
`GRPCRouteReconciler`, and both conversions return `&v1alpha1.MeshHTTPRoute{}`
(`http_route_conversion.go:109,192`; `grpc_route_conversion.go:102,155`).

So the 3.0 picture is:

```
Gateway API HTTPRoute / GRPCRoute   ← what users write (GAMMA style, parentRef → Service/MeshService)
            ↓  control plane reconciler
      MeshHTTPRoute                 ← generated; also still hand-writable
            ↓
         Envoy
```

`UPGRADE.md` protects that path explicitly, in the entry that retires the built-in gateway
controllers:

> Gateways you run yourself and the Gateway API `HTTPRoute` GAMMA path are unaffected.

A hand-written `MeshHTTPRoute` remains fully supported on 3.0. **Converting one to a Gateway API
`HTTPRoute` is a modernization choice, not a 3.0 requirement.**

## 2. The built-in gateway API *is* removed

> ### Built-in gateway API and CRDs removed
>
> The built-in gateway API has been removed entirely. The `MeshGateway`,
> `MeshGatewayRoute`, `MeshGatewayInstance`, and `MeshGatewayConfig` resources,
> their Go/proto types, their Kubernetes CRDs
> (`meshgateways.kuma.io`, `meshgatewayroutes.kuma.io`,
> `meshgatewayinstances.kuma.io`, `meshgatewayconfigs.kuma.io`), and KDS sync
> registration for these types no longer exist. `MeshGateway` is also no longer
> a valid `targetRef.kind` for any policy.

**Read the 3.0 notes in order.** The removal lands as three separate entries in the same
release, and reading only the middle one is misleading:

| Order in `UPGRADE.md` | Entry | Effect |
|---|---|---|
| ~line 1386 | Built-in gateway Kubernetes controllers removed | CP stops reconciling `MeshGatewayInstance`; resources go inert. Says the CRDs are *"not removed by this change"* |
| ~line 1851 | Built-in gateway no longer falls back to `MeshGatewayRoute` | A gateway host with no `MeshHTTPRoute`/`MeshTCPRoute` serves `404` |
| ~line 1952 | Built-in gateway API and CRDs removed | The types and CRDs are deleted |

The "not removed by this change" sentence is scoped to the controllers entry only; a later
change in the same release completes the removal. **Action required** is to delete any remaining
`MeshGateway`/`MeshGatewayRoute`/`MeshGatewayInstance`/`MeshGatewayConfig` objects *before*
upgrading, since the Helm CRD deletion takes stored resources with it.

Also from that entry: a `Dataplane` with `networking.gateway.type: BUILTIN` is now rejected at
admission **and update**, and `networking.gateway` plus the `DELEGATED` type are removed later in
the same release (see the `kuma.io/gateway is removed` entry, ~line 1336, whose replacement is
`traffic.kuma.io/exclude-inbound-ports` on the pod plus `kuma.io/ignore: "true"` on the fronting
`Service`).

## 3. `targetRef` narrowing on MeshHTTPRoute

From `pkg/plugins/policies/meshhttproute/api/v1alpha1/validation.go`:

| | v2.14.4 | master (3.0) |
|---|---|---|
| top-level `spec.targetRef` | `Mesh`, `MeshGateway` *(system role only, with `GatewayListenerTagsAllowed`)*, `MeshSubset`, `MeshService`, `MeshServiceSubset`, `Dataplane` | **`Mesh`, `Dataplane` only** — both roles, and the field type becomes `TopLevelTargetRef` |
| `to[].targetRef` | had a `case common_api.MeshGateway:` branch | `MeshService`, `MeshExternalService`, `MeshMultiZoneService` |

So a 2.x `MeshHTTPRoute` written as `targetRef: {kind: MeshService, name: backend}` needs its
top-level ref rewritten to `Dataplane` + labels before 3.0 — the same expansion problem, and the
same reason for warn-only treatment, as `warnDeprecatedTopLevelTargetRef`.

### The narrowing is universal — full matrix

`MeshHTTPRoute` is not a special case. On master **every** policy restricts its top-level
`targetRef` to `Mesh` and `Dataplane`, with no per-policy exceptions and no system-role
exception. `to[]`, by contrast, keeps four distinct shapes. Extracted from every
`pkg/plugins/policies/*/api/v1alpha1/valid*.go` on `master` (`2bdad1995`) and, for the
before column, at `v2.14.4`; Kong Mesh (`ca9683a`, 2026-09-18) ships only `MeshOPA`.

| Policy | top-level `targetRef` (3.0) | `to[]` (3.0) | top-level in 2.14.4 |
|---|---|---|---|
| MeshAccessLog | Mesh, Dataplane | Mesh, MeshService, MeshExternalService, MeshMultiZoneService, MeshHTTPRoute | + MeshSubset, MeshGateway, MeshService, MeshServiceSubset |
| MeshCircuitBreaker | Mesh, Dataplane | Mesh, MeshService, MeshExternalService, MeshMultiZoneService | + MeshSubset, MeshService, MeshGateway, MeshServiceSubset |
| MeshFaultInjection | Mesh, Dataplane | **Mesh only** | + MeshSubset, MeshGateway, MeshService, MeshServiceSubset |
| MeshHealthCheck | Mesh, Dataplane | Mesh, MeshService, MeshExternalService, MeshMultiZoneService | + MeshSubset, MeshGateway, MeshService, MeshServiceSubset |
| MeshHTTPRoute | Mesh, Dataplane | MeshService, MeshExternalService, MeshMultiZoneService *(no Mesh)* | + MeshGateway, MeshSubset, MeshService, MeshServiceSubset |
| MeshLoadBalancingStrategy | Mesh, Dataplane | Mesh, MeshService, MeshExternalService, MeshMultiZoneService, MeshHTTPRoute | + MeshSubset, MeshGateway, MeshService, MeshServiceSubset |
| MeshMetric | Mesh, Dataplane | — | + MeshSubset, MeshService, MeshServiceSubset, MeshGateway |
| MeshPassthrough | Mesh, Dataplane | — | + MeshSubset |
| MeshProxyPatch | Mesh, Dataplane | — | + MeshSubset, MeshService, MeshServiceSubset, MeshGateway |
| MeshRateLimit | Mesh, Dataplane | **Mesh only** | + MeshSubset, MeshGateway, MeshService, MeshServiceSubset, **MeshHTTPRoute** |
| MeshRetry | Mesh, Dataplane | Mesh, MeshService, MeshExternalService, MeshMultiZoneService, MeshHTTPRoute | + MeshSubset, MeshGateway, MeshService, MeshServiceSubset, **MeshHTTPRoute** |
| MeshTCPRoute | Mesh, Dataplane | MeshService, MeshExternalService, MeshMultiZoneService *(no Mesh)* | + MeshSubset, MeshGateway, MeshService, MeshServiceSubset |
| MeshTimeout | Mesh, Dataplane | Mesh, MeshService, MeshExternalService, MeshMultiZoneService, MeshHTTPRoute | + MeshSubset, MeshGateway, MeshService, MeshServiceSubset, **MeshHTTPRoute** |
| MeshTLS | Mesh, Dataplane | — | + MeshSubset |
| MeshTrace | Mesh, Dataplane | — | + MeshSubset, MeshGateway, MeshService, MeshServiceSubset |
| MeshTrafficPermission | Mesh, Dataplane | — *(`rules[]`)* | + MeshSubset, MeshService, MeshServiceSubset |
| MeshOPA *(Kong Mesh)* | Mesh, Dataplane | — | *(MeshService — see CLAUDE.md)* |

So `MeshTCPRoute` **did** undergo the same narrowing as `MeshHTTPRoute`, and its `backendRefs`
lost `MeshServiceSubset` alongside it.

**The four `to[]` shapes:**

1. **Outbound + route anchor** — Mesh, the three service kinds, `MeshHTTPRoute`:
   MeshAccessLog, MeshLoadBalancingStrategy, MeshRetry, MeshTimeout
2. **Outbound, no route anchor** — Mesh + three service kinds: MeshCircuitBreaker, MeshHealthCheck
3. **`Mesh` only** — MeshFaultInjection, MeshRateLimit (inbound policies; the destination is the
   policy's own target)
4. **Route backends** — three service kinds, *no* `Mesh`: MeshHTTPRoute, MeshTCPRoute

Seven policies have no `to[]` at all (MeshMetric, MeshPassthrough, MeshProxyPatch, MeshTLS,
MeshTrace, MeshTrafficPermission, MeshOPA) — they are top-level + `default`/`rules[]` only.

**Other deltas visible in the same sweep:**

- `validateFrom` is gone from every policy on master (MeshAccessLog, MeshCircuitBreaker,
  MeshFaultInjection, MeshRateLimit, MeshTimeout, MeshTLS, MeshTrafficPermission all had one at
  2.14.4) — the `from[]` removal, mesh-wide.
- `MeshServiceSubset` is dropped from `MeshHTTPRoute`/`MeshTCPRoute` `backendRefs` and `filters`.
- `MeshLoadBalancingStrategy`'s `to[]` **gained** `MeshExternalService` (2.14.4 had Mesh,
  MeshService, MeshMultiZoneService, MeshHTTPRoute).
- Possible upstream leftover: `MeshTimeout` and `MeshRetry` still carry a
  `if topLevelKind == common_api.MeshHTTPRoute` branch in `validateTo` (yielding
  `Mesh, MeshExternalService`), which looks unreachable now that no policy accepts
  `MeshHTTPRoute` at top level. Worth a question upstream rather than an assumption.

## 4. The `404` change — the one that breaks silently

> ### A request matching no `MeshHTTPRoute` rule now gets a `404`
>
> When a `MeshHTTPRoute` applies to a destination, a request that matches none of its rules is
> answered with `404` instead of being sent to that destination. This is what the Gateway API
> requires of an `HTTPRoute`, and it is what the GAMMA conformance suite asserts. Before this
> change the unmatched request fell through to the destination service as if no route existed.

Scope: when `to[].targetRef` names a `MeshService` **without** a `sectionName`, the rules apply to
*every* HTTP port of the destination, so the `404` covers ports no rule mentions. Non-HTTP ports
are unaffected, and a destination with no `MeshHTTPRoute` at all is unaffected.

The trap named by upstream is the route that exists only as a policy anchor:

> A `MeshHTTPRoute` matching `/api` that exists so a `MeshTimeout`, `MeshRetry`, or
> `MeshAccessLog` can target it now answers `404` on every other path of that destination. On a
> gRPC destination the same `404` reaches the client as `UNIMPLEMENTED`.

That pattern is still legal on 3.0 — `MeshTimeout`'s validator on master still accepts
`MeshHTTPRoute` as a `to[].targetRef.kind` (`meshtimeout/api/v1alpha1/validator.go:55-84`) —
which is exactly why the change bites: nothing about the anchoring policy stops working, only the
traffic does. (Anchoring must now be expressed through `to[]`: `MeshHTTPRoute` was a valid
*top-level* kind for `MeshTimeout`, `MeshRetry` and `MeshRateLimit` in 2.14.4 and is not on
master — see the matrix below.)

Fix, per `UPGRADE.md`:

```yaml
rules:
  - matches:
      - path:
          type: PathPrefix
          value: /
    default:
      backendRefs:
        - kind: MeshService
          labels:
            kuma.io/display-name: backend
          port: 80
```

> Put it on the route itself when the other policies targeting that route should also cover the
> unmatched traffic, or on a second `MeshHTTPRoute` when they should not.

## 5. Precedence changes between generated and hand-written routes

Two entries, both about who wins a tie:

- **Generated producer routes now win the same ties as hand-written ones.** A `MeshHTTPRoute`
  generated from an `HTTPRoute` whose parent lives in the `HTTPRoute`'s own namespace is now
  created in that namespace instead of the Kuma system namespace, so it gets
  `kuma.io/policy-role=producer` and **ties** with an equivalent hand-written route (previously
  the hand-written one always won, because the generated one ranked `system`). Ties fall back to
  resource name. Any policy selecting the generated route by
  `k8s.kuma.io/namespace: <kuma-system>` must be updated to the `HTTPRoute`'s namespace.
- **Conflicting `HTTPRoute`s on the same parent now tie-break by `creationTimestamp`** (oldest
  wins) rather than by name. A hand-written `MeshHTTPRoute` carries no such label and still takes
  precedence over a generated route it ties with.

## 6. Smaller items

- **Schema defaults are no longer materialized.** `MeshHTTPRoute`/`MeshRetry` header matches no
  longer store `type: Exact`, and `MeshHTTPRoute`/`MeshTCPRoute` backendRefs no longer store
  `weight: 1`. Behaviour is unchanged, but stored resources shrink — expect GitOps drift.
- **RBAC.** The control plane now needs read access to Gateway API `GRPCRoute`.

## What this means for kuma-migrator

| Area | Was | Now (shipped) |
|---|---|---|
| `MeshHTTPRoute`/`MeshTCPRoute` conversion | always converted to Gateway API (`ScenarioGW`) | **Removed.** Both detect as `ScenarioPassthrough` and are emitted byte-identical. The rewrite was never a 3.0 requirement and moved the route into the *generated* precedence class (§5); `MeshTCPRoute` → `TCPRoute` produced something Kuma has never reconciled on any version |
| catch-all advisory | lived in `TransformMeshHTTPRoute` (`route.go`) because `DetectScenario` always converted the kind away | Moved to `warnMeshHTTPRouteNoCatchAll` in `ScanForDeprecations` (v3 only), so it fires on the `MeshHTTPRoute` itself |
| top-level `targetRef` on a `MeshHTTPRoute` | covered generically by `warnDeprecatedTopLevelTargetRef` | Confirmed correct for 3.0 by §3 — `Mesh`/`Dataplane` only |
| `MeshGatewayInstance` under v3 | errors, points at the delegated-gateway replacement | Correct, and now backed by the CRD-removal entry. Worth naming `traffic.kuma.io/exclude-inbound-ports` and `kuma.io/ignore` explicitly in the error text |
| `MeshGateway` as a `targetRef.kind` | listed as a known v3 gap, no scanner | Now confirmed removed (§2) — the gap can be closed with a real check |

### Cross-check against kong-mesh-v3-readiness

Marcin's `preflight` lists `MeshGateway` and `MeshGatewayRoute` under **removed resources**, and
that is **correct** — an earlier reading of mine that called it an over-flag was based on the
scoped "not removed by this change" sentence in §2's middle entry. Recorded here because it is
the exact class of divergence a lockstep test against `preflight.RemovedKinds()` would catch
automatically, in whichever direction the error runs.

## Open questions

- Does Kong Mesh's `UPGRADE_km.md` add anything gateway- or route-specific on top of this?
- `MeshGatewayRoute` still converts to Gateway API, which suits replacing the built-in gateway
  with one you run — the only option on 3.0. But `UPGRADE.md` names `MeshHTTPRoute`/`MeshTCPRoute`
  as its replacement, which is right when Kuma keeps routing. Should the tool generate that form
  too (behind a flag, or by detecting whether a `MeshGateway` is present in the input)?

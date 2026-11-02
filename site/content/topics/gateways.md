---
title: Gateways, Gateway API and GAMMA
description: The built-in gateway removal in Kuma 3.0, MeshGateway* to Gateway API, delegated gateways, and why MeshHTTPRoute and MeshTCPRoute stay.
order: 60
nav_title: Gateways & GAMMA
---

Kuma 2.x has three gateway paths: the **built-in gateway** ([MeshGateway](policy:MeshGateway),
[MeshGatewayRoute](policy:MeshGatewayRoute), [MeshGatewayInstance](policy:MeshGatewayInstance),
`MeshGatewayConfig`), **north-south Gateway API** (`Gateway`/`HTTPRoute` reconciled into the
built-in gateway), and **delegated gateways** (a gateway you run, joined to the mesh). Mesh
routing itself uses [MeshHTTPRoute](policy:MeshHTTPRoute) and [MeshTCPRoute](policy:MeshTCPRoute),
or Gateway API `HTTPRoute`/`GRPCRoute` attached to a `Service` (the GAMMA, east-west case).

Kuma 3.0 removes the first two. The delegated gateway is the only gateway shape left, and the
GAMMA path stays.

## What changed upstream

### Built-in gateway removed

Kuma 3.0 retires the built-in gateway in several separate `UPGRADE.md` entries. Read them
together: the middle one says the CRDs are "not removed by this change", and a later entry in the
same release removes them.

| Entry | Effect |
|---|---|
| [North-south Gateway API support removed](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#north-south-gateway-api-built-in-gateway-support-removed) | `Gateway`, `GatewayClass` and `ReferenceGrant` are no longer reconciled. An `HTTPRoute` whose `parentRefs` target a `Gateway` is ignored. The Helm chart no longer installs the `kuma` `GatewayClass` |
| [Built-in gateway Kubernetes controllers removed](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#built-in-gateway-kubernetes-controllers-removed) | `MeshGatewayInstance` no longer reconciled; its `Service`, `Deployment` and `BUILTIN` `Dataplane` are left as they are |
| [Built-in gateway no longer falls back to MeshGatewayRoute](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#built-in-gateway-no-longer-falls-back-to-meshgatewayroute-or-legacy-connection-policies) | A gateway host with no `MeshHTTPRoute`/`MeshTCPRoute` serves `404` |
| [Built-in gateway API and CRDs removed](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#built-in-gateway-api-and-crds-removed) | `MeshGateway`, `MeshGatewayRoute`, `MeshGatewayInstance`, `MeshGatewayConfig`: types, CRDs and KDS sync deleted. `MeshGateway` is no longer a valid `targetRef.kind` for any policy |
| [`kuma.io/gateway` is removed](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#kumaiogateway-is-removed) | The Pod annotation, the `Dataplane` label and `Dataplane.networking.gateway` no longer exist. A Pod still carrying the annotation is admitted and treated as a normal workload |
| [The `BUILTIN` gateway type is removed from the API](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#the-builtin-gateway-type-and-its-statistics-are-removed-from-the-api) | A `Dataplane` with `type: BUILTIN` loads as an ordinary `Dataplane` with no inbounds |

Because the 3.0 Helm chart deletes the four CRDs, any `MeshGateway*` object still stored is
deleted with them. Delete them yourself, after the replacement serves traffic and **before**
upgrading.

### Delegated gateways without `kuma.io/gateway`

What the `kuma.io/gateway` marking did that mattered — Kuma not proxying the traffic the gateway
terminates — now comes from keeping the listen ports out of inbound redirection:

```yaml
# Pod template of the gateway
metadata:
  annotations:
    traffic.kuma.io/exclude-inbound-ports: "8000,8443"   # replaces kuma.io/gateway
---
# Service in front of the gateway
metadata:
  annotations:
    kuma.io/ignore: "true"
```

List every port the gateway accepts traffic on: the annotation is comma-separated and has no
all-ports value. A port left off is redirected into Envoy, so outside clients become mesh inbound
traffic subject to `MeshTrafficPermission` and mTLS and are rejected. Without `kuma.io/ignore` the
`Service` generates a `MeshService`, and in-mesh clients that address the gateway through it get
a destination with no endpoints. On Universal, drop `kuma.io/gateway` from the `Dataplane` labels
(a leftover `kuma.io/` label fails registration on 3.0) and run `kuma-dp` with
`--exclude-inbound-ports`.

### MeshHTTPRoute and MeshTCPRoute stay

`MeshHTTPRoute` and `MeshTCPRoute` are current policies on 2.x and 3.0. Kuma's Gateway API
integration compiles **into** them: on 3.0 the control plane registers an `HTTPRoute` reconciler
and a `GRPCRoute` reconciler (kuma#18280), and both produce a generated `MeshHTTPRoute` for an
`HTTPRoute`/`GRPCRoute` whose parent is a `Service` or `MeshService`:

```text
Gateway API HTTPRoute / GRPCRoute   (GAMMA: parentRef → Service or MeshService)
            ↓  control plane reconciler
      MeshHTTPRoute                 (generated; hand-written ones are still supported)
            ↓
         Envoy
```

`UPGRADE.md` states that "Gateways you run yourself and the Gateway API `HTTPRoute` GAMMA path
are unaffected." Kuma has no Gateway API `TCPRoute` reconciler at 2.14 or on 3.0.

### Routing behaviour changes on 3.0

| Change | Effect |
|---|---|
| [A request matching no `MeshHTTPRoute` rule gets a `404`](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#a-request-matching-no-meshhttproute-rule-now-gets-a-404) | Before, it fell through to the destination. With `to[].targetRef` naming a `MeshService` without `sectionName`, this covers **every** HTTP port of the destination. On gRPC the client sees `UNIMPLEMENTED`. The common trap is a route that exists only so a `MeshTimeout`/`MeshRetry`/`MeshAccessLog` can target it |
| [A match without a path ranks as `PathPrefix /`](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#a-meshhttproute-match-without-a-path-ranks-as-pathprefix-) | Header-only rules next to a catch-all now receive their traffic; pathless matches rank above `RegularExpression` matches |
| [Rule with no resolvable backendRef answers `500`](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#a-meshhttproute-rule-whose-backendrefs-all-fail-to-resolve-answers-500) | No fallback to the destination service; also when the referenced port does not exist ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#a-meshhttproute-backendref-naming-a-port-the-destination-lacks-now-fails-closed)) |
| [Route `backendRef` no longer accepts `MeshServiceSubset`](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#route-backendref-no-longer-accepts-meshservicesubset) | Only `MeshService`, `MeshExternalService`, `MeshMultiZoneService`; `tags` removed from the schema |
| [Real-resource selection by `labels` only](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#real-resource-policy-selection-now-uses-labels-only) | `targetRef`, `to[].targetRef` and route `backendRefs` lose `name`/`namespace` |
| [Generated producer routes tie with hand-written ones](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#generated-gateway-api-producer-routes-now-win-the-same-ties-as-hand-written-ones) | A route generated from an `HTTPRoute` lands in the `HTTPRoute`'s namespace with `kuma.io/policy-role=producer`, so it ties with an equivalent hand-written route that used to always win |
| [HTTPRoute conflicts resolve by `creationTimestamp`](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#gateway-api-httproute-conflicts-on-the-same-parent-now-resolve-by-creationtimestamp) | Oldest wins, not name |
| [Cross-namespace `backendRefs` need a `ReferenceGrant`](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#gateway-api-cross-namespace-backendrefs-now-require-a-referencegrant) | For the GAMMA translation, for `Service` and `MeshService` backends |
| [`kuma.io/mesh` annotation no longer honoured](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#kumaiomesh-annotation-no-longer-honored-on-kubernetes) | On Pods, Services, HTTPRoutes and Namespaces; only the label counts |
| [RBAC: control plane reads `GRPCRoute`](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#rbac-control-plane-now-reads-gateway-api-grpcroutes) | Add it if you manage RBAC yourself |

The top-level `targetRef` of every route is `Mesh` or `Dataplane` only on 3.0, and route `to[]`
entries accept only the three service kinds (no `Mesh`). The full per-policy matrix is on
[Policy targeting](policy-targeting.md#kinds-accepted-per-policy).

## What kuma-migrator does

| Input | `--to-latest v2` | `--to-latest v3` |
|---|---|---|
| `MeshGateway` | `Gateway` (`gateway.networking.k8s.io/v1`) named after the `MeshGateway`, in the namespace of the `MeshGatewayInstance` sharing its `kuma.io/service` tag. Listeners keep port, protocol, hostname, TLS `certificateRefs`; a bare `*` hostname is omitted; names are `<protocol>-<port>`, made unique. `gatewayClassName` resolved to the `GatewayClass` generated from that same instance | Same name, namespace and listeners. `gatewayClassName: REPLACE-WITH-YOUR-GATEWAYCLASS` with a warning. Advisory: delete the source `MeshGateway` before upgrading |
| `MeshGatewayInstance` | `GatewayClass` (named after the instance, `controllerName: gateways.kuma.io/controller`) + `MeshGatewayConfig` carrying `replicas`, `serviceType`, `tags` minus `kuma.io/service`. Both cluster-scoped: no namespace on the `MeshGatewayConfig` or the `parametersRef` | **Error**. Original written to `_manual/`. The message lists the settings to carry over to your own `Deployment`/`Service` and the delegated-gateway annotations |
| `MeshGatewayRoute` (HTTP) | `HTTPRoute` whose `parentRefs` name the converted `Gateway` (resolved through the `MeshGateway` selecting the route's `kuma.io/service` tag) and, from the other selector tags, its listener as `sectionName`. The route is placed in the Gateway's namespace. `kuma.io/mesh` as a **label**, integer `port`. A backend in another namespace gets a `ReferenceGrant`; a route whose namespace stays unknown gets a warning instead | Same, plus the delete-before-upgrade advisory and a note that Kuma 3.0 ignores Gateway-parented routes (your gateway implementation reconciles them) |
| `MeshGatewayRoute` (TCP) | `TCPRoute` | Same |
| `MeshHTTPRoute`, `MeshTCPRoute` | Copied unchanged (Passthrough) and scanned | `targetRef`/`to[]`/`backendRefs` `name`/`namespace` → labels; a `MeshServiceSubset` backendRef whose only tag is `kuma.io/service` → `MeshService`; other tags warned. Advisory when a `MeshHTTPRoute` has no catch-all rule (`404` change) |
| Policy with top-level `targetRef.kind: MeshGateway` | Copied (valid on 2.14 for system-role policies) | Warning: rejected on 3.0, re-target the gateway's own proxies with `kind: Dataplane` + labels |
| `Dataplane` `networking.gateway` (any type) | Copied | Block removed; non-`kuma.io/` gateway tags moved to `Dataplane` labels |

Code: [`gateway.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/gateway.go),
[`gatewayroute.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/gatewayroute.go),
[`v3_gateway.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/v3_gateway.go).

### Why MeshHTTPRoute is not converted to HTTPRoute

Rewriting a hand-written `MeshHTTPRoute` as a Gateway API `HTTPRoute` is not part of any upgrade,
and it changes behaviour:

- the generated `MeshHTTPRoute` is owned by the `HTTPRoute`;
- it ranks as a generated producer route, which now ties with hand-written routes, and
  conflicting `HTTPRoute`s tie-break by `creationTimestamp`;
- a `MeshTCPRoute` would become a `TCPRoute`, which Kuma has never reconciled.

Moving mesh routing to Gateway API is an architecture decision about where the routes live, so
the tool leaves it to you.

### MeshGatewayRoute: two possible successors

The tool converts `MeshGatewayRoute` to Gateway API, which fits replacing the built-in gateway
with a gateway you run. `UPGRADE.md` also names `MeshHTTPRoute`/`MeshTCPRoute` as the
replacement, which fits when Kuma keeps doing the routing. The manifest does not say which path
you are on, so the conversion warning states both.

## What it cannot do

- **Build the gateway.** A delegated gateway needs a container image and a pod spec (Kong Gateway,
  Envoy Gateway, Contour, …). `MeshGatewayInstance` carries neither.
- **Pick the `GatewayClass` on v3.** No Kuma `GatewayClass` exists on 3.0; it depends on the
  implementation you adopt.
- **Port gateway-scoped policies.** A `MeshTimeout` or `MeshRateLimit` that targeted a
  `MeshGateway` must be rewritten for the new gateway: as a `Dataplane` policy on the gateway's
  proxies, or as configuration of the gateway implementation.
- **Decide the catch-all.** Whether unmatched traffic should still reach a destination after 3.0
  is a routing decision. The tool reports routes without a catch-all; you add the rule.

## Manual steps

1. Choose the gateway implementation and install it on 2.14 next to the built-in gateway.
2. Apply the converted `Gateway` and `HTTPRoute`/`TCPRoute` objects with the new
   `gatewayClassName`, plus the generated `ReferenceGrant`s. Set `metadata.namespace` on any
   `Gateway` or route the tool left without one (it warns when the `MeshGatewayInstance` was not
   in the input).
3. Deploy the gateway workload as a delegated gateway: `traffic.kuma.io/exclude-inbound-ports` on
   the pod, `kuma.io/ignore: "true"` on its `Service`, settings from the `MeshGatewayInstance`
   (replicas, service type).
4. Move traffic (DNS or load balancer) to the new gateway and verify it.
5. Re-target policies that used `kind: MeshGateway`.
6. Add a catch-all rule to every `MeshHTTPRoute` the `v3` report flags, on the route itself or on a
   second route.
7. Delete `MeshGatewayRoute`, `MeshGateway`, `MeshGatewayInstance` and `MeshGatewayConfig`
   objects, the `Gateway`/`ReferenceGrant` objects you created for the built-in gateway, and
   `gapi-` prefixed Secrets. Then upgrade.

A catch-all rule, from `UPGRADE.md`:

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

## Example

Input — four documents: a built-in gateway, its instance, a route, and a `MeshHTTPRoute` that only
matches `/api`. `MeshGateway` and `MeshGatewayRoute` are cluster-scoped, so they have no
namespace:

```yaml
apiVersion: kuma.io/v1alpha1
kind: MeshGateway
metadata:
  name: edge-gateway
  labels:
    kuma.io/mesh: default
spec:
  selectors:
    - match:
        kuma.io/service: edge-gateway_kong-mesh-system_svc
  conf:
    listeners:
      - port: 8080
        protocol: HTTP
        hostname: shop.example.com
        tags:
          port: http-8080
---
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayInstance
metadata:
  name: edge-gateway
  namespace: kong-mesh-system
  labels:
    kuma.io/mesh: default
spec:
  replicas: 2
  serviceType: LoadBalancer
  tags:
    kuma.io/service: edge-gateway_kong-mesh-system_svc
---
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayRoute
metadata:
  name: edge-route
  labels:
    kuma.io/mesh: default
spec:
  selectors:
    - match:
        kuma.io/service: edge-gateway_kong-mesh-system_svc
  conf:
    http:
      rules:
        - matches:
            - path:
                match: PREFIX
                value: /api
          backends:
            - destination:
                kuma.io/service: backend_demo_svc_3001
---
apiVersion: kuma.io/v1alpha1
kind: MeshHTTPRoute
metadata:
  name: backend-api
  namespace: demo
  labels:
    kuma.io/mesh: default
spec:
  targetRef:
    kind: Mesh
  to:
    - targetRef:
        kind: MeshService
        name: backend
      rules:
        - matches:
            - path:
                type: PathPrefix
                value: /api
          default:
            backendRefs:
              - kind: MeshService
                name: backend
                port: 3001
```

Output with `--to-latest v2` (the `MeshHTTPRoute` is copied unchanged):

```yaml
# routing/Gateway-kong-mesh-system-edge-gateway.yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: edge-gateway
  namespace: kong-mesh-system
spec:
  gatewayClassName: edge-gateway
  listeners:
  - hostname: shop.example.com
    name: http-8080
    port: 8080
    protocol: HTTP
```

```yaml
# routing/GatewayClass-edge-gateway.yaml
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: edge-gateway
spec:
  controllerName: gateways.kuma.io/controller
  parametersRef:
    group: kuma.io
    kind: MeshGatewayConfig
    name: edge-gateway
```

```yaml
# routing/MeshGatewayConfig-edge-gateway.yaml
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayConfig
metadata:
  labels:
    kuma.io/mesh: default
  name: edge-gateway
spec:
  replicas: 2
  serviceType: LoadBalancer
```

```yaml
# routing/HTTPRoute-kong-mesh-system-edge-route.yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  labels:
    kuma.io/mesh: default
  name: edge-route
  namespace: kong-mesh-system
spec:
  parentRefs:
  - group: gateway.networking.k8s.io
    kind: Gateway
    name: edge-gateway
  rules:
  - backendRefs:
    - group: ""
      kind: Service
      name: backend
      namespace: demo
      port: 3001
    matches:
    - path:
        type: PathPrefix
        value: /api
```

```yaml
# other/ReferenceGrant-demo-kong-mesh-system-edge-route.yaml
apiVersion: gateway.networking.k8s.io/v1beta1
kind: ReferenceGrant
metadata:
  name: kong-mesh-system-edge-route
  namespace: demo
spec:
  from:
  - group: gateway.networking.k8s.io
    kind: HTTPRoute
    namespace: kong-mesh-system
  to:
  - group: ""
    kind: Service
    name: backend
```

The `Gateway` and the `HTTPRoute` both land in `kong-mesh-system`, the namespace of the
`MeshGatewayInstance` whose `kuma.io/service` tag the `MeshGateway` and the route select. A
Gateway listener accepts routes only from its own namespace by default, and Kuma 2.x creates the
instance for a Gateway in the Gateway's namespace. `MeshGatewayConfig` is cluster-scoped, and
Kuma rejects a `GatewayClass` whose `parametersRef` names a namespace, so neither carries one.
`kuma.io/service` is dropped from the `MeshGatewayConfig` tags: Kuma generates
`<gateway>_<namespace>_svc` for each Gateway of the class.

With `--to-latest v3`:

- the `Gateway` is the same except `gatewayClassName: REPLACE-WITH-YOUR-GATEWAYCLASS`;
- no `GatewayClass` or `MeshGatewayConfig` is written, and the `MeshGatewayInstance` goes to
  `_manual/routing/MeshGatewayInstance-kong-mesh-system-edge-gateway.yaml`;
- the `HTTPRoute` and `ReferenceGrant` are the same;
- the `MeshHTTPRoute` selects by labels:

```yaml
# routing/MeshHTTPRoute-demo-backend-api.yaml (v3)
apiVersion: kuma.io/v1alpha1
kind: MeshHTTPRoute
metadata:
  labels:
    kuma.io/mesh: default
  name: backend-api
  namespace: demo
spec:
  targetRef:
    kind: Mesh
  to:
  - rules:
    - default:
        backendRefs:
        - kind: MeshService
          labels:
            k8s.kuma.io/namespace: demo
            kuma.io/display-name: backend
          port: 3001
      matches:
      - path:
          type: PathPrefix
          value: /api
    targetRef:
      kind: MeshService
      labels:
        k8s.kuma.io/namespace: demo
        kuma.io/display-name: backend
```

`v3` messages (shortened):

```text
✗  MeshGatewayInstance "edge-gateway" (kong-mesh-system) has no successor on Kuma 3.0 … Replace it
   with a delegated gateway: a Deployment and Service you manage … annotation
   traffic.kuma.io/exclude-inbound-ports: "<ports>" … kuma.io/ignore: "true" … Settings to carry
   over to the Deployment/Service: replicas=2, serviceType=LoadBalancer,
   tags{kuma.io/service=edge-gateway_kong-mesh-system_svc}. …
⚠  MeshHTTPRoute "backend-api": none of its rules is a catch-all … on Kuma 3.0 a request matching
   no rule is answered with a 404 instead of falling through to the destination …
```

## Related

- [Policy targeting](policy-targeting.md) — the `targetRef` matrix
- [Dataplanes](dataplanes.md) — `networking.gateway` and Pod annotations
- [Access control](access-control.md) — `MeshTrafficPermission` for gateway traffic

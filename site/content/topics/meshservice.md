---
title: MeshService
description: Moving policies, routes and workload addresses from kuma.io/service tags to MeshService, and setting meshServices.mode before Kuma 3.0.
order: 40
---

On 2.x a destination is identified by the `kuma.io/service` tag of its inbounds — on
Kubernetes a generated value such as `backend_demo_svc_3001`. [MeshService](policy:MeshService)
replaces it with a real resource that has a name, a namespace, ports and a hostname, and that
lives in one zone. On 3.0 `kuma.io/service`-based destinations are gone: unified resource
naming is unconditional and the `meshServices` switch has no effect.

## What changed upstream

| Version | Change |
|---|---|
| 2.8 | `MeshService` `spec.ports[].protocol` renamed to `appProtocol`; `HostnameGenerator` templates applied to `MeshService`s ([CHANGELOG](https://github.com/kumahq/kuma/blob/master/CHANGELOG.md)) |
| 2.9 | `MeshService` generation controlled by `Mesh.spec.meshServices.mode` ([CHANGELOG](https://github.com/kumahq/kuma/blob/master/CHANGELOG.md)) |
| 2.10 | Names of `Mesh`, `MeshService`, `MeshExternalService` and `MeshMultiZoneService` that are not RFC 1035 labels are deprecated ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md)) |
| 2.14 | `HostnameGenerator` templates validated at creation (RFC 1123 subdomain); `MeshMultiZoneService` names over 63 characters deprecated ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md)) |
| 3.0 | `meshServices` has no effect; unified naming is unconditional. The field stays as deprecated so stored values keep syncing to 2.x zones. 3.0 rejects any mode other than `Exclusive` ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshservices-removed-from-the-mesh-schema)) |
| 3.0 | Policies select `MeshService` by `labels` only ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#real-resource-policy-selection-now-uses-labels-only)) |
| 3.0 | `MeshService` `spec.selector.dataplaneTags` removed; an unmigrated selector is dropped and the `MeshService` matches no proxies ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#dataplanetags-removed-from-the-meshservice-selector)) |
| 3.0 | `MeshService` `spec.identities[]` accepts SPIFFE IDs only (`type: ServiceTag` rejected) ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshservicespecidentities-now-accepts-spiffe-ids-only)) |
| 3.0 | Non-RFC 1035 names rejected ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#names-of-mesh-zone-meshservice-meshexternalservice-and-meshmultizoneservice-must-be-rfc-1035-labels)) |
| 3.0 | `reachableServices` removed; `reachableBackends` refs select by `labels` only; a transparent-proxy data plane without `reachableBackends` gets no outbounds ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#data-plane-proxies-without-reachablebackends-get-no-outbounds)) |

The upstream migration guide is the "Migration" section of the
[MeshService docs](https://kuma.io/docs/latest/networking/meshservice/).

### `meshServices.mode`

| Mode | Behaviour on 2.x |
|---|---|
| `Disabled` | No `MeshService` generation. A `Mesh` with no `meshServices` block resolves to this |
| `Everywhere` | `MeshService`s generated; both `kuma.io/service` and `MeshService` produce Envoy clusters (about twice the resources) |
| `ReachableBackends` | `MeshService`s generated but only configured on a proxy that lists them in `reachableBackends`. `kuma.io/service` stays fully active. Use it to migrate consumer by consumer |
| `Exclusive` | Only `MeshService`, `MeshExternalService` and `MeshMultiZoneService`. `kuma.io/service` matching is off |

Only `Exclusive` turns `kuma.io/service` off. On 3.0 every mesh behaves as `Exclusive`. Because a
2.x zone reads a missing field as `Disabled` and then deletes every generated `MeshService`,
`Exclusive` must be stored on every mesh **before the Global CP is upgraded**. Drop the field once
every zone runs 3.0.

## What kuma-migrator does

The Subset scenario rewrites `kuma.io/service` and `k8s.kuma.io/service-name` selectors
([`convert.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/convert.go)). A
Kubernetes `kuma.io/service` value `<name>_<namespace>_svc_<port>` is decoded into name,
namespace and port; a Universal value is used as the name.

| Input | `--to-latest v2` | `--to-latest v3` |
|---|---|---|
| `spec.targetRef` `MeshSubset` with `kuma.io/service: '*'` | `kind: Mesh` | Same |
| `spec.targetRef` `MeshSubset` with a service tag | `kind: Dataplane` with `labels: {app: <name>, k8s.kuma.io/namespace: <ns>}` plus other tags; `app` is a best guess and warned | Same |
| `to[]`/`from[]` `MeshSubset` with a service tag | `kind: MeshService` with `name`/`namespace` (same namespace as the policy) or `labels` (system namespace, other tags) | `kind: MeshService` with `labels` only |
| `MeshService` ref with a Kuma-internal name (`backend_demo_svc_3001`) | Decoded: `to[]`/`from[]` → `MeshService` name/namespace; top level → `Dataplane` | Same, with labels |
| `rules[]` and `to[]` in one policy | Split: `rules[]` keeps the name, `to[]` goes to `<name>-outbound` | Same |
| `Mesh` | `meshServices.mode: Exclusive` injected, with a warning to migrate policies and workloads first | Same, with the mixed-version note |
| `MeshService` `ports[].protocol` | Renamed to `appProtocol` | Same |
| `MeshService` `selector.dataplaneTags` | Advisory | Rewritten to `dataplaneLabels.matchLabels`; a `kuma.io/`-prefixed tag is not converted (reserved label prefix) and is warned |
| `MeshService` `identities[]` `type: ServiceTag` | Advisory | Entry dropped |
| `Dataplane` `reachableServices` | Warning | Converted to `reachableBackends` refs with labels where the value decodes; warned otherwise |
| `reachableBackends` ref by `name`/`namespace` | — | Rewritten to labels |
| Non-RFC 1035 `Mesh`/`MeshService`/`MeshExternalService`/`MeshMultiZoneService` name | Warning | Warning |
| `HostnameGenerator` template that renders an invalid DNS subdomain | Warning | Warning |
| Deployment/StatefulSet env var holding `<svc>_<ns>_svc_<port>.mesh` | Listed in the report's address mapping, with the Kubernetes and `.mesh` replacements | Same |

`reachableBackends` and `dataplaneTags` are rewritten under `v3` only: on 2.14 a labels selector
also matches same-named services synced from other zones, so the rewrite would widen selection.

## What it cannot do

- **Know the pod labels.** A top-level `kuma.io/service` selector becomes `Dataplane` labels with
  `app: <name>`. Most charts set `app`, but the tool cannot check. Every such case is warned.
- **Choose between `MeshService` and `MeshMultiZoneService`.** A `MeshService` is one zone's
  service. If clients relied on `kuma.io/service` load-balancing across zones, they need a
  [MeshMultiZoneService](policy:MeshMultiZoneService), which you create. See
  [Multizone](multizone.md).
- **Check label refinements.** A `MeshSubset` that combined a service tag with another tag (for
  example `version: v2`) becomes a `MeshService` selected by `kuma.io/display-name`,
  `k8s.kuma.io/namespace` and `version: v2`. The `MeshService` only matches if it carries that
  label, which comes from the Kubernetes `Service`, not from the pods.
- **Find the MeshService port** of a Universal outbound or a `reachableServices` entry whose name
  does not decode.
- **Rename resources.** A name that is not an RFC 1035 label must be renamed, which changes the
  hostname and every reference to it.

## Manual steps

1. On 2.14, set `meshServices.mode: ReachableBackends` (gradual) or `Everywhere` on each mesh.
2. For every consumer of a `kuma.io/service` destination, decide `MeshService` (one zone) or
   `MeshMultiZoneService` (all zones), and create the `MeshMultiZoneService`s.
3. Apply the `v2` output: policies and routes that refer to `MeshService`.
4. Update workload addresses listed in the report's address mapping (for example
   `backend_demo_svc_3001.mesh` → `backend.demo.svc.cluster.local:3001`).
5. Fill `reachableBackends` on transparent-proxy data planes (required on 3.0).
6. Set `meshServices.mode: Exclusive`. This must be stored before the Global CP upgrade.
7. Rename resources the report flags as non-RFC 1035.
8. Before upgrading, run `--to-latest v3` and apply the labels-only selectors and the
   `dataplaneLabels` selectors.

## Example

Input — two files: a policy still on `MeshSubset`, and one partially migrated
(`kind: MeshService` with the Kuma-internal name):

```yaml
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: web-to-backend
  namespace: kong-mesh-system
  labels:
    kuma.io/mesh: default
spec:
  targetRef:
    kind: MeshSubset
    tags:
      kuma.io/service: web_demo_svc_8080
  to:
    - targetRef:
        kind: MeshSubset
        tags:
          kuma.io/service: backend_demo_svc_3001
          version: v2
      default:
        http:
          requestTimeout: 5s
---
apiVersion: kuma.io/v1alpha1
kind: MeshRetry
metadata:
  name: web-retry
  namespace: demo
  labels:
    kuma.io/mesh: default
spec:
  targetRef:
    kind: Mesh
  to:
    - targetRef:
        kind: MeshService
        name: backend_demo_svc_3001
      default:
        http:
          numRetries: 2
```

Output with `--to-latest v2`:

```yaml
# resiliency/MeshTimeout-kong-mesh-system-web-to-backend.yaml
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  labels:
    kuma.io/mesh: default
  name: web-to-backend
  namespace: kong-mesh-system
spec:
  targetRef:
    kind: Dataplane
    labels:
      app: web
      k8s.kuma.io/namespace: demo
  to:
  - default:
      http:
        requestTimeout: 5s
    targetRef:
      kind: MeshService
      labels:
        k8s.kuma.io/namespace: demo
        kuma.io/display-name: backend
        version: v2
```

```yaml
# resiliency/MeshRetry-demo-web-retry.yaml
apiVersion: kuma.io/v1alpha1
kind: MeshRetry
metadata:
  labels:
    kuma.io/mesh: default
  name: web-retry
  namespace: demo
spec:
  targetRef:
    kind: Mesh
  to:
  - default:
      http:
        numRetries: 2
    targetRef:
      kind: MeshService
      name: backend
      namespace: demo
```

With `--to-latest v3` the `MeshTimeout` is identical (it already uses labels) and the `MeshRetry`
destination becomes a label selector:

```yaml
# resiliency/MeshRetry-demo-web-retry.yaml (v3)
    targetRef:
      kind: MeshService
      labels:
        k8s.kuma.io/namespace: demo
        kuma.io/display-name: backend
```

Warning on both targets:

```
⚠  spec.targetRef Dataplane labels use 'app: web' as a best-guess pod label — verify this
   matches your actual pod labels before applying
```

A Universal `MeshService` still selecting by `dataplaneTags: {kuma.io/service: backend}` is
reported but not rewritten under `v3`, because `kuma.io/service` cannot be a `Dataplane` label:

```
⚠  MeshService "backend": spec.selector.dataplaneTags is removed in 3.0 and dropped silently,
   leaving a MeshService that matches no proxies. Not converted: kuma.io/service=backend cannot
   be a Dataplane label (reserved kuma.io/ prefix), and dropping it would widen the selector. …
```

## Related

- [Policy targeting](policy-targeting.md) — labels-only selection and policy roles
- [Multizone](multizone.md) — `MeshMultiZoneService`
- [Dataplanes](dataplanes.md) — `reachableServices` and `reachableBackends`
- [Meshes](meshes.md)

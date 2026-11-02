---
title: Meshes
description: Every field of the Mesh resource that changed between 2.x and 3.0, and what kuma-migrator does with each one.
order: 10
---

On Kuma 3.0 the [Mesh](policy:Mesh) resource is reduced to its name and a deprecated
`meshServices` block. Every other field (`mtls`, `tracing`, `logging`, `metrics`, `networking`,
`routing`, `constraints`, `skipCreatingInitialPolicies`) is a `reserved` field number in
[`mesh.proto`](https://github.com/kumahq/kuma/blob/master/api/mesh/v1alpha1/mesh.proto). The
behaviour each field configured has moved to a policy or to the control plane.

## What changed upstream

| Field | Successor | Upstream change |
|---|---|---|
| `mtls` | [MeshIdentity](policy:MeshIdentity) + [MeshTrust](policy:MeshTrust), [MeshTLS](policy:MeshTLS) for permissive mode | Removed in 3.0; a mesh whose only identity source is `mtls` serves plaintext ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshmtls-no-longer-produces-mtls)). See [mTLS and workload identity](mtls-identity.md) |
| `metrics` | [MeshMetric](policy:MeshMetric) | Removed in 3.0 (`reserved` in `mesh.proto`) |
| `tracing` | [MeshTrace](policy:MeshTrace) | Removed in 3.0, silently ignored ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshspectracing-removed)) |
| `logging` | [MeshAccessLog](policy:MeshAccessLog) | Removed in 3.0, silently ignored ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshspeclogging-removed)) |
| `networking.outbound.passthrough` | [MeshPassthrough](policy:MeshPassthrough) | Removed in 3.0. A transparent-proxy data plane that no `MeshPassthrough` selects now drops external traffic ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#outbound-passthrough-defaults-to-none)) |
| `routing.zoneEgress` | none; egress use is implicit | Removed in 3.0 ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#routingzoneegress-removed-from-the-mesh-schema)) |
| `routing.localityAwareLoadBalancing` | [MeshLoadBalancingStrategy](policy:MeshLoadBalancingStrategy) | Removed in 3.0 ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshspecroutinglocalityawareloadbalancing-removed)). See [Multizone](multizone.md) |
| `routing.defaultForbidMeshExternalServiceAccess` | [MeshTrafficPermission](policy:MeshTrafficPermission) | Removed in 3.0 ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#routingdefaultforbidmeshexternalserviceaccess-removed-from-the-mesh-schema)) |
| `skipCreatingInitialPolicies` | none | Removed in 3.0: a new mesh gets no default policies at all, so a mesh without a `MeshRetry` does not retry ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshes-no-longer-come-with-default-policies)) |
| `constraints.dataplaneProxy` | none | Removed in 3.0 (`reserved` in `mesh.proto`). It refused data plane proxies whose tags matched no `requirements` entry or matched a `restrictions` entry ([data plane proxy membership](https://developer.konghq.com/mesh/configure-data-plane-proxy-membership/)); the 3.0 membership validator checks nothing. The nearest controls are dataplane tokens bound to the mesh and tags (`kumactl generate dataplane-token --mesh <mesh> --tag key=value`) and `MeshTrafficPermission` |
| `meshServices.mode` | none | Has no effect on 3.0 zones, but must be `Exclusive` before the Global CP upgrade; 3.0 rejects any other mode ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshservices-removed-from-the-mesh-schema)). See [MeshService](meshservice.md) |

The `Mesh` CRD is `x-kubernetes-preserve-unknown-fields` and the control plane drops unknown
`Mesh` fields silently, so a 2.x `Mesh` manifest still applies on 3.0. The removed fields
simply stop doing anything ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshmtls-is-removed-from-the-api)).

Names of `Mesh` resources must be RFC 1035 labels on 3.0 (lowercase, alphabetic start, at most
63 characters). Since 2.10 such names were accepted with a deprecation warning; 3.0 rejects them,
including in Global → Zone sync ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#names-of-mesh-zone-meshservice-meshexternalservice-and-meshmultizoneservice-must-be-rfc-1035-labels)).

## What kuma-migrator does

A `Mesh` is handled by the Mesh scenario
([`mesh.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/mesh.go),
[`v3_mesh.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/v3_mesh.go)).
Companion policies are named `<mesh>-metrics`, `<mesh>-passthrough`, `<mesh>-tls-permissive`.

| Field | `--to-latest v2` | `--to-latest v3` |
|---|---|---|
| `metrics` | The `enabledBackend` becomes a mesh-wide `MeshMetric`; backends that are not enabled are dropped; no `enabledBackend` means no `MeshMetric` | Same |
| `tracing` | Removed, no companion. Backends travel with each converted `TrafficTrace` (see below) | Same |
| `logging` | Removed, no companion. Backends travel with each converted `TrafficLog` (see below) | Same |
| `networking.outbound.passthrough: false` | `MeshPassthrough` with `passthroughMode: None` | Same |
| `networking.outbound.passthrough: true` | Removed from the output, with a warning to add a `MeshPassthrough` before 3.0 | `MeshPassthrough` with `passthroughMode: All` |
| `networking.outbound.passthrough` unset | Nothing | Warning: check that a `MeshPassthrough` covers proxies that reach external destinations |
| `mtls` | Kept, advisory | Removed, **blocking** warning. See [mTLS and workload identity](mtls-identity.md) |
| `mtls.backends[].mode: PERMISSIVE` | Kept, warning to add a `MeshTLS` before 3.0 | `MeshTLS` with `rules[].default.mode: Permissive` |
| `routing.zoneEgress`, `routing.localityAwareLoadBalancing`, `routing.defaultForbidMeshExternalServiceAccess` | Kept, warning per field | Removed, with a note on what replaces each one |
| `skipCreatingInitialPolicies` | Kept | Removed, with the retry note |
| `constraints` | Kept, advisory: membership enforcement is lost on 3.0 | Removed, with a warning naming what is no longer enforced and the dataplane-token alternative |
| `meshServices` | `mode: Exclusive` injected | `mode: Exclusive` injected, with the mixed-version note |
| Name not an RFC 1035 label | Warning | Warning |

### Logging and tracing

On 2.x the `Mesh.logging` and `Mesh.tracing` backends only declare destinations. Nothing is
logged or traced until a `TrafficLog` or `TrafficTrace` selects traffic and names a backend, or
falls back to `defaultBackend`
([traffic log docs](https://kuma.io/docs/latest/policies/traffic-log/)). No default `TrafficLog`
or `TrafficTrace` is created with a mesh. A mesh-wide `MeshAccessLog` or `MeshTrace` would turn
logging or tracing on for every proxy, so none is generated.

Each [TrafficLog](policy:TrafficLog) and [TrafficTrace](policy:TrafficTrace) is converted on its
own, with the backend it references (or the Mesh `defaultBackend`) copied inline from the `Mesh`
in the same input. The `Mesh` warning lists, per backend, how many `TrafficLog`/`TrafficTrace`
resources in the input reference it. A backend nothing references was inactive, so dropping it
loses nothing. If the `TrafficLog`/`TrafficTrace` resources were not extracted with the `Mesh`,
add them to the input.

`metrics` is different: `metrics.enabledBackend` turns Prometheus metrics on for every proxy in
the mesh, so the enabled backend becomes a mesh-wide `MeshMetric`.

### System namespace

On Kubernetes the generated mesh-scoped resources (`MeshMetric`, `MeshPassthrough`, `MeshTLS`,
`MeshOpenTelemetryBackend`, and the `MeshExternalService` converted from an `ExternalService`)
go in the control-plane namespace. The pre-pass over the input directory picks it:

1. `systemNamespace` in the config file (`~/.config/kuma-migrator.yaml` or
   `$KUMA_MIGRATOR_CONFIG`), when set.
2. `kong-mesh-system` when any input document has `namespace: kong-mesh-system`.
3. `kuma-system` when any input document has `namespace: kuma-system`.
4. `kong-mesh-system` when the input holds a Kong Mesh-only kind (`OPAPolicy`, `MeshOPA`,
   `MeshGlobalRateLimit`, `AccessRole`, `AccessRoleBinding`, `AccessAudit`).
5. `kuma-system` otherwise.

```yaml
# ~/.config/kuma-migrator.yaml
systemNamespace: my-mesh-system
```

A Universal `Mesh` (`type: Mesh`) gets Universal companions: `type`, `name` and `mesh` at the top
level, no namespace.

## What it cannot do

- **Write the `MeshIdentity`.** The trust domain, the CA material and the per-workload SPIFFE
  paths are not in the `Mesh` manifest. See [mTLS and workload identity](mtls-identity.md).
- **Generate the cross-zone `MeshLoadBalancingStrategy`.** On 3.0 `crossZone` is accepted only on
  a `to[]` entry that targets a `MeshMultiZoneService`, and the manifest does not say which
  services need one. See [Multizone](multizone.md).
- **Resolve a backend whose `Mesh` is not in the input.** A `TrafficLog`/`TrafficTrace` whose
  `Mesh` was not extracted with it is converted without a backend, and the warning names the
  backend to write by hand.
- **Carry `constraints`.** `Mesh.spec.constraints` (Dataplane admission requirements) is a
  `reserved` field in `mesh.proto` on 3.0 and has no successor. The tool copies it through
  unchanged on both targets without a warning.

## Manual steps

1. Run `migrate --dry-run --to-latest v2` and read the `Mesh` warnings.
2. Set `meshServices.mode: Exclusive` on every mesh once its policies and workloads are migrated
   (see [MeshService](meshservice.md)). This must be done before the Global CP upgrade.
3. Apply the `v2` companions (`MeshMetric`, `MeshPassthrough`) and the `MeshAccessLog`/`MeshTrace`
   converted from each `TrafficLog`/`TrafficTrace`.
4. Migrate identity to `MeshIdentity` on 2.14 (see [mTLS and workload identity](mtls-identity.md)).
5. Add a `MeshTLS` `Permissive` policy for meshes that rely on `PERMISSIVE`, and a
   `MeshPassthrough` for meshes whose transparent-proxy workloads reach external destinations.
6. Add a `MeshRetry` to any mesh that relies on the default retry policy and does not already
   have one: new meshes on 3.0 get none.
7. Before the 3.0 upgrade, run `migrate --to-latest v3` and apply the cleaned `Mesh`.

## Example

Input:

```yaml
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
  mtls:
    enabledBackend: ca-1
    backends:
      - name: ca-1
        type: builtin
        mode: PERMISSIVE
  metrics:
    enabledBackend: prom
    backends:
      - name: prom
        type: prometheus
        conf:
          port: 5670
          path: /metrics
  networking:
    outbound:
      passthrough: true
  routing:
    zoneEgress: true
    localityAwareLoadBalancing: true
```

Output with `--to-latest v2`. The `MeshMetric` is extracted; `mtls` and `routing` are kept with
warnings; `passthrough: true` is dropped as the 2.x default:

```yaml
# mesh/Mesh-default.yaml
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
  meshServices:
    mode: Exclusive
  mtls:
    backends:
    - mode: PERMISSIVE
      name: ca-1
      type: builtin
    enabledBackend: ca-1
  routing:
    localityAwareLoadBalancing: true
    zoneEgress: true
```

```yaml
# observability/MeshMetric-kuma-system-default-metrics.yaml
apiVersion: kuma.io/v1alpha1
kind: MeshMetric
metadata:
  labels:
    kuma.io/mesh: default
  name: default-metrics
  namespace: kuma-system
spec:
  default:
    backends:
    - prometheus:
        path: /metrics
        port: 5670
      type: Prometheus
  targetRef:
    kind: Mesh
```

Output with `--to-latest v3`. The `Mesh` keeps only `meshServices`; the same `MeshMetric` is
generated, plus a `MeshPassthrough` and a `MeshTLS`:

```yaml
# mesh/Mesh-default.yaml
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
  meshServices:
    mode: Exclusive
```

```yaml
# routing/MeshPassthrough-kuma-system-default-passthrough.yaml
apiVersion: kuma.io/v1alpha1
kind: MeshPassthrough
metadata:
  labels:
    kuma.io/mesh: default
  name: default-passthrough
  namespace: kuma-system
spec:
  default:
    passthroughMode: All
  targetRef:
    kind: Mesh
```

```yaml
# zero-trust/MeshTLS-kuma-system-default-tls-permissive.yaml
apiVersion: kuma.io/v1alpha1
kind: MeshTLS
metadata:
  labels:
    kuma.io/mesh: default
  name: default-tls-permissive
  namespace: kuma-system
spec:
  rules:
  - default:
      mode: Permissive
  targetRef:
    kind: Mesh
```

Warnings on the `v3` run (shortened):

```text
⚠  spec.mtls is REMOVED in 3.0 and has been dropped from the Mesh output. BLOCKING: before
   upgrading, create a MeshIdentity (and MeshTrust) that selects every proxy in this mesh …
⚠  the enabled mTLS backend used mode: PERMISSIVE … a MeshTLS "default-tls-permissive" with
   rules[].default.mode: Permissive has been generated …
⚠  routing.zoneEgress=true is removed in 3.0. Egress use is now implicit …
⚠  routing.localityAwareLoadBalancing=true is removed in 3.0 … The migrator does not generate
   that policy.
⚠  removed fields that 3.0 no longer has in the Mesh schema: mtls, routing.zoneEgress,
   routing.localityAwareLoadBalancing.
```

## Related

- [Choosing a target version](../guides/target-version.md)
- [Observability](observability.md), [mTLS and workload identity](mtls-identity.md),
  [MeshService](meshservice.md), [Multizone](multizone.md)

---
title: Multizone
description: Control plane modes, which control plane to extract from, zone proxies, MeshMultiZoneService and cross-zone load balancing across 2.x and 3.0.
order: 50
---

A multizone deployment has one Global control plane and one Zone control plane per zone.
Resources are created on either side and synced over KDS. That decides where `extract` must run,
and 3.0 changes the topology itself: zone proxies become ordinary `Dataplane`s, a zone's public
address comes from a new resource, `standalone` mode is gone, and the Global CP can no longer run
on Kubernetes.

## What changed upstream

| Version | Change |
|---|---|
| 3.0 | `standalone` control plane mode removed: rename it to `zone`; the two were already identical ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#standalone-mode-removed)) |
| 3.0 | Global control plane on Kubernetes no longer supported: `mode=global` with `environment=kubernetes` or `store.type=kubernetes` is rejected. Run it on Universal with PostgreSQL ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#global-control-plane-on-kubernetes-is-no-longer-supported)) |
| 3.0 | Zones no longer require `kuma.io/origin: zone` on locally applied resources; any other value is still rejected ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#zones-no-longer-require-the-kumaioorigin-label)) |
| 3.0 | Control-plane-owned labels (`kuma.io/origin`, `kuma.io/zone`, …) rejected when they differ from the computed value ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#control-plane-owned-labels-are-rejected-when-they-differ-from-the-computed-value)) |
| 3.0 | [ZoneIngress](policy:ZoneIngress) and [ZoneEgress](policy:ZoneEgress) resources, CRDs, endpoints and KDS sync removed ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#zoneingress-and-zoneegress-resources-removed)) |
| 3.0 | Standalone zone proxies (`kuma-dp --proxy-type=ingress|egress`, Helm `ingress.enabled`/`egress.enabled`) no longer served. Cross-zone traffic goes through mesh-scoped zone proxies: regular `Dataplane`s with `ZoneIngress`/`ZoneEgress` listeners ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#standalone-zoneingresszoneegress-proxies-are-no-longer-supported)) |
| 3.0 | A remote zone's public address comes only from `MeshZoneAddress`, not from `ZoneIngress` `advertisedAddress`. Kubernetes creates it per mesh-scoped zone proxy `Service`; on Universal you write it, right after the zone CP upgrade ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#cross-zone-meshservice-routing-requires-meshzoneaddress)) |
| 3.0 | Once a zone is upgraded it sends real-resource traffic with the KRI SNI, which a 2.x zone cannot terminate: cross-zone `MeshService` traffic to a 2.x zone stops until that zone is upgraded too ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshexternalservice-clusters-require-a-meshidentity)) |
| 3.0 | [MeshLoadBalancingStrategy](policy:MeshLoadBalancingStrategy) `localityAwareness.crossZone` accepted only on a `to[]` entry targeting a [MeshMultiZoneService](policy:MeshMultiZoneService) ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshloadbalancingstrategy-cross-zone-settings-now-require-a-meshmultizoneservice-to-target)) |
| 3.0 | `Mesh.routing.localityAwareLoadBalancing` and `routing.zoneEgress` removed ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshspecroutinglocalityawareloadbalancing-removed)) |
| 3.0 | `Zone` names must be RFC 1035 labels; a Zone CP with a non-conforming `KUMA_MULTIZONE_ZONE_NAME` fails to start ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#names-of-mesh-zone-meshservice-meshexternalservice-and-meshmultizoneservice-must-be-rfc-1035-labels)) |
| 3.0 | `meshServices.mode: Exclusive` must be stored before the Global CP upgrade, or 2.x zones delete their generated `MeshService`s ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshservices-removed-from-the-mesh-schema)) |

### MeshService versus MeshMultiZoneService

A `kuma.io/service` destination load-balanced across every zone that had the service. A
[MeshService](policy:MeshService) belongs to one zone: traffic sent to it only goes to that zone.
To keep cross-zone load balancing or failover, create a `MeshMultiZoneService` that selects the
per-zone `MeshService`s, and target it from clients and from `MeshLoadBalancingStrategy`
([MeshService docs](https://kuma.io/docs/latest/networking/meshservice/),
[MeshMultiZoneService docs](https://kuma.io/docs/latest/networking/meshmultizoneservice/)).

## What kuma-migrator does

### Extraction

`extract` detects the control plane mode (`GET /config` with `--kumactl-context`, `KUMA_MODE`
with `--kube-context`) and filters on `kuma.io/origin`:

| CP mode | Kept | Skipped |
|---|---|---|
| `global` | `kuma.io/origin: global` and unlabelled resources | `kuma.io/origin: zone`: read-only copies of zone-created resources. Listed after extraction with the zone to extract from (its `kuma.io/zone` label) |
| `zone` | `kuma.io/origin: zone` and gateway-local kinds | Resources synced from the Global CP |
| `standalone`, unknown | Everything | Nothing |

`MeshGatewayInstance` and `MeshGatewayConfig` are zone-local and never synced to the Global CP.
Each context is written to its own directory (`<context>-global-ctx`, `<context>-zone-ctx`,
`<context>-standalone-ctx`). Details: [Extract](../guides/extract.md).

> **Note:** On Kong Mesh 3.0, `GET /config` needs the `VIEW_CONTROL_PLANE_METADATA` access type.
> Without it `extract` warns and continues with the mode unknown, so no origin filter is applied.
> See [Access control](access-control.md).

### Migration

| Input | `--to-latest v2` | `--to-latest v3` |
|---|---|---|
| Universal `ZoneIngress` / `ZoneEgress` | Copied unchanged (`SCANNED`), with an advisory: removed in 3.0, move to mesh-scoped zone proxies (available on 2.14) before the zone CP upgrade | Moved to `_manual/`. **Blocking** warning with the mesh-scoped zone proxy `Dataplane` to write, the `MeshZoneAddress` snippet built from `advertisedAddress`/`advertisedPort`, and the CRD deletion commands |
| `MeshLoadBalancingStrategy` `crossZone` on a `to[]` entry that is not `MeshMultiZoneService` | Copied | Warning: rejected by 3.0 |
| `Mesh.routing.localityAwareLoadBalancing` | Warning | Removed, with the `MeshMultiZoneService` note |
| Control-plane-owned labels (`kuma.io/origin`, `kuma.io/zone`, …) on a resource's own metadata | Kept | Removed |
| `to[]` selector of a zone-origin policy | `name`/`namespace` kept | Labels, with `kuma.io/zone` pinned from the policy's own `kuma.io/zone` label |
| `MeshExternalService` with a `kuma.io/zone` label | — | Warning: per-zone routing removed; every zone's egress dials the endpoint |
| Pod annotations `kuma.io/ingress` / `kuma.io/egress` | — | Warning |
| `Zone` name not an RFC 1035 label | Warning | Warning |

On Kubernetes, `ZoneIngress`, `ZoneEgress`, `Dataplane` and `Workload` are on the default skip
list (the control plane generates them), so only Universal zone proxies reach `migrate`.

## What it cannot do

- **Upgrade the topology.** Moving a Kubernetes-native Global CP to Universal + PostgreSQL,
  renaming `standalone` to `zone` in `KUMA_MODE`/`controlPlane.mode`, and redeploying zone proxies
  as mesh-scoped ones are control plane and Helm changes, not manifests. The extractor still
  labels a standalone context `-standalone-ctx` and accepts a Kubernetes Global CP; both
  describe topologies 3.0 does not support.
- **Write `MeshMultiZoneService`.** Which services must keep cross-zone behaviour is a design
  decision.
- **Create `MeshZoneAddress`.** It does not exist before 3.0 and must be created right after the
  zone CP upgrade. The `v3` warning contains the snippet.

## Manual steps

1. Extract from the Global CP and from every Zone CP. Check the zone-origin skip list printed by
   the Global extraction.
2. On 2.14, set `meshServices.mode: Exclusive` on every mesh (see [MeshService](meshservice.md)).
3. Create `MeshMultiZoneService`s for services that need cross-zone load balancing, and move
   `crossZone` settings onto `to[]` entries that target them.
4. Deploy mesh-scoped zone proxies **before** upgrading each zone CP. Kubernetes:
   `meshes[].ingress.enabled` / `meshes[].egress.enabled` in the Helm values. Universal: a
   `Dataplane` per mesh with `networking.listeners` of type `ZoneIngress`/`ZoneEgress`, with a
   token issued without `--proxy-type`. Then delete the `ZoneIngress`/`ZoneEgress` resources.
5. Move a Kubernetes-native Global CP to Universal with PostgreSQL. Rename `standalone` to `zone`.
6. Upgrade the Global CP, then each zone. On Universal, apply the `MeshZoneAddress` immediately
   after each zone CP upgrade. On Kubernetes, delete the four zone proxy CRDs after the upgrade.

## Example

Input — two files: a Universal zone ingress and a cross-zone load-balancing policy:

```yaml
type: ZoneIngress
name: zone-ingress-1
zone: zone-east
networking:
  address: 10.0.1.5
  advertisedAddress: 203.0.113.5
  port: 10001
  advertisedPort: 10001
---
apiVersion: kuma.io/v1alpha1
kind: MeshLoadBalancingStrategy
metadata:
  name: backend-failover
  namespace: kong-mesh-system
  labels:
    kuma.io/mesh: default
spec:
  targetRef:
    kind: Mesh
  to:
    - targetRef:
        kind: MeshService
        labels:
          kuma.io/display-name: backend
      default:
        localityAwareness:
          crossZone:
            failover:
              - to:
                  type: Any
```

With `--to-latest v2` both are copied unchanged. The `ZoneIngress` gets an advisory (shortened):

```text
⚠  ZoneIngress "zone-ingress-1": the ZoneIngress resource is removed in 3.0 (CRD, API and KDS sync),
   and a 3.0 control plane no longer serves kuma-dp --proxy-type=ingress|egress. 2.14 already
   supports the replacement, mesh-scoped zone proxies: … Move to them and delete this ZoneIngress
   before upgrading the zone control plane to 3.0. …
```

With `--to-latest v3` the `ZoneIngress` is moved to `_manual/other/ZoneIngress-zone-ingress-1.yaml`
and both documents are reported (shortened):

```text
⚠  BLOCKING — ZoneIngress "zone-ingress-1": the ZoneIngress resource is removed in 3.0 (CRD, API
   and KDS sync) … deploy mesh-scoped zone proxies (Kubernetes: meshes[].ingress.enabled /
   meshes[].egress.enabled; Universal: one regular Dataplane per mesh with
   `networking: {address: 10.0.1.5, listeners: [{type: ZoneIngress, address: 10.0.1.5, port: 10001}]}`,
   its token reissued without --proxy-type), then delete this ZoneIngress. …
⚠  ZoneIngress "zone-ingress-1": 3.0 takes a zone's public address only from MeshZoneAddress … on
   Universal, right after upgrading the zone control plane, apply one per mesh served cross-zone:
   `type: MeshZoneAddress, name: zone-proxy-ingress, mesh: <mesh>, labels: {kuma.io/origin: zone},
   spec: {address: 203.0.113.5, port: 10001}`.
⚠  MeshLoadBalancingStrategy "backend-failover": to[0].default.localityAwareness.crossZone is set
   but to[0].targetRef.kind is "MeshService", not MeshMultiZoneService — Kuma 3.0 rejects this at
   create/update … Remove crossZone, or retarget this entry at a MeshMultiZoneService.
```

## Related

- [Extract](../guides/extract.md)
- [MeshService](meshservice.md), [Policy targeting](policy-targeting.md)
- [External services](external-services.md) — egress and `MeshExternalService`

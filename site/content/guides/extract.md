---
title: Extract
description: Pull resources out of a running Kuma or Kong Mesh control plane, and the control plane mode rules that decide what is kept.
order: 50
---

`extract` reads every writable Kuma resource from a control plane and writes one YAML file per
resource. The output directory is the input of [`migrate`](migrate.md). `extract` only reads:
it never writes to the control plane.

## Connection modes

| | `--kube-context` | `--kumactl-context` |
|---|---|---|
| Tool | `kubectl` | `kumactl` CLI, or direct HTTPS for Konnect |
| Type discovery | Every `kuma.io/v1alpha1` CRD in the cluster | `GET <cp>/_resources` |
| Listing | `kubectl get <plural> -A -o json` per CRD | `kumactl get <type> [--mesh <mesh>] -o yaml` per type and mesh |
| Mode detection | `KUMA_MODE` on the control plane Deployment in `kuma-system` or `kong-mesh-system` | `GET <cp>/config` |
| Zone federation | `KUMA_MULTIZONE_ZONE_GLOBAL_ADDRESS` and `KUMA_MULTIZONE_ZONE_NAME` on the Deployment | `multizone.zone.globalAddress` and `multizone.zone.name` in `GET <cp>/config` |
| Environment | Kubernetes skip list always; `KUMA_ENVIRONMENT` and `KUMA_STORE_TYPE` recorded | `environment` and `store.type` fields of `GET <cp>/config` |
| Output format | Kubernetes, as returned by the API server | Universal by default; `--output-format kubernetes` converts it |
| Apply command in the report | `kubectl apply -f <dir>/` | one `kumactl apply -f <file>` per file |

Both paths exclude kinds whose name contains `Insight`, and kinds in the skip list (see
[Configuration file](cli.md#configuration-file)).

The `--kumactl-context` path resolves the context from `~/.kumactl/config` (or
`$KUMACTL_CONFIG`). When `$KUMACTL_CONFIG` is set, every `kumactl` call gets
`--config-file $KUMACTL_CONFIG`: `kumactl` itself only reads `~/.kumactl/config`. It ignores the
`readOnly` flag that `/_resources` returns: a control plane with `ApiServer.ReadOnly=true`
reports every type as read-only, which would extract nothing. `extract` never writes through
that API, so the flag does not apply to it.

### Generated resources

Resources the control plane generates and reconciles itself are skipped in every mode and
counted after extraction. They carry a `kuma.io/managed-by` label:

| Value | Resource |
|---|---|
| `k8s-controller` | `MeshService` generated from a Kubernetes `Service`; `MeshZoneAddress`; `Workload` |
| `meshservice-generator` | `MeshService` generated from Dataplane inbounds on Universal |
| `workload-generator` | `Workload` on Universal |

A hand-written `MeshService` has no such label and is extracted. The `kuma.io/env` label does
not tell the two apart: Kuma sets it on every `MeshService` created on a zone.

### Server-written fields

`extract` drops the fields the server writes, so each file can be applied again as it is:

| Format | Dropped | Why |
|---|---|---|
| both | `status` | computed by the control plane; `migrate` does not read it |
| Kubernetes | `metadata.resourceVersion` | a stale value makes `kubectl apply` fail with a conflict |
| Kubernetes | `metadata.uid`, `creationTimestamp`, `generation`, `selfLink`, `managedFields`, `deletionTimestamp`, `deletionGracePeriodSeconds` | owned by the API server |
| Kubernetes | `metadata.ownerReferences` | Kuma's mutating webhook adds the `Mesh` owner on create, with the UID of the `Mesh` in the target cluster. A copied reference carries the source cluster's UID: anywhere else, the garbage collector deletes the object as orphaned. Kuma 3.0 also rejects a `Mesh` owner that disagrees with the resource's mesh |
| Kubernetes | annotation `kubectl.kubernetes.io/last-applied-configuration` | written by client-side `kubectl apply` |
| Universal | `creationTime`, `modificationTime`, `kri` | computed by the control plane |

Labels and other annotations are kept, including the `kuma.io/*` labels the control plane
computes: the zone filter and `migrate` read them.

Code: [`pkg/extractor/kube.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/extractor/kube.go),
[`pkg/extractor/kumactl.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/extractor/kumactl.go).

## Output layout

```text
<output-dir>/
  <context>-global-ctx/            ← context name + control plane mode
    .kuma-migrator.json            ← how this context was extracted
    mesh-default/                  ← one directory per mesh, prefixed "mesh-"
      resiliency/
      routing/
      zero-trust/
      observability/
      mesh/
      other/
    mesh-prod/
      resiliency/
      mesh/Mesh-prod.yaml          ← a Mesh goes under its own directory
    global-scoped-resources/       ← resources with no mesh (Zone, HostnameGenerator, …)
      other/
  <context>-zone-ctx/              ← a Zone control plane
    mesh-default/
      resiliency/
  <context>-standalone-ctx/
  <context>-unknown-ctx/           ← mode could not be detected
```

The kind subfolders come from
[`pkg/resource/subfolder.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/resource/subfolder.go):

| Subfolder | Kinds |
|---|---|
| `resiliency` | `MeshCircuitBreaker`, `MeshRetry`, `MeshTimeout`, `MeshHealthCheck`, `MeshRateLimit`, `MeshFaultInjection` and their legacy equivalents |
| `routing` | `MeshHTTPRoute`, `MeshTCPRoute`, `MeshLoadBalancingStrategy`, `MeshPassthrough`, `MeshService`, `MeshExternalService`, `MeshMultiZoneService`, the gateway kinds, `ExternalService`, `TrafficRoute`, `VirtualOutbound` |
| `zero-trust` | `MeshTrafficPermission`, `MeshTLS`, `MeshIdentity`, `MeshTrust`, `MeshOPA`, `TrafficPermission`, `OPAPolicy`, `Secret`, `GlobalSecret` |
| `observability` | `MeshMetric`, `MeshTrace`, `MeshAccessLog`, `TrafficLog`, `TrafficTrace` |
| `mesh` | `Mesh`, `MeshProxyPatch`, `ProxyTemplate` |
| `other` | everything else |

`.kuma-migrator.json` records the extraction:

```json
{"tool":"kumactl","context":"konnect-global-prod","cpMode":"global","isKonnect":true,"environment":"kubernetes"}
{"tool":"kubectl","context":"zone-eu-west1-prod","cpMode":"zone","environment":"kubernetes","storeType":"kubernetes","federated":true}
```

`migrate` reads it to pick the skip list (`environment`), to write the right apply command
(`tool`, `context`, `isKonnect`) into the report, and, under `--to-latest v3`, to check the
control plane topology (`cpMode`, `environment`, `storeType`, `federated`; see
[Upgrade plans](upgrade-plans.md#control-plane-topology)). Without it, the report assumes
`kubectl` and skips the topology check. `federated` is written for zone control planes only.

Extract one mesh only:

```bash
kuma-migrator extract --kumactl-context global-cp --output-dir ./raw-policies --mesh default
```

The mesh of a resource is its `kuma.io/mesh` label, or the top-level `mesh` field: Universal
format stores it there, and so do the Kubernetes CRDs of the legacy kinds (`TrafficPermission`,
`MeshGatewayRoute`, …), which carry no label. A `Mesh` belongs to itself and is written to
`mesh-<name>/mesh/`; `--mesh` keeps only that mesh's `Mesh`. `migrate` also reads the older
layout, where `Mesh` files sit under `global-scoped-resources/mesh/`.

Running `extract` against several control planes with the same `--output-dir` is supported:
each control plane gets its own `<context>-<mode>-ctx/` directory.

## Control plane modes

`extract` detects the mode of the control plane and filters on the `kuma.io/origin` label.
The mode decides both the directory suffix and the filter. For a zone control plane, the filter
also depends on whether the zone is federated, that is, connected to a Global control plane.

| Mode | Directory | Kept | Skipped |
|---|---|---|---|
| `global` | `<context>-global-ctx` | `kuma.io/origin: global` and resources with no origin label | `kuma.io/origin: zone` (read-only copies synced up from zones), listed after extraction with the zone to target; `MeshGatewayInstance` and `MeshGatewayConfig` |
| `zone`, federated | `<context>-zone-ctx` | `kuma.io/origin: zone` and resources with no origin label (created on the zone, counted after extraction) | `kuma.io/origin: global` (copies synced from the Global control plane; extract them there), counted after extraction |
| `zone`, not federated | `<context>-zone-ctx` | everything | nothing |
| `standalone` | `<context>-standalone-ctx` | everything | nothing |
| unknown | `<context>-unknown-ctx` | everything | nothing |

A zone counts as federated when it has both a Global address and a zone name, the same test as
Kuma's `IsFederatedZoneCP`. The Helm chart always sets `KUMA_MULTIZONE_ZONE_GLOBAL_ADDRESS` in
zone mode, empty when `controlPlane.kdsGlobalAddress` is unset, so `extract` checks the value.

Kuma 3.0 removes the `standalone` mode: it becomes `zone`. The two already behave the same,
and a 2.x control plane started with `KUMA_MODE=standalone` already reports `zone` through
`GET /config`, so the `--kumactl-context` path sees it as a non-federated zone. `extract` keeps
the `-standalone-ctx` directory name for a control plane that the `--kube-context` path still
reads as `standalone`, so existing extract directories stay valid.

The filter is implemented in
[`pkg/extractor/extractor.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/extractor/extractor.go).
Kuma's multizone resource sync is described in the
[Kuma multizone docs](https://kuma.io/docs/latest/production/deployment/multi-zone/).

### Global control plane

Extract the Global control plane first:

```bash
# kumactl path
kuma-migrator extract --kumactl-context global-cp --output-dir ./raw-policies

# or the kubectl path, same control plane
kuma-migrator extract --kube-context global-cp --output-dir ./raw-policies
```

Console output on a Global control plane:

```text
CP mode:        global
Attached zones: zone-eu-west, zone-us-east
[INFO] MeshGatewayInstance and MeshGatewayConfig are zone-local and skipped here.
       Run extract against each Zone CP to capture gateway instances.
Found 24 writable resource type(s)
Extracted 83 resource(s) to ./raw-policies

  ⚠  Zone-origin resources skipped on Global CP — extract from their zone instead:
     MeshTimeout/my-timeout        →  zone: zone-eu-west
     MeshRateLimit/rate-limit-svc  →  zone: zone-us-east
```

### Zone control planes

Zone control planes hold what the Global control plane does not:

- resources created on the zone: producer policies, namespace-scoped consumer policies,
  anything applied directly to the zone cluster;
- `MeshGatewayInstance` and `MeshGatewayConfig`, which are never synced to the Global
  control plane.

```bash
kuma-migrator extract --kube-context prod-zone-eu --output-dir ./raw-policies
kuma-migrator extract --kube-context prod-zone-us --output-dir ./raw-policies
```

Console output on a federated Zone control plane:

```text
CP mode:        zone (eu-west), federated
[WARN] Extracting from a federated Zone CP — copies synced from Global (kuma.io/origin: global) are skipped.
       For a complete policy set, also run extract against the Global CP.
[INFO] Resources created on this zone are extracted: kuma.io/origin: zone, or no origin label
       (Kuma 2.x does not label legacy policies, ExternalService, ProxyTemplate, MeshGatewayRoute, …).
Extracted 12 resource(s) to ./raw-policies

[INFO] 4 resource(s) without a kuma.io/origin label kept (created on this zone): ExternalService 1, TrafficPermission 3
[INFO] 31 resource(s) synced from the Global CP (kuma.io/origin: global) skipped — extract them from the Global CP.
```

Only `kuma.io/origin: global` marks a synced copy. The Global-to-zone sync (KDS) sets it on
every resource it writes (`pkg/kds/context/context.go` in Kuma). `kuma.io/origin: zone` is set
by Kuma's defaulting webhook, which the 2.x Helm chart registers for the new-style policies,
`Mesh`, `Dataplane`, `MeshGateway` and the zone proxies only. The legacy policies,
`ExternalService`, `ProxyTemplate`, `MeshGatewayRoute`, `OPAPolicy`, `AccessRole` and
`AccessAudit` never get the label on Kubernetes, and a resource created before Kuma 2.7 may
lack it too. Both were created on the zone, so `extract` keeps them.

### Zone control plane without a Global

A zone control plane that is not connected to a Global control plane (`KUMA_MODE=zone` with no
Global address) owns every resource in its store, including those with no origin label, so
`extract` applies no origin filter:

```text
CP mode:        zone (eu-west), not federated
[INFO] This Zone CP is not connected to a Global CP: every resource in its store is its own.
       No kuma.io/origin filter is applied — all resources are extracted.
```

### Where gateway resources live

| Kind | Created on | Global extraction | Zone extraction |
|---|---|---|---|
| `MeshGateway`, `MeshGatewayRoute`, `MeshHTTPRoute`, `MeshTCPRoute` | Global (usual) | extracted | skipped (`kuma.io/origin: global`) |
| `MeshGateway`, `MeshGatewayRoute`, `MeshHTTPRoute`, `MeshTCPRoute` | Zone | not present | extracted (`kuma.io/origin: zone` or no origin label) |
| `MeshGatewayInstance`, `MeshGatewayConfig` | Zone only | skipped | extracted, with or without an origin label |
| any policy with `kuma.io/origin: zone` | Zone, synced read-only to Global | skipped, warning printed | extracted |

See [Gateways](../topics/gateways.md) and [Multizone](../topics/multizone.md).

## Environment detection

The `--kumactl-context` path reads `environment` (`kubernetes` or `universal`) from
`GET <cp>/config`, prints it, and records it in `.kuma-migrator.json`. On Universal,
`Dataplane`, `ZoneIngress` and `ZoneEgress` are hand-written, so they are extracted and
scanned. `Workload` is generated by the control plane in both environments and stays skipped.
The `--kube-context` path always uses the Kubernetes skip list. It records the Deployment's
`KUMA_ENVIRONMENT` and `KUMA_STORE_TYPE` in `.kuma-migrator.json` (the Helm chart sets both to
`kubernetes`; a Global control plane deployed with `controlPlane.environment: universal` on
PostgreSQL reports otherwise), which the 3.0 topology check reads.

If `GET <cp>/config` fails, `extract` prints a warning and continues in unknown mode: no origin
filter, Kubernetes skip list. On Kong Mesh 3.0, `/config` requires the
`VIEW_CONTROL_PLANE_METADATA` access type, so a `401` or `403` here usually means the role
behind the `kumactl` context lacks it. Check it with `kumactl get access-roles -o yaml`.

## Konnect

A control plane URL containing `api.konghq.com` is treated as Kong Konnect:

- **Authentication.** `kumactl` stores a Konnect personal access token as a header
  (`Authorization: Bearer kpat_…`) instead of `authType: tokens`. `extract` reads both forms.
  A system account token (`spat_…`) with read-only access is enough; see
  [Konnect read-only access](konnect-access.md).
- **Mode.** Konnect has no `/config` endpoint; it is always treated as a Global control plane.
- **Fetching.** Meshes, zones and resources are read with direct authenticated HTTPS calls to
  the Konnect API, not through the `kumactl` binary.
- **Format.** Konnect list endpoints return Universal format (`{total, items: [...]}`)
  regardless of the requested format. `--output-format kubernetes` converts each item.

## Universal format

`kumactl` and Konnect return Universal format: `type`, `name` and `mesh` at the top level, no
`apiVersion` or `metadata`. `extract` handles both single resources and
`{total: N, items: [...]}` lists. By default the files keep Universal format.
`--output-format kubernetes` converts them: `type` becomes `kind`, `name` becomes
`metadata.name`, `mesh` becomes the `kuma.io/mesh` label, and the control-plane-only fields
(`kri`, `creationTime`, `modificationTime`) are dropped. Documents that are already in
Kubernetes format are not changed.

`migrate` reads both formats.

## Self-hosted kumactl specifics

`/_resources` returns each type's API path, not its `kumactl` subcommand. The two are the same
for every type except `HealthCheck`: API path `health-checks`, subcommand `healthchecks`.
`extract` maps it. If `kumactl` still has no subcommand for a type (it answers
`unknown flag: --mesh`, or prints its usage), `extract` warns and reads that type from the
control plane API directly, following the pages. A type that fails does not stop the remaining
types.

Use `-k` / `--tls-skip-verify`, or `adminServer.tlsSkipVerify: true` in the configuration
file, for a control plane API with a self-signed certificate.

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
| Listing | `kubectl get <kind> -o yaml` per CRD | `kumactl get <type> [--mesh <mesh>] -o yaml` per type and mesh |
| Mode detection | `KUMA_MODE` on the control plane Deployment in `kuma-system` or `kong-mesh-system` | `GET <cp>/config` |
| Environment | Always `kubernetes` | `environment` field of `GET <cp>/config` |
| Output format | Kubernetes, as returned by the API server | Universal by default; `--output-format kubernetes` converts it |
| Apply command in the report | `kubectl apply -f <dir>/` | one `kumactl apply -f <file>` per file |

Both paths exclude kinds whose name contains `Insight`, and kinds in the skip list (see
[Configuration file](cli.md#configuration-file)).

The `--kumactl-context` path resolves the context from `~/.kumactl/config` (or
`$KUMACTL_CONFIG`). It ignores the `readOnly` flag that `/_resources` returns: a control plane
with `ApiServer.ReadOnly=true` reports every type as read-only, which would extract nothing.
`extract` never writes through that API, so the flag does not apply to it.

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
{"tool":"kumactl","context":"konnect-global-prod","cpMode":"global","isKonnect":true}
{"tool":"kubectl","context":"zone-eu-west1-prod","cpMode":"zone","environment":"kubernetes"}
```

`migrate` reads it to pick the skip list (`environment`) and to write the right apply command
(`tool`, `context`, `isKonnect`) into the report. Without it, the report assumes `kubectl`.

Extract one mesh only:

```bash
kuma-migrator extract --kumactl-context global-cp --output-dir ./raw-policies --mesh default
```

Running `extract` against several control planes with the same `--output-dir` is supported:
each control plane gets its own `<context>-<mode>-ctx/` directory.

## Control plane modes

`extract` detects the mode of the control plane and filters on the `kuma.io/origin` label.
The mode decides both the directory suffix and the filter.

| Mode | Directory | Kept | Skipped |
|---|---|---|---|
| `global` | `<context>-global-ctx` | `kuma.io/origin: global` and resources with no origin label | `kuma.io/origin: zone` (read-only copies synced up from zones), listed after extraction with the zone to target; `MeshGatewayInstance` and `MeshGatewayConfig` |
| `zone` | `<context>-zone-ctx` | `kuma.io/origin: zone`, plus gateway kinds with no origin label (`MeshGateway`, `MeshGatewayInstance`, `MeshGatewayConfig`, `MeshGatewayRoute`, `MeshHTTPRoute`, `MeshTCPRoute`) | `kuma.io/origin: global` (already captured from the Global control plane) and other resources with no origin label |
| `standalone` | `<context>-standalone-ctx` | everything | nothing |
| unknown | `<context>-unknown-ctx` | everything | nothing |

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

- policies created on the zone (`kuma.io/origin: zone`): producer policies, namespace-scoped
  consumer policies, anything applied directly to the zone cluster;
- `MeshGatewayInstance` and `MeshGatewayConfig`, which are never synced to the Global
  control plane.

```bash
kuma-migrator extract --kube-context prod-zone-eu --output-dir ./raw-policies
kuma-migrator extract --kube-context prod-zone-us --output-dir ./raw-policies
```

Console output on a Zone control plane:

```text
CP mode:        zone (eu-west)
[WARN] Extracting from a Zone CP. Only resources with kuma.io/origin: zone will be kept.
       For a complete policy set, also run extract against the Global CP.
[INFO] MeshGatewayInstance and MeshGatewayConfig are zone-local and will be extracted here.
```

### Where gateway resources live

| Kind | Created on | Global extraction | Zone extraction |
|---|---|---|---|
| `MeshGateway`, `MeshGatewayRoute`, `MeshHTTPRoute`, `MeshTCPRoute` | Global (usual) | extracted | skipped (`kuma.io/origin: global`) |
| `MeshGateway`, `MeshGatewayRoute`, `MeshHTTPRoute`, `MeshTCPRoute` | Zone | not present | extracted (no origin label) |
| `MeshGatewayInstance`, `MeshGatewayConfig` | Zone only | skipped | extracted, with or without an origin label |
| any policy with `kuma.io/origin: zone` | Zone, synced read-only to Global | skipped, warning printed | extracted |

See [Gateways](../topics/gateways.md) and [Multizone](../topics/multizone.md).

## Environment detection

The `--kumactl-context` path reads `environment` (`kubernetes` or `universal`) from
`GET <cp>/config`, prints it, and records it in `.kuma-migrator.json`. On Universal,
`Dataplane`, `ZoneIngress` and `ZoneEgress` are hand-written, so they are extracted and
scanned. `Workload` is generated by the control plane in both environments and stays skipped.
The `--kube-context` path always uses the Kubernetes skip list.

If `GET <cp>/config` fails, `extract` prints a warning and continues in unknown mode: no origin
filter, Kubernetes skip list. On Kong Mesh 3.0, `/config` requires the
`VIEW_CONTROL_PLANE_METADATA` access type, so a `401` or `403` here usually means the role
behind the `kumactl` context lacks it. Check it with `kumactl get access-roles -o yaml`.

## Konnect

A control plane URL containing `api.konghq.com` is treated as Kong Konnect:

- **Authentication.** `kumactl` stores a Konnect personal access token as a header
  (`Authorization: Bearer kpat_…`) instead of `authType: tokens`. `extract` reads both forms.
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

`/_resources` reports some types as mesh-scoped that `kumactl` then rejects with
`unknown flag: --mesh`. `extract` detects that error and fetches the type once without
`--mesh`. A type that fails this way does not stop the remaining types.

Use `-k` / `--tls-skip-verify`, or `adminServer.tlsSkipVerify: true` in the configuration
file, for a control plane API with a self-signed certificate.

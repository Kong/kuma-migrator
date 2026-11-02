---
title: Migrate
description: Preview the migration with --dry-run, write the migrated manifests, and the output layout they land in.
order: 60
---

`migrate` reads a directory of Kuma manifests, usually the output of [`extract`](extract.md),
transforms every document and writes the result plus a Markdown report. It never contacts a
control plane.

`--dry-run` runs the same transforms and the same deprecation scan. It changes two things:
no YAML is written, and the report is `migration-plan.md` instead of `migration-report.md`.

## Dry run

Point `--input-dir` at the whole extracted tree. Filter to one mesh with `--mesh` if needed.

```bash
# All meshes
kuma-migrator migrate --input-dir ./raw-policies --output-dir ./migrated --dry-run

# One mesh
kuma-migrator migrate --input-dir ./raw-policies --output-dir ./migrated --mesh default --dry-run

# Target 3.0 instead of the latest 2.x
kuma-migrator migrate --input-dir ./raw-policies --output-dir ./migrated --to-latest v3 --dry-run
```

The console lists every file with its scenario, where it came from and where it would be
written, followed by warnings and errors. `migration-plan.md` in the output directory holds the
same information. See [Output and reports](output-and-reports.md).

## Write the output

Run the same command without `--dry-run`:

```bash
kuma-migrator migrate --input-dir ./raw-policies --output-dir ./migrated
kuma-migrator migrate --input-dir ./raw-policies --output-dir ./migrated --mesh default
kuma-migrator migrate --input-dir ./raw-policies --output-dir ./migrated --to-latest v3
```

This writes the migrated YAML and `migration-report.md`. The input directory is never
modified. Use a separate output directory per target, for example `./migrated-v2` and
`./migrated-v3`: the two targets produce different files, and a run does not delete files left
by a previous run in the same directory.

See [Target versions](target-version.md) for what `--to-latest` changes, and
[Apply](apply.md) for the order to apply the output in.

## Mesh filter

`--mesh <name>` keeps files under `mesh-<name>/` and drops files under other `mesh-*/`
directories. Files that are not under a mesh directory (`global-scoped-resources/`, a flat
input tree) are always processed.

## Input layout

`migrate` recognises the context and mesh from the path of each file:

| Path below `--input-dir` | Context directory | Mesh |
|---|---|---|
| `<sub>/file.yaml` | none | none |
| `<dir>/<sub>/file.yaml` | `<dir>` | none |
| `<ctx>/global-scoped-resources/<sub>/file.yaml` | `<ctx>` | none |
| `<ctx>/mesh-<mesh>/<sub>/file.yaml` | `<ctx>` | `<mesh>` |

`<sub>` is one of the kind subfolders (`resiliency`, `routing`, `zero-trust`, `observability`,
`mesh`, `other`). A hand-built input tree works as long as it follows one of these shapes.
Every file ending in `.yaml` or `.yml` is read; a file can hold several documents separated by
`---`.

## Output layout

The output mirrors the input layout. Each document is written to the kind subfolder of its
**output** kind, named `<Kind>-<name>.yaml`, or `<Kind>-<namespace>-<name>.yaml` when it has a
namespace.

```text
migrated/
  konnect-global-prod-global-ctx/
    mesh-default/
      resiliency/
        MeshTimeout-my-timeout.yaml
      routing/
      zero-trust/
      observability/
      mesh/
        Mesh-default.yaml
      other/
    global-scoped-resources/
      routing/
        Gateway-gateways-edge.yaml
        HTTPRoute-gateways-edge.yaml
      other/
        ReferenceGrant-apps-gateways-edge.yaml
    _manual/                         ← --to-latest v3 only
      mesh-default/routing/TrafficRoute-route-all.yaml
  zone-eu-west1-prod-zone-ctx/
    mesh-default/
      resiliency/
  migration-report.md
```

| Output | Location |
|---|---|
| Kuma resources from a mesh directory | `<ctx>/mesh-<mesh>/<sub>/` |
| Kuma resources with no mesh directory | `<ctx>/global-scoped-resources/<sub>/` |
| Gateway API output (`Gateway`, `HTTPRoute`, `TCPRoute`, `GatewayClass`, `ReferenceGrant`) | `<ctx>/global-scoped-resources/<sub>/`, even when the source came from a mesh directory. `ReferenceGrant` has no kind subfolder of its own and lands in `other/` |
| Documents a 3.0 control plane cannot accept (`--to-latest v3`) | `<ctx>/_manual/<same relative path>` |
| Flat input (no context directory) | `<sub>/` |

Gateway API resources are Kubernetes-native objects applied to the clusters that run the
gateway, not Kuma resources stored by the Global control plane. They are grouped under
`global-scoped-resources/` so that the per-mesh directories only hold Kuma resources. A
converted gateway from a Zone control plane lands in `<zone-ctx>/global-scoped-resources/routing/`.
See [Gateways](../topics/gateways.md).

`MeshHTTPRoute` and `MeshTCPRoute` are not converted. They pass through to
`<ctx>/mesh-<mesh>/routing/` unchanged. See [MeshHTTPRoute](policy:MeshHTTPRoute).

One input document can produce several output documents:

| Input | Extra output |
|---|---|
| Legacy policy with several specific sources | one policy per source, named `<name>-0`, `<name>-1`, … |
| `Mesh*` policy with both `from[]` and `to[]` | the `to[]` half as `<name>-outbound` |
| `Mesh` with embedded observability or passthrough | `MeshMetric`, `MeshTrace`, `MeshAccessLog`, `MeshPassthrough` |
| `Mesh` under `--to-latest v3` | `MeshTLS` (permissive backend), `MeshPassthrough` |
| Inline OpenTelemetry endpoint under `--to-latest v3` | `MeshOpenTelemetryBackend` in `other/` |
| `MeshGatewayRoute` with a cross-namespace backend | `ReferenceGrant` |
| `MeshGatewayInstance` under `--to-latest v2` | `GatewayClass` + `MeshGatewayConfig` |

Errored documents are copied unchanged. Under `--to-latest v2` the copy stays in the apply
tree, since applying it back to the 2.x control plane it came from changes nothing. Under
`--to-latest v3` it goes to `_manual/`, which no apply step uses.

## Scenarios

Each document is classified into one scenario, which decides the transform:

| Scenario | Trigger | Output |
|---|---|---|
| Legacy | `sources`/`destinations`/`selectors`, or one of the legacy kinds | `Mesh*` policy with `targetRef`/`to`/`from`/`rules`/`default` |
| Subset | `MeshSubset` with service tags, or `MeshService` with a Kuma-generated `_svc_` name | `Dataplane`/`MeshService` references |
| Rules | `MeshTimeout`, `MeshCircuitBreaker`, `MeshRateLimit`, `MeshAccessLog`, `MeshTLS` with `from[]` | `rules[]` |
| Mesh | `Mesh` with embedded observability or passthrough | standalone policies |
| ExternalService | `ExternalService` | `MeshExternalService` |
| Gateway | `MeshGateway`, `MeshGatewayInstance`, `MeshGatewayRoute` | Gateway API |
| OPAPolicy | Kong Mesh `OPAPolicy` | `MeshOPA` |
| Passthrough | anything already current | unchanged, still scanned |

The per-kind behaviour on each target is on the [policy pages](../policies/_index.md). How the
pipeline is put together is in [Architecture](architecture.md).

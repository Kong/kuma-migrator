---
title: Output and reports
description: What migrate prints while it runs, and what the Markdown report contains.
order: 90
---

## Console output

Each input file gets one line: a status icon, a scenario label, the mesh (omitted for
global-scoped files) and the file name. Two faint lines below show the input path (`←`) and
the first output path (`→`), relative to `--input-dir` and `--output-dir`. Warnings follow as
`⚠`, errors as `✗`.

Output of `migrate --dry-run` on a Global and a Zone context (warnings shortened):

```text
  kuma-migrator  migrate --dry-run

  ✓  MESH              default  default.yaml
       ←  kind-global-global-ctx/mesh-default/mesh/default.yaml
       →  kind-global-global-ctx/mesh-default/mesh/Mesh-default.yaml
       ⚠  Mesh.spec.meshServices.mode set to Exclusive — required for MeshService-based policies to take effect (Kuma 2.9+). …
       ⚠  Mesh "default": spec.mtls is the legacy mTLS/identity model and is removed in Kuma 3.0. …
  ✓  LEGACY            default  timeout-default.yaml
       ←  kind-global-global-ctx/mesh-default/resiliency/timeout-default.yaml
       →  kind-global-global-ctx/mesh-default/resiliency/MeshTimeout-backend-timeout.yaml
  ⚠  PARTIAL ERROR     default  route.yaml
       ←  kind-global-global-ctx/mesh-default/routing/route.yaml
       →  kind-global-global-ctx/mesh-default/routing/TrafficRoute-route-all-default.yaml
       ✗  TrafficRoute requires manual migration to MeshHTTPRoute or MeshTCPRoute
  ✓  SUBSET            default  meshretry.yaml
       ←  kind-zone-eu-zone-ctx/mesh-default/resiliency/meshretry.yaml
       →  kind-zone-eu-zone-ctx/mesh-default/resiliency/MeshRetry-demo-backend-retry.yaml
  ✓  RULES             default  meshtimeout.yaml
       ←  kind-zone-eu-zone-ctx/mesh-default/resiliency/meshtimeout.yaml
       →  kind-zone-eu-zone-ctx/mesh-default/resiliency/MeshTimeout-demo-backend-inbound.yaml
       ⚠  MeshTimeout "backend-inbound": from[] migrated to rules[] (Kuma 2.10+). …

  ────────────────────────────────────────────────────────────
  5 file(s) processed  ·  4 migrated  ·  0 already migrated  ·  0 skipped  ·  1 error(s)


Report written to: migrated/migration-plan.md
```

| Label | Meaning |
|---|---|
| `LEGACY`, `SUBSET`, `RULES`, `MESH`, `EXTERNAL SERVICE`, `GATEWAY`, `OPA` | Migrated, by scenario |
| `ALREADY MIGRATED` | Passed through unchanged; already uses the current API |
| `SKIP` | Not a Kuma policy, or a kind in the skip list |
| `ERROR` | The file could not be read or written |
| `PARTIAL ERROR` | At least one document in the file could not be converted; the rest were |

A file labelled `SKIP` or `ALREADY MIGRATED` can still carry warnings: the label reflects
scenario detection, while the deprecation scan runs on every document. A Kubernetes
`Deployment` is not a Kuma policy (`SKIP`), but a deprecated `kuma.io/*` annotation on it is
still reported. A `HostnameGenerator` passes through (`ALREADY MIGRATED`), but its
`spec.template` is still checked against the 2.14 validation.

## Report sections

`migrate --dry-run` writes `migration-plan.md`; `migrate` writes `migration-report.md`. Both
are written to `--output-dir` by
[`pkg/migrator/report.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/report.go).

| Section | Content |
|---|---|
| Header | Input and output directories, the target (`v2` or `v3`). Under `v3`, a banner that the output must not be applied to a 2.x control plane. Under `--dry-run`, a banner that no files were written |
| Summary | Files processed, migrated, already migrated, skipped, errors |
| Files That Would Be Migrated (`--dry-run`) / Migrated Files | One table per `<context>/<subfolder>/`, for example `kind-global-global-ctx/resiliency/`, with the warnings of each file. Errored files are grouped separately |
| Already Migrated | Files passed through unchanged, with their warnings |
| Skipped | Non-policy files, with their warnings |
| Action Items | Errors, workload service address mappings, deprecated annotations. Present only when one of them is non-empty |
| Apply Checklist | Ordered steps with the `kubectl` or `kumactl` commands for each context. A three-step version under `--dry-run`. See [Apply](apply.md) |
| Original Resources to Delete | Resources whose kind changed, with delete commands in a collapsible block. Under `v3`, kinds 3.0 removes are listed in the next section instead |
| Delete Before Upgrading to 3.0 | `v3` only. See below |

### Delete Before Upgrading to 3.0

Under `--to-latest v3` this section lists every source object of a kind 3.0 removes: the
legacy policies, `ExternalService`, `OPAPolicy`, `MeshGlobalRateLimit`, `MeshGateway*`, and
`ZoneIngress`/`ZoneEgress`. For each it shows whether it was converted, where its `_manual/`
copy is if it was not, and the delete command for the tool the context was extracted with.

```markdown
| Source file | Kind | Name | Namespace / mesh | Replacement |
|---|---|---|---|---|
| `kind-global-global-ctx/mesh-default/resiliency/timeout-default.yaml` | `Timeout` | `backend-timeout` | mesh `default` | converted (see the apply steps) |
| `kind-global-global-ctx/mesh-default/routing/route.yaml` | `TrafficRoute` | `route-all-default` | mesh `default` | none — manual migration, copy at `kind-global-global-ctx/_manual/mesh-default/routing/TrafficRoute-route-all-default.yaml` |
```

The Apply Checklist gets a matching step before the Global control plane upgrade. See
[Upgrade plans](upgrade-plans.md) for where it fits.

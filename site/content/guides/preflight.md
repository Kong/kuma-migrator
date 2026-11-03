---
title: Live control plane audit
description: Audit a running 2.x control plane for Kong Mesh 3.0 readiness with kuma-migrator preflight or extract --preflight, and read the result in the migrate report.
order: 57
---

`migrate` reads manifests. Some 3.0 blockers are not in any manifest: the control plane and
zone versions, control plane settings, zones that are offline, the default policies the control
plane created, and features in the Envoy configuration of running proxies. The readiness audit
reads those from the running control plane.

The audit is the engine of `kong-mesh-v3-preflight`, written by the Kong Mesh engineering team
([Kong/kong-mesh-v3-readiness](https://github.com/Kong/kong-mesh-v3-readiness), Apache 2.0).
kuma-migrator imports it as a Go library, so it needs no separate install.

| | `migrate --to-latest v3` | Readiness audit |
|---|---|---|
| Reads | Extracted YAML files | The control plane REST API |
| Writes | Rewritten manifests, report, Apply Checklist | `v3-preflight.json`, `v3-preflight.html` |
| Covers | Fields and kinds to rewrite or remove | Versions, CP settings, zones, stored resources, Envoy config |
| Changes anything | Output files only | Nothing, read-only |

Run both before an upgrade. Several checks overlap (a `Mesh` without `meshServices.mode:
Exclusive`, `from[]` on `MeshTimeout`); the migrate output fixes those, the audit confirms what
is still on the control plane.

## Audit while extracting

Add `--preflight` to a `--kumactl-context` extraction. The audit runs after the files are
written, against the same control plane and token:

```bash
kuma-migrator extract --kumactl-context prod-global -o ./raw-policies --preflight
kuma-migrator migrate --input-dir ./raw-policies --output-dir ./migrated --to-latest v3
```

The report files land in the context directory, next to `.kuma-migrator.json`:

```text
raw-policies/
  prod-global-global-ctx/
    .kuma-migrator.json
    v3-preflight.json
    v3-preflight.html
    mesh-default/...
```

A failed audit is printed as a warning and does not fail the extraction. `migrate` reads only
`.yaml` files, so the report files do not affect the migration.

`--preflight` needs the control plane API, so it is refused with `--kube-context`. Port-forward
the control plane and run `preflight --address` instead (below).

## Audit on its own

```bash
# the current kumactl context
kuma-migrator preflight -o ./audit

# a named context, Konnect included
kuma-migrator preflight --kumactl-context prod-global -o ./audit

# an address, e.g. a port-forwarded Kubernetes control plane
kubectl port-forward -n kong-mesh-system svc/kong-mesh-control-plane 5681 &
kuma-migrator preflight --address http://localhost:5681 --token "$TOKEN" -o ./audit
```

Point `-o` at an extract context directory to have `migrate` pick the result up.

Audit the Global and every zone. On a Global, the audit also reads each zone's settings as the
zone reported them over KDS.

## What it needs

| Item | Detail |
|---|---|
| Token | Read access to the API. On Kong Mesh, `/config` (the CP settings) is access-controlled: the `GENERATE_DATAPLANE_TOKEN` access type on 2.x, `VIEW_CONTROL_PLANE_METADATA` on 3.0. Without it the settings are not audited and the report is inconclusive |
| Latest 2.x patch | Read from the [kumahq/kuma releases](https://github.com/kumahq/kuma/releases) (one GitHub API request). Set `--latest-version 2.14.7` to skip the request, for example without internet access. Without a value the version check does not run and the report is inconclusive |
| Envoy inspection | Off by default. `--inspect-dataplanes N` (`--preflight-inspect-dataplanes` on `extract`) fetches up to N config dumps, one request per proxy |

On **Konnect** the Global's own settings and version are managed by Kong, so the audit treats the
control plane as a Global, skips those two, and audits every zone. The
[read-only token](konnect-access.md) is enough. The Konnect path has not yet been run against a
real Konnect organization.

## Reading the result

The terminal shows the status and every blocker. The HTML report has the details, examples
named by KRI, coverage gaps and the manual checks the API cannot answer.

| Status | Meaning |
|---|---|
| `clean` | No blocker, every check ran |
| `blockers` | At least one blocker, every check ran |
| `inconclusive` | At least one check could not run (coverage gap). Blockers may be listed too |
| `failed` | The audit could not run: unreachable address, not a Kuma API. The command exits non-zero |

`preflight` exits 0 whenever a report is produced, blockers included, so read the status rather
than the exit code.

The `migrate` report adds a **Live Control Plane Audit** section per context directory that has
a `v3-preflight.json`, on both targets: status, control plane, counts, the blocker table and the
coverage gaps. See [Output and reports](output-and-reports.md).

## Flags

See [CLI reference](cli.md#preflight).

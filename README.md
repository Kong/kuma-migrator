# kuma-migrator

**[kong.github.io/kuma-migrator](https://kong.github.io/kuma-migrator/)**

A CLI that migrates Kuma and Kong Mesh policy manifests across the API changes in the 2.x line
and into 3.0 — from legacy `sources`/`destinations` policies through to `MeshService`, the Rules
API, and Gateway API CRDs.

It pulls resources straight from a running control plane, rewrites what can be rewritten safely,
and reports what cannot — with the reason, so you can judge it rather than trust it blindly.

<p align="center">
  <img src="docs/assets/migration-overview.svg" alt="Three examples of kuma-migrator's mechanical translations: a Dataplane tag becomes a structured targetRef, a legacy policy kind like Timeout becomes MeshTimeout, and a MeshSubset tag selector becomes a Dataplane/MeshService reference" width="900">
</p>

## Why

The Kuma and Kong Mesh policy API changed substantially across 2.0–2.14, and 3.0 removes a number
of fields that 2.14 still accepts. Some of those removals are silent: the manifest applies
cleanly and simply stops doing what it used to. `kuma-migrator` automates the mechanical parts,
flags the parts that need a human, and writes a Markdown report of every change it made or would
make.

## Install

```bash
brew install --cask bcollard/kuma-migrator/kuma-migrator
```

Pre-built binaries for Linux, macOS, and Windows are on the
[Releases](https://github.com/Kong/kuma-migrator/releases) page.
See [Installation](docs/installation.md) for all options.

## Quick start

```
extract → migrate → apply
```

```bash
# 1. Pull resources from your control plane (Global CP first)
kuma-migrator extract --kube-context prod-global --output-dir ./raw-policies

# 2. Write the migrated manifests + migration-report.md
#    (add --dry-run to preview first — writes migration-plan.md, no YAML)
kuma-migrator migrate --input-dir ./raw-policies --output-dir ./migrated

# 3. Apply them yourself, in the order the report gives you — this step is
#    manual; kuma-migrator never touches your control plane's actual state
kubectl apply -f ./migrated/prod-cp-global-ctx/mesh-default/resiliency/
```

Run step 2 with `--dry-run` first and read the plan, then read the report before applying —
the apply **order matters**, and `Mesh` resources go last because they switch on
`meshServices.mode: Exclusive`.

## Choosing a target: `--to-latest v2|v3`

`migrate` (including its `--dry-run` mode) takes `--to-latest v2` (default, latest 2.x) or `v3`
(3.0). One output cannot serve both lines: 3.0 removes fields 2.14 still requires, and some 3.0
replacements (`MeshOpenTelemetryBackend`, `SecureDataSource`) do not exist before 2.14.

```bash
kuma-migrator migrate --input-dir ./raw-policies --output-dir ./migrated --to-latest v3
```

`v2` keeps the output applicable to a 2.x control plane and reports 3.0 removals as
forward-looking advisories. `v3` rewrites what it safely can and flags the rest.
See [Choosing a target version](docs/target-version.md).

## What it handles

| Scenario | Migration |
|---|---|
| **Legacy** | `sources`/`destinations`/`selectors` policies → `targetRef`/`to`/`from`/`rules`, with the `conf` body rewritten to the successor's schema — except `TrafficRoute` (ambiguous HTTP vs TCP) and `VirtualOutbound` (no single successor), which have no mechanical conversion and are reported for manual migration |
| **Subset** | `MeshSubset` with service tags → `Dataplane`/`MeshService` |
| **Rules** | Deprecated `from[]` → `rules[]`, for `MeshTimeout`/`MeshCircuitBreaker`/`MeshRateLimit`/`MeshAccessLog`/`MeshTLS` only (Kuma 2.10+) — `MeshTrafficPermission`/`MeshFaultInjection` use a different, SPIFFE-identity-based `rules[]` shape and are **not** auto-converted; see [MeshTrafficPermission modes](docs/meshtrafficpermission-modes.md) |
| **Mesh** | `Mesh` CRD observability → standalone `MeshMetric`/`MeshTrace`/`MeshAccessLog` |
| **ExternalService** | `ExternalService` → `MeshExternalService` |
| **GW** | `MeshGateway` and route CRDs → Gateway API |
| **OPAPolicy** | Kong Mesh `OPAPolicy` → `MeshOPA` |

Alongside these it scans every output document for deprecated and removed fields across the 2.x
line and 3.0. A few are repaired in place; the rest are reported with the reason they are not
rewritten automatically — usually because the manifest alone does not carry enough information,
and guessing would silently change behaviour.

Full list: [Migration paths and deprecation warnings](docs/migration-paths.md).

## Documentation

| Page | What it covers |
|---|---|
| [Installation](docs/installation.md) | Homebrew, binaries, from source |
| [CLI reference](docs/cli-reference.md) | Every command, flag, and the config file |
| [Extracting from a control plane](docs/extract.md) | CP-mode rules, output layout, Konnect, Universal vs Kubernetes format |
| [Plan and migrate](docs/plan-and-migrate.md) | Dry run, output layout, Gateway API placement |
| [Choosing a target version](docs/target-version.md) | `--to-latest` and the checks that change with it |
| [Applying the migrated manifests](docs/apply.md) | Apply order, and cleaning up changed kinds |
| [Migration paths and deprecations](docs/migration-paths.md) | Everything detected, fixed or flagged |
| [MeshTrafficPermission modes](docs/meshtrafficpermission-modes.md) | `from[]` vs `rules[]`, and why it is not mechanical |
| [Transformation examples](docs/transformation-examples.md) | Before-and-after YAML per scenario |
| [Console output and reports](docs/output-and-reports.md) | What you see, and what the report contains |
| [Notes and caveats](docs/notes.md) | Behaviour worth knowing before relying on the output |

Index: [docs/](docs/README.md).

## Changelog

Actively maintained — 17 releases since April 2026, five of them in the last four days. Full
history: [Releases](https://github.com/Kong/kuma-migrator/releases) ·
[compare view](https://github.com/Kong/kuma-migrator/compare).

| Version | Date | Summary |
|---|---|---|
| [v0.7.0](https://github.com/Kong/kuma-migrator/releases/tag/v0.7.0) | 2026-09-04 | **Breaking:** removed the `plan` command — use `migrate --dry-run` instead, same flags and output. Also redesigned the pipeline diagram to mark `apply` as a manual step, not a command |
| [v0.6.3](https://github.com/Kong/kuma-migrator/releases/tag/v0.6.3) | 2026-09-04 | The migration report's "Already Migrated"/"Skipped" sections no longer silently drop deprecation warnings attached to those files; fixed `HostnameGenerator` being misclassified as "Skipped" |
| [v0.6.2](https://github.com/Kong/kuma-migrator/releases/tag/v0.6.2) | 2026-09-03 | Release artifacts now carry a SLSA build provenance attestation, verifiable with `gh attestation verify` |
| [v0.6.1](https://github.com/Kong/kuma-migrator/releases/tag/v0.6.1) | 2026-09-02 | Closed four `--to-latest v3` gaps found auditing upstream kuma/kong-mesh: `Dataplane` `BUILTIN` gateway type, `MeshOPA` targetRef/DataSource fixes, `MeshLoadBalancingStrategy` `crossZone`, `MeshHTTPRoute` catch-all |
| [v0.6.0](https://github.com/Kong/kuma-migrator/releases/tag/v0.6.0) | 2026-09-01 | Fixed `Gateway.spec.gatewayClassName` to resolve a real `GatewayClass` instead of a dead controllerName string; stopped emitting a dead built-in-gateway pair under v3 |
| [v0.5.0](https://github.com/Kong/kuma-migrator/releases/tag/v0.5.0) | 2026-08-14 | Legacy policy `conf` bodies are converted to the successor's schema instead of copied verbatim |
| [v0.4.0](https://github.com/Kong/kuma-migrator/releases/tag/v0.4.0) | 2026-08-10 | Added `--to-latest v2\|v3` targeting, closed 2.13/2.14 deprecation gaps, fixed `OPAPolicy` handling and an extract data-loss bug |
| [v0.3.1](https://github.com/Kong/kuma-migrator/releases/tag/v0.3.1) | 2026-06-30 | Added Kuma 2.11–2.14 deprecation scans and the `MeshIdentity`/`MeshTrust` `mtls` advisory |
| [v0.2.8](https://github.com/Kong/kuma-migrator/releases/tag/v0.2.8) | 2026-04-25 | Fixed mesh/zone listing on Konnect control planes |
| [v0.2.7](https://github.com/Kong/kuma-migrator/releases/tag/v0.2.7) | 2026-04-24 | `migrate`/`plan` output shows mesh name and file paths |
| [v0.2.6](https://github.com/Kong/kuma-migrator/releases/tag/v0.2.6) | 2026-04-22 | Zone-origin skip on Global CP; normalised old Kuma-internal `MeshService` names |
| [v0.2.5](https://github.com/Kong/kuma-migrator/releases/tag/v0.2.5) | 2026-04-22 | `--output-format` flag, context-first output directory layout, per-environment skip lists |
| [v0.2.4](https://github.com/Kong/kuma-migrator/releases/tag/v0.2.4) | 2026-04-21 | Universal-environment detection, split Kubernetes/Universal skip lists |
| [v0.2.3](https://github.com/Kong/kuma-migrator/releases/tag/v0.2.3) | 2026-04-20 | Added `-k`/`--tls-skip-verify`; stopped trusting `readOnly` from `/_resources` |
| [v0.2.2](https://github.com/Kong/kuma-migrator/releases/tag/v0.2.2) | 2026-04-19 | Full Universal-format support for Konnect extract and migrate |
| [v0.2.1](https://github.com/Kong/kuma-migrator/releases/tag/v0.2.1) | 2026-04-18 | Colorized CLI output |
| [v0.2.0](https://github.com/Kong/kuma-migrator/releases/tag/v0.2.0) | 2026-04-17 | Initial CLI implementation, then moved from a personal repo into the Kong org |

## Development

```bash
make test      # run unit tests
make build     # compile binary to ./dist/kuma-migrator
make snapshot  # local GoReleaser dry-run (requires goreleaser)
make lint      # run golangci-lint
make clean     # remove ./dist/
```

Contributions: see [CONTRIBUTING.md](CONTRIBUTING.md).

## Requirements

- Go 1.24+ (to build from source)
- `kubectl` and/or `kumactl` on `PATH`, depending on which extract mode you use
- For Kong Mesh control planes, use the Kong Mesh build of `kumactl` — the OSS Kuma binary
  refuses to talk to a Kong Mesh CP

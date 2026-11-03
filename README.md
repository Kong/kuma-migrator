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
See [Installation](https://kong.github.io/kuma-migrator/docs/guides/installation/) for all options.

## Quick start

```
extract → migrate → apply
```

```bash
# 1a. Pull resources via kumactl (self-hosted OR Konnect-hosted, zonal or Global CP)
kuma-migrator extract --kumactl-context konnect-global-prod --output-dir ./raw-policies

# 1b. ...or via kubectl (self-hosted, Kubernetes-backed CP)
kuma-migrator extract --kube-context zone-eu-west1-prod --output-dir ./raw-policies

# 2. Write the migrated manifests + migration-report.md
#    (add --dry-run to preview first — writes migration-plan.md, no YAML)
kuma-migrator migrate --input-dir ./raw-policies --output-dir ./migrated

# Now read migration-report.md — it gives the exact apply order for your
# setup (Mesh resources go last: they switch on meshServices.mode: Exclusive)

# 3a. Apply them yourself if you used --kumactl-context (self-hosted or
#     Konnect) — kumactl before 2.14 rejects a directory, so apply one file at a time
kumactl config control-planes switch --name konnect-global-prod
kumactl apply -f ./migrated/konnect-global-prod-global-ctx/mesh-default/resiliency/MeshTimeout-my-timeout.yaml

# 3b. ...or via kubectl if you extracted via --kube-context — this step
#     is manual; kuma-migrator never touches your control plane's state
kubectl apply -f ./migrated/zone-eu-west1-prod-zone-ctx/mesh-default/resiliency/
```

## Choosing a target: `--to-latest v2|v3`

`migrate` (including its `--dry-run` mode) takes `--to-latest v2` (default, latest 2.x) or `v3`
(Kuma / Kong Mesh 3.0, checked against the released 3.0.0 upgrade notes). One output cannot serve
both lines: 3.0 removes fields 2.14 still requires, and some 3.0 replacements
(`MeshOpenTelemetryBackend`, `SecureDataSource`) do not exist before 2.14.

```bash
kuma-migrator migrate --input-dir ./raw-policies --output-dir ./migrated --to-latest v3
```

`v2` keeps the output applicable to a 2.x control plane and reports 3.0 removals as
forward-looking advisories. `v3` rewrites what it safely can (for example `Mesh.mtls` removal,
labels-only `targetRef`s, inline OTel endpoints → `MeshOpenTelemetryBackend`), flags the rest,
writes what 3.0 cannot accept to a separate `_manual/` subtree, and lists the objects to delete
before the upgrade.
See [Target versions](https://kong.github.io/kuma-migrator/docs/guides/target-version/).

## What it handles

| Scenario | Migration |
|---|---|
| **Legacy** | `sources`/`destinations`/`selectors` policies → `targetRef`/`to`/`from`/`rules`, with the `conf` body rewritten to the successor's schema — except `TrafficRoute` (ambiguous HTTP vs TCP) and `VirtualOutbound` (no single successor), which have no mechanical conversion and are reported for manual migration |
| **Subset** | `MeshSubset` with service tags → `Dataplane`/`MeshService` |
| **Rules** | Deprecated `from[]` → `rules[]`, for `MeshTimeout`/`MeshCircuitBreaker`/`MeshRateLimit`/`MeshAccessLog`/`MeshTLS` only (Kuma 2.10+) — `MeshTrafficPermission` uses a different, SPIFFE-identity-based `rules[]` shape and is **not** auto-converted (`MeshFaultInjection` only under `v3`, when every source is `kind: Mesh`); see [Access control](https://kong.github.io/kuma-migrator/docs/topics/access-control/) |
| **Mesh** | `Mesh` CRD observability and passthrough → standalone `MeshMetric`/`MeshTrace`/`MeshAccessLog`/`MeshPassthrough`; under `v3` also removes the fields 3.0 drops (`mtls`, `routing.*`) |
| **ExternalService** | `ExternalService` → `MeshExternalService` |
| **GW** | `MeshGateway` and `MeshGatewayRoute` → Gateway API (with a `ReferenceGrant` for cross-namespace backends). `MeshHTTPRoute`/`MeshTCPRoute` pass through unchanged |
| **OPAPolicy** | Kong Mesh `OPAPolicy` → `MeshOPA` |

Alongside these it scans every output document for deprecated and removed fields across the 2.x
line and 3.0. A few are repaired in place; the rest are reported with the reason they are not
rewritten automatically — usually because the manifest alone does not carry enough information,
and guessing would silently change behaviour.

Per-kind behaviour on each target: [Policy reference](https://kong.github.io/kuma-migrator/docs/policies/).

## Documentation

Full documentation: **[kong.github.io/kuma-migrator/docs](https://kong.github.io/kuma-migrator/docs/)**.

| Page | What it covers |
|---|---|
| [Installation](https://kong.github.io/kuma-migrator/docs/guides/installation/) | Homebrew, binaries, from source |
| [Getting started](https://kong.github.io/kuma-migrator/docs/guides/getting-started/) | The three steps of a migration, then end-to-end runs for Kubernetes, multizone, Kong Mesh, Konnect and Universal |
| [Upgrade plans](https://kong.github.io/kuma-migrator/docs/guides/upgrade-plans/) | Upgrade paths, which target to use, the sequence for 3.0, rollback |
| [CLI reference](https://kong.github.io/kuma-migrator/docs/guides/cli/) | Every command, flag, and the config file |
| [Extract](https://kong.github.io/kuma-migrator/docs/guides/extract/) | CP-mode rules, output layout, Konnect, Universal vs Kubernetes format |
| [Konnect read-only access](https://kong.github.io/kuma-migrator/docs/guides/konnect-access/) | A read-only system account token for Konnect, from `kuma-migrator konnect token-script` |
| [Migrate](https://kong.github.io/kuma-migrator/docs/guides/migrate/) | Dry run, output layout, Gateway API placement |
| [Target versions](https://kong.github.io/kuma-migrator/docs/guides/target-version/) | `--to-latest` and the checks that change with it |
| [Apply](https://kong.github.io/kuma-migrator/docs/guides/apply/) | Apply order, and cleaning up changed kinds |
| [Output and reports](https://kong.github.io/kuma-migrator/docs/guides/output-and-reports/) | What you see, and what the report contains |
| [Topics](https://kong.github.io/kuma-migrator/docs/topics/) | One area of the mesh (identity, gateways, multizone, access control, …) across every kind |
| [Policy reference](https://kong.github.io/kuma-migrator/docs/policies/) | One page per resource kind: what is detected, fixed or flagged on each target |

## Changelog

Actively maintained — 17 releases since April 2026, five of them in the last four days. Full
history: [Releases](https://github.com/Kong/kuma-migrator/releases) ·
[compare view](https://github.com/Kong/kuma-migrator/compare).

| Version | Date | Summary |
|---|---|---|
| [v0.8.0](https://github.com/Kong/kuma-migrator/releases/tag/v0.8.0) | 2026-09-18 | **Breaking:** `MeshHTTPRoute`/`MeshTCPRoute` are no longer converted to Gateway API. Both are current policies on 2.x *and* 3.0 — Kuma compiles `HTTPRoute`/`GRPCRoute` **into** a `MeshHTTPRoute` — and Kuma has never reconciled Gateway API `TCPRoute` at all, so that output was dead on every version. They now pass through unchanged, and the 3.0 `404` catch-all advisory fires on the `MeshHTTPRoute` itself |
| [v0.7.1](https://github.com/Kong/kuma-migrator/releases/tag/v0.7.1) | 2026-09-18 | Corrected the `--to-latest v3` delegated-gateway guidance: 3.0 *removes* the `kuma.io/gateway` marking rather than renaming it, so the tool now points at `traffic.kuma.io/exclude-inbound-ports` and `kuma.io/ignore`. Top-level `targetRef` checks apply 3.0's uniform `Mesh`/`Dataplane`-only rule to every policy |
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

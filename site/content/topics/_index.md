---
title: Topics
description: Migration planning by area of the mesh — what changed upstream, what kuma-migrator does on each target, and the manual steps left to you.
order: 30
---

A topic page covers one area of a mesh — identity, gateways, multizone, access control — across
every resource kind that touches it. Use it to plan the migration of that area: what changed
upstream between 2.x, 2.14 and 3.0, what `kuma-migrator` rewrites with `--to-latest v2` and
`--to-latest v3`, what it cannot rewrite and why, and the manual steps in the order to run them.

The [policy pages](../policies/_index.md) cover the same ground from the other direction: one
page per resource kind, with its status on each target and the files that handle it. A topic
page links to the policy pages of every kind it discusses.

## How to read a topic page

| Section | Content |
|---|---|
| What changed upstream | The upstream changes with the version that introduced them, linked to the Kuma [`UPGRADE.md`](https://github.com/kumahq/kuma/blob/master/UPGRADE.md), the [Kong Mesh upgrade notes](https://developer.konghq.com/mesh/version-specific-upgrade-notes/) or the docs page they come from |
| What kuma-migrator does | A `v2` / `v3` comparison table |
| What it cannot do | The information the fix needs that a manifest does not carry |
| Manual steps | The remaining work, in the order to run it |
| Example | Input and output from a real `migrate` run. Output is never written by hand |

Every example was produced by running `kuma-migrator migrate --input-dir in --output-dir out
--to-latest v2|v3` on the input shown. Warnings quoted on these pages are shortened; the full
text is in `migration-report.md`.

## Pages

| Topic | Covers |
|---|---|
| [Meshes](meshes.md) | The `Mesh` resource: observability, passthrough, routing and `meshServices` fields |
| [mTLS and workload identity](mtls-identity.md) | `Mesh.mtls` → `MeshIdentity` + `MeshTrust`, `PERMISSIVE` → `MeshTLS` |
| [External PKI](external-pki.md) | Kong Mesh Vault, ACM Private CA and cert-manager backends → `MeshIdentity` `Extension` provider |
| [MeshService](meshservice.md) | `kuma.io/service` tags → `MeshService`, `meshServices.mode`, hostnames, names |
| [Multizone](multizone.md) | Control plane modes, origin filtering, zone proxies, cross-zone load balancing |
| [Gateways, Gateway API and GAMMA](gateways.md) | Built-in gateway removal, `MeshGateway*` → Gateway API, `MeshHTTPRoute` on 3.0 |
| [External services](external-services.md) | `ExternalService` → `MeshExternalService`, TLS data sources, passthrough |
| [Access control](access-control.md) | `MeshTrafficPermission` `from[]` vs `rules[]`, `MeshOPA`, Kong Mesh RBAC |
| [Observability](observability.md) | Metrics, tracing and access logs; OpenTelemetry backends |
| [Policy targeting](policy-targeting.md) | `targetRef` kinds per position and target, policy roles, control-plane-owned labels |
| [Dataplanes](dataplanes.md) | Universal `Dataplane` fields and Pod annotations removed in 3.0 |
| [Legacy policies](legacy-policies.md) | The 12 source/destination policies and their `Mesh*` successors |

## Before you start

- Pick the target first. [Choosing a target version](../guides/target-version.md) explains why
  the `v2` and `v3` outputs differ and must not be mixed.
- Kong Mesh supports upgrading at most two minor versions at a time, so a fleet on 2.8 goes
  2.8 → 2.10 → 2.12 → 2.14 before 3.0. Most 3.0 preparation has to be applied on 2.14, because
  the replacements (`MeshIdentity` extensions, `MeshOpenTelemetryBackend`, the
  `SecureDataSource` shape) do not exist earlier.
- Extract from every control plane that holds resources, not only the Global one. See
  [Multizone](multizone.md) and [Extract](../guides/extract.md).

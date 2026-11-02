---
title: Policy reference
description: One page per Kuma and Kong Mesh resource kind — what kuma-migrator does with it under --to-latest v2 and v3, and the upstream changes behind it.
order: 40
nav_title: Policies
---

Each resource kind that `kuma-migrator migrate` reads or emits has a page. The page states what
the tool does with an input of that kind under each target, the kinds it writes, the upstream
changes that drive it, and the source files that implement it.

The [topic pages](../topics/_index.md) cover the same ground by area of the mesh (identity,
gateways, multizone). Use a topic page to plan a migration and a policy page to look up one kind.

## How to read the tables

Each group table below has one row per kind:

| Column | Content |
|---|---|
| Kind | The resource kind as it appears in `kind` (Kubernetes) or `type` (Universal); Kong Mesh-only kinds are marked |
| v2 | Status with `--to-latest v2` (the default), followed by the kinds written for that input |
| v3 | Status with `--to-latest v3`, followed by the kinds written for that input |

No kind after the status means nothing is written to the apply tree.

On a policy page, the **Behaviour per target** section repeats the status per target and
explains it: which fields are rewritten, what is reported and why, what errors, and what you
must do by hand. The **Timeline** section lists the upstream deprecations, removals and behaviour changes, oldest
first, as published in the Kuma [`UPGRADE.md`](https://github.com/kumahq/kuma/blob/master/UPGRADE.md)
and the [Kong Mesh upgrade notes](https://developer.konghq.com/mesh/version-specific-upgrade-notes/).

## Status values

| Status | Meaning |
|---|---|
| `converted` | Rewritten into a different kind (see Output) |
| `rewritten` | Same kind, fields changed automatically |
| `passthrough` | Copied unchanged |
| `warned` | Copied, with warnings you must act on |
| `error` | Not converted; reported as manual work (under v3, the original is written to `_manual/`) |
| `removed` | The kind no longer exists on that target; the report lists it for deletion |
| `skipped` | Not extracted or not processed by default (see the skip lists in `pkg/config/config.go`) |

A status describes the usual case. Most kinds have checks that only fire on some inputs, such as
a deprecated `targetRef` kind or an invalid name; the page notes list them.

## v2 and v3 targets

`--to-latest v2` produces output a 2.14 control plane accepts. Changes that 3.0 requires are
reported as advisories and never applied, because some of them (labels-only references,
`SecureDataSource`, `MeshOpenTelemetryBackend`) are invalid or unsupported before 2.14.

`--to-latest v3` rewrites what the manifest has enough information for and reports the rest as
blocking work. Some rules apply to every Kuma document under v3 and are not repeated on each
page:

- Control-plane-owned labels (`kuma.io/origin`, `kuma.io/zone`, `kuma.io/policy-role`, …) are
  removed, because 3.0 rejects a supplied value that differs from the computed one.
- In Mesh* policies, `name`/`namespace`/`mesh` on references to `MeshService`,
  `MeshExternalService`, `MeshMultiZoneService` and `MeshHTTPRoute` become labels.
- A top-level `targetRef.kind` other than `Mesh` or `Dataplane` is reported.
- Sources of kinds 3.0 removes are listed under "Delete Before Upgrading to 3.0" in the report.
  Those not converted are copied to `_manual/`, which no apply step reads.

See [target versions](../guides/target-version.md) for how to choose between them.

## Source of these tables

The tables and the per-kind pages are generated from `site/data/policies.yaml`. Edit that file,
not the generated HTML, and run `make site`.

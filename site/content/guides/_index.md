---
title: Guides
description: Install kuma-migrator, run it against your control planes, plan an upgrade, and apply the result.
order: 20
---

The guides cover the tool itself: how to install it, how to run each command, what it writes,
and how to use its output to plan and carry out an upgrade. For what happens to a specific
resource kind, see the [policy pages](../policies/_index.md); for one area of the mesh across
kinds, see the [topics](../topics/_index.md).

## Getting started

| Guide | What it covers |
|---|---|
| [Installation](installation.md) | Homebrew, release archives, building from source, verifying a release |
| [Typical usage](usage.md) | End-to-end runs for a single Kubernetes zone, multizone, self-hosted Kong Mesh, Konnect and Universal |
| [Upgrade plans](upgrade-plans.md) | Supported upgrade paths, which target to use when, the sequence for 3.0, rollback |

## Running the tool

| Guide | What it covers |
|---|---|
| [CLI reference](cli.md) | Every command, flag and configuration file setting |
| [Extract](extract.md) | Reading a control plane: connection modes, control plane modes, origin filtering, layout |
| [Migrate](migrate.md) | `--dry-run`, the real run, input and output layout, scenarios |
| [Target versions](target-version.md) | `--to-latest v2` and `v3`: what each rewrites, warns about or refuses |
| [Apply](apply.md) | The Apply Checklist, apply order, removing originals, the `_manual/` subtree |
| [Output and reports](output-and-reports.md) | Console output and the sections of the Markdown report |

## Reference

| Guide | What it covers |
|---|---|
| [Transformation examples](examples.md) | Before-and-after YAML per scenario, on both targets |
| [Architecture](architecture.md) | How the pipeline is built, with links to the source |
| [Notes and caveats](notes.md) | Behaviour to know before relying on the output |

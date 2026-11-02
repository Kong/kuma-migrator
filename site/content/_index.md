---
title: Documentation
description: How to use kuma-migrator to move Kuma and Kong Mesh manifests across the 2.x API changes and into 3.0.
order: 0
---

`kuma-migrator` reads the resources of a running Kuma or Kong Mesh control plane, rewrites
them for the latest 2.x line or for 3.0, and writes a report of every change, every warning
and every step left to you. It never writes to a control plane: you review the output and
apply it yourself.

## Quick start

```bash
brew install --cask bcollard/kuma-migrator/kuma-migrator
kuma-migrator extract --kube-context prod-eks --output-dir ./raw-policies
kuma-migrator migrate --input-dir ./raw-policies --output-dir ./migrated --dry-run
kuma-migrator migrate --input-dir ./raw-policies --output-dir ./migrated
less ./migrated/migration-report.md   # follow its Apply Checklist
```

Add `--to-latest v3` to `migrate` to target Kuma / Kong Mesh 3.0. Start with
[Getting started](guides/getting-started.md) for your setup.

## How the documentation is organised

| Section | Organised by | Use it to |
|---|---|---|
| [Guides](guides/_index.md) | Task | Install the tool, run `extract` and `migrate`, read the report, plan an upgrade, apply the output |
| [Topics](topics/_index.md) | Area of the mesh | Plan one area (identity, gateways, multizone, access control, observability, …) across every kind that touches it |
| [Policies](policies/_index.md) | Resource kind | Look up what the tool does with one kind on each target, the kinds it emits, and the upstream changes behind it |

A typical path: [Upgrade plans](guides/upgrade-plans.md) for the order of work, the topics
for the areas your plan flags, and the policy pages for the kinds in your report.

---
title: Upgrade plans
description: Plan a Kuma or Kong Mesh upgrade with kuma-migrator, from the next 2.x minor to 3.0, and read the report sections that drive it.
order: 30
---

`kuma-migrator` does not upgrade control planes. It tells you which manifests must change
before, during and after an upgrade, writes the changed manifests, and lists what to delete.
This page puts those outputs in the order an upgrade needs them.

## Supported upgrade paths

Kuma and Kong Mesh support upgrades of at most two minor versions at a time. The same rule
applies between `kuma-dp` and `kuma-cp`, between Zone and Global control planes, and to the
data in the store ([Kuma upgrades](https://kuma.io/docs/latest/production/upgrades-tuning/upgrades/),
[Kong Mesh upgrade](https://developer.konghq.com/mesh/upgrade/)).

| From | Path |
|---|---|
| 2.8 | 2.8 → 2.10 → 2.12 → 2.14 → 3.0 |
| 2.9 | 2.9 → 2.11 → 2.13 → 2.14 → 3.0 |
| 2.12 | 2.12 → 2.14 → 3.0 |
| 2.13 LTS | 2.13 → 2.14 → 3.0 |

Latest releases as of 2026-10-30: Kuma 3.0.1 and Kong Mesh 3.0.1, with 2.14.7 and 2.13.12
(LTS) patch releases.

Reach the latest 2.14 patch before 3.0. Several preparation steps need it:
`MeshOpenTelemetryBackend` exists from 2.14, `MeshExternalService` accepts the 3.0
`SecureDataSource` shape from 2.14.6
([Upgrade to 2.14.6](https://github.com/kumahq/kuma/blob/v3.0.1/UPGRADE.md#upgrade-to-2146)),
and Kong Mesh's latest 2.14 patch accepts the renamed KDS authentication settings
([Kong Mesh upgrade notes](https://developer.konghq.com/mesh/version-specific-upgrade-notes/)).

3.0 also changes the supported topology. Check these before planning manifest work:

| Change | Action | Source |
|---|---|---|
| A Kubernetes-native Global control plane is not supported | Move the Global control plane to Universal with a PostgreSQL store before upgrading | [Kuma `UPGRADE.md`](https://github.com/kumahq/kuma/blob/v3.0.1/UPGRADE.md#global-control-plane-on-kubernetes-is-no-longer-supported) |
| `standalone` mode is removed | Rename it to `zone` | [Kuma `UPGRADE.md`](https://github.com/kumahq/kuma/blob/v3.0.1/UPGRADE.md#standalone-mode-removed) |
| Meshes no longer get default policies | Write the policies you relied on | [Kuma `UPGRADE.md`](https://github.com/kumahq/kuma/blob/v3.0.1/UPGRADE.md#meshes-no-longer-come-with-default-policies) |

## Which target for which step

| Upgrade step | Run | Apply on |
|---|---|---|
| 2.x → a later 2.x | `--to-latest v2` | the 2.x control plane, after or before the step as the report says |
| 2.14 → 3.0, preparation | `--to-latest v2` | 2.14 |
| 2.14 → 3.0, final manifests | `--to-latest v3` | 3.0, after the control planes are upgraded |

`v2` output stays valid on 2.x and reports 3.0 removals as advisories. `v3` output removes
fields that 2.14 still requires, so it is never applied before the upgrade. See
[Target versions](target-version.md).

## Recommended sequence for 3.0

1. **Upgrade to the latest 2.14 patch**, two minors at a time. At each step, run
   `extract` and `migrate --to-latest v2 --dry-run` and fix what the plan reports.
2. **Extract every control plane** into one directory: the Global control plane first, then
   each zone. See [Extract](extract.md).

   ```bash
   kuma-migrator extract --kumactl-context global-prod        --output-dir ./raw-policies
   kuma-migrator extract --kube-context    zone-eu-west1-prod --output-dir ./raw-policies
   kuma-migrator extract --kube-context    zone-us-east1-prod --output-dir ./raw-policies
   ```

3. **Dry-run both targets** into separate directories. The v3 plan is the list of work the
   3.0 upgrade needs.

   ```bash
   kuma-migrator migrate -i ./raw-policies -o ./plan-v2 --dry-run
   kuma-migrator migrate -i ./raw-policies -o ./plan-v3 --dry-run --to-latest v3
   ```

4. **Migrate to v2 and apply on 2.14.** This replaces the kinds 3.0 removes with their
   successors while the control plane still accepts both: legacy policies become `Mesh*`
   policies, `ExternalService` becomes `MeshExternalService`, `OPAPolicy` becomes `MeshOPA`,
   built-in gateways become Gateway API resources. Follow the Apply Checklist, skipping its
   control plane upgrade steps, and check traffic.

   ```bash
   kuma-migrator migrate -i ./raw-policies -o ./migrated-v2
   ```

5. **Move every mesh to `MeshIdentity`.** On 3.0 `Mesh.mtls` issues nothing: a mesh whose
   only identity source is `mtls` serves plaintext and `MeshTrafficPermission`/`MeshTLS` stop
   applying
   ([Kuma `UPGRADE.md`](https://github.com/kumahq/kuma/blob/v3.0.1/UPGRADE.md#meshmtls-no-longer-produces-mtls)).
   This is a CA cutover the manifests cannot describe; the tool reports it and does not
   convert it. Rewrite `MeshTrafficPermission` `from[]` to `rules[]` at the same time. See
   [mTLS and identity](../topics/mtls-identity.md) and
   [Access control](../topics/access-control.md).
6. **Set `meshServices.mode: Exclusive` on every mesh** before the Global control plane is
   upgraded. A 2.x zone reads a missing field as `Disabled` and stops serving `MeshService`
   outbounds and DNS
   ([Kuma `UPGRADE.md`](https://github.com/kumahq/kuma/blob/v3.0.1/UPGRADE.md#meshservices-removed-from-the-mesh-schema)).
   The `Mesh` files in `./migrated-v2` set it; apply them last, as the checklist says, once
   every workload and policy uses `MeshService` names. See
   [MeshService](../topics/meshservice.md).
7. **Migrate to v3** and review the report.

   ```bash
   kuma-migrator migrate -i ./raw-policies -o ./migrated-v3 --to-latest v3
   ```

   Resolve every entry under **Action Items → Errors**. Each `_manual/` file needs a
   decision: rewrite it by hand, or drop it.
8. **Delete the removed kinds on 2.14**, using **Delete Before Upgrading to 3.0** in
   `./migrated-v3/migration-report.md`. Delete an object only after its replacement from
   step 4 is applied and checked.
9. **Upgrade the Global control plane to 3.0**, then **each Zone control plane**.
10. **Apply the v3 output** to the 3.0 control planes, following the Apply Checklist of
    `./migrated-v3/migration-report.md`. Nothing under `_manual/` is applied.
11. **Restart the data planes** so they pick up the 3.0 `kuma-dp`.
12. **Clean up.** Once every zone runs 3.0, drop `meshServices` from the `Mesh` manifests you
    keep. Remove the input files and any originals still listed under
    **Original Resources to Delete**.

For 2.x-to-2.x steps, steps 2 to 4 are the whole plan: extract, dry-run with `v2`, migrate,
apply, then upgrade.

## Use --dry-run to plan

`migrate --dry-run` runs every transform and every check without writing YAML. The plan
answers the planning questions directly:

| Question | Where to look in `migration-plan.md` |
|---|---|
| How much converts automatically | **Summary**: migrated vs errors |
| What needs a person | **Action Items → Errors**, and each warning that says *BLOCKING* or *manual* |
| What 3.0 deletes | **Delete Before Upgrading to 3.0** (v3 only) |
| What workloads must change | **Workload Service Address Mappings**, **Deprecated Annotations** |
| Which files change per context | **Files That Would Be Migrated**, grouped by context and subfolder |

Compare the two plans to see what 3.0 adds on top of the 2.x work:

```bash
diff <(grep -o '⚠ [^|]*' ./plan-v2/migration-plan.md | sort -u) \
     <(grep -o '⚠ [^|]*' ./plan-v3/migration-plan.md | sort -u)
```

Re-run the dry run after each fix until the error count is zero. It reads local files only,
so it is safe to run as often as needed.

## Read the Apply Checklist

The checklist in `migration-report.md` is generated from the run's output. Its steps and their
order are described in [Apply](apply.md). Two points matter for an upgrade plan:

- The checklist always contains the "Upgrade the Global Control Plane" and "Upgrade Zone
  Control Planes" steps. In a `v2` run used to prepare on the current version, skip them.
- In a `v3` run, the checklist orders the steps as: fix errors, delete the removed kinds,
  upgrade, apply policies, apply `Mesh` last. The `Mesh` step there comes after the upgrade,
  but `meshServices.mode: Exclusive` must already be set before it (step 6 above).

## Read Delete Before Upgrading to 3.0

This section is written by `--to-latest v3` runs only. One row per source object of a kind
3.0 removes:

| Column | Meaning |
|---|---|
| Source file | The extracted file, relative to `--input-dir` |
| Kind, Name, Namespace / mesh | The object to delete |
| Replacement | `converted (see the apply steps)`, or `none — manual migration` with the path of the `_manual/` copy |

The collapsed block under the table holds the delete commands, `kubectl delete` or
`kumactl delete` depending on how the context was extracted. 3.0 rejects these kinds and does
not migrate stored objects, so they must be deleted on 2.14. For a row marked `converted`,
the replacement must come from the `v2` output applied in step 4: the `v3` replacement is
not valid on 2.14.

## Rollback

| Point | How to go back |
|---|---|
| After applying `v2` output on 2.x | Delete the new-kind resources and keep the originals. The originals stay in the control plane until you delete them, and the extracted tree holds a copy. |
| After deleting removed kinds | Re-apply them from the extracted tree on 2.14. |
| After the `MeshIdentity` cutover | Upstream gives no rolling path back once proxies on 3.0 stop trusting legacy mesh certificates ([Kuma `UPGRADE.md`](https://github.com/kumahq/kuma/blob/v3.0.1/UPGRADE.md#meshmtls-no-longer-produces-mtls)). Plan the cutover so it is complete and verified on 2.14. |
| After upgrading to 3.0 | Store compatibility follows the two-minor rule. A 3.0 control plane drops `Mesh.mtls` the next time it writes a `Mesh`; re-apply the `Mesh` from the extracted tree after rolling back. |

Keep the extracted tree, both plans and both reports under version control. They record what
was in the control plane before the upgrade and what changed.

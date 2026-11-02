---
title: Apply
description: The order to apply the migrated manifests in, why the order matters, and how to remove the originals afterwards.
order: 80
---

`kuma-migrator` never applies anything. You apply the output with `kubectl` or `kumactl`,
following the **Apply Checklist** in `migration-report.md`. The checklist is built from the
actual output: steps that do not apply to your run are left out and the rest are renumbered.
It is written by `writeApplyChecklist` in
[`pkg/migrator/report.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/report.go).

## Apply Checklist steps

The steps below appear in this order when present.

| Step | When it appears |
|---|---|
| Fix errors | the report's **Action Items → Errors** is not empty |
| Update workload env vars | `kuma.io/service`-encoded addresses were found in Deployment or StatefulSet env vars |
| Fix deprecated annotations | `"yes"`/`"no"` values were found on `kuma.io/*` annotations |
| Delete the resources 3.0 removes | `--to-latest v3` and the report has a **Delete Before Upgrading to 3.0** section |
| Upgrade the Global control plane | always |
| Upgrade the Zone control planes | always |
| Install Gateway API CRDs | a `MeshGateway`, `MeshGatewayInstance` or `MeshGatewayRoute` was converted |
| Apply policies | always, per context and kind subfolder |
| Apply `Mesh` CRs last | a `Mesh` is in the output |
| Verify traffic health | always |
| Delete the original policy files | always |
| Plan your next upgrade | always |

### Fix errors

Nothing after this step is safe to apply until every error is resolved. Each error names the
document and the reason; the original is in the output tree (`v2`) or under `_manual/` (`v3`).

### Update workload env vars and annotations

Workloads that address services by their `kuma.io/service` name (for example
`backend_demo_svc_3001`) stop resolving once the mesh runs `meshServices.mode: Exclusive`. The
report lists each env var with its replacement address. Annotation values `"yes"`/`"no"` on
`kuma.io/*` annotations must become `"true"`/`"false"`.

### Delete the resources 3.0 removes

`--to-latest v3` only. Delete these on the 2.x control plane: 3.0 rejects these kinds and
does not migrate stored objects. If one still serves traffic (`ExternalService`,
`MeshGateway*`, `OPAPolicy`), first apply its replacement on 2.14 from a `--to-latest v2` run of
the same input, check it, then delete the original. See [Upgrade plans](upgrade-plans.md).

### Upgrade the control planes

Upgrade the Global control plane, then the Zone control planes. Kuma and Kong Mesh support
upgrades of at most two minor versions at a time, for example 2.10 → 2.12 → 2.14. See the
[Kuma upgrade guide](https://kuma.io/docs/latest/production/upgrades-tuning/upgrades/) and the
[Kong Mesh upgrade guide](https://developer.konghq.com/mesh/upgrade/).

### Install Gateway API CRDs

Only when a built-in gateway resource was converted. Install on every Kubernetes cluster that
runs a gateway, never on a Universal zone:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/latest/download/standard-install.yaml
```

When the output contains a `TCPRoute`, install the experimental channel instead (a superset of
standard):

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/latest/download/experimental-install.yaml
```

See [Gateways](../topics/gateways.md).

### Apply policies

One context at a time, in the order the report lists: Global and standalone contexts before
Zone contexts, and within a context `resiliency`, `routing`, `zero-trust`, `observability`,
then `other`. The command depends on how the context was extracted, which the report reads
from `.kuma-migrator.json`:

- **`extract --kumactl-context`**, including Konnect. `kumactl` does not accept a directory
  for `-f`, so the report lists one command per file, after a reminder to select the context:

  ```bash
  kumactl config use-context konnect-global-prod
  kumactl apply -f ./migrated/konnect-global-prod-global-ctx/mesh-default/resiliency/MeshTimeout-my-timeout.yaml
  kumactl apply -f ./migrated/konnect-global-prod-global-ctx/mesh-default/resiliency/MeshRetry-my-retry.yaml
  ```

- **`extract --kube-context`**. One directory apply per kind subfolder:

  ```bash
  kubectl apply -f ./migrated/zone-eu-west1-prod-zone-ctx/mesh-default/resiliency/
  ```

Without `.kuma-migrator.json` (a hand-built input tree), the report uses `kubectl`.

`global-scoped-resources/` holds both Kuma global resources and the Gateway API output.
Gateway API objects go to the Kubernetes cluster that runs the gateway: a Zone cluster.
A gateway that was created directly on a Zone control plane is extracted into that zone's
context, and its Gateway API output lands in `<zone-ctx>/global-scoped-resources/routing/`.

### Apply Mesh CRs last

The migrated `Mesh` sets `spec.meshServices.mode: Exclusive`. That turns off routing by
`kuma.io/service` tags, so applying it before every policy and workload is migrated breaks
any workload still addressed by a tag. If a `Mesh` was not in the input, set the mode by hand:

```bash
kubectl patch mesh <name> --type merge -p '{"spec":{"meshServices":{"mode":"Exclusive"}}}'
```

> **v3:** `meshServices.mode: Exclusive` must be set on every mesh **before** the Global
> control plane is upgraded to 3.0: a 2.x zone reads a missing field as `Disabled` and stops
> serving `MeshService` outbounds
> ([Kuma `UPGRADE.md`](https://github.com/kumahq/kuma/blob/master/UPGRADE.md)). The checklist
> places this step after the upgrade steps, so apply it from a `--to-latest v2` run on 2.14
> first. [Upgrade plans](upgrade-plans.md) gives the full sequence.

### Verify, then clean up

Check service-to-service traffic in every mesh and watch your observability stack. Then
delete the original input files; `migrate` never modifies them. Resources whose kind changed
also need deleting from the control plane, see below. If you have not reached the target
version yet, repeat `extract`, `migrate --dry-run` and `migrate` for the next upgrade step.

`--dry-run` writes a three-step checklist instead: fix errors and warnings, run `migrate`,
follow the checklist of the real report.

## Remove originals whose kind changed

`kubectl apply` of the new kind does not remove the old object. After traffic is verified,
delete the originals of every resource whose kind changed:

| Old kind | New kind |
|---|---|
| `Timeout`, `Retry`, `TrafficPermission`, … | `MeshTimeout`, `MeshRetry`, `MeshTrafficPermission`, … |
| `ExternalService` | `MeshExternalService` |
| `MeshGateway` | `Gateway` |
| `MeshGatewayInstance` | `GatewayClass` + `MeshGatewayConfig` (`v2` only) |
| `MeshGatewayRoute` | `HTTPRoute` / `TCPRoute` |
| `OPAPolicy` | `MeshOPA` |

The report section **Original Resources to Delete** lists them with ready-to-run delete
commands. Under `--to-latest v3` the kinds 3.0 removes are listed in **Delete Before Upgrading
to 3.0** instead, because they must go before the upgrade, not after it.

## Manual subtree

Under `--to-latest v3`, two kinds of documents go to a `_manual/` subtree instead of the apply
tree:

- originals whose conversion failed (`TrafficRoute`, `VirtualOutbound`, `MeshGatewayInstance`,
  legacy `TrafficPermission`, …);
- documents that pass through as a kind 3.0 removes (`MeshGlobalRateLimit`, Universal
  `ZoneIngress`/`ZoneEgress`).

```text
migrated/
  <context>-global-ctx/
    mesh-default/resiliency/...          ← apply tree
    global-scoped-resources/routing/...  ← apply tree
    _manual/
      mesh-default/routing/TrafficRoute-route-all.yaml
```

No checklist step applies `_manual/`: `kubectl apply -f <dir>/` is not recursive, and the
`kumactl` file list never includes these paths. Each file is listed in **Action Items →
Errors** or in **Delete Before Upgrading to 3.0** with what to do about it. Under `v2` there is
no `_manual/` subtree: errored originals stay in the apply tree, since they came from the 2.x
control plane they are applied back to.

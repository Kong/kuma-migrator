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
| Set `meshServices.mode: Exclusive` on every mesh | `--to-latest v3` (always) |
| Finish the `MeshIdentity` cutover | `--to-latest v3` and a `Mesh` in the input sets `spec.mtls` |
| Delete the resources 3.0 removes | `--to-latest v3` and the report has a **Delete Before Upgrading to 3.0** section |
| Upgrade the Global control plane | always |
| Upgrade the Zone control planes | always |
| Install Gateway API CRDs | a `MeshGateway`, `MeshGatewayInstance` or `MeshGatewayRoute` was converted |
| Apply backends, then policies | always, per context and kind subfolder |
| Apply `Mesh` CRs last (`v2`) / Apply the `Mesh` CRs (`v3`) | a `Mesh` is in the output |
| Verify traffic health | always |
| Delete the original policy files | always |
| Plan your next upgrade | always |

### Fix errors

Nothing after this step is safe to apply until every error is resolved. Each error names the
document and the reason. The original, or the converted output the target rejects, is under
`_manual/` on both targets, and **Kept Under `_manual/`** lists every file there with its reason.

### Update workload env vars and annotations

Workloads that address services by their `kuma.io/service` name (for example
`backend_demo_svc_3001`) stop resolving once the mesh runs `meshServices.mode: Exclusive`. The
report lists each env var with its replacement address. Annotation values `"yes"`/`"no"` on
`kuma.io/*` annotations must become `"true"`/`"false"`.

### Set meshServices.mode: Exclusive

`--to-latest v3` only. 3.0 makes unified naming unconditional: a 2.x zone reads a missing
`meshServices` field as `Disabled` and deletes every generated `MeshService`, and a 3.0 zone
rejects a mesh with any other mode
([Kuma `UPGRADE.md`](https://github.com/kumahq/kuma/blob/master/UPGRADE.md), "`meshServices`
removed from the `Mesh` schema"). The mode must therefore be set on the 2.x Global control
plane **before** it is upgraded. The checklist prints one command per `Mesh` of the input that
changes only `meshServices` and keeps `spec.mtls`:

```bash
# kubectl contexts
kubectl patch mesh default --type merge -p '{"spec":{"meshServices":{"mode":"Exclusive"}}}'
# kumactl contexts (Universal, Konnect); kumactl has no patch command, so this needs yq v4
kumactl get mesh default -o yaml | yq '.meshServices.mode = "Exclusive"' | kumactl apply -f -
```

It also prints the command to list meshes, for meshes that were not in the input.
`Exclusive` turns off routing by `kuma.io/service` tags, so workload env vars must already use
the new addresses (the earlier step).

This step comes before the `MeshIdentity` cutover because the cutover depends on it: on Kong
Mesh 2.14.7 a `MeshIdentity` in a mesh without `Exclusive` reports `MeshIdentity requires
MeshServices to be enabled on the mesh` and never becomes ready.

### Finish the MeshIdentity cutover

`--to-latest v3` only, when a `Mesh` in the input sets `spec.mtls`, and after the previous step:
on 2.14 a `MeshIdentity` in a mesh that is not `Exclusive` stays `DependenciesReady=False` with
reason `MeshServicesDisabled` and never issues certificates. 3.0 no longer produces
mTLS from `Mesh.mtls` and drops the legacy CA from the trust bundle, so a mesh without a
`MeshIdentity` serves plaintext after the upgrade, with no rolling path back. Create a
`MeshIdentity` and `MeshTrust` that select every proxy of the mesh on the 2.x control plane and
confirm traffic uses them. See [mTLS and workload identity](../topics/mtls-identity.md).

Do not apply the `--to-latest v3` `Mesh` files at this point: they drop `spec.mtls`, which
turns mTLS off on a 2.x control plane.

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

### Apply backends, then policies

One context at a time, in the order the report lists: Global and standalone contexts before
Zone contexts, and within a context `backends`, `resiliency`, `routing`, `zero-trust`,
`observability`, then `other`.

`backends/` holds `MeshOpenTelemetryBackend` objects, including the ones `--to-latest v3`
generates from inline `openTelemetry.endpoint` values. `MeshTrace`, `MeshAccessLog` and
`MeshMetric` select them by label. The control plane accepts a policy whose `backendRef`
matches nothing (the validators check only the shape of the reference) and skips that backend
until it exists, so applying the policy first loses telemetry in between.

The command depends on how the context was extracted and on the control plane's environment,
both read from `.kuma-migrator.json`:

- **`extract --kumactl-context`** from a Universal control plane, or from Konnect. `kumactl`
  before 2.14 does not accept a directory for `-f`, so the report lists one command per file,
  after the command that selects the context (`kumactl config control-planes switch`; there is
  no `kumactl config use-context`). `kumactl apply` of an existing resource replaces its whole
  spec and labels, so it removes the fields the migrated file drops:

  ```bash
  kumactl config control-planes switch --name konnect-global-prod
  kumactl apply -f ./migrated/konnect-global-prod-global-ctx/mesh-default/resiliency/MeshTimeout-my-timeout.yaml
  kumactl apply -f ./migrated/konnect-global-prod-global-ctx/mesh-default/resiliency/MeshRetry-my-retry.yaml
  ```

- **`extract --kube-context`**. One directory apply per kind subfolder:

  ```bash
  kubectl apply -f ./migrated/zone-eu-west1-prod-zone-ctx/mesh-default/resiliency/
  ```

  When the subfolder holds a file that rewrites an object of the input (same kind, namespace and
  name, different content), the report lists that subfolder file by file instead, with
  `kubectl replace` for the rewrites and `kubectl apply` for the rest:

  ```bash
  kubectl replace -f ./migrated/zone-eu-west1-prod-zone-ctx/mesh-default/resiliency/MeshTimeout-kong-mesh-system-mesh-timeout-all-default.yaml --save-config
  kubectl apply -f ./migrated/zone-eu-west1-prod-zone-ctx/mesh-default/resiliency/MeshTimeout-kong-mesh-system-mesh-timeout-all-default-outbound.yaml
  ```

  `kubectl apply` merges into the live object. When that object has no
  `kubectl.kubernetes.io/last-applied-configuration` annotation, because the control plane,
  Helm or `kubectl create` made it, the merge keeps every field the new file drops. A
  `from[]` policy split into `rules[]` then still carries its old `to[]` and is rejected with
  `fields 'to' and 'from' must be empty when 'rules' is defined` (seen on the control-plane
  default `mesh-timeout-all-default`), and a migrated `Mesh` keeps its embedded backends.
  `kubectl apply --server-side --force-conflicts` does not remove them either: fields owned by
  another field manager stay. `kubectl replace` writes the whole object. `--save-config`
  records the new content, so a later `kubectl apply` of the same file merges correctly.
  `kubectl replace` fails with `NotFound` when the object was deleted since `extract`; use
  `kubectl create -f <file>` for that file. Files that only add fields, and files identical to
  their input, keep `kubectl apply`.

  `migrate` drops `metadata.resourceVersion` and `metadata.uid` that `kubectl get -o yaml`
  copies into the extracted tree. Both are write preconditions: with them, `kubectl replace` and
  `kubectl apply` fail with a conflict as soon as the live object changed after `extract`.

- **`extract --kumactl-context`** from a Kubernetes-backed control plane
  (`"environment": "kubernetes"`). `kumactl` cannot create, update or delete resources there:
  they are CRDs owned by the Kubernetes API. The report uses `kubectl` directory applies, says
  why, and names the kumactl context so you can point `kubectl` at the cluster that runs that
  control plane. If the files are in Universal format (the `extract` default), `kubectl`
  cannot apply them either and the report says to re-extract with `--output-format kubernetes`.
  The same rule picks the tool for the delete commands of **Delete Before Upgrading to 3.0**.

Without `.kuma-migrator.json` (a hand-built input tree), the report uses `kubectl`.

`global-scoped-resources/` holds both Kuma global resources and the Gateway API output.
Gateway API objects go to the Kubernetes cluster that runs the gateway: a Zone cluster.
A gateway that was created directly on a Zone control plane is extracted into that zone's
context, and its Gateway API output lands in `<zone-ctx>/global-scoped-resources/routing/`.

### Apply Mesh CRs

**`v2`: last.** The migrated `Mesh` sets `spec.meshServices.mode: Exclusive`. That turns off
routing by `kuma.io/service` tags, so applying it before every policy and workload is migrated
breaks any workload still addressed by a tag. If a `Mesh` was not in the input, set the mode
by hand (`kubectl patch`, or the `kumactl get | yq | kumactl apply` form above). A migrated
`Mesh` drops its embedded observability backends, so on a kubectl context it is listed with
`kubectl replace`, for the reason given under the policy step.

**`v3`: after the upgrade.** The mode is already set by the pre-upgrade step. The v3 `Mesh`
files drop `spec.mtls` and the other fields 3.0 removed; 3.0 ignores those fields, so applying
the files once every control plane runs 3.0 and the `MeshIdentity` cutover is done only makes
the stored `Mesh` match what 3.0 uses. Applied on 2.x, they would turn mTLS off.

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

These documents go to a `_manual/` subtree instead of the apply tree:

- originals whose conversion failed (`TrafficRoute`, `VirtualOutbound`, and under `v3`
  `MeshGatewayInstance`, legacy `TrafficPermission`, …), on both targets;
- converted output the target control plane rejects as written (the transform reports it
  with a "rejected by the … control plane as converted" reason). The converted document is
  kept, not the original;
- under `v3`, documents that pass through as a kind 3.0 removes (`MeshGlobalRateLimit`,
  `MeshGatewayConfig`, Universal `ZoneIngress`/`ZoneEgress`). Their file is labelled `MANUAL`
  on stdout.

The report's **Kept Under `_manual/`** section lists every file of the subtree with its source
file, kind, name and reason.

```text
migrated/
  <context>-global-ctx/
    mesh-default/resiliency/...          ← apply tree
    global-scoped-resources/routing/...  ← apply tree
    _manual/
      mesh-default/routing/TrafficRoute-route-all.yaml
```

No checklist step applies `_manual/`: `kubectl apply -f <dir>/` is not recursive, and the
file lists never include these paths. Besides **Kept Under `_manual/`**, each file is listed in
**Action Items → Errors** or in **Delete Before Upgrading to 3.0** with what to do about it.
Under `v2` an errored original also goes to `_manual/`: applying it back to the 2.x control
plane it came from would change nothing, but listing it among the apply steps read as if it had
been migrated.

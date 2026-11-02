# Applying the migrated manifests

The order to apply in, why that order matters, and how to clean up the originals afterwards.

[← Back to the README](../README.md)

---

## Apply order

`kuma-migrator migrate` writes a numbered **Apply Checklist** into `migration-report.md`,
built from your actual output. The steps below are numbered for reference here, but several
only appear when relevant to what was found — your actual report renumbers sequentially and
skips whichever of these don't apply, so its step 4 may not be this page's step 4. This is
what each step means, in the order they always appear when present.

1. **Fix errors** — only if the report's Action Items → Errors section is non-empty. Nothing
   past this point is safe to apply until every error is resolved.
2. **Update workload env vars** — only if legacy `kuma.io/service`-encoded addresses were found
   in Deployment/StatefulSet env vars (Action Items → Workload Service Address Mappings).
3. **Fix deprecated annotations** — only if any `"yes"`/`"no"` `kuma.io/*` annotation values
   were found (Action Items → Deprecated Annotations).
4. **Delete the resources 3.0 removes** — `--to-latest v3` only, when the report has a
   **Delete Before Upgrading to 3.0** section. Do it on the 2.x control plane: 3.0 rejects these
   kinds and does not migrate stored objects. If one still serves traffic (`ExternalService`,
   `MeshGateway*`, `OPAPolicy`), first apply its replacement on 2.14 from a `--to-latest v2` run
   of the same input, verify it, then delete it. See [below](#the-_manual-subtree-v3).
5. **Upgrade the Global Control Plane** to the target Kuma / Kong Mesh version.
6. **Upgrade Zone Control Planes.** Kong Mesh supports at most two minor versions per step
   (e.g. 2.7 → 2.9 → 2.11).
7. **Install Gateway API CRDs** — only if a `MeshGateway`/`MeshGatewayRoute` was migrated. Applied to
   every Kubernetes cluster (Global CP and each Zone, **never** to a Universal zone):
   ```bash
   kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/latest/download/standard-install.yaml
   ```
   If the migration produced a `TCPRoute`, also install the experimental channel (a superset
   of standard):
   ```bash
   kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/latest/download/experimental-install.yaml
   ```
8. **Apply policies** (resiliency, routing, zero-trust, observability), one context at a time,
   in the order the report lists them. **The command differs by how that context was
   extracted** — the report gets this from `.kuma-migrator.json`, written during `extract`:
   - **`extract --kumactl-context`** (including every Konnect-hosted CP) → `kumactl` rejects a
     directory as `-f`, so the report lists **one `kumactl apply -f <file>` per file**,
     preceded by a context reminder:
     ```bash
     kumactl config use-context konnect-global-prod
     kumactl apply -f ./migrated/konnect-global-prod-global-ctx/mesh-default/resiliency/MeshTimeout-my-timeout.yaml
     kumactl apply -f ./migrated/konnect-global-prod-global-ctx/mesh-default/resiliency/MeshRetry-my-retry.yaml
     ```
   - **`extract --kube-context`** → a single directory apply per context:
     ```bash
     kubectl apply -f ./migrated/zone-eu-west1-prod-zone-ctx/mesh-default/resiliency/
     ```

   Global-scoped resources (`global-scoped-resources/`, e.g. Kubernetes-native Gateway API CRDs)
   apply to the **zone clusters**, not the Global CP.
9. **Apply `Mesh` CRs last**, same per-context command shape as step 8. These enable
   `spec.meshServices.mode: Exclusive`, which disables legacy `kuma.io/service` routing —
   applying them before every other policy and workload env var is migrated will break any
   workload still addressed by a tag. Under `--to-latest v3` this field must be set **before**
   the Global CP upgrade (a 2.x zone reads a missing field as `Disabled`); see
   [Choosing a target version](target-version.md). If a `Mesh` wasn't in the input directory, patch it
   manually instead:
   ```bash
   kubectl patch mesh <name> --type merge -p '{"spec":{"meshServices":{"mode":"Exclusive"}}}'
   ```
10. **Verify traffic health.** Check service-to-service connectivity across all meshes and
   watch your observability stack before proceeding.
11. **Delete the original policy files** once the migrated policies are confirmed working —
    the originals were not modified. (This is about the *input files*; resources whose *kind*
    changed also need deleting from the cluster — see [below](#clean-up-the-originals).)
12. **Plan your next upgrade** if you haven't yet reached the target version — re-run
    `extract` + `migrate --dry-run` + `migrate` for each minor-version step.

`--dry-run` writes a shorter three-step version of this list (fix warnings, run `migrate` for
real, follow its checklist) — it never applies anything itself.

## When MeshGateway was zone-local

The zone-cluster Gateway-API-CRD step above doesn't apply if your `MeshGateway` and route CRDs
were created directly on a Zone CP rather than via the Global CP: they are extracted into
`<context>-zone-ctx/<mesh>/routing/` and migrated there instead, alongside that zone's own
policies. The migration report will tell you which case applies.

## Clean up the originals

After verifying traffic health, delete the original resources whose **kind changed**
during migration (these cannot be replaced with `kubectl apply` — the old kind must
be explicitly removed):

| Old kind | New kind |
|---|---|
| `Timeout`, `Retry`, `TrafficPermission`, … | `MeshTimeout`, `MeshRetry`, `MeshTrafficPermission`, … |
| `ExternalService` | `MeshExternalService` |
| `MeshGateway` | `Gateway` |
| `MeshGatewayInstance` | `GatewayClass` + `MeshGatewayConfig` (`v2` only) |
| `MeshGatewayRoute` | `HTTPRoute` / `TCPRoute` |
| `OPAPolicy` | `MeshOPA` |

The migration report (`migration-report.md`) contains a ready-to-run
`kubectl delete` command list for all such resources. Under `--to-latest v3` these kinds are
listed in **Delete Before Upgrading to 3.0** instead, because they must go before the upgrade
rather than after it.

## The `_manual/` subtree (v3)

Under `--to-latest v3`, two kinds of documents are written to a separate `_manual/` subtree
instead of the apply tree:

- originals whose conversion failed (`TrafficRoute`, `VirtualOutbound`, `MeshGatewayInstance`,
  legacy `TrafficPermission`, …);
- documents that pass through as a kind 3.0 removes (`MeshGlobalRateLimit`, Universal
  `ZoneIngress`/`ZoneEgress`).

```
migrated/
  <context>-global-ctx/
    mesh-default/resiliency/...          ← apply tree
    global-scoped-resources/routing/...  ← apply tree
    _manual/
      mesh-default/routing/TrafficRoute-route-all.yaml
```

No checklist step applies `_manual/`: `kubectl apply -f <dir>/` is not recursive, and the
`kumactl` per-file list never includes these paths. Each file is listed in the report's Errors
section or in **Delete Before Upgrading to 3.0**, with what to do about it. Under `v2` there is
no `_manual/` subtree: errored originals stay in place, since they came from the 2.x control
plane they are applied back to.

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
4. **Upgrade the Global Control Plane** to the target Kuma / Kong Mesh version.
5. **Upgrade Zone Control Planes.** Kong Mesh supports at most two minor versions per step
   (e.g. 2.7 → 2.9 → 2.11).
6. **Install Gateway API CRDs** — only if a Gateway/route scenario was migrated. Applied to
   every Kubernetes cluster (Global CP and each Zone, **never** to a Universal zone):
   ```bash
   kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/latest/download/standard-install.yaml
   ```
   If the migration produced a `TCPRoute`, also install the experimental channel (a superset
   of standard):
   ```bash
   kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/latest/download/experimental-install.yaml
   ```
7. **Apply policies** (resiliency, routing, zero-trust, observability), one context at a time,
   in the order the report lists them. **The command differs by how that context was
   extracted** — the report gets this from `.kuma-migrator.json`, written during `extract`:
   - **`extract --kube-context`** → a single directory apply per context:
     ```bash
     kubectl apply -f ./migrated/prod-cp-global-ctx/mesh-default/resiliency/
     ```
   - **`extract --kumactl-context`** (including every Konnect-hosted CP) → `kumactl` rejects a
     directory as `-f`, so the report lists **one `kumactl apply -f <file>` per file**,
     preceded by a context reminder:
     ```bash
     kumactl config use-context prod-cp
     kumactl apply -f ./migrated/prod-cp-global-ctx/mesh-default/resiliency/MeshTimeout-my-timeout.yaml
     kumactl apply -f ./migrated/prod-cp-global-ctx/mesh-default/resiliency/MeshRetry-my-retry.yaml
     ```

   Global-scoped resources (`global-scoped-resources/`, e.g. Kubernetes-native Gateway API CRDs)
   apply to the **zone clusters**, not the Global CP.
8. **Apply `Mesh` CRs last**, same per-context command shape as step 7. These enable
   `spec.meshServices.mode: Exclusive`, which disables legacy `kuma.io/service` routing —
   applying them before every other policy and workload env var is migrated will break any
   workload still addressed by a tag. If a `Mesh` wasn't in the input directory, patch it
   manually instead:
   ```bash
   kubectl patch mesh <name> --type merge -p '{"spec":{"meshServices":{"mode":"Exclusive"}}}'
   ```
9. **Verify traffic health.** Check service-to-service connectivity across all meshes and
   watch your observability stack before proceeding.
10. **Delete the original policy files** once the migrated policies are confirmed working —
    the originals were not modified. (This is about the *input files*; resources whose *kind*
    changed also need deleting from the cluster — see [below](#clean-up-the-originals).)
11. **Plan your next upgrade** if you haven't yet reached the target version — re-run
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
| `MeshGatewayInstance` | `GatewayClass` + `MeshGatewayConfig` |
| `MeshGatewayRoute`, `MeshHTTPRoute`, `MeshTCPRoute` | `HTTPRoute` / `TCPRoute` |
| `OPAPolicy` | `MeshOPA` |

The migration report (`migration-report.md`) contains a ready-to-run
`kubectl delete` command list for all such resources.

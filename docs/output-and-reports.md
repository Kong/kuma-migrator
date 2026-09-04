# Console output and reports

What the CLI prints while it runs, and what the generated Markdown report contains.

[← Back to the README](../README.md)

---

## Console output

Each file gets a scenario label, the mesh it belongs to (omitted for global-scoped resources),
and the filename. Two faint lines below show where it came from (`←`) and where it was written
(`→`); warnings appear as `⚠`.

```
  kuma-migrator  migrate --dry-run

  ✓  OPA               default  meshopa.yaml
       ←  prod-cp-global-ctx/mesh-default/zero-trust/meshopa.yaml
       →  prod-cp-global-ctx/mesh-default/zero-trust/MeshOPA-my-opa.yaml
       ⚠  MeshOPA "my-opa": spec.targetRef.{name,namespace} still accepted in 2.14 but
          removed in 3.0 — use spec.targetRef.labels["kuma.io/display-name"] instead. …
  ✓  RULES             default  meshtls.yaml
       ←  prod-cp-global-ctx/mesh-default/zero-trust/meshtls.yaml
       →  prod-cp-global-ctx/mesh-default/zero-trust/MeshTLS-kuma-system-strict-tls.yaml
       ⚠  MeshTLS "strict-tls": from[] migrated to rules[] (Kuma 2.10+). …
  ●  ALREADY MIGRATED  default  mesh-retry.yaml
  –  SKIP              deployment.yaml: no recognised Kuma policy documents

  ────────────────────────────────────────────────────────────
  3 file(s) processed  ·  2 migrated  ·  0 already migrated  ·  1 skipped  ·  0 errors
```

Scenario labels are `LEGACY`, `SUBSET`, `RULES`, `MESH`, `EXTERNAL SERVICE`, `GATEWAY`, and `OPA`.

> A file can be labelled `SKIP` or `ALREADY MIGRATED` and still carry warnings — both labels
> reflect scenario detection, not whether the deprecation scan found something to flag. A plain
> Kubernetes `Deployment`, for instance, is not a Kuma policy at all (`SKIP`), but a deprecated
> `kuma.io/*` annotation on it is still reported. A `HostnameGenerator` is not a policy either
> (`ALREADY MIGRATED`, passed through unchanged), but its `spec.template` is still checked
> against the 2.14 validation.

## Report format

The Markdown report (`migration-plan.md` under `--dry-run`, `migration-report.md` otherwise)
contains:

- **Summary table** — files processed, migrated, already migrated, skipped, errors
- **Files That Would Be Migrated** (`--dry-run`) / **Migrated Files** (real run) — compact table
  per `contextDir/meshDir/subfolder` (e.g. `prod-cp-global-ctx/default/resiliency/`), with
  per-file warning blocks where relevant; `mesh/` noted as "apply last"
- **Already Migrated** — files passed through unchanged, with any deprecation warnings still
  listed per file
- **Skipped** — non-policy YAML files, with any deprecation warnings still listed per file
- **Action Items** (when present) — errors, workload service address mappings,
  deprecated annotations
- **Apply Checklist** — ordered, numbered steps with the correct `kubectl`/`kumactl apply` paths
  for each context (see [Applying the migrated manifests](apply.md)); a short 3-step stub under
  `--dry-run`
- **Original Resources to Delete** — resources whose kind changed; includes a collapsible
  `kubectl delete` command list

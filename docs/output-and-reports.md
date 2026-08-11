# Console output and reports

What the CLI prints while it runs, and what the generated Markdown report contains.

[← Back to the README](../README.md)

---

## Console output

Each file gets a scenario label, the mesh it belongs to (omitted for global-scoped resources),
and the filename. Two faint lines below show where it came from (`←`) and where it was written
(`→`); warnings appear as `⚠`.

```
  kuma-migrator  plan

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

> A file can be labelled `SKIP` and still produce output and warnings. The label reflects
> scenario detection — whether the document is a *policy* the migrator transforms — not whether
> anything was written. A `HostnameGenerator`, for instance, is not a policy, but it is still
> copied through and scanned for the 2.14 template validation.

## Report format

The Markdown report (`migration-plan.md` or `migration-report.md`) contains:

- **Summary table** — files processed, migrated, already migrated, skipped, errors
- **Migrated Files** — compact table per `contextDir/meshDir/subfolder` (e.g. `prod-cp-global-ctx/default/resiliency/`),
  with per-file warning blocks where relevant; `mesh/` noted as "apply last"
- **Already Migrated** — files passed through unchanged
- **Skipped Files** — non-policy YAML files
- **Action Items** (when present) — errors, workload service address mappings,
  deprecated annotations
- **Apply Checklist** — ordered, numbered steps with the correct `kubectl apply -f` paths;
  includes a dedicated step for `global-scoped-resources/` Gateway API resources when present
- **Original Resources to Delete** — resources whose kind changed; includes a collapsible
  `kubectl delete` command list

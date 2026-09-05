# Plan and migrate

Previewing changes, writing the migrated manifests, and the output layout they land in.

[← Back to the README](../README.md)

---

`plan` is not a separate command — it's `migrate --dry-run`. Both paths run the exact same
transform and deprecation-scanning logic; `--dry-run` only changes whether output files get
written and which report is produced.

## Dry run (`--dry-run`)

Preview all changes **without writing any output files**.
Point `--input-dir` at the entire extracted tree and optionally filter to a single mesh:

```bash
# All meshes
kuma-migrator migrate --input-dir ./raw-policies --output-dir ./migrated --dry-run

# Single mesh only
kuma-migrator migrate --input-dir ./raw-policies --output-dir ./migrated --mesh default --dry-run

# Target the 3.0 line instead of the latest 2.x
kuma-migrator migrate --input-dir ./raw-policies --output-dir ./migrated --to-latest v3 --dry-run
```

Writes `migration-plan.md` in the output directory. Review before running the same command
again without `--dry-run`.

## Migrate (writes output)

Transform policies and write migrated YAML files:

```bash
# All meshes
kuma-migrator migrate --input-dir ./raw-policies --output-dir ./migrated

# Single mesh only
kuma-migrator migrate --input-dir ./raw-policies --output-dir ./migrated --mesh default

# Target the 3.0 line instead of the latest 2.x
kuma-migrator migrate --input-dir ./raw-policies --output-dir ./migrated --to-latest v3
```

Writes `migration-report.md` alongside the migrated YAML files. See
[Choosing a target version](target-version.md) for what `--to-latest` changes.

## Output layout

The output preserves the input layout, keeping context and mesh subdirectories intact:

```
migrated/
  konnect-global-prod-global-ctx/    ← context + CP mode dir
    mesh-default/                    ← mesh (prefixed with "mesh-")
      resiliency/
      routing/
      zero-trust/
      observability/
      mesh/
    global-scoped-resources/         ← global-scoped resources AND Gateway API CRDs
      routing/
        Gateway-my-gw.yaml
        HTTPRoute-my-route.yaml
      mesh/
  zone-eu-west1-prod-zone-ctx/       ← zone-origin policies
    mesh-default/
      resiliency/
```

**Gateway API resources**: `MeshGateway`, `MeshHTTPRoute`, `MeshTCPRoute`, and `MeshGatewayRoute`
are transformed into Gateway API CRDs (`Gateway`, `HTTPRoute`, `TCPRoute`). Because these are
Kubernetes-native resources that must be applied to **zone clusters** rather than the Global CP,
the migrator redirects them to the `global-scoped-resources/` subdirectory (alongside
global-scoped Kuma resources) even when the source file came from a mesh-scoped input directory.

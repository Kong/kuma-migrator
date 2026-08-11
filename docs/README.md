# kuma-migrator documentation

[← Back to the README](../README.md)

## Getting started

| Page | What it covers |
|---|---|
| [Installation](installation.md) | Homebrew, pre-built binaries, building from source |
| [CLI reference](cli-reference.md) | Every command, flag, and the config file |

## The workflow

| Page | What it covers |
|---|---|
| [Extracting from a control plane](extract.md) | `extract`, CP-mode rules, output layout, Konnect, Universal vs Kubernetes format |
| [Plan and migrate](plan-and-migrate.md) | `plan` and `migrate`, and the layout they produce |
| [Choosing a target version](target-version.md) | `--to-latest v2\|v3` and the checks that change with it |
| [Applying the migrated manifests](apply.md) | Apply order, and cleaning up resources whose kind changed |

## Reference

| Page | What it covers |
|---|---|
| [Migration paths and deprecation warnings](migration-paths.md) | Every transformation, and every deprecation detected but not auto-fixed |
| [MeshTrafficPermission: `from[]` vs `rules[]`](meshtrafficpermission-modes.md) | The two identity models, and why the move between them is not mechanical |
| [Transformation examples](transformation-examples.md) | Before-and-after YAML per scenario |
| [Console output and reports](output-and-reports.md) | What the CLI prints, and what the Markdown report contains |
| [Notes and caveats](notes.md) | Behaviour worth knowing before relying on the output |

## Not product documentation

[`contrib/`](contrib/) holds drafts written for upstream projects rather than for this tool —
currently a `MeshTrafficPermission` comparison intended for the Kuma website.

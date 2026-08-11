# CLI reference

Every command and flag. Run `kuma-migrator <command> --help` for the same information at the terminal.

[← Back to the README](../README.md)

---

## Commands

```
kuma-migrator extract  --kube-context <ctx> | --kumactl-context <ctx>
                       --output-dir <dir> [--mesh <name>]
                       [--output-format kubernetes|universal] [--tls-skip-verify]

kuma-migrator plan     --input-dir <dir> --output-dir <dir>
                       [--mesh <name>] [--to-latest v2|v3]

kuma-migrator migrate  --input-dir <dir> --output-dir <dir>
                       [--mesh <name>] [--to-latest v2|v3]

kuma-migrator version
kuma-migrator completion <bash|zsh|fish|powershell>
```

## extract

Pulls resources from a running control plane. `--kube-context` and `--kumactl-context` are
mutually exclusive; exactly one is required. Kinds containing `Insight` are always excluded.

| Flag | Short | Required | Description |
|---|---|---|---|
| `--kube-context` | | one of | Kubernetes context to use (kubectl) |
| `--kumactl-context` | | one of | kumactl context name (kumactl CLI) |
| `--output-dir` | `-o` | yes | Directory to write extracted YAML files |
| `--mesh` | | no | Restrict extraction to the named Kuma mesh (default: all meshes). Global-scoped resources are extracted regardless |
| `--output-format` | `-f` | no | YAML format for extracted files: `universal` (default) or `kubernetes` |
| `--tls-skip-verify` | `-k` | no | Disable TLS certificate verification for the CP admin server (self-signed certs) |

> **Pick `--output-format kubernetes` for a Kubernetes-backed control plane.** Universal output
> cannot be applied back to one: `kumactl apply` refuses resources managed as CRDs, and the store
> requires Universal names to be `name.namespace`, which cluster-scoped legacy resources do not
> have. The tool detects the environment and warns when the combination is wrong.

See [Extracting from a control plane](extract.md) for CP-mode behaviour and output layout.

## plan / migrate

Identical flags. `plan` writes only `migration-plan.md` and no YAML; `migrate` writes the
transformed manifests plus `migration-report.md`.

| Flag | Short | Required | Description |
|---|---|---|---|
| `--input-dir` | `-i` | yes | Directory containing source policy YAML files |
| `--output-dir` | `-o` | yes | Directory for output files and the Markdown report |
| `--mesh` | | no | Restrict processing to the named mesh subdirectory (default: all meshes). Files with no mesh directory are always processed |
| `--to-latest` | | no | Target major version: `v2` (default, latest 2.x) or `v3` (3.0) |

Invalid `--to-latest` values fail before any work is done:

```
Error: invalid --to-latest value "v4": want "v2" or "v3"
```

See [Choosing a target version](target-version.md) for what the choice changes.

## Configuration file

Optional, at `~/.config/kuma-migrator.yaml` (override with `$KUMA_MIGRATOR_CONFIG`).

```yaml
skip:
  kubernetes: [Dataplane, ZoneIngress, ZoneEgress, Zone, Workload, Secret, GlobalSecret]
  universal:  [Zone, Workload, Secret, GlobalSecret]
adminServer:
  tlsSkipVerify: false
```

`skip` lists kinds that are never extracted or transformed, per deployment environment. The
legacy flat-list form (`skip: [Kind1, Kind2]`) still works and applies to both environments.

> **An explicit `skip` block replaces the built-in defaults — it is not merged with them.** A
> config file written against an older release will not pick up changes to those defaults. Omit
> the block entirely to always track the built-in list.

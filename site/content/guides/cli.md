---
title: CLI reference
description: Every command, flag and configuration file setting of kuma-migrator.
order: 40
---

Run `kuma-migrator <command> --help` for the same information in the terminal. Flags are
defined in [`cmd/extract.go`](https://github.com/Kong/kuma-migrator/blob/main/cmd/extract.go)
and [`cmd/migrate.go`](https://github.com/Kong/kuma-migrator/blob/main/cmd/migrate.go).

## Commands

```text
kuma-migrator extract  --kube-context <ctx> | --kumactl-context <ctx>
                       --output-dir <dir> [--mesh <name>]
                       [--output-format kubernetes|universal] [--tls-skip-verify]

kuma-migrator migrate  --input-dir <dir> --output-dir <dir>
                       [--mesh <name>] [--to-latest v2|v3] [--dry-run]

kuma-migrator konnect token-script --region <region>
                       [--scope mesh|org] [--control-plane <name>]...
                       [--account-name <name>] [--expires-in <duration>] [-o <file>]

kuma-migrator version
kuma-migrator completion <bash|zsh|fish|powershell>
```

There is no `plan` command. `migrate --dry-run` replaces it, with the same flags.

## extract

Reads resources from a running control plane and writes one YAML file per resource.
`--kube-context` and `--kumactl-context` are mutually exclusive, and one of them is required.
Kinds whose name contains `Insight` are always excluded.

| Flag | Short | Required | Default | Description |
|---|---|---|---|---|
| `--kube-context` | | one of the two | | Kubernetes context; resources are read with `kubectl` |
| `--kumactl-context` | | one of the two | | `kumactl` context; resources are read through the control plane API |
| `--output-dir` | `-o` | yes | | Directory for the extracted files. Created if missing |
| `--mesh` | | no | all meshes | Extract only this mesh. Global-scoped resources are always extracted |
| `--output-format` | `-f` | no | `universal` | `universal` (`type`/`name`/`mesh`) or `kubernetes` (`apiVersion`/`kind`/`metadata`). Only affects the `--kumactl-context` path |
| `--tls-skip-verify` | `-k` | no | `false` | Skip TLS verification of the control plane API certificate |

> **Warning:** Use `--output-format kubernetes` when the control plane behind a
> `--kumactl-context` runs on Kubernetes. Universal output cannot be applied back to it:
> `kumactl apply` refuses resources that Kubernetes manages as CRDs, and the store requires
> Universal names of the form `name.namespace`, which cluster-scoped legacy resources do not
> have. `extract` detects this combination and prints a warning.

See [Extract](extract.md) for control plane mode detection and the output layout.

## migrate

Reads the extracted tree, transforms every document and writes the result. With `--dry-run`
it writes only `migration-plan.md`. Without it, it writes the migrated YAML and
`migration-report.md`. Both modes run the same transforms and the same deprecation scan.

| Flag | Short | Required | Default | Description |
|---|---|---|---|---|
| `--input-dir` | `-i` | yes | | Directory with the source YAML files, usually an `extract` output directory |
| `--output-dir` | `-o` | yes | | Directory for the output files and the Markdown report. Created if missing |
| `--mesh` | | no | all meshes | Process only `mesh-<name>/` directories for this mesh. Files outside any mesh directory are always processed |
| `--to-latest` | | no | `v2` | Target line: `v2` (latest 2.x, 2.14) or `v3` (3.0) |
| `--dry-run` | | no | `false` | Write `migration-plan.md` only, no YAML |

An invalid `--to-latest` value fails before any file is read:

```text
Error: invalid --to-latest value "v4": want "v2" or "v3"
```

See [Migrate](migrate.md) and [Target versions](target-version.md).

## konnect token-script

Writes the bash script a Konnect organization admin runs to create a read-only system account
and print a short-lived token for `extract`. It does not call Konnect.

| Flag | Short | Required | Default | Description |
|---|---|---|---|---|
| `--region` | | yes | | Konnect region: `us`, `eu`, `au`, `me`, `in`, `sg` |
| `--scope` | | no | `mesh` | `mesh` (`Viewer` on Mesh control planes) or `org` (Organization Admin (Read Only) team) |
| `--control-plane` | | no | all in the region | Mesh control plane name. Repeatable. `--scope mesh` only |
| `--account-name` | | no | `kuma-migrator-readonly` | System account to create or reuse |
| `--expires-in` | | no | `336h` | Token lifetime, `1h` to `8760h` |
| `--output` | `-o` | no | stdout | File to write the script to |

See [Konnect read-only access](konnect-access.md).

## version and completion

`kuma-migrator version` and `kuma-migrator --version` print the build version.
`kuma-migrator completion <shell>` prints a shell completion script:

```bash
kuma-migrator completion zsh > "${fpath[1]}/_kuma-migrator"
```

## Configuration file

Optional. Read from `~/.config/kuma-migrator.yaml`, or from the path in
`$KUMA_MIGRATOR_CONFIG`. Parsed by
[`pkg/config/config.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/config/config.go).

```yaml
skip:
  kubernetes: [Dataplane, AccessRoleBinding, ZoneEgress, ZoneIngress, Zone, Workload, Secret, GlobalSecret]
  universal:  [AccessRoleBinding, Zone, Workload, Secret, GlobalSecret]
adminServer:
  tlsSkipVerify: false
```

| Key | Effect |
|---|---|
| `skip.kubernetes` | Kinds never extracted or transformed in a Kubernetes environment |
| `skip.universal` | Kinds never extracted or transformed in a Universal environment |
| `skip: [Kind1, Kind2]` | Flat list, applied to both environments |
| `adminServer.tlsSkipVerify` | Same as `--tls-skip-verify`. Either one being `true` disables verification |

The values above are the built-in defaults. On Universal, `Dataplane`, `ZoneIngress` and
`ZoneEgress` are hand-written, so they are extracted and scanned. `Workload` is generated by
the control plane in both environments and stays skipped. `AccessRole` is in neither list: it
is scanned for the Kong Mesh 3.0 changes to `rules[].types` and `when[]`.

`migrate` picks the list per context directory from the `environment` that `extract` recorded
in `.kuma-migrator.json`. Without that record, a document in Universal shape (`type`, no
`kind`) gets the `universal` list and every other document gets the `kubernetes` list.

> **Warning:** An explicit list for an environment replaces the built-in default for that
> environment; it is not merged with it. A config file written for an older release does not
> pick up later changes to the defaults. Leave the key out to keep the built-in list.

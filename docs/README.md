# kuma-migrator documentation

The documentation moved to <https://kong.github.io/kuma-migrator/docs/>. Its sources are in
[`site/content/`](../site/content/).

## Guides

| Page | What it covers |
|---|---|
| [Installation](https://kong.github.io/kuma-migrator/docs/guides/installation/) | Homebrew, release archives, building from source |
| [Typical usage](https://kong.github.io/kuma-migrator/docs/guides/usage/) | End-to-end runs for Kubernetes, multizone, Kong Mesh, Konnect and Universal |
| [Upgrade plans](https://kong.github.io/kuma-migrator/docs/guides/upgrade-plans/) | Upgrade paths, which target to use, the sequence for 3.0, rollback |
| [CLI reference](https://kong.github.io/kuma-migrator/docs/guides/cli/) | Every command, flag and the configuration file |
| [Extract](https://kong.github.io/kuma-migrator/docs/guides/extract/) | Control plane modes, origin filtering, output layout, Konnect, Universal |
| [Migrate](https://kong.github.io/kuma-migrator/docs/guides/migrate/) | `--dry-run`, the real run, input and output layout |
| [Target versions](https://kong.github.io/kuma-migrator/docs/guides/target-version/) | `--to-latest v2\|v3` and what changes with it |
| [Apply](https://kong.github.io/kuma-migrator/docs/guides/apply/) | Apply order, removing originals, the `_manual/` subtree |
| [Output and reports](https://kong.github.io/kuma-migrator/docs/guides/output-and-reports/) | Console output and the Markdown report |
| [Transformation examples](https://kong.github.io/kuma-migrator/docs/guides/examples/) | Before-and-after YAML per scenario |
| [Architecture](https://kong.github.io/kuma-migrator/docs/guides/architecture/) | How the pipeline is built |
| [Notes and caveats](https://kong.github.io/kuma-migrator/docs/guides/notes/) | Behaviour to know before relying on the output |

## Topics and policies

| Page | What it covers |
|---|---|
| [Topics](https://kong.github.io/kuma-migrator/docs/topics/) | One area of the mesh (identity, gateways, multizone, access control, …) across every kind |
| [Policies](https://kong.github.io/kuma-migrator/docs/policies/) | One page per resource kind: what the tool does on each target |

## Not product documentation

[`contrib/`](contrib/) holds drafts written for upstream projects rather than for this tool.

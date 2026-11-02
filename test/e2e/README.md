# End-to-end validation against real control planes

Migrates a fixture corpus with `kuma-migrator migrate` and applies the output to real Kong Mesh
control planes:

| Target | Control planes | How output is applied |
|---|---|---|
| `--to-latest v2` | Kong Mesh 2.14.7 on Kubernetes + Universal | Kubernetes: `kubectl apply --dry-run=server` (runs the Kuma validating webhooks, which declare `sideEffects: None`). Universal: real `kumactl apply` against a memory-store CP. |
| `--to-latest v3` | Kong Mesh 3.0.1 on Kubernetes + Universal | same |

Every Kubernetes CP runs in `controlPlane.mode: zone` with no Global (the chart default; 3.0 removed
`standalone`). Universal CPs are `kong/kuma-cp` containers with `KUMA_MODE=zone` and
`KUMA_STORE_TYPE=memory`. No license is needed.

## Run it

```bash
test/e2e/run.sh up          # clusters, Helm installs, Universal CPs, kumactl downloads
test/e2e/run.sh test        # migrate + apply; writes test/e2e/results/
test/e2e/run.sh extract     # 2.14 CPs only: apply the input, check what extract writes
test/e2e/run.sh questions   # admission probes behind results/questions.md
test/e2e/run.sh down        # delete clusters and containers
test/e2e/run.sh all         # up + test + down
```

| Variable | Default | Meaning |
|---|---|---|
| `E2E_PLATFORM` | `marina` on macOS, `kind` elsewhere | how clusters are created |
| `E2E_ENVS` | `kubernetes universal` | restrict to one environment |
| `E2E_WORK` | `$TMPDIR/kuma-migrator-e2e` | kumactl binaries, kumactl configs, migrate output, logs |
| `E2E_UCP_HOST` | `localhost` | where the Universal CP ports are published |
| `KM_V2` / `KM_V3` | `2.14.7` / `3.0.1` | Kong Mesh versions (chart, image, kumactl) |

Requirements: `go`, `kubectl`, `helm`, `jq`, `docker`, `curl`, plus `marina` (macOS) or `kind`.

### macOS (marina)

`run.sh` runs `marina up`, creates clusters `km214` and `km30` (`marina cluster create`), and runs
the Universal CPs in the marina VM's Docker (`eval "$(marina docker-env)"`). The Universal CP ports
(25681 for 2.14, 35681 for 3.0) reach the Mac's `localhost` only with
`network.disablePortMirroring: false` in `~/.marina/config.yaml`. With port mirroring disabled,
set `E2E_UCP_HOST` to the VM's `lima0` IP (`marina status -o json`).

### CI (kind)

`.github/workflows/e2e.yml` runs the same script with `E2E_PLATFORM=kind`, one job per
environment, on `workflow_dispatch`, weekly, and on pull requests touching `pkg/migrator/**`,
`pkg/extractor/**` or `test/e2e/**`. Results and migrate output are uploaded as artifacts.

kumactl is downloaded from `packages.konghq.com` into `$E2E_WORK/bin/<version>/` and always called
with `--config-file $E2E_WORK/kumactl-<cp>.config`; `~/.kumactl/config` is never read or written.
The admin token is read from inside the container
(`/busybox/wget http://localhost:5681/global-secrets/admin-user-token`), because a 3.0 CP only
treats in-container requests as localhost admin.

## What `test` does

For each environment:

1. Seed the 2.14 CP (`fixtures/seed/`): namespaces and the Secrets the fixtures reference
   (`extract` skips Secrets, so they are environment state, not migrator input).
2. Apply the 2.x input (`fixtures/<env>/`) to the 2.14 CP for real, `Mesh` files first. Any
   rejection means the fixture is not real 2.x input.
3. Run `kuma-migrator extract` against that CP and list input objects it did not write
   (`results/extract-<env>.md`).
4. `migrate --to-latest v2`, then apply every file named by the report's **Apply Checklist**, in
   checklist order, to the 2.14 CP. Files the checklist lists as `kubectl replace -f <file>
   --save-config` (documents that rewrite an existing object) are validated with
   `kubectl replace --dry-run=server`. On the 3.0 CP, seeded with the meshes only, a replace
   that returns NotFound falls back to `kubectl apply --dry-run=server`.
5. Seed the 3.0 CP (`fixtures/seed/v3/`, the meshes), `migrate --to-latest v3`, apply the
   checklist files to the 3.0 CP.
6. Apply every file under `_manual/` to the 3.0 CP. These are expected to be rejected.

Kubernetes files of a namespaced kind with no `metadata.namespace` are reported as
`NO NAMESPACE` and validated with `-n kong-mesh-system`: applied as-is they land in the caller's
namespace, where the Kuma webhooks do not run unless it is labelled
`kuma.io/sidecar-injection`.

Apply-tree files the checklist does not mention are reported as `NOT IN CHECKLIST`.

The script exits non-zero when a rejection is not listed in `expected-rejections.txt`. Remove a
line from that file when the bug behind it is fixed.

## Layout

```text
test/e2e/
  run.sh                     harness
  questions.sh               probes for results/questions.md (sourced by run.sh)
  expected-rejections.txt    known rejections, grouped by cause
  fixtures/
    kubernetes/k8s-zone-ctx/ 2.x input in extract layout (kubectl context)
    universal/uni-zone-ctx/  2.x input in extract layout (kumactl context)
    seed/                    namespaces, Secrets, meshes the input needs
  questions/                 one-off probe inputs
  results/                   generated reports (<target>-<env>.md, extract-<env>.md,
                             questions-raw.md) and the hand-written questions.md
```

The fixtures cover every kind in `site/data/policies.yaml` that a zone CP accepts: all 12 legacy
policies (tagged and wildcard selectors), `MeshSubset` and partially-migrated `_svc_` references,
`from[]`/`to[]` policies, `Mesh` with builtin and provided mTLS, logging/tracing/metrics backends
and both passthrough values, `ExternalService` with TLS, `MeshGateway`/`MeshGatewayInstance`/
`MeshGatewayRoute`/`MeshGatewayConfig`, `MeshHTTPRoute`/`MeshTCPRoute`, `MeshPassthrough`,
`MeshService` with `dataplaneTags`, `MeshExternalService`, `MeshMultiZoneService`, `OPAPolicy`,
`MeshOPA` (legacy targetRef and DataSource), `MeshGlobalRateLimit`, `AccessRole`, `AccessAudit`,
`HostnameGenerator`, `ContainerPatch`, `MeshIdentity`, `MeshTrust`, `MeshTLS`,
`MeshOpenTelemetryBackend`, and on Universal `Dataplane` (inbound tags, outbound tags, builtin
gateway), `ZoneIngress`, `ZoneEgress` and `Zone`. The Universal set is the Kubernetes set
converted to Universal format, minus Kubernetes-only kinds.

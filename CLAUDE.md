
# kuma-migrator — Claude Instructions

## Project Context

You are an expert Go developer building a CLI tool called `kuma-migrator`.
Its purpose is to read existing Kuma and Kong-Mesh YAML manifests and transform them
across all supported migration paths in the Kuma/Kong Mesh 2.x lifecycle.

## Technology Stack

* **Language:** Go (1.24+)
* **CLI Framework:** `github.com/spf13/cobra`
* **YAML Parsing:** `sigs.k8s.io/yaml` (crucial for matching Kuma's Kubernetes-style JSON tags).

## Workspace Rules (CRITICAL)

1.  **The Reference Folder:** The `./reference/` directory contains massive codebases (`kuma`, `madrs`, `snippets`). **DO NOT read these entire directories into your main context.**
2.  **Use Sub-agents:** When you need to look up how Kuma implements a struct or what an ADR (or "MADR") says, spawn a sub-agent to search the `./reference/` folder, extract the specific Go structs or rules, and report back.
3.  **Skills:** The exact mapping rules for translating YAMLs will be stored in `./.claude/skills/migration-rules.md`. Always consult this file before writing translation logic.
4.  **Preserve YAML:** When rewriting YAML files, ensure you do not drop unrelated fields. Use strict unmarshaling based on Kuma's native Go structs where possible.

## References

* under `reference/docs/app/mesh` you will find the Kuma and Kong Mesh documentation.
* under `reference/kuma/docs/madr/decisions` you will find the Kuma ADRs related to the migration.
* under `reference/kong-mesh` you will find the codebase of Kong Mesh (enterprise fork of Kuma). Use it to understand enterprise-specific policies like `MeshOPA`, `MeshOPAPolicy`, etc.
* under `reference/snippets` you will find code snippets for traversing YAML files and defining Cobra commands.
* under `reference/kuma-website/app` you will find the Kuma documentation (user-facing).

## CLI Commands

```
kuma-migrator extract --kube-context <ctx>    --output-dir <dir>   # pull resources via kubectl
kuma-migrator extract --kumactl-context <ctx> --output-dir <dir>   # pull resources via kumactl
kuma-migrator plan    --input-dir <dir> --output-dir <dir>         # dry-run, writes migration-plan.md
kuma-migrator migrate --input-dir <dir> --output-dir <dir>         # transforms + writes migration-report.md
```

### extract command

Two mutually-exclusive modes, both write one YAML file per resource:

**`--kube-context`** — queries the kube API server for all `kuma.io/v1alpha1` CRDs, fetches
every instance with `kubectl get <kind> -o yaml`. Insight kinds are excluded via `isInsightKind`.

**`--kumactl-context`** — resolves the context from `~/.kumactl/config` (or `$KUMACTL_CONFIG`),
calls `GET <cpURL>/_resources` to discover all writable resource types (`readOnly: false`),
lists mesh names via `kumactl get meshes -o yaml`, then calls
`kumactl get <path> [--mesh <mesh>] -o yaml` for each type × mesh combination.

Both modes detect the CP mode at runtime (`GET <cpURL>/config` for kumactl; `KUMA_MODE` env var
on the CP Deployment for kubectl) and apply a zone filter: on a Zone CP, only resources with
`kuma.io/origin: zone` are extracted (resources synced from the Global CP carry `kuma.io/origin: global`
and are skipped). Unknown mode falls back to extracting everything.

Key files: `pkg/extractor/kube.go`, `pkg/extractor/kumactl.go`, `pkg/extractor/extractor.go`,
`pkg/extractor/cpmode.go`.

### Konnect (hosted) specifics

- **Detection**: URL contains `api.konghq.com`. Logged as `Platform: Kong Konnect (hosted)`.
- **Authentication**: kumactl stores PATs as `headers: [{key: Authorization, value: "Bearer kpat_..."}]`
  in the kumactl config (not `authType: tokens`/`authConf`). `resolveKumactlContext` scans
  `kumactlAPIServer.Headers` for the `Authorization` key and strips the `Bearer ` prefix.
  Struct: `type kumactlHeader struct { Key, Value string }`.
- **CP mode**: Konnect has no `/config` endpoint. Always treated as Global CP.
- **Scope fallback**: `/_resources` sometimes reports resource types as Mesh-scoped but kumactl
  rejects `--mesh` for them ("unknown flag: --mesh"). `isUnknownMeshFlag(err)` detects this
  and retries the extraction globally (breaking out of the mesh loop).
- **Universal list format**: kumactl on Konnect returns `{total: N, items: [...]}` JSON with
  no top-level `kind`. `writeSingleResourceDoc` detects this and recurses into `items`.

### Universal format YAML (migrate pipeline)

Kuma's Universal format uses `type` instead of `kind` and top-level `name`/`mesh` fields
instead of `metadata`. All migrate-side parsing must normalise these:

- **`DetectScenario`** (`detect.go`): `kind := p.Kind; if kind == "" { kind = p.Type }`.
  All downstream checks use the normalised `kind` variable.
- **`meshNeedsMigration`** (`mesh.go`): `meshProbe` has both `Spec.{Metrics,Tracing,...}` and
  top-level `{Metrics,Tracing,...}` fields. Effective values are resolved with fallback:
  `metrics := p.Spec.Metrics; if metrics == nil { metrics = p.Metrics }`.
- **`TransformFromToRules`** (`rulesapi.go`): uses a `map[string]interface{}` round-trip via
  `applyFromToRulesMap` to preserve all top-level Universal fields (`type`, `name`, `mesh`,
  `kri`, `creationTime`, `labels`). The typed `KubePolicy` struct path (`applyFromToRules`)
  is kept only for the second-pass inside `transformScenarioSubset`.
- **`extractNameFromObj`**: checks `obj["metadata"]["name"]` first, falls back to `obj["name"]`.

### Kuma resource labels relevant to extraction and migration

| Label | Values | Meaning |
|---|---|---|
| `kuma.io/origin` | `global` / `zone` | Set by CP. `global` = synced from Global CP; `zone` = created locally in this zone. |
| `kuma.io/policy-role` | `system` / `producer` / `consumer` / `workload-owner` | Set by CP based on namespace + spec shape. Does **not** affect extraction filtering (origin label covers this). Must be **preserved** by migration transforms — Subset/Passthrough/Rules scenarios do preserve it; Legacy (Universal-format) inputs don't carry it. |

`kuma.io/policy-role` priority order (low → high): `system` → `producer` → `consumer` → `workload-owner`.

## Supported Scenarios (all implemented)

| Scenario | Trigger | Output |
|---|---|---|
| Legacy | `sources`/`destinations` policies or legacy type names | `targetRef`/`to`/`from` |
| Subset | `MeshSubset` with `kuma.io/service` or `k8s.kuma.io/service-name` tags | `Dataplane`/`MeshService` |
| Passthrough | Already using `MeshService` — pass-through | unchanged |
| Rules | New-style Mesh* with deprecated `from[]` (Kuma 2.10+) | `rules[]` |
| Mesh | `Mesh` CRD with embedded observability | standalone companion CRDs |
| ExternalService | `ExternalService` | `MeshExternalService` |
| GW | `MeshGateway`, `MeshGatewayInstance`, `MeshGatewayRoute`, `MeshHTTPRoute`, `MeshTCPRoute` | Gateway API CRDs |
| OPAPolicy | Kong Mesh `OPAPolicy` | `MeshOPA` |

## Deprecation Warnings (all implemented via `ScanForDeprecations`)

- `MeshMetric sidecar.regex` → auto-fixed to `sidecar.profiles.exclude` (v2.7)
- `MeshHealthCheck healthyPanicThreshold` → warn, move to `MeshCircuitBreaker` (v2.10)
- `MeshTrust spec.origin` → warn, deprecated in favour of `status.origin` (v2.13)
- `MeshTrafficPermission`/`MeshFaultInjection` `from[].targetRef.kind: MeshService` → warn (v2.7)
- `MeshTrafficPermission action: ALLOW/DENY` → warn, use `Allow`/`Deny` (Kong Mesh 2.1)
- `MeshLoadBalancingStrategy hashPolicies[].type: SourceIP` → warn, use `Connection` (v2.10)
- `Dataplane transparentProxying.redirectPortInboundV6` → warn, field removed (v2.9)
- `kuma.io/*` annotation `yes`/`no` → scanner, use `true`/`false` (v2.9)
- `MeshSubset` in `spec.targetRef` without service-identity tags → warn, use `Dataplane` with labels (v2.10)

## Kong Mesh Specifics

### Two-minor-version upgrade constraint
Kong Mesh supports upgrading **at most two minor versions** at a time.
Example valid path: 2.7 → 2.9 → 2.11 → 2.13.
Skipping more than one minor version is unsupported.

### OPAPolicy → MeshOPA
- `kind: OPAPolicy` was the legacy Kong Mesh OPA integration (removed in Kong Mesh 2.13.x when dynamic config is used).
- `kind: MeshOPA` is the new policy. Structural change:
  - `spec.conf.policies[].inlineString` → `spec.default.appendPolicies[].rego.inlineString`
  - `spec.conf.agentConfig.inlineString` → `spec.default.agentConfig.inlineString` (if present)
- The `targetRef` is preserved as-is.

### MeshOPA dynamic vs static config
- **Static** (current `MeshOPA`): `spec.default.appendPolicies[].rego.inlineString`
- **Dynamic** (via `MeshOPAPolicy` resource): separate resource for runtime policy updates.
  `kuma-migrator` produces static `MeshOPA` output; dynamic config requires manual setup.

## Coding Standards

* Write clean, modular Go code separating CLI commands (`cmd/`) from business logic (`pkg/`).
* Always include unit tests for the YAML transformation logic.
* Ask for user approval before making destructive changes or executing massive file rewrites.
* Keep `ScanForDeprecations` in `deprecation.go` as the post-pass for all deprecation detection.
  It is called on **every output document** regardless of scenario.
* New scenarios go in: `types.go` (constant), `detect.go` (detection), `<name>.go` (transform),
  `transform.go` (routing), `migrator.go` (label constant + report counting).

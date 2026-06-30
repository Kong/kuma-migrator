
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

1.  **The Reference KB:** The authoritative upstream sources (Kuma, Kong Mesh, kuma-website, developer.konghq.com docs, Kong ADRs) live in the **kong-ama knowledge base** at `/Users/baptiste.collard@konghq.com/projects/kong/kong-ama/repos/`. These are kept fresh by `kong-ama/fetch-github-repos.sh` (re-run it to refresh). **DO NOT read these entire directories into your main context.** The local `./reference/` folder now only holds project-unique material not in the KB (`snippets/`, `konnect/`); the stale duplicate clones were removed in favour of the KB.
2.  **Use Sub-agents:** When you need to look up how Kuma implements a struct or what an ADR (or "MADR") says, spawn a sub-agent to search the **kong-ama KB** (`/Users/baptiste.collard@konghq.com/projects/kong/kong-ama/repos/`), extract the specific Go structs or rules, and report back.
3.  **Skills:** The exact mapping rules for translating YAMLs will be stored in `./.claude/skills/migration-rules.md`. Always consult this file before writing translation logic.
4.  **Preserve YAML:** When rewriting YAML files, ensure you do not drop unrelated fields. Use strict unmarshaling based on Kuma's native Go structs where possible.

## References

Upstream sources live in the **kong-ama KB** (`/Users/baptiste.collard@konghq.com/projects/kong/kong-ama/repos/`), refreshed via `kong-ama/fetch-github-repos.sh`:

* `repos/kuma/` — Kuma codebase. Kuma MADRs (ADRs) are under `repos/kuma/docs/madr/decisions`.
* `repos/kong-mesh/` — Kong Mesh codebase (enterprise fork of Kuma). Use it for enterprise-specific policies like `MeshOPA`, `MeshOPAPolicy`, etc. Kong Mesh MADRs are under `repos/kong-mesh/docs/madr/decisions`.
* `repos/kuma-website/app/` — Kuma user-facing documentation.
* `repos/developer.konghq.com/app/` — Kong developer docs (Kong Mesh docs + per-version CHANGELOG under `app/assets/mesh/raw/CHANGELOG.md`).
* `repos/architecture-decision-records/` — Kong (KongHQ-CX) ADRs.

Project-unique material that is **not** in the KB stays under `./reference/`:

* `reference/snippets/` — demo/setup shell snippets and example manifests (mesh bootstrap, MeshService, kuma.io-service).
* `reference/konnect/` — Konnect CP manager manifest.

## CLI Commands

```
kuma-migrator extract --kube-context <ctx>    --output-dir <dir> [--mesh <mesh>] [--output-format kubernetes|universal]
kuma-migrator extract --kumactl-context <ctx> --output-dir <dir> [--mesh <mesh>] [--output-format kubernetes|universal]
kuma-migrator plan    --input-dir <dir> --output-dir <dir>        [--mesh <mesh>]
kuma-migrator migrate --input-dir <dir> --output-dir <dir>        [--mesh <mesh>]
```

### extract command

Two mutually-exclusive modes, both write one YAML file per resource:

**`--kube-context`** — queries the kube API server for all `kuma.io/v1alpha1` CRDs, fetches
every instance with `kubectl get <kind> -o yaml`. Insight kinds are excluded via `isInsightKind`.

**`--kumactl-context`** — resolves the context from `~/.kumactl/config` (or `$KUMACTL_CONFIG`),
calls `GET <cpURL>/_resources` to discover all writable resource types, lists mesh names (via
direct HTTP for Konnect, `kumactl get meshes -o yaml` for self-hosted CPs), then calls
`kumactl get <path> [--mesh <mesh>] -o yaml` for each type × mesh combination.

The `readOnly` field from `/_resources` is intentionally **ignored**. When the CP API server
is configured with `ApiServer.ReadOnly=true` (common on self-hosted Global CPs), every type
is reported as `readOnly=true`, which would produce zero results. Insight kinds are excluded
by name (`isInsightKind`) instead.

Both modes detect the CP mode at runtime (`GET <cpURL>/config` for kumactl; `KUMA_MODE` env var
on the CP Deployment for kubectl) and apply origin-based filtering:

| CP mode | Filter applied |
|---|---|
| `zone` | Only `kuma.io/origin: zone` kept; resources with `origin: global` or no label skipped (except gateway-local kinds) |
| `global` | Resources with `kuma.io/origin: zone` **skipped** — these are zone-created policies synced read-only to the Global CP. The user is told which zone to target (via `kuma.io/zone` label). Resources with `origin: global` or no label are extracted normally. |
| `standalone` / unknown | All resources extracted (no origin filter) |

Zone-origin skips on Global CP are accumulated into `[]ZoneOriginSkip` and printed after
`ExtractDone` as a `⚠` warning section listing each skipped resource and the zone to target.
Unknown mode falls back to extracting everything.

The kumactl path also reads the `environment` field from `GET <cpURL>/config`
(`"kubernetes"` or `"universal"`) to select the appropriate default skip list. See
**Environment-aware skip lists** below.

**`--mesh <name>` filter**: when set, only the named mesh is iterated for Mesh-scoped resources.
Global-scoped resources (no mesh association) are always extracted regardless of this flag.

**Output directory layout** — context-first: context+mode label at the top level, mesh name
(prefixed with `mesh-`) underneath, kind subfolder last. Global-scoped resources go into a
`global-scoped-resources/` subdirectory alongside the per-mesh directories:
```
<output-dir>/
  <context>-global-ctx/         ← kumactl/kubectl context name + "-global-ctx"
    mesh-<mesh-name>/           ← one dir per Kuma mesh, prefixed with "mesh-"
      <kind-subfolder>/
    global-scoped-resources/    ← global-scoped resources (Zone, HostnameGenerator, …)
      <kind-subfolder>/
  <context>-zone-ctx/           ← same for zone CPs
    mesh-<mesh-name>/
      <kind-subfolder>/
  <context>-standalone-ctx/
    ...
```

`cpModeDirectoryLabel(contextName, mode string) string` in `cpmode.go` builds the top-level
directory label: `contextName + "-global-ctx"` / `"-zone-ctx"` / `"-standalone-ctx"` / `"-unknown-ctx"`.

`GlobalScopedDir = "global-scoped-resources"` and `MeshDirPrefix = "mesh-"` constants in
`cpmode.go` are used by both the extractor and migrator path-building code.

Key files: `pkg/extractor/kube.go`, `pkg/extractor/kumactl.go`, `pkg/extractor/extractor.go`,
`pkg/extractor/cpmode.go`.

The `--mesh` flag and `--output-format` flag are threaded through:
- `ExtractViaKumactl(contextName, outputDir, meshFilter, outputFormat string)` — filters `loopMeshes`; passes outputFormat down; accumulates `[]ZoneOriginSkip` and calls `printZoneOriginSkips` after `ExtractDone`
- `ExtractViaKubectl(kubeContext, outputDir, meshFilter, outputFormat string)` — same accumulation; outputFormat accepted but unused (kubectl always returns K8s format)
- `dumpKumactlResources(..., meshName, meshFilter, outputFormat string, skips *[]ZoneOriginSkip)` — passes all to `writeResourceFiles`
- `dumpCRDInstances(..., cpModeDir, meshFilter string, skips *[]ZoneOriginSkip)` — reads mesh from `kuma.io/mesh` label; applies zone-origin filter inline before per-resource YAML fetch
- `writeResourceFiles(data, outputDir, skipSet, cpMode, cpModeDir, meshName, meshFilter, outputFormat string, skips *[]ZoneOriginSkip)` — skip if `meshFilter != "" && meshName != "" && meshName != meshFilter`; applies `universalToKubernetes` conversion when `outputFormat == "kubernetes"` and resource lacks `kind`
- Path computed as `<outputDir>/<cpModeDir>/mesh-<meshName>/<sub>` (or `<cpModeDir>/global-scoped-resources/<sub>` for global-scoped)

`ZoneOriginSkip` struct (in `extractor.go`): `Kind`, `Name`, `ZoneName` (value of `kuma.io/zone` label, empty when absent).
`printZoneOriginSkips(skips []ZoneOriginSkip)` (in `extractor.go`): prints a `⚠` warning section after extraction listing each skipped resource and its zone.

`universalToKubernetes(obj map[string]interface{}) map[string]interface{}` in `extractor.go`
converts a Universal-format resource map to Kubernetes format: `type`→`kind`, `name`→`metadata.name`,
`mesh`→`metadata.labels["kuma.io/mesh"]`, merges existing labels. Drops CP-internal fields
(`kri`, `creationTime`, `modificationTime`). Called inside `writeSingleResourceDoc` when
`outputFormat == "kubernetes"` and the document has `type` but no `kind`.

### Environment-aware skip lists

`config.go` defines two built-in skip lists:
- `DefaultSkipKindsKubernetes` — includes `Dataplane`, `ZoneIngress`, `ZoneEgress`, `Workload`
  (CP-managed on Kubernetes, never hand-authored)
- `DefaultSkipKindsUniversal` — same list minus those four kinds (hand-authored on Universal,
  may contain deprecated fields that the migrator should scan)

`Config.SkipSetForEnv(env string)` picks the right default; an explicit user `skip` list
always takes precedence. The kubectl path always passes `CPEnvKubernetes`; the kumactl path
passes the detected environment from `/config`.

Constants `CPEnvKubernetes = "kubernetes"` and `CPEnvUniversal = "universal"` live in
`pkg/extractor/cpmode.go` alongside the `CPMode*` constants.

### Context metadata file

`ExtractViaKubectl` and `ExtractViaKumactl` both write a `.kuma-migrator.json` file into
the top-level context directory (`<outputDir>/<cpModeDir>/.kuma-migrator.json`) immediately
after `dirLabel` is computed. This records how the extraction was performed:

```json
{"tool":"kubectl","context":"my-kube-ctx","cpMode":"global","isKonnect":false}
```

`ContextMeta` struct and `WriteContextMeta` / `ReadContextMeta` helpers live in
`pkg/extractor/cpmode.go`. The migrator's `report.go` imports the extractor package to call
`ReadContextMeta(inputDir, cpModeDir)` when building the Apply Checklist — it uses `tool`
to decide between `kubectl apply -f <dir>/` (kubectl) and `kumactl apply -f <file>` per file
(kumactl). `kumactl` does not accept a directory as the `-f` argument, so each file is listed
individually. `isKonnect` in the metadata adds a Konnect-specific label to the checklist.
Falls back to `kubectl` when the metadata file is absent (older extract output).

### Konnect (hosted) specifics

- **Detection**: URL contains `api.konghq.com`. Logged as `Platform: Kong Konnect (hosted)`.
- **Authentication**: kumactl stores PATs as `headers: [{key: Authorization, value: "Bearer kpat_..."}]`
  in the kumactl config (not `authType: tokens`/`authConf`). `resolveKumactlContext` scans
  `kumactlAPIServer.Headers` for the `Authorization` key and strips the `Bearer ` prefix.
  Struct: `type kumactlHeader struct { Key, Value string }`.
- **CP mode**: Konnect has no `/config` endpoint. Always treated as Global CP.
- **Resource fetching**: for Konnect, `dumpKumactlResources` bypasses the kumactl CLI and
  makes a direct authenticated HTTP GET. URL shape:
  global-scoped: `<cpURL>/<path>`;
  mesh-scoped: `<cpURL>/meshes/<mesh>/<path>`.
  The `/api` suffix is stripped from the cpURL before constructing resource URLs
  (`strings.TrimSuffix(base, "/api")`).
  Konnect list endpoints always return Universal format `{total, items:[{type,name,...}]}`
  regardless of any `?format=kubernetes` parameter (the format parameter only works for
  single-resource GETs). The `universalToKubernetes` conversion in `writeSingleResourceDoc`
  handles this transparently when `outputFormat == "kubernetes"`.
  The Konnect check is done via `konnectURLCheck` (a package-level `var` defaulting to
  `isKonnectURL`), which tests can override without needing a real `api.konghq.com` URL.
- **Mesh and zone listing**: `listMeshNames` and `listZoneNamesKumactl` both accept `cpURL`
  and `bearerToken`. For Konnect URLs they use `authenticatedGet(<base>/meshes)` /
  `authenticatedGet(<base>/zones)` directly; for self-hosted CPs they fall back to
  `kumactl --context <ctx> get meshes/zones -o yaml`. The kumactl CLI path fails on Konnect
  because the context is not registered in the local kumactl installation.
- **Scope fallback**: `/_resources` sometimes reports resource types as Mesh-scoped but kumactl
  rejects `--mesh` for them ("unknown flag: --mesh"). `isUnknownMeshFlag(err)` detects this
  and retries the extraction globally (breaking out of the mesh loop). This check only applies
  to the kumactl CLI path; Konnect uses direct HTTP and does not trigger it.
- **Universal list format**: kumactl on self-hosted CPs (not Konnect) may return
  `{total: N, items: [...]}` JSON with no top-level `kind`. `writeSingleResourceDoc` detects
  this and recurses into `items`. Konnect list endpoints also return this format (the
  `?format=kubernetes` parameter has no effect on list responses).

### migrate / plan pipeline

`Plan(inputDir, outputDir, meshFilter string)` and `Migrate(inputDir, outputDir, meshFilter string)` call
`runMigration(inputDir, outputDir string, writeFiles bool, meshFilter string)`.

`runMigration` detects the context directory and mesh directory from each file's relative path using
`isKindSubfolder(s string) bool` (returns true for `resiliency`, `routing`, `zero-trust`, `mesh`,
`observability`, `other`). Detection rule: the first non-kind-subfolder path component is `cpModeDir`
(the context label); the second non-kind-subfolder component that is not the reserved
`"global-scoped-resources"` is `meshDir` (with the `"mesh-"` prefix stripped).

| Path pattern | cpModeDir | meshDir |
|---|---|---|
| `<sub>/file.yaml` | `""` | `""` |
| `<anyDir>/<sub>/file.yaml` | `<anyDir>` | `""` |
| `<ctx>/global-scoped-resources/<sub>/file.yaml` | `<ctx>` | `""` (reserved dir) |
| `<ctx>/mesh-<mesh>/<sub>/file.yaml` | `<ctx>` | `<mesh>` (prefix stripped) |

When `meshFilter != ""` and `meshDir != ""` and `meshDir != meshFilter`, the file is skipped.
Files with `meshDir == ""` (no mesh dir detected) are **always** processed regardless of meshFilter.

`processFile(inputPath, outputDir, cpModeDir, meshDir string, ...)` computes the output path as:
- `<outputDir>/<cpModeDir>/mesh-<meshDir>/<sub>/` when both are set (context-first layout)
  - Gateway API output kinds are redirected to `<outputDir>/<cpModeDir>/global-scoped-resources/<sub>/`
- `<outputDir>/<cpModeDir>/global-scoped-resources/<sub>/` when only cpModeDir is set (no mesh → global subdir)
- `<outputDir>/<sub>/` when both are empty (flat / legacy)

`FileReport.CPModeDir` holds the context directory label; `FileReport.MeshDir` holds the plain mesh name (no `mesh-` prefix); `FileReport.InputRelPath` and `FileReport.OutputRelPath` hold the input/output paths relative to their respective root directories (computed in `runMigration` and `processFile` respectively). `FileReport.OutputRelPaths []string` holds **all** output file paths (every doc produced, including split docs like the `-outbound` counterpart from the `from[]`+`to[]` split) — used by the Apply Checklist to enumerate individual files for `kumactl apply`.

**migrate/plan stdout format** — each file line shows: scenario label (fixed 18-char column) · mesh name in bold magenta (omitted for global-scoped) · filename. Two faint gray lines below show `← <inputRelPath>` and `→ <outputRelPath>`. UI helpers: `ui.FileMigrated(scenario, meshName, filename)`, `ui.DocRelPaths(inputRel, outputRel)`.

### Partially-migrated policies (old Kuma-internal MeshService names)

Policies in the wild are sometimes partially migrated: `kind: MeshSubset` was changed to
`kind: MeshService` but the Kuma-generated internal CRD name (e.g. `echo_demo_svc_8000`)
was left unchanged. The migrator handles these transparently:

- **Detection** (`detect.go`): `probeRefHasOldMeshServiceName` fires when a `probeRef` has
  `kind: MeshService` and a name matching the `_svc_` pattern. Checked for `spec.targetRef`,
  `to[]`, and `from[]`. Triggers `ScenarioSubset` even without any `MeshSubset` ref.
- **Conversion** (`convert.go`): `ConvertTargetRef` decodes old-format names via
  `ParseKumaServiceTag` when `kind == "MeshService"` and the name contains `_svc_`.
  - `topLevel=true` (spec.targetRef): → `kind: Dataplane` (MeshService invalid at top level)
  - `topLevel=false` (to[]/from[]): → `kind: MeshService` with decoded `name`/`namespace`
  - Namespace scoping rule applied: same namespace → `name+namespace`; cross-namespace → `labels`

### Universal format YAML (migrate pipeline)

Kuma's Universal format uses `type` instead of `kind` and top-level `name`/`mesh` fields
instead of `metadata`. All migrate-side parsing must normalise these:

- **`DetectScenario`** (`detect.go`): `kind := p.Kind; if kind == "" { kind = p.Type }`.
  All downstream checks use the normalised `kind` variable.
- **`meshNeedsMigration`** (`mesh.go`): `meshProbe` has both `Spec.{Metrics,Tracing,...}` and
  top-level `{Metrics,Tracing,...}` fields. Effective values are resolved with fallback:
  `metrics := p.Spec.Metrics; if metrics == nil { metrics = p.Metrics }`.
- **`TransformMesh`** (`mesh.go`): when `obj["spec"]` is nil (Universal format — no `spec`
  wrapper), sets `spec = obj` so `meshServices`, `metrics`, etc. are read and written at
  the top level. The final `yaml.Marshal(obj)` then produces correct Universal-format output.
- **`TransformFromToRules`** (`rulesapi.go`): uses a `map[string]interface{}` round-trip via
  `applyFromToRulesMap` to preserve all top-level Universal fields (`type`, `name`, `mesh`,
  `kri`, `creationTime`, `labels`). The typed `KubePolicy` struct path (`applyFromToRules`)
  is kept only for the second-pass inside `transformScenarioSubset`.
  **Split when `from[]` + `to[]` coexist**: Kuma 2.10+ forbids `rules[]` and `to[]` in the
  same spec. When both are present `TransformFromToRules` produces **two output documents**:
  the first keeps the original name with `rules[]` (inbound); the second appends `-outbound`
  to the name and carries `to[]` (outbound). A warning is emitted and both must be applied.
- **`extractNameFromObj`**: checks `obj["metadata"]["name"]` first, falls back to `obj["name"]`.

### Kuma resource labels relevant to extraction and migration

| Label | Values | Meaning |
|---|---|---|
| `kuma.io/origin` | `global` / `zone` | Set by CP. `global` = synced from Global CP; `zone` = created locally in this zone. On Global CP, `zone`-origin resources are **skipped** during extraction (use `kuma.io/zone` to find the originating zone). |
| `kuma.io/zone` | zone name | Present on resources with `kuma.io/origin: zone`. Used by the extractor to tell the user which Zone CP to target when skipping a zone-origin resource on a Global CP. |
| `kuma.io/policy-role` | `system` / `producer` / `consumer` / `workload-owner` | Set by CP based on namespace + spec shape. Does **not** affect extraction filtering (origin label covers this). Must be **preserved** by migration transforms — Subset/Passthrough/Rules scenarios do preserve it; Legacy (Universal-format) inputs don't carry it. |

`kuma.io/policy-role` priority order (low → high): `system` → `producer` → `consumer` → `workload-owner`.

## Supported Scenarios (all implemented)

| Scenario | Trigger | Output |
|---|---|---|
| Legacy | `sources`/`destinations` policies or legacy type names | `targetRef`/`to`/`from` |
| Subset | `MeshSubset` **or `MeshService` with old Kuma-internal name** in any targetRef | `Dataplane`/`MeshService` |
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
- `Dataplane transparentProxying.reachableServices` → warn, names must be updated to MeshService display names in Exclusive mode (v2.10)
- `kuma.io/*` annotation `yes`/`no` → scanner, use `true`/`false` (v2.9)
- `MeshTrafficPermission spec.*.spiffeId` → auto-fixed to `spiffeID` casing (v2.12)
- `MeshLoadBalancingStrategy to[].default.loadBalancer.{ringHash,maglev}.hashPolicies` → auto-fixed to `to[].default.hashPolicies` (v2.12; distinct from the `SourceIP→Connection` type change above)
- `MeshService spec.ports[].protocol` → auto-fixed to `appProtocol` (v2.8)
- `MeshMetric`/`MeshTrace`/`MeshAccessLog` inline `openTelemetry.endpoint` → warn, define a `MeshOpenTelemetryBackend` and reference it via `backendRef` (deprecated v2.14, removed 3.0)
- `MeshAccessLog` `openTelemetry.attributes[].key` reserved `otel.` prefix / non-lowercase / placeholder keys → warn, stricter validation rejects on reapply (v2.14)
- `Mesh spec.routing.defaultForbidMeshExternalServiceAccess` → warn, removed in 3.0 (use `MeshTrafficPermission`)
- `Mesh spec.mtls.backends` → **advisory only** (warn): legacy mTLS/identity model; Kuma 2.12+ successor is `MeshIdentity` + `MeshTrust`, and the experimental SPIFFE `rules[]` MTP API requires `MeshIdentity`. **Not auto-converted** — it's a guided CA cutover (Kuma MADR-074); trust domain (zone/runtime-derived), per-workload SPIFFE paths, and CA key material (CP Secret / DataSource / Kong Mesh Vault backend) are not in the manifest, and the builtin backend mints a new CA. `spec.mtls` is **not deprecated** (safe to leave). The `MeshTLS` policy is orthogonal (tlsVersion/ciphers/mode) and is **not** the identity source. `warnMeshMtlsBackends` in `deprecation.go`. MeshFaultInjection never requires identity (its `rules[].matches[].spiffeID` is an optional client selector).
- `MeshTrafficPermission`/`MeshFaultInjection` `from[]` → warn, deprecated in favour of `rules[]` API (MFI v2.13, MTP v2.14, removed 3.0). **Intentionally not auto-converted**: the `rules[]` API matches clients by SPIFFE identity (MTP, via `default.{allow,deny,allowWithShadowDeny}`, requires `MeshIdentity`, default-deny) / `matches[]` SpiffeID·SNI (MFI), while `from[].targetRef` uses tag/label selectors. The SPIFFE trust-domain + identity strings are not present in the manifest, so a mechanical rewrite would either fail or silently widen access (a security regression for MTP). The warning lists the manual steps. `warnFromDeprecatedForRulesAPI` in `deprecation.go`.
- deprecated **top-level `spec.targetRef.kind`** (any policy) → warn: `MeshSubset` (only when no service-identity tags) / `MeshService` / `MeshServiceSubset` → use `Dataplane` with labels; `MeshHTTPRoute` → reference in `spec.to[].targetRef` (v2.10/2.11). Mirrors upstream `validators.TopLevelTargetRefDeprecations`. Warn-only (not auto-converted) because a `MeshService`/`MeshServiceSubset` selector can't be expanded to the equivalent `Dataplane` label set from the manifest alone — only the legacy Kuma-internal `_svc_` names carry enough info, and those are already rewritten to `Dataplane` by `ScenarioSubset` before this post-pass. `warnDeprecatedTopLevelTargetRef` in `deprecation.go`.
- `Mesh`/`MeshService`/`MeshExternalService`/`MeshMultiZoneService` names violating RFC 1035 or exceeding 63 chars → warn, becomes a hard error in 3.0 (via `ValidateResourceName`)

`ScanForDeprecations` normalises `kind` from `obj["type"]` when `obj["kind"]` is empty, so
Universal-format resources (including `Dataplane`) are handled correctly.
`warnDataplaneRedirectPortInboundV6` checks both top-level `networking` (Universal) and
`spec.networking` (Kubernetes) paths.

## Kong Mesh Specifics

### Two-minor-version upgrade constraint
Kong Mesh supports upgrading **at most two minor versions** at a time.
Example valid path: 2.8 → 2.10 → 2.12 → 2.14.
Skipping more than one minor version is unsupported.
Latest released as of 2026-06: **Kuma 2.14.0** and **Kong Mesh 2.14.0** (2.13.x is the Kong Mesh LTS line). 3.0 is the next major and carries several removals the deprecation scanner now warns about.

### OPAPolicy → MeshOPA
- `kind: OPAPolicy` was the legacy Kong Mesh OPA integration. It is **not removed as a CRD**; it became **non-functional under the default dynamic-config OPA runtime in Kong Mesh 2.13.x** (the legacy runtime can still be selected with `KMESH_OPA_EXPERIMENTAL_USE_DYNAMIC_CONFIG=false`). Migrating to `MeshOPA` is the supported forward path.
- `kind: MeshOPA` is the new policy. Structural change:
  - `spec.conf.policies[].inlineString` → `spec.default.appendPolicies[].rego.inlineString`
  - `spec.conf.agentConfig.inlineString` → `spec.default.agentConfig.inlineString` (if present)
- The `targetRef` is preserved as-is.

### MeshOPA dynamic vs static config
- **Static** (current `MeshOPA`): `spec.default.appendPolicies[].rego.inlineString`
- **Dynamic** (via `MeshOPAPolicy` resource): separate resource for runtime policy updates.
  `kuma-migrator` produces static `MeshOPA` output; dynamic config requires manual setup.

## Distribution

Homebrew tap: **`bcollard/homebrew-kuma-migrator`** (GitHub: `bcollard/homebrew-kuma-migrator`).
Do **not** publish to `Kong/homebrew-kuma-migrator`.

## Coding Standards

* Write clean, modular Go code separating CLI commands (`cmd/`) from business logic (`pkg/`).
* Always include unit tests for the YAML transformation logic.
* Ask for user approval before making destructive changes or executing massive file rewrites.
* Keep `ScanForDeprecations` in `deprecation.go` as the post-pass for all deprecation detection.
  It is called on **every output document** regardless of scenario.
* New scenarios go in: `types.go` (constant), `detect.go` (detection), `<name>.go` (transform),
  `transform.go` (routing), `migrator.go` (label constant + report counting).

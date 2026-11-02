
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

1.  **The Reference KB:** The authoritative upstream sources (Kuma, Kong Mesh, kuma-website, developer.konghq.com docs, Kong ADRs) live in the **kong-ama knowledge base** at `/Users/baptiste.collard/projects/kong/kong-ama/repos/`. These are kept fresh by `kong-ama/fetch-github-repos.sh` (re-run it to refresh). **DO NOT read these entire directories into your main context.** The local `./reference/` folder now only holds project-unique material not in the KB (`snippets/`, `konnect/`); the stale duplicate clones were removed in favour of the KB.
2.  **Use Sub-agents:** When you need to look up how Kuma implements a struct or what an ADR (or "MADR") says, spawn a sub-agent to search the **kong-ama KB** (`/Users/baptiste.collard/projects/kong/kong-ama/repos/`), extract the specific Go structs or rules, and report back.
3.  **Skills:** The exact mapping rules for translating YAMLs will be stored in `./.claude/skills/migration-rules.md`. Always consult this file before writing translation logic.
4.  **Preserve YAML:** When rewriting YAML files, ensure you do not drop unrelated fields. Use strict unmarshaling based on Kuma's native Go structs where possible.

## References

Upstream sources live in the **kong-ama KB** (`/Users/baptiste.collard/projects/kong/kong-ama/repos/`), refreshed via `kong-ama/fetch-github-repos.sh`:

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
kuma-migrator migrate --input-dir <dir> --output-dir <dir>        [--mesh <mesh>] [--to-latest v2|v3] [--dry-run]
```

There is no separate `plan` command — `--dry-run` on `migrate` is what used to be `plan` (removed;
see git history around the `--dry-run` flag's introduction). `migrator.Plan`/`migrator.Migrate` in
`pkg/migrator` are still two separate Go functions (kept for their own extensive test coverage),
but `cmd/migrate.go`'s `RunE` is the only caller and picks between them based on `--dry-run`.

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

`AccessRole` is **not** in either list (it is scanned for Kong Mesh 3.0 changes);
`AccessRoleBinding` still is. `AccessAudit` was never skipped.

`migrate` uses the same split: `runMigration` reads `ContextMeta.Environment` from each context
directory's `.kuma-migrator.json` and builds a per-document `skipPolicy`. When no environment is
recorded (older extract), a Universal-shaped document (`type`, no `kind`) gets the Universal list
and anything else the Kubernetes list. `Config.SkipSet()` is no longer called by `migrate` —
before this, `migrate` always used the Kubernetes list, so no Dataplane was ever scanned.

Constants `CPEnvKubernetes = "kubernetes"` and `CPEnvUniversal = "universal"` live in
`pkg/extractor/cpmode.go` alongside the `CPMode*` constants.

### Context metadata file

`ExtractViaKubectl` and `ExtractViaKumactl` both write a `.kuma-migrator.json` file into
the top-level context directory (`<outputDir>/<cpModeDir>/.kuma-migrator.json`) immediately
after `dirLabel` is computed. This records how the extraction was performed:

```json
{"tool":"kubectl","context":"my-kube-ctx","cpMode":"global","isKonnect":false,"environment":"kubernetes"}
```

`environment` (`kubernetes`/`universal`) selects the skip list in `migrate` (see above).

When `GET <cpURL>/config` fails, `detectKumactlCPMode` prints `cpConfigUnavailableWarning` and
falls back to unknown mode. Kong Mesh 3.0 requires the `VIEW_CONTROL_PLANE_METADATA` access type
for `/config`, so a 401/403 is the expected failure for a role without it.

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

### Target major version (`--to-latest`)

`migrate` (including its `--dry-run` mode) accepts `--to-latest v2|v3` (default `v2`). `TargetVersion` lives in
`pkg/migrator/target.go` (`TargetV2`, `TargetV3`, `ParseTargetVersion`, `IsV3()`,
`Describe()`, `removalNote()`), and is threaded through
`Plan`/`Migrate` → `runMigration` → `processFile` → `TransformDocument` → `ScanForDeprecations`
and the individual transforms.

| Target | Meaning | Behaviour |
|---|---|---|
| `v2` (default) | latest 2.x (2.14.x) | Output stays applicable to a 2.x CP. 3.0 removals are reported as forward-looking advisories, never rewritten. |
| `v3` | 3.0 | 3.0 removals are rewritten where the manifest carries enough information; otherwise reported as blocking work. |

The split exists because the two lines need genuinely different output — 3.0 removes fields
2.14 still requires, and some 3.0 replacements (`MeshOpenTelemetryBackend`, `SecureDataSource`)
do not exist before 2.14.

**Target-sensitive behaviour:**

- `Dataplane networking.inbound[].tags` → warns **only under v3**. Inbound tags are mandatory in
  2.x (`kuma.io/service` is on every Universal Dataplane), so a v2 advisory would fire on every
  Dataplane in the input and be unactionable.
- `MeshOPA spec.targetRef.{name,namespace,mesh}` → **auto-converted under v3** by
  `fixMeshOPATargetRefForV3` in `opapolicy.go`; preserved under v2.
- inline `openTelemetry.endpoint` → under v2 the warning explicitly says *do not* migrate while
  any CP/DP is below 2.14 (MOTB does not exist there and the CP silently skips the OTel route);
  under v3 it is a hard removal.
- `MeshExternalService` TLS `DataSource` and `MeshGlobalRateLimit` warnings change wording by target.

The selected target is recorded on `MigrationReport.Target` and printed in the report header;
a v3 report also carries a banner warning that the output must not be applied to a 2.x CP.

### migrate (and its --dry-run mode) pipeline

`Plan(inputDir, outputDir, meshFilter string, target TargetVersion)` and
`Migrate(inputDir, outputDir, meshFilter string, target TargetVersion)` call
`runMigration(inputDir, outputDir string, writeFiles bool, meshFilter string, target TargetVersion)`.
`cmd/migrate.go`'s `RunE` calls `Plan` when `--dry-run` is set and `Migrate` otherwise — there is
no separate `plan` subcommand.

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

**migrate/--dry-run stdout format** — each file line shows: scenario label (fixed 18-char column) · mesh name in bold magenta (omitted for global-scoped) · filename. Two faint gray lines below show `← <inputRelPath>` and `→ <outputRelPath>`. UI helpers: `ui.FileMigrated(scenario, meshName, filename)`, `ui.DocRelPaths(inputRel, outputRel)`.

**`REWRITTEN` label** (`labelRewritten`): a `ScenarioPassthrough` document whose output differs
from its input (`sameYAML`) or that gained companion documents — e.g. v3 label stripping,
`name`→labels refs, inline OTel endpoint → generated `MeshOpenTelemetryBackend`. Same
`DocChange.Rewritten` mechanism as `SCANNED` (which is the non-policy equivalent). Counted in
`MigratedCount`, listed in the migrated-files tables with Scenario `Passthrough, rewritten for
the target` (`scenarioCell`), printed via `ui.FileMigrated("REWRITTEN", …)`.

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
- **Universal in ⇒ Universal out, for every converter.** The legacy converters build `KubePolicy`
  structs; `TransformDocumentWithOptions` re-renders them with `kubePolicyAsUniversal` when
  `isUniversalShape(raw)` (top-level `type`/`name`/`mesh`, input top-level `labels` carried over,
  `kuma.io/mesh` never a label, body under `spec`). Generated companions use `buildCompanion`
  (`mesh.go`), whose Universal branch is the same `universalDoc` helper; the v3 MeshHealthCheck
  panic-threshold snippet uses it too. Gateway API output is Kubernetes-only by nature.
  `universal_output_test.go` pins every scenario × target.

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
| Legacy | `sources`/`destinations`/`selectors` policies or legacy type names | `targetRef`/`to`/`from`/`rules`/`default` — see **Legacy policy conversion** |
| Subset | `MeshSubset` **or `MeshService` with old Kuma-internal name** in any targetRef | `Dataplane`/`MeshService` |
| Passthrough | Already using `MeshService` — pass-through | unchanged |
| Rules | New-style Mesh* with deprecated `from[]` (Kuma 2.10+) | `rules[]` |
| Mesh | `Mesh` CRD with embedded observability | standalone companion CRDs |
| ExternalService | `ExternalService` | `MeshExternalService` |
| GW | `MeshGateway`, `MeshGatewayInstance`, `MeshGatewayRoute` | Gateway API CRDs. **`MeshHTTPRoute`/`MeshTCPRoute` are NOT in this scenario** — they are current policies on 2.x and 3.0 and pass through unchanged |
| OPAPolicy | Kong Mesh `OPAPolicy` | `MeshOPA` |

## Legacy policy conversion (ScenarioLegacy)

The 12 legacy (non-`Mesh*`) policy resources are the set Kuma 3.0 removes from the
resource registry. Coverage:

| Legacy kind | Handling |
|---|---|
| `Timeout`, `CircuitBreaker`, `Retry`, `HealthCheck`, `TrafficLog` | converted, **outbound** shape |
| `TrafficPermission`, `FaultInjection`, `RateLimit` | converted, **inbound** (inverted) shape |
| `TrafficTrace`, `ProxyTemplate` | converted, **selector** shape |
| `TrafficRoute` | error — ambiguous HTTP vs TCP, manual |
| `VirtualOutbound` | error — manual (see below) |

### Shape (`legacyShapes` in `transform.go`)

| Shape | Mapping |
|---|---|
| outbound | `sources` → `spec.targetRef`; `destinations` → `to[]` |
| inbound | `destinations` → `spec.targetRef`; `sources` → `from[]` |
| selector | `selectors` → `spec.targetRef`; conf → `spec.default` (no `to[]`/`from[]` on `MeshTrace`/`MeshProxyPatch`) |

`FaultInjection` and `RateLimit` are inbound policies like `TrafficPermission` — the
fault/limit is enforced by the **destination's** sidecar. `UniversalPolicy.Selectors`
must exist or `TrafficTrace`/`ProxyTemplate` parse with zero sources and zero
destinations and lose both scope and conf.

`RateLimit` → `MeshRateLimit` emits **`rules[]`, not `from[]`**: `from[]` accepts only
`kind: Mesh` and 3.0 removes it, with `rules[]` documented as the mechanical
equivalent for the all-clients case. Any specific `sources` selector is lost — warned
as a scope widening.

An empty `destinations` list means "all destinations": substitute a wildcard selector
so the conf still has a `to[]` entry, otherwise it is dropped silently.

### `conf` → `default` (`legacyconf.go`)

A legacy `conf` body is **never** structurally compatible with the new `default`
section; a verbatim copy applies cleanly and silently does nothing. One converter per
kind, each written as an explicit list of moves so `confMapper.unmapped()` can warn
about every input leaf no move consumed. Highlights (full table in
`.claude/skills/migration-rules.md` §6b):

- `Timeout`: `connectTimeout`→`connectionTimeout`, `tcp.idleTimeout`→`idleTimeout`,
  no `grpc` section (folds into `http`)
- `CircuitBreaker`: `thresholds`→`connectionLimits`, rest under `outlierDetection`,
  every detector renamed (`totalErrors`→`totalFailures`, `standardDeviation`→`successRate`, …)
- `Retry`: `tcp.maxConnectAttempts`→`maxConnectAttempt` (singular), `retryOn` enum
  recased, `retriableMethods`→`retryOn: [HttpMethod*]`, grpc `cancelled`→`Canceled`;
  `retriableStatusCodes` has **no equivalent** (dropped, warned)
- `HealthCheck`: `http.requestHeadersToAdd` → `{add,set}` split (`append` unset ⇒ `add`)
- `FaultInjection`: single conf → one-element `default.http[]`
- `RateLimit`: `http.{requests,interval}`→`local.http.requestRate.{num,interval}`
- `ProxyTemplate`: `modifications`→`appendModifications`, operations recased;
  `httpFilter`/`networkFilter`/`virtualHost` have no plain `Add` → `AddLast` (warned);
  `imports`/`resources` have no equivalent

Percentages are emitted as numbers when whole and strings when fractional — the new
schemas type them `anyOf: [integer, string]`.

### Cross-document context (`TransformOptions`)

`TrafficLog`/`TrafficTrace` `conf.backend` is a **name reference** into
`Mesh.spec.logging.backends` / `spec.tracing.backends`; `MeshAccessLog`/`MeshTrace`
declare the backend inline. `runMigration` runs `BuildMeshBackendIndex(inputDir)` as a
pre-pass and threads it through `TransformOptions` into
`TransformDocumentWithOptions`. `TransformDocument(raw, target)` remains as a wrapper
with no index — every consumer degrades to a warning naming the backend to write by
hand rather than emitting an empty policy. Backend→`default.backends[]` conversion is
shared with `TransformMesh` via `tracingBackendToNew`/`loggingBackendToNew`
(`legacybackend.go`).

### `VirtualOutbound` — no single successor

`conf.host`/`conf.port` are Go templates rendered from **arbitrary Dataplane tags**
(`parameters[].tagKey`), with a VIP allocated per rendered hostname.
`HostnameGenerator` is the closest replacement but is not equivalent:

| | `VirtualOutbound` | `HostnameGenerator` |
|---|---|---|
| selects | Dataplanes by tags | `MeshService`/`MeshExternalService`/`MeshMultiZoneService` by label selector |
| template vars | any Dataplane tag via `parameters[]` | fixed `.Name`, `.DisplayName`, `.Namespace`, `.Mesh`, `.Zone` + a `label` function |
| port | templatable (`conf.port`) | **not templatable** — ports come from the MeshService |
| result | VIP + hostname | hostname published in the resource's `status.addresses` |

Upstream `UPGRADE.md` directs `VirtualOutbound` → `MeshHTTPRoute`/`MeshTCPRoute` for
the routing half. The migrator errors with both halves named; the original document is
preserved in the output directory (same handling as `TrafficRoute`).

### Non-policy kinds

`recognisedNonPolicyKinds` in `detect.go` → `ScenarioPassthrough`. Currently just
`ContainerPatch`: Kubernetes-only JSON patches for the injected sidecar/init
containers, no `targetRef` successor, **not** one of the 12 removed legacy policies,
and still served in Kuma 3.0. It is copied through unchanged rather than reported as
unrecognised input.

## Kuma / Kong Mesh 3.0 GA coverage (`--to-latest v3`)

Kuma 3.0.0 and Kong Mesh 3.0.0/3.0.1 are released (2026-10). The 3.0.0 sections of
`kuma/UPGRADE.md` and `kong-mesh/UPGRADE_km.md` were audited entry by entry on 2026-11-02 and
every manifest-relevant entry is handled. The 3.0 work lives in dedicated files:

| File | Covers |
|---|---|
| `v3_mesh.go` | `Mesh.mtls` removed (stripped + blocking MeshIdentity warning), `mtls` PERMISSIVE → companion `MeshTLS` `Permissive`, removed Mesh fields (`routing.zoneEgress`, `routing.localityAwareLoadBalancing`, `routing.defaultForbidMeshExternalServiceAccess`, `skipCreatingInitialPolicies`) deleted, `outbound.passthrough: true` → companion `MeshPassthrough` `All` (3.0's default for transparent proxies is `None`), `meshServices` mixed-version note, `MeshMetric` `ActiveMTLSBackend`, inline OTel `endpoint` → generated `MeshOpenTelemetryBackend` + `backendRef.labels` (`convertInlineOtelEndpointsForV3`, called from `TransformDocumentWithOptions`, the only place that can add a document) |
| `v3_policy.go` | CP-owned labels stripped from every Kuma output doc (`stripControlPlaneLabels`, list from kuma `labels/registry.go`), labels-only real-resource targetRefs and route backendRefs (`fixRealResourceTargetRefsForV3`, `fixRouteBackendRefNamesForV3`), `to[].targetRef.kind` matrix from the 3.0 validators (`warnV3ToTargetRefKinds`), `healthyPanicThreshold` → companion `MeshCircuitBreaker`, hand-written MFI `from[]` (all `kind: Mesh`) → `rules[]`, empty MTP match entries, `MeshProxyPatch` circuit breakers |
| `v3_gateway.go` / `v3_mes.go` | route `MeshServiceSubset` backendRefs, MES TLS `inlineString`/`secret` → `SecureDataSource` (`inline` stays warn-only), MES needs a `MeshIdentity` (`MeshIdentityIndex` pre-pass in `legacybackend.go`), MES `kuma.io/zone` label |
| `v3_dataplane.go` | `networking.gateway` (any type) removed + tags → labels, inbound `kuma.io/protocol` tag → `inbound[].protocol`, inbound tags → labels, outbound `tags` → `backendRef`, `reachableServices` → `reachableBackends`, `reachableBackends` name refs → labels, `advertisedAddress`/redirect ports/`ipFamilyMode`/`probes` warnings |
| `v3_scan.go` | `MeshService` `dataplaneTags` → `dataplaneLabels.matchLabels`, `identities[]` `ServiceTag` dropped, `MeshPassthrough` port + filter-chain conflict checks, `ZoneIngress`/`ZoneEgress` removed (+ `MeshZoneAddress` snippet), `ContainerPatch` probe port 9901 → 9902, pod-template annotation scanning (`podAnnotationSites`) |
| `v3_kongmesh.go` | `AccessRole`/`AccessAudit` `rules[].types` renames, `when[]` qualifier cleanup without widening, `when[].targetRef` kind checks; delete-before-upgrade note per removed kind |

Behaviour of the conversion paths under v3:
- Legacy `TrafficPermission` errors (needs SPIFFE `rules[]`); legacy `FaultInjection` converts to
  `rules[]` only when every source is `kuma.io/service: '*'`, otherwise errors.
- A legacy selector with no service tag becomes `Dataplane{labels}` at top level and errors in `to[]`.
- The Subset scenario splits `rules[]` + `to[]` into a `-outbound` doc on **both** targets (it was
  invalid on 2.x too); the `-outbound` half never gets `kuma.io/display-name`.
- Generated Gateway API objects carry `kuma.io/mesh` as a **label** (the annotation is ignored on
  3.0); `backendRefs[].port` is an integer; a cross-namespace backend gets a `ReferenceGrant`
  (both targets — Gateway-parented routes always needed one).

**Output tree under v3**: originals whose conversion errored and documents that pass through as a
kind 3.0 removes go to `<out>/<ctx>/_manual/...` (`ManualDir`), never into `OutputRelPaths`, so the
Apply Checklist cannot apply them. The report adds a "Delete Before Upgrading to 3.0" section and a
checklist step before the Global CP upgrade. v2 output is unchanged (no `_manual`). The kumactl
type spellings in the delete commands (`v3DeleteCommand`) were written from memory — verify them.

**Conversion rules that apply on both targets:**
- A top-level reference to a service is `Dataplane` + `labels: {app, k8s.kuma.io/namespace}` (best-guess
  `app`, warned) — never `Dataplane{name}`: a Dataplane name is the pod name. Selector labels are
  built by `selectorLabelsFromTags`, which drops `kuma.io/*` tags that are not real Dataplane labels.
- v2 legacy `TrafficPermission`/`FaultInjection` sources become `from[]` `MeshSubset{kuma.io/service}`
  (the 2.7+ form), not `MeshService`.
- The Subset scenario is map-based (`transformScenarioSubset`), so Universal input keeps
  `type`/`name`/`mesh`. Any generated doc with an empty kind/type or name is an error.
- Mesh `logging`/`tracing` produce **no** companion: on 2.x a TrafficLog/TrafficTrace activates a
  backend, and its conversion carries the backend inline. `LegacyObservabilityIndex` (pre-pass)
  names which backends were in use. `metrics` produces a `MeshMetric` for `enabledBackend` only.
- Companions go to the system namespace from `resolveSystemNamespace`: config `systemNamespace:`,
  else inferred from the input (`kong-mesh-system` / `kuma-system` / a Kong Mesh-only kind), else
  `kuma-system`. Universal input gets Universal-format companions (`buildCompanion`).
- Gateway API output: `GatewayRefIndex` (pre-pass) maps a `kuma.io/service` tag to the converted
  Gateway's name, namespace and listener names, so route `parentRefs`/`sectionName` name a real
  Gateway. The Gateway (and a namespace-less route) lives in the `MeshGatewayInstance`'s namespace.
  `MeshGatewayConfig` is cluster-scoped (no namespace, no `parametersRef.namespace`).

**Documents the target rejects** — `TransformDocumentWithOptions` returns `*RejectedByTargetError`
(`rejected.go`) **with** the converted docs; `Rejected[].Index` names the rejected ones. Under v3:
`spec.from` on MTP/MFI, a top-level kind other than `Mesh`/`Dataplane`, `name`/`namespace` on a
real-resource ref, a `to[]` kind the 3.0 validator refuses. `processFile` writes the listed docs to
`_manual/` and the rest to the apply tree, and reports the file as `PARTIAL ERROR`.

**Report and checklist:** labels `SCANNED` (non-policy kind the scan rewrote or flagged) and
`MANUAL` (sent to `_manual/` without an error) exist alongside `SKIP`. Under v3 the checklist runs,
before the Global upgrade: the MeshIdentity cutover (when an input Mesh has `mtls`), then
`meshServices.mode: Exclusive` per mesh (`kubectl patch` / `kumactl … | yq … | kumactl apply -f -`);
the v3 `Mesh` files are applied only after the upgrade. `MeshOpenTelemetryBackend` lands in a
`backends/` subfolder applied before policies. `tool=kumactl` + `environment=kubernetes` (not
Konnect) gets `kubectl` commands (`resolveContextTool`). `MeshGatewayConfig` is a v3 removed kind.

**Judgement calls worth knowing:**
- Legacy `HealthCheck` `healthyPanicThreshold` under v3: the companion `MeshCircuitBreaker` must
  enable a detector (3.0's validator requires one and the threshold is ignored without outlier
  detection), so it sets `detectors.totalFailures.consecutive: 5`. That turns on outlier ejection.
- `reachableBackends` name→labels and `MeshService` `dataplaneTags` rewrites are v3-only: on 2.14 a
  labels selector matches same-named services synced from other zones, which widens selection.

Remaining topology gaps (not manifest-level): `standalone` CP mode is removed (rename to `zone`)
and a Kubernetes-native Global CP is unsupported on 3.0. The extractor still labels output
`-standalone-ctx` and detects `mode=global` on Kubernetes.

### Closed

- **`Gateway.spec.gatewayClassName`** — was hardcoded to `gateways.kuma.io/controller`, which is
  a **controllerName**, not the name of a GatewayClass object. It named nothing the tool creates,
  so every converted Gateway applied cleanly and was never reconciled — on **v2 as well as v3**.
  `resolveGatewayClassName` (`gateway.go`) now resolves the real class, target-aware:
  - **v2** — the GatewayClass is generated from the companion `MeshGatewayInstance` and named
    after it. The two documents are linked only by the shared `kuma.io/service` tag
    (`MeshGateway.spec.selectors[].match` / `spec.tags` vs `MeshGatewayInstance.spec.tags`), so
    `GatewayClassIndex` maps tag → class name, built by the same pre-pass as `MeshBackendIndex`.
    Ambiguity (one tag, several instances) picks the first and warns.
  - **v3** — nothing to resolve: 3.0's Gateway API integration is reduced to `HTTPRoute` and,
    since kuma#18280 (merged 2026-09-01), `GRPCRoute` (`plugin_gateway.go` registers an
    `HTTPRouteReconciler` plus a `GRPCRouteReconciler`; the sole surviving `GatewayClass` path,
    `removeGatewayClassFinalizers`, strips finalizers from Kuma-controlled classes at startup).
    The operator must point the Gateway at whatever implementation they adopt.
  - Unresolvable on either target → `gatewayClassPlaceholder`
    (`REPLACE-WITH-YOUR-GATEWAYCLASS`) plus a warning. An unresolvable class leaves the Gateway
    unreconciled either way, so the placeholder's job is to read as a to-do in
    `kubectl describe gateway` rather than as a Kuma misconfiguration.

  **`MeshGateway` is still converted under v3** rather than erroring like `MeshGatewayInstance`:
  the listener block (ports, protocols, hostnames, TLS `certificateRefs`) is valid Gateway API on
  3.0 and is the whole value of the conversion. Only the class reference was ever 3.0-invalid.
  `MeshGatewayRoute`/`MeshHTTPRoute` → `HTTPRoute` is unaffected (that reconciler survives).
- **`MeshGatewayInstance` under v3** — `TransformMeshGatewayInstance(raw, target)` now errors
  under `--to-latest v3` instead of emitting `GatewayClass` + `MeshGatewayConfig`, both of which
  are dead on 3.0. The error names what was removed, points at the delegated-gateway
  replacement (a Deployment/Service you own), carries over the settings the manifest does hold
  (`replicas`, `serviceType`, `tags`) via `carriedGatewayInstanceSettings`, says to delete the
  leftover `MeshGateway*` objects before upgrading, and says to re-run with `--to-latest v2` for
  the old output. A pod spec and container image cannot be synthesised from a
  MeshGatewayInstance, so this is reported rather than half-generated. **v2 behaviour is
  unchanged.**

  The error used to recommend labelling the pod `kuma.io/gateway: enabled`. That was **wrong for
  3.0**: `UPGRADE.md`'s "`kuma.io/gateway` is removed" entry deletes the Pod annotation, the
  `Dataplane` label and `Dataplane.networking.gateway` outright — the marking is removed, not
  renamed, and a Pod still carrying it is *ignored* rather than rejected, so following the old
  advice silently leaves the gateway's traffic redirected into Envoy and subject to
  `MeshTrafficPermission`/mTLS. The error now names `traffic.kuma.io/exclude-inbound-ports`
  (comma-separated, no all-ports spelling) on the pod plus `kuma.io/ignore: "true"` on the
  fronting Service, and the Universal equivalent (`kuma-dp --exclude-inbound-ports`).
- **`MeshGateway` as a `targetRef.kind`** — 3.0 removes the built-in gateway API in full
  (`MeshGateway`, `MeshGatewayRoute`, `MeshGatewayInstance`, `MeshGatewayConfig` — resources,
  Go/proto types, CRDs *and* KDS sync), and with it `MeshGateway` stops being a valid
  `targetRef.kind` for any policy. Covered by the uniform v3 rule in
  `warnDeprecatedTopLevelTargetRef` (below). Note the 3.0 notes retire the built-in gateway in
  **three separate entries**, and the middle one ("Built-in gateway Kubernetes controllers
  removed") says the CRDs are *"not removed by this change"* — that sentence is scoped to that
  change only, and a later entry ("Built-in gateway API and CRDs removed") completes the
  removal. Reading only the middle entry gives the wrong answer.
- **Converted `MeshGateway`/`MeshGatewayRoute` source objects under v3** — the converted Gateway
  API output is valid on 3.0, but the *source* object is not merely left inert: the 3.0 Helm
  chart deletes `meshgateways.kuma.io` / `meshgatewayroutes.kuma.io` and anything still stored
  under them goes with the CRD. `removedGatewaySourceNote(scenario)` (`gateway.go`, called from
  `TransformDocumentWithOptions`) emits the delete-before-upgrading advisory for both kinds
  under `--to-latest v3`. `MeshGatewayInstance` is excluded — it errors instead of converting.
- **Top-level `targetRef` is `Mesh`/`Dataplane`-only for every policy on 3.0** — verified by
  extracting every `SupportedKinds` list from `pkg/plugins/policies/*/api/v1alpha1/valid*.go` on
  kuma master and at `v2.14.4`, plus Kong Mesh's `MeshOPA`. No per-policy exception and no
  system-role exception (the `SystemPolicyRole` branch that allowed `MeshGateway` now lists the
  same two kinds). `warnDeprecatedTopLevelTargetRef(obj, name, kind, target)` therefore applies a
  uniform rule under v3 — including `MeshGateway` and a tag-carrying `MeshSubset`, both of which
  stay silent under v2 — and falls through to a generic advisory for any other kind. `to[]`, by
  contrast, keeps **four** distinct shapes; the full matrix is in `docs/meshhttproute-3.0.md`.

### `MeshHTTPRoute` is NOT removed in 3.0 — it is the compile target

Correcting a wrong model this project carried: `MeshHTTPRoute` and `MeshTCPRoute` both survive
3.0 (`pkg/plugins/policies/meshhttproute`, `meshtcproute` present on master). Kuma's Gateway API
reconcilers translate *inward* — `plugin_gateway.go` registers an `HTTPRouteReconciler` and a
`GRPCRouteReconciler`, and all four conversion paths return `&v1alpha1.MeshHTTPRoute{}`
(`http_route_conversion.go:109,192`, `grpc_route_conversion.go:102,155`). So 3.0 is
*Gateway API `HTTPRoute`/`GRPCRoute` → generated `MeshHTTPRoute` → Envoy*, with hand-written
`MeshHTTPRoute` still fully supported; `UPGRADE.md` protects that path explicitly (*"Gateways you
run yourself and the Gateway API `HTTPRoute` GAMMA path are unaffected"*).

**Decided (and shipped): the conversion is gone.** `MeshHTTPRoute`/`MeshTCPRoute` now detect as
`ScenarioPassthrough`; `route.go` and `route_test.go` were deleted along with the
`ScenarioHTTPRoute`/`ScenarioTCPRoute` constants. Rewriting them was never a 3.0 requirement and
was not free: a generated route lands in the `HTTPRoute`'s own namespace with
`kuma.io/policy-role=producer`, so it *ties* with an equivalent hand-written `MeshHTTPRoute`
(which previously always won, the generated one ranking `system`), and conflicting `HTTPRoute`s
tie-break by `creationTimestamp` rather than name. `MeshTCPRoute` was worse: **Kuma has never had
a Gateway API `TCPRoute` reconciler** (none at `v2.14.4`, none on master), so that output was
never reconciled on any version — the same class of bug as the old hardcoded `gatewayClassName`.

Because the kind now survives into the output, the catch-all advisory moved from
`TransformMeshHTTPRoute` to `warnMeshHTTPRouteNoCatchAll` in `ScanForDeprecations` (v3 only,
map-based, with `httpMatchesAreCatchAll`). Two shared symbols that lived in `route.go`
(`gatewayAPIVersionAlpha2`, `sectionNameFromTags`) moved to `gatewayroute.go`.

`MeshGatewayRoute` is still converted to Gateway API, because on 3.0 the built-in gateway is
deleted and a gateway you run is the only option. But `UPGRADE.md` names the *other* target —
*"Migrate gateway routing to `MeshHTTPRoute`/`MeshTCPRoute`, which replace `MeshGatewayRoute`"* —
which is right when Kuma keeps doing the routing. The manifest does not say which path the
operator is on, so `gatewayRouteSuccessorNote` states the choice instead of guessing, and flags
the TCPRoute caveat.
- **`Dataplane networking.gateway`** — the released 3.0 notes say the whole block (`DELEGATED`
  too) is ignored, not rejected; the proxy loads as an ordinary Dataplane. Handled by
  `fixDataplaneGatewayForV3` (`v3_dataplane.go`), which replaced `warnDataplaneGatewayBuiltinType`.
- **Kong Mesh `MeshOPA` `spec.targetRef.kind: MeshService`** — no longer valid in 3.0 (only
  `Mesh`/`Dataplane` remain). Found auditing `kong-mesh/UPGRADE_km.md` on 2026-09-02.
  `fixMeshOPATargetRefForV3` (`opapolicy.go`) now converts `kind: MeshService` → `kind:
  Dataplane` **before** its existing `name`→label move, so that move (and any conflict it
  reports) targets the label key that matches the *final* kind: `labels["app"]` (Dataplane's
  convention) rather than `labels["kuma.io/display-name"]` (MeshService's) — getting this
  order backwards was the actual bug this closes. `warnMeshOPATargetRefFields` carries the
  matching v2 forward-looking advisory.
- **Kong Mesh `MeshOPA` `spec.default.agentConfig` / `appendPolicies[].rego` legacy
  `DataSource`** — 3.0 replaces the flat `{secret|inline|inlineString}` shape with the
  discriminated `SecureDataSource` (`type` + `insecureInline.value` / `secretRef.{kind,name}`);
  a `MeshOPA` still in the old shape is rejected at write time (`UPGRADE_km.md`: "MeshOPA data
  sources use the SecureDataSource shape"). `fixMeshOPADataSourcesForV3` (`opapolicy.go`)
  auto-converts under `--to-latest v3` — unlike the `MeshExternalService` TLS `DataSource` case
  (which stays warn-only because it holds credential material), rego source and OPA agent
  config are not credentials, so decoding the base64 `inline` variant here introduces no new
  exposure. `warnMeshOPALegacyDataSource` carries the matching v2 advisory. Both fixes are
  wired into `ScanForDeprecations`'s `MeshOPA` case (not just `TransformOPAPolicy`), which
  closes a passthrough gap: a document arriving already as `kind: MeshOPA` (never routed
  through `TransformOPAPolicy`, since `DetectScenario` only maps `kind: OPAPolicy` there) was
  previously warned about but never actually fixed under v3.

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
- `Mesh spec.mtls.backends` → **advisory only** (warn): legacy mTLS/identity model; Kuma 2.12+ successor is `MeshIdentity` + `MeshTrust`, and the experimental SPIFFE `rules[]` MTP API requires `MeshIdentity`. **Not auto-converted** — it's a guided CA cutover (Kuma MADR-074); trust domain (zone/runtime-derived), per-workload SPIFFE paths, and CA key material (CP Secret / DataSource / Kong Mesh Vault backend) are not in the manifest, and the builtin backend mints a new CA. **`Mesh.mtls` is removed in 3.0** and silently dropped on write: a mesh whose only identity source is `mtls` serves plaintext and MTP/MeshTLS stop applying. Under v2 the advisory says the migration is required before 3.0; under v3 `spec.mtls` is stripped with a blocking warning (see the 3.0 GA coverage section). The `MeshTLS` policy is orthogonal (tlsVersion/ciphers/mode) and is **not** the identity source. `warnMeshMtlsBackends` in `deprecation.go`. MeshFaultInjection never requires identity (its `rules[].matches[].spiffeID` is an optional client selector). When the Mesh uses a Kong Mesh enterprise CA backend (`vault`, `acmpca`, `cert-manager`; all removed in Kong Mesh 3.0), the advisory additionally names the **Kong Mesh 2.14 `MeshIdentity` `Extension` provider** that replaces it: `spec.provider.type: Extension` with `spec.provider.extension.name` = `vault` / `acmpca` / `certmanager` (constants in `kong-mesh/pkg/plugins/resources/meshidentity/*/api/*.go`). `certmanager` is Kubernetes-only. This mapping is **undocumented on the docs site** — `meshidentity/index.md` still lists only `Bundled` and `Spire`. Mapping table: `caBackendExtensionMap` in `deprecation.go`.
- `Mesh` without `meshServices.mode: Exclusive` → `TransformMesh` injects it (both targets). 3.0 makes unified naming unconditional and rejects any other mode, and 2.x zones read a missing field as `Disabled`, so it must be set before the Global CP upgrade. Under v3 the warning adds: drop `spec.meshServices` once every zone runs 3.0. (`warnMeshServicesDefaultFlip` was removed: it was unreachable and its text was wrong.)
- `MeshTrafficPermission`/`MeshFaultInjection` `from[]` → warn, deprecated in favour of `rules[]` API (MFI: `rules[]` API landed v2.13.0, `from` deprecated **v2.14.0**; MTP v2.14; removed 3.0). **Intentionally not auto-converted**: the `rules[]` API matches clients by SPIFFE identity (MTP, via `default.{allow,deny,allowWithShadowDeny}`, requires `MeshIdentity`, default-deny) / `matches[]` SpiffeID·SNI (MFI), while `from[].targetRef` uses tag/label selectors. The SPIFFE trust-domain + identity strings are not present in the manifest, so a mechanical rewrite would either fail or silently widen access (a security regression for MTP). The warning lists the manual steps. `warnFromDeprecatedForRulesAPI` in `deprecation.go`.
- deprecated **top-level `spec.targetRef.kind`** (any policy) → warn: `MeshSubset` (under v2, only when no service-identity tags) / `MeshService` / `MeshServiceSubset` → use `Dataplane` with labels; `MeshHTTPRoute` → reference in `spec.to[].targetRef` (v2.10/2.11). Mirrors upstream `validators.TopLevelTargetRefDeprecations`. Warn-only (not auto-converted) because a `MeshService`/`MeshServiceSubset` selector can't be expanded to the equivalent `Dataplane` label set from the manifest alone — only the legacy Kuma-internal `_svc_` names carry enough info, and those are already rewritten to `Dataplane` by `ScenarioSubset` before this post-pass. **Under v3 the rule is uniform and absolute**: `Mesh`/`Dataplane` are the only kinds any policy accepts at top level, so `MeshGateway` (valid at 2.14 for system-role policies, removed with the built-in gateway API) and a *tagged* `MeshSubset` are flagged too, and any other kind falls through to a generic advisory. `warnDeprecatedTopLevelTargetRef(obj, name, kind, target)` in `deprecation.go`; matrix in `docs/meshhttproute-3.0.md`.
- `Mesh`/`Zone`/`MeshService`/`MeshExternalService`/`MeshMultiZoneService` names violating RFC 1035 (**lowercase only**) or exceeding 63 chars → warn, a hard error in 3.0 (via `ValidateResourceName`)
- `MeshTrust spec.origin` → warn. **Removed** in 2.13 from both the API and the Kubernetes CRD schema, so a manifest still setting it can be rejected as unknown-field input (strict validation / server-side apply). Value now published read-only at `status.origin.kri`. This is the single hard YAML break in 2.13. Note the Kuma website still documents `spec.origin` as a live field with no deprecation marker — `UPGRADE.md` is authoritative. `warnMeshTrustOrigin`.
- `HostnameGenerator spec.template` → warn when the rendered template would not be a valid RFC 1123 DNS subdomain (leading/trailing dot, consecutive dots, uppercase). Kuma 2.14 validates at creation; earlier versions accepted it and silently produced a broken hostname. Checked by substituting each `{{ ... }}` expression with one valid label char, so only defects in the literal skeleton are flagged. `warnHostnameGeneratorTemplate`. **HostnameGenerator was removed from both skip lists** so it is actually scanned.
- `MeshPassthrough spec.default.appendMatch[]` → warn on partial wildcards (`*foo.com`), `type: Domain` with protocol `tcp`/`mysql`, and wildcard domains on an L7 protocol with no port. Mirrors upstream `wildcardPartialPrefixPattern` and the surrounding checks in the MeshPassthrough validator (v2.14, rejects on apply). `warnMeshPassthroughDomains`.
- **Deprecated Pod annotations** → warn. `deprecatedPodAnnotations` in `deprecation.go` mirrors upstream `PodAnnotationDeprecations` (`kuma/pkg/plugins/runtime/k8s/metadata/annotations.go`, verified against **release-2.14**), exact-match not prefix-match:
  | Annotation | Note |
  |---|---|
  | `prometheus.metrics.kuma.io/port` / `/path` | deprecated → `MeshMetric` policy |
  | `kuma.io/virtual-probes` | deprecated, removed in a future release; default flipped to disabled in 2.13; replacement is the Application Probe Proxy |
  | `kuma.io/virtual-probes-port` | replaced by `kuma.io/application-probe-proxy-port` |
  | `kuma.io/builtindns` / `kuma.io/builtindnsport` | **no longer supported and IGNORED** → `kuma.io/builtin-dns` / `kuma.io/builtin-dns-port`. Silently runs with the default. |
  | `kuma.io/sidecar-injection` | not supported as an annotation — must be a **label**; as an annotation it has no effect |

  **Do not prefix-match `prometheus.metrics.kuma.io/`**: the `aggregate-<name>-(port|path|enabled|address)` family is *not* deprecated, so a prefix match flags working manifests.
- `Dataplane networking.inbound[].tags` → warn **under v3 only**, removed in 3.0 and dropped **silently** (proto `reserved` + `AllowUnknownFields`), so the manifest applies cleanly and simply loses the tags; anything selecting on them stops matching with nothing logged. `warnDataplaneInboundTags`.
- `MeshExternalService spec.tls.verification.{caCert,clientCert,clientKey}.{inline,inlineString,secret}` → warn, 3.0 replaces `DataSource` with `SecureDataSource` (`type` + `insecureInline.value` / `secretRef`). Under v3, `inlineString` and `secret` are **auto-converted** (`fixMESDataSourcesForV3`, no decoding involved); `inline` stays warn-only: it was base64 and `insecureInline.value` is plain text, so rewriting means decoding credential material and re-emitting it in the clear — an operator decision. 2.14.6+ accepts both shapes. `warnMeshExternalServiceDataSource`.
- `MeshOPA spec.targetRef.{name,namespace,mesh}` → removed in 3.0. **Auto-converted under v3**: `name` → a display-name label (preserving scope), `namespace`/`mesh` dropped. Dropping `name` instead is what causes 3.0's silent scope-widening, where the rego starts evaluating requests it never saw. Refuses to overwrite a conflicting existing display-name label. `fixMeshOPATargetRefForV3` in `opapolicy.go`, `warnMeshOPATargetRefFields` in `deprecation.go`.
- `MeshOPA spec.targetRef.kind: MeshService` → no longer valid in 3.0 (only `Mesh`/`Dataplane`). **Auto-converted under v3**: `kind` → `Dataplane`, and this runs *before* the `name`/`namespace`/`mesh` fixes above so the display-name label lands under the correct key for the final kind — `labels["app"]` (Dataplane's convention), not `labels["kuma.io/display-name"]` (MeshService's). Same functions as above.
- `MeshOPA spec.default.agentConfig` / `spec.default.appendPolicies[].rego` legacy flat `DataSource` (`secret`/`inline`/`inlineString`) → 3.0 requires the discriminated `SecureDataSource` (`type` + `insecureInline.value` / `secretRef.{kind,name}`); the old shape is rejected at write time. **Auto-converted under v3** (`fixMeshOPADataSourcesForV3` in `opapolicy.go`) — unlike `MeshExternalService`'s TLS `DataSource` (left to the operator because it's credential material), rego/agent-config text is not a credential, so decoding the base64 `inline` variant is safe to do automatically. `warnMeshOPALegacyDataSource` carries the v2 advisory. Both MeshOPA v3 fixes are wired into `ScanForDeprecations`'s `MeshOPA` case as well as `TransformOPAPolicy`, so a document that arrives already as `kind: MeshOPA` (bypassing `TransformOPAPolicy` entirely, since `DetectScenario` only routes `kind: OPAPolicy` there) still gets fixed, not just warned about.
- `MeshGlobalRateLimit` → warn, removed in 3.0 with no in-mesh replacement. Leftover objects go **inert** rather than being rejected and Helm does not delete the CRD, so the policy stays listed while enforcing nothing. `warnMeshGlobalRateLimitRemoved`.
- inline `openTelemetry.endpoint` warning now also states the `backendRef` constraints (selects by `labels` only — `name` unsupported; mutually exclusive with inline `endpoint`; MOTB is `kuma-system`-only; `endpoint.path` must be empty with `protocol: grpc`).
- `Dataplane networking.gateway` (**any** type, `DELEGATED` included) → **auto-fixed under v3**: 3.0 ignores the whole block and loads the proxy as an ordinary Dataplane (BUILTIN: with no inbounds). `fixDataplaneGatewayForV3` deletes it, moves non-`kuma.io/` gateway tags into Dataplane labels (needed for MLBS `affinityTags`), drops `kuma.io/gateway`/`kuma.io/proxy-type` labels, and points at `--exclude-inbound-ports`. Silent under v2.
- `MeshLoadBalancingStrategy to[].default.localityAwareness.crossZone` set on a `to[]` entry whose `targetRef.kind` is not `MeshMultiZoneService` → warn **under v3 only**. New 3.0-dev validator restriction (kuma#18210, 2026-08-26; not yet in the 2.14 line). `warnMeshLoadBalancingStrategyCrossZoneTarget`.
- `MeshHTTPRoute` with no catch-all rule (empty `matches[]`, or an unconditional `PathPrefix: "/"` match) → advisory **under v3 only**, kuma#18268 (2026-09-01) changed unmatched requests from falling through to being answered with a `404` on that destination's HTTP ports (upstream's stated reason: *"This is what the Gateway API requires of an `HTTPRoute`, and it is what the GAMMA conformance suite asserts"*; on a gRPC destination the client sees `UNIMPLEMENTED`). It covers **every** HTTP port of the destination when `to[].targetRef` names a `MeshService` with no `sectionName` — easy to hit by accident when a `MeshHTTPRoute` exists only to anchor another policy (`MeshTimeout`/`MeshRetry`/`MeshAccessLog`) via a narrow match. Checked by `warnMeshHTTPRouteNoCatchAll` in `ScanForDeprecations` (`httpMatchesAreCatchAll`); `MeshHTTPRoute` is passthrough. A match without a path ranks as `PathPrefix /` on 3.0 but is not treated as catch-all.

- `MeshHTTPRoute` `spec.to[].rules[].default.backendRefs[]` (kind `MeshService`) naming a `port`/`sectionName` the `MeshService` lacks → warn **under v3 only** (UPGRADE.md "A `MeshHTTPRoute` `backendRef` naming a port the destination lacks now fails closed": unresolved instead of falling through to another port; all-unresolved rule answers 500). Matching mirrors `MeshServiceResource.FindPortByName`: the ref string equals a port's `name` or its `port` number as a string; `targetPort` never matches. Needs `TransformOptions.MeshServicePorts` (`*MeshServicePortIndex`, built by `BuildTransformOptions`, keyed by mesh/namespace/display name); a `MeshService` missing from the input is noted **once per run** (`notedMissing` on the shared pointer). Runs in `TransformDocumentWithOptions`' post-pass (`warnRouteBackendRefPortsForV3`, `v3_routeports.go`), after the scan rewrote refs to labels. Mirror backendRefs are skipped.

`ScanForDeprecations` normalises `kind` from `obj["type"]` when `obj["kind"]` is empty, so
Universal-format resources (including `Dataplane`) are handled correctly.
`warnDataplaneRedirectPortInboundV6` checks both top-level `networking` (Universal) and
`spec.networking` (Kubernetes) paths.

## Kong Mesh Specifics

### Two-minor-version upgrade constraint
Kong Mesh supports upgrading **at most two minor versions** at a time.
Example valid path: 2.8 → 2.10 → 2.12 → 2.14.
Skipping more than one minor version is unsupported.
Latest released as of 2026-10-30: **Kuma 3.0.1** / **Kong Mesh 3.0.1**, with 2.14.7 and 2.13.12 (LTS) patch lines. Kong Mesh 3.0 module path is `github.com/Kong/kong-mesh/v3`.

### OPAPolicy → MeshOPA
- `kind: OPAPolicy` was the legacy Kong Mesh OPA integration. It is **not removed as a CRD**; it became **non-functional under the default dynamic-config OPA runtime in Kong Mesh 2.13.x** (the legacy runtime can still be selected with `KMESH_OPA_EXPERIMENTAL_USE_DYNAMIC_CONFIG=false` on 2.x; that env var, `--opa-config-path` and `KMESH_OPA_CONFIG_PATH` are removed on 3.0, where `OPAPolicy` itself is removed). Migrating to `MeshOPA` is the supported forward path.
- `kind: MeshOPA` is the new policy. Structural change:
  - `spec.conf.policies[].inlineString` → `spec.default.appendPolicies[].rego.inlineString`
  - `spec.conf.agentConfig.inlineString` → `spec.default.agentConfig.inlineString` (if present)
- The `targetRef` is preserved as-is.

### Kong Mesh 2.13 / 2.14 API surface

**No enterprise CRD schema changed between 2.12.x → 2.13.x → 2.14.x.** Diffs on
`opa-policy.yaml`, `kuma.io_meshopas.yaml`, `kuma.io_meshglobalratelimits.yaml`, `access-*.yaml`
are `controller-gen` annotation bumps only. Verified against the frozen per-version assets under
`developer.konghq.com/app/assets/mesh/2.1{3,4}.x/raw/crds/`.

- `OPAPolicy` is still shipped, served and storage in **both** 2.13.x and 2.14.x, with no
  `deprecated`/`deprecationWarning` marker on the CRD. It is de-facto legacy from 2.13 (the
  dynconfig path only supports `MeshOPA`) and is **removed in 3.0**, where leftover objects are
  *rejected*. Note `UPGRADE_km.md` describes the removed resource as
  `config.kong-mesh.io/v1alpha1` — that apiVersion is wrong; the shipped CRD is
  `opapolicies.kuma.io`, group `kuma.io`, `scope: Cluster`.
- `KMESH_OPA_EXPERIMENTAL_USE_DYNAMIC_CONFIG` is a **data plane** env var (not CP config).
  Documented only as an opt-out, so the default is `true` in 2.13.x and 2.14.x. Gone on 3.0.
- **`MeshApiRateLimit` does not exist** in any Kong Mesh or Kuma source — do not add handling
  for it. Likely a conflation with OSS `MeshRateLimit` or enterprise `MeshGlobalRateLimit`.
- 2.14 adds `MeshIdentity` `spec.provider.type: Extension` + `spec.provider.extension.{name,config}`,
  with Kong Mesh registering `acmpca`, `vault`, `certmanager`.

### MeshOPA dynamic vs static config
- **Static** (current `MeshOPA`): `spec.default.appendPolicies[].rego.inlineString`
- **Dynamic** (via `MeshOPAPolicy` resource): separate resource for runtime policy updates.
  `kuma-migrator` produces static `MeshOPA` output; dynamic config requires manual setup.

## Distribution

Homebrew tap: **`bcollard/homebrew-kuma-migrator`** (GitHub: `bcollard/homebrew-kuma-migrator`).
Do **not** publish to `Kong/homebrew-kuma-migrator`.

`.github/workflows/release.yml` runs GoReleaser once (`release --clean`), which builds every
platform archive, writes `dist/checksums.txt`, creates the GitHub release, uploads the archives,
and pushes the Homebrew cask to the tap above — all in that single step. Right after, the
workflow attests `dist/checksums.txt` (`actions/attest-build-provenance`, `subject-checksums`)
and uploads the resulting bundle to the same release. This runs *after* publish rather than
before it: GoReleaser has no supported way to build once and pause before publishing (re-running
it would re-embed a fresh `-X main.date`, changing every archive's bytes), so there is no way to
attest strictly before the archives become downloadable without accepting that rebuild-drift.
`dist/` still holds byte-identical copies after the run (`--clean` wipes stale output *before*
building, not after), so the attested digests are guaranteed to match what's already in
`checksums.txt` and already embedded in the cask's `sha256` fields — verified by hand against a
real release (v0.6.1) before adding this. Verify any release artifact with `gh attestation verify
<archive> --repo Kong/kuma-migrator`; documented for users in `docs/installation.md`.

## Documentation site

https://kong.github.io/kuma-migrator/ is GitHub Pages serving `docs/` as-is (`.nojekyll`). The
landing page is hand-written (`docs/index.html`); everything under `/docs/` is **generated** from
`site/` by `site/gen` (a separate Go module, not part of the CLI module) into `docs/docs/`, and the
output is committed. Contract and page format: `site/README.md`.

- `site/content/{guides,topics}/*.md` — pages; `site/data/policies.yaml` — one entry per kind,
  rendered as `/docs/policies/` and `/docs/policies/<kind>/` with v2/v3 behaviour.
- `make site` regenerates; `make site-check` (CI: `.github/workflows/site.yml`) fails when the
  committed output is stale, or on any dangling `.md`/`policy:`/anchor link.
- **When behaviour changes, update the matching `policies.yaml` entry and topic page** in the
  same change, then `make site`. The old `docs/*.md` files are stubs pointing at the new URLs.

## Coding Standards

* Write clean, modular Go code separating CLI commands (`cmd/`) from business logic (`pkg/`).
* Always include unit tests for the YAML transformation logic.
* Ask for user approval before making destructive changes or executing massive file rewrites.
* Keep `ScanForDeprecations` in `deprecation.go` as the post-pass for all deprecation detection.
  It is called on **every output document** regardless of scenario.
* New scenarios go in: `types.go` (constant), `detect.go` (detection), `<name>.go` (transform),
  `transform.go` (routing), `migrator.go` (label constant + report counting).

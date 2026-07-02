# kuma-migrator

A CLI tool that migrates Kuma and Kong Mesh policy YAML manifests across all supported
migration paths — from legacy `sources`/`destinations` policies through to the new
`MeshService`-based API, Rules API, and Gateway API CRDs.

## Background

Kuma and Kong Mesh have evolved their policy API substantially across versions 2.0–2.14
(with several removals slated for 3.0).
`kuma-migrator` automates the mechanical parts of the migration, flags the parts that
require manual intervention, and produces a human-readable Markdown report of every
change made (or that will be made).

## Supported migration paths

| Scenario | Description |
|---|---|
| **Legacy** | Old-style `sources`/`destinations` policies (e.g. `Timeout`, `TrafficPermission`) → `targetRef`/`to`/`from` |
| **Subset** | New `Mesh*` policy types still using `MeshSubset` with `kuma.io/service` or `k8s.kuma.io/service-name` tags → `Dataplane`/`MeshService` |
| **Passthrough** | Already using `MeshService` kind throughout — passed through unchanged |
| **Rules** | New-style `Mesh*` policies with deprecated `from[]` → `rules[]` (Kuma 2.10+) |
| **Mesh** | `Mesh` CRD with embedded observability/passthrough → standalone `MeshMetric`, `MeshTrace`, `MeshAccessLog`, `MeshPassthrough` CRDs |
| **ExternalService** | `ExternalService` → `MeshExternalService` |
| **GW** | `MeshGateway` → `Gateway`, `MeshGatewayInstance` → `GatewayClass`+`MeshGatewayConfig`, `MeshGatewayRoute`/`MeshHTTPRoute`/`MeshTCPRoute` → Gateway API `HTTPRoute`/`TCPRoute` |
| **OPAPolicy** | Kong Mesh `OPAPolicy` → `MeshOPA` (Kong Mesh 2.5+) |

### Deprecated-field warnings (auto-detected, not auto-transformed)

The tool also emits warnings for deprecated fields that require manual action:

- `MeshMetric` `spec.default.sidecar.regex` → `sidecar.profiles.exclude` *(auto-fixed, Kuma 2.7)*
- `MeshService` `spec.ports[].protocol` → `appProtocol` *(auto-fixed, Kuma 2.8)*
- `MeshHealthCheck` `healthyPanicThreshold` moved to `MeshCircuitBreaker` *(warn, Kuma 2.10)*
- `MeshTrafficPermission` `spec.*.spiffeId` → `spiffeID` casing *(auto-fixed, Kuma 2.12)*
- `MeshLoadBalancingStrategy` `loadBalancer.{ringHash,maglev}.hashPolicies` → `to[].default.hashPolicies` *(auto-fixed, Kuma 2.12)*
- `MeshTrust` `spec.origin` deprecated → `status.origin` *(warn, Kuma 2.13)*
- `MeshTrafficPermission`/`MeshFaultInjection` `from[].targetRef.kind: MeshService` deprecated *(warn, Kuma 2.7)*
- `MeshTrafficPermission` `action: ALLOW/DENY` uppercase casing → `Allow`/`Deny` *(warn, Kong Mesh 2.1)*
- `MeshLoadBalancingStrategy` `hashPolicies[].type: SourceIP` → `Connection` *(warn, Kuma 2.10)*
- `Dataplane` `transparentProxying.redirectPortInboundV6` removed *(warn, Kuma 2.9)*
- `Dataplane` `transparentProxying.reachableServices` → MeshService display names / `reachableBackends` *(warn, Kuma 2.10)*
- `MeshMetric`/`MeshTrace`/`MeshAccessLog` inline `openTelemetry.endpoint` → `MeshOpenTelemetryBackend` + `backendRef` *(warn, deprecated 2.14, removed 3.0)*
- `MeshAccessLog` `openTelemetry.attributes[].key` stricter validation (reserved `otel.` prefix, casing, placeholders) *(warn, Kuma 2.14)*
- `Mesh` `spec.routing.defaultForbidMeshExternalServiceAccess` removed *(warn, Kuma 3.0)*
- `Mesh` `spec.mtls.backends` → `MeshIdentity` + `MeshTrust` successor model *(advisory only — guided CA cutover, not a transform; `spec.mtls` is not deprecated)*
- `MeshTrafficPermission`/`MeshFaultInjection` `from[]` deprecated → `rules[]` API *(warn — manual, MFI 2.13 / MTP 2.14)*
- Deprecated top-level `spec.targetRef.kind`: `MeshSubset`/`MeshService`/`MeshServiceSubset` → `Dataplane`; `MeshHTTPRoute` → `spec.to[].targetRef` *(warn, Kuma 2.10/2.11)*
- `kuma.io/*` annotation values `"yes"`/`"no"` → `"true"`/`"false"` *(scanner, Kuma 2.9)*
- Legacy `kuma.io/service`-encoded addresses in Deployment/StatefulSet env vars *(scanner)*
- RFC 1035/1123 name validation for `Mesh*Service` resources — hard error in 3.0 *(warn)*

### MeshTrafficPermission: `from[]` (stable) vs `rules[]` (experimental)

`MeshTrafficPermission` has **two modes**. The migrator flags the deprecated `from[]` field
but does **not** auto-convert it, because the two modes use fundamentally different identity
models and a mechanical rewrite could silently widen access. The difference is only lightly
documented upstream (the stable policy page links to the experimental page; there is no
side-by-side comparison or migration guide), so it is summarised here.

| | Stable (`from[]`) | Experimental (`rules[]`) |
|---|---|---|
| Spec shape | `spec.targetRef` + `from[]`, each `{targetRef, default.action}` | `spec.targetRef` + `rules[]`, each `{default.{allow,deny,allowWithShadowDeny}}` |
| Client selector | tag/label `targetRef` (`Mesh`/`MeshSubset`/`MeshServiceSubset`) | **SPIFFE identity** matchers (`spiffeID`, optional `sni`) |
| Identity source | legacy `Mesh.spec.mtls` (builtin/provided CA); SPIFFE derived from `kuma.io/service` | **`MeshIdentity` + `MeshTrust`** (required) |
| Verbs | `action: Allow` / `Deny` / `AllowWithShadowDeny` per source | `allow[]` / `deny[]` / `allowWithShadowDeny[]` lists of matchers |
| Evaluation | ordered — later `from[]` entries override earlier (last match wins) | `deny` > `allow`/`allowWithShadowDeny` > default |
| Default posture (no policy) | permissive (Kuma ships a default allow-all policy) | **default-deny** |
| Prerequisite | Mutual TLS enabled | `MeshIdentity` enabled |
| Status / version | stable/GA | experimental; matchers since 2.12, `from[]` deprecated in 2.14 |

**Why it's not auto-convertible:** the `rules[]` API matches on SPIFFE identity strings
(`spiffe://<trust-domain>/ns/<ns>/sa/<sa>`), whereas `from[]` uses tag selectors like
`kuma.io/service: orders`. The trust domain (zone/runtime-derived) and the per-workload
identity path are **not present in the policy manifest**, and the posture flips from
permissive to default-deny — so translating `from[]` → `rules[]` is a guided operator task,
not a field mapping. See the [`meshtrafficpermission_experimental`](https://kuma.io/docs/latest/policies/meshtrafficpermission_experimental/)
docs and [`MeshIdentity`](https://kuma.io/docs/latest/policies/meshidentity/) / [`MeshTrust`](https://kuma.io/docs/latest/policies/meshtrust/).

## Installation

### Homebrew (macOS and Linux)

Supported platforms: macOS Apple Silicon (`arm64`), macOS Intel (`amd64`),
Linux `amd64`, Linux `arm64`.

```bash
brew tap bcollard/kuma-migrator
brew install --cask kuma-migrator
```

Or as a one-liner:

```bash
brew install --cask bcollard/kuma-migrator/kuma-migrator
```

Upgrade to the latest version at any time:

```bash
brew upgrade --cask kuma-migrator
```

### Pre-built binaries

Download the binary for your platform from the
[GitHub Releases](https://github.com/Kong/kuma-migrator/releases) page.
Archives are provided for:

| Platform | Architecture |
|---|---|
| Linux | `amd64`, `arm64` |
| macOS | `amd64` (Intel), `arm64` (Apple Silicon) |
| Windows | `amd64` |

**Linux (amd64):**

```bash
VERSION=$(gh release view --repo Kong/kuma-migrator --json tagName --jq '.tagName' | tr -d 'v')
curl -L "https://github.com/Kong/kuma-migrator/releases/latest/download/kuma-migrator_${VERSION}_linux_amd64.tar.gz" | tar xz
sudo mv kuma-migrator /usr/local/bin/
```

**Linux (arm64):**

```bash
VERSION=$(gh release view --repo Kong/kuma-migrator --json tagName --jq '.tagName' | tr -d 'v')
curl -L "https://github.com/Kong/kuma-migrator/releases/latest/download/kuma-migrator_${VERSION}_linux_arm64.tar.gz" | tar xz
sudo mv kuma-migrator /usr/local/bin/
```

### From source

```bash
git clone https://github.com/Kong/kuma-migrator.git
cd kuma-migrator
make build
# binary at ./dist/kuma-migrator
```

## Usage

### Workflow

```
extract → plan → migrate → apply
```

### 1. Extract

Pull resources directly from running control planes into a local directory,
one YAML file per resource, organised by CP context, mesh, and policy type:

```
<output-dir>/
  <context-name>-global-ctx/         ← kumactl/kubectl context name + CP mode suffix
    mesh-default/                     ← mesh name prefixed with "mesh-"
      resiliency/
      routing/
      zero-trust/
      observability/
      mesh/
    mesh-prod/                        ← another mesh
      resiliency/
    global-scoped-resources/          ← global-scoped resources (Zone, HostnameGenerator, …)
      routing/
      mesh/
  <context-name>-zone-ctx/           ← Zone CP resources (kuma.io/origin: zone only)
    mesh-default/
      resiliency/
  <context-name>-standalone-ctx/
    mesh-default/
      resiliency/
```

> **Note on MeshGateway and route CRDs**: these may be created on the Global CP *or* directly on
> a Zone CP depending on your setup. See the [Resource type placement](#resource-type-placement) table below.

#### Resource type placement

Some resource types have a fixed home CP and are handled specially:

| Kind | Where created | Global CP extraction | Zone CP extraction |
|---|---|---|---|
| `MeshGateway`, `MeshHTTPRoute`, `MeshTCPRoute`, `MeshGatewayRoute` | **Global CP** (typical) | extracted normally | skipped (`kuma.io/origin: global`) |
| `MeshGateway`, `MeshHTTPRoute`, `MeshTCPRoute`, `MeshGatewayRoute` | **Zone CP** (less common) | not present | extracted (no origin label) |
| `MeshGatewayInstance` | Zone CP | **skipped** (never synced to Global) | extracted (may have no origin label) |
| `MeshGatewayConfig` | Zone CP | **skipped** (never synced to Global) | extracted (may have no origin label) |
| Policy types with `kuma.io/origin: zone` | Zone CP (synced to Global read-only) | **skipped** + warning printed | extracted (origin: zone) |
| All other policy types | Global CP | extracted normally | skipped unless `kuma.io/origin: zone` |

> `MeshGatewayInstance` and `MeshGatewayConfig` are strictly zone-local and are never synced
> to the Global CP. Extract them by running against each Zone CP.
>
> `MeshGateway` and route CRDs can be created on either the Global CP or a Zone CP.
> When synced from Global to Zone they carry `kuma.io/origin: global` and are filtered out
> on zone extraction. When created directly on a Zone CP they have no origin label and are kept.
>
> **Zone-origin resources on Global CP** — Kuma syncs zone-created policies to the Global CP
> as read-only copies (labelled `kuma.io/origin: zone`). The tool skips these and prints a
> warning after extraction listing each skipped resource and the zone to target (from the
> `kuma.io/zone` label).

The top-level directory encodes the kumactl/kubectl context name and CP mode, with mesh
subdirectories inside. Global-scoped resources (Zone, HostnameGenerator, …) go into
`global-scoped-resources/` alongside the per-mesh directories:

```
<output-dir>/
  prod-cp-global-ctx/              ← context "prod-cp" + global CP
    mesh-default/                  ← mesh (prefixed with "mesh-")
      resiliency/
      routing/
      zero-trust/
      observability/
      mesh/
    mesh-prod/                     ← another mesh
      resiliency/
    global-scoped-resources/       ← global-scoped resources (Zone, HostnameGenerator, …)
      mesh/
  zone-eu-west-zone-ctx/           ← context "zone-eu-west" + zone CP
    mesh-default/
      resiliency/
```

Use `--mesh <name>` to extract only the resources belonging to a specific mesh:

```bash
kuma-migrator extract --kumactl-context global-cp --output-dir ./raw-policies --mesh default
```

#### Always extract from the Global CP first

```bash
# kubectl path
kuma-migrator extract --kube-context prod-global --output-dir ./raw-policies

# kumactl path
kuma-migrator extract --kumactl-context global-cp --output-dir ./raw-policies
```

The tool auto-detects the CP mode and prints it. On a Global CP it also lists
attached zones and notes which resource types are skipped:

```
CP mode:        global
Attached zones: zone-eu-west, zone-us-east
[INFO] MeshGatewayInstance and MeshGatewayConfig are zone-local and skipped here.
       Run extract against each Zone CP to capture gateway instances.
Found 24 writable resource type(s) (skip-list excluded)
Extracted 83 resource(s) to ./raw-policies

  ⚠  Zone-origin resources skipped on Global CP — extract from their zone instead:
     MeshTimeout/my-timeout        →  zone: zone-eu-west
     MeshRateLimit/rate-limit-svc  →  zone: zone-us-east
```

#### Also extract from Zone CPs

Zone CPs contain:
- Policies with `kuma.io/origin: zone` (producer policies, namespace-scoped consumer
  policies, or any policy applied directly to a zone cluster)
- `MeshGatewayInstance` and `MeshGatewayConfig` — zone-local resources that may lack
  `kuma.io/origin` labels but are always extracted from zone CPs

```bash
kuma-migrator extract --kube-context prod-zone-eu --output-dir ./raw-policies
kuma-migrator extract --kube-context prod-zone-us --output-dir ./raw-policies
```

Output is written under `<context>-zone-ctx/mesh-<mesh>/` (e.g. `raw-policies/zone-eu-west-zone-ctx/mesh-default/`).
On a Zone CP the tool warns and filters automatically:

```
CP mode:        zone (eu-west)
[WARN] Extracting from a Zone CP. Only resources with kuma.io/origin: zone will be kept.
       For a complete policy set, also run extract against the Global CP.
[INFO] MeshGatewayInstance and MeshGatewayConfig are zone-local and will be extracted here.
[INFO] MeshGateway and route CRDs (MeshHTTPRoute, MeshTCPRoute, MeshGatewayRoute):
       - If created on the Global CP: synced here with kuma.io/origin: global → skipped (extract from Global CP).
       - If created directly on this Zone CP: no origin label → extracted here.
```

Resources synced from the Global CP (`kuma.io/origin: global`) are always skipped on
Zone CPs — they are already captured by the Global CP extraction.

**kubectl path** — discovers all `kuma.io/v1alpha1` CRDs from the kube API server,
then fetches every instance with `kubectl get <kind> -o yaml`.

**kumactl path** — resolves the context from `~/.kumactl/config` (or `$KUMACTL_CONFIG`),
queries `GET <cpURL>/_resources` to discover all writable resource types, lists all Mesh
names, then calls `kumactl get <type> [--mesh <mesh>] -o yaml` for each type.
Insight kinds are excluded by name. The `readOnly` flag from `/_resources` is intentionally
ignored — when the CP API server is configured with `ApiServer.ReadOnly=true` every type is
reported as read-only, which would produce zero results. The migrator only reads resources
and never writes back through this API, so the flag is irrelevant.

The deployment environment (`kubernetes` or `universal`) is auto-detected from
`GET <cpURL>/config` and printed in the extract output. On Universal CPs, `Dataplane`,
`ZoneIngress`, `ZoneEgress`, and `Workload` resources are **not** skipped — they are
hand-authored YAMLs that may contain deprecated fields the migrator can warn about or fix.

**Kong Konnect (hosted)** — automatically detected when the CP URL contains `api.konghq.com`.
kumactl stores Personal Access Tokens for Konnect as HTTP headers (`Authorization: Bearer kpat_…`)
rather than the `authType: tokens` format used by self-hosted CPs. The tool reads both formats.
Konnect is always treated as a Global CP (no `/config` endpoint). Some resource types are
incorrectly reported as Mesh-scoped in `/_resources` but reject `--mesh`; the tool
automatically retries them as Global-scoped and emits a debug log line.

**Universal format YAML** — kumactl (and Konnect in particular) returns resources in Kuma's
Universal format (`type: MeshMetric`, `name: my-policy` at the top level, no `apiVersion`/`metadata`
wrapper). The extract pipeline handles this transparently, including list responses of the form
`{total: N, items: [...]}`. By default, extracted files preserve the Universal format as-is.
Use `--output-format kubernetes` to have the tool convert Universal resources to Kubernetes
format (`apiVersion`, `kind`, `metadata`) in-place during extraction. Resources that are
already in Kubernetes format (kubectl path) are never modified. The migrate pipeline also
understands Universal format: scenario detection, mesh migration, and the Rules API
from[]→rules[] transformation all work with both formats.

### 2. Plan (dry run)

Preview all changes **without writing any output files**.
Point `--input-dir` at the entire extracted tree and optionally filter to a single mesh:

```bash
# All meshes
kuma-migrator plan --input-dir ./raw-policies --output-dir ./migrated

# Single mesh only
kuma-migrator plan --input-dir ./raw-policies --output-dir ./migrated --mesh default
```

Writes `migration-plan.md` in the output directory. Review before proceeding.

### 3. Migrate

Transform policies and write migrated YAML files:

```bash
# All meshes
kuma-migrator migrate --input-dir ./raw-policies --output-dir ./migrated

# Single mesh only
kuma-migrator migrate --input-dir ./raw-policies --output-dir ./migrated --mesh default
```

The output preserves the input layout, keeping context and mesh subdirectories intact:

```
migrated/
  prod-cp-global-ctx/                ← context + CP mode dir
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
  zone-eu-west-zone-ctx/             ← zone-origin policies
    mesh-default/
      resiliency/
```

**Gateway API resources**: `MeshGateway`, `MeshHTTPRoute`, `MeshTCPRoute`, and `MeshGatewayRoute`
are transformed into Gateway API CRDs (`Gateway`, `HTTPRoute`, `TCPRoute`). Because these are
Kubernetes-native resources that must be applied to **zone clusters** rather than the Global CP,
the migrator redirects them to the `global-scoped-resources/` subdirectory (alongside
global-scoped Kuma resources) even when the source file came from a mesh-scoped input directory.

### 4. Apply (in order)

After upgrading your control planes, apply the migrated manifests in this order.
Substitute `prod-cp-global-ctx` and `zone-eu-west-zone-ctx` with your actual context directory names.

```bash
# 1. Global CP policies — resiliency, routing, zero-trust, observability
kubectl apply -f ./migrated/prod-cp-global-ctx/mesh-default/resiliency/
kubectl apply -f ./migrated/prod-cp-global-ctx/mesh-default/routing/
kubectl apply -f ./migrated/prod-cp-global-ctx/mesh-default/zero-trust/
kubectl apply -f ./migrated/prod-cp-global-ctx/mesh-default/observability/

# 2. Gateway API resources + global-scoped resources — apply to EACH zone cluster
#    These are Kubernetes-native CRDs (Gateway, HTTPRoute, …) and must be applied to
#    zone clusters, not the Global CP. Global-scoped Kuma resources (Zone, HostnameGenerator)
#    also live here.
kubectl --context <zone-eu-west-context> apply -f ./migrated/prod-cp-global-ctx/global-scoped-resources/routing/
kubectl --context <zone-us-east-context> apply -f ./migrated/prod-cp-global-ctx/global-scoped-resources/routing/

# 3. Zone-origin policies (if any were extracted from Zone CPs)
kubectl --context <zone-eu-west-context> apply -f ./migrated/zone-eu-west-zone-ctx/mesh-default/resiliency/
kubectl --context <zone-eu-west-context> apply -f ./migrated/zone-eu-west-zone-ctx/mesh-default/routing/
kubectl --context <zone-eu-west-context> apply -f ./migrated/zone-eu-west-zone-ctx/mesh-default/zero-trust/
kubectl --context <zone-eu-west-context> apply -f ./migrated/zone-eu-west-zone-ctx/mesh-default/observability/

# 4. Mesh CRs last — these enable spec.meshServices.mode: Exclusive
#    Applying them before all other policies are in place will break
#    any workload still addressed by a kuma.io/service tag.
kubectl apply -f ./migrated/prod-cp-global-ctx/mesh-default/mesh/

# 5. Global-scoped Kuma CRs (Zones, HostnameGenerators, etc.)
kubectl apply -f ./migrated/prod-cp-global-ctx/global-scoped-resources/mesh/
```

### 5. (Optional) Skip step 2 when MeshGateway was zone-local

If your `MeshGateway` and route CRDs were created directly on a Zone CP (not via the Global CP),
they will be extracted into `<context>-zone-ctx/<mesh>/routing/` and migrated there.
The migration report will tell you which case applies.

### 6. Clean up original resources

After verifying traffic health, delete the original resources whose **kind changed**
during migration (these cannot be replaced with `kubectl apply` — the old kind must
be explicitly removed):

| Old kind | New kind |
|---|---|
| `Timeout`, `Retry`, `TrafficPermission`, … | `MeshTimeout`, `MeshRetry`, `MeshTrafficPermission`, … |
| `ExternalService` | `MeshExternalService` |
| `MeshGateway` | `Gateway` |
| `MeshGatewayInstance` | `GatewayClass` + `MeshGatewayConfig` |
| `MeshGatewayRoute`, `MeshHTTPRoute`, `MeshTCPRoute` | `HTTPRoute` / `TCPRoute` |
| `OPAPolicy` | `MeshOPA` |

The migration report (`migration-report.md`) contains a ready-to-run
`kubectl delete` command list for all such resources.

### Flags

#### extract

| Flag | Short | Required | Description |
|---|---|---|---|
| `--kube-context` | | one of | Kubernetes context to use (kubectl) |
| `--kumactl-context` | | one of | kumactl context name (kumactl CLI) |
| `--output-dir` | `-o` | yes | Directory to write extracted YAML files |
| `--mesh` | | no | Restrict extraction to the named Kuma mesh (default: all meshes) |
| `--output-format` | `-f` | no | YAML format for extracted files: `universal` (default) or `kubernetes` |
| `--tls-skip-verify` | `-k` | no | Disable TLS certificate verification for the CP admin server (self-signed certs) |

#### plan / migrate

| Flag | Short | Required | Description |
|---|---|---|---|
| `--input-dir` | `-i` | yes | Directory containing source policy YAML files |
| `--output-dir` | `-o` | yes | Directory for output files and the Markdown report |
| `--mesh` | | no | Restrict processing to the named mesh subdirectory (default: all meshes) |

## Console output

```
[MIGRATED LEGACY]  timeout-policy.yaml
[MIGRATED SUBSET]  traffic-permission.yaml
[MIGRATED RULES]   mesh-timeout.yaml
[MIGRATED MESH]    default-mesh.yaml
[MIGRATED ES]      external-db.yaml
[MIGRATED GW]      gateway.yaml
[MIGRATED OPA]     opa-policy.yaml
[ALREADY MIGRATED] mesh-retry.yaml
[SKIP]             deployment.yaml: no recognised Kuma policy documents

Summary: 9 file(s) processed — 7 migrated, 1 already migrated, 1 skipped, 0 error(s)
```

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

## Transformation examples

### Scenario: Legacy

```yaml
# Before
type: Timeout
mesh: default
name: my-timeout
sources:
  - match:
      kuma.io/service: '*'
destinations:
  - match:
      kuma.io/service: backend_demo_svc_3001
conf:
  connectTimeout: 5s
```

```yaml
# After
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: my-timeout
spec:
  targetRef:
    kind: Mesh
  to:
    - targetRef:
        kind: MeshService
        name: backend
        namespace: demo
      default:
        connectTimeout: 5s
```

### Scenario: Subset

```yaml
# Before
apiVersion: kuma.io/v1alpha1
kind: MeshTrafficPermission
metadata:
  namespace: kong-mesh-system
  name: allow-backend-to-redis
spec:
  targetRef:
    kind: MeshSubset
    tags:
      k8s.kuma.io/service-name: redis
  from:
    - targetRef:
        kind: MeshSubset
        tags:
          kuma.io/service: backend_demo_svc_3001
      default:
        action: Allow
```

```yaml
# After
apiVersion: kuma.io/v1alpha1
kind: MeshTrafficPermission
metadata:
  namespace: kong-mesh-system
  name: allow-backend-to-redis
spec:
  targetRef:
    kind: Dataplane
    labels:
      kuma.io/display-name: redis
  from:
    - targetRef:
        kind: MeshService
        name: backend
        namespace: demo
      default:
        action: Allow
```

### Scenario: Rules (Kuma 2.10+)

```yaml
# Before
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: backend-timeout
spec:
  targetRef:
    kind: Dataplane
    labels:
      app: backend
  from:
    - targetRef:
        kind: Mesh
      default:
        connectTimeout: 5s
```

```yaml
# After
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: backend-timeout
spec:
  targetRef:
    kind: Dataplane
    labels:
      app: backend
  rules:
    - default:
        connectTimeout: 5s
```

### Scenario: GW — MeshHTTPRoute → HTTPRoute

```yaml
# Before
apiVersion: kuma.io/v1alpha1
kind: MeshHTTPRoute
metadata:
  name: gw-to-frontend
  namespace: kong-mesh-system
spec:
  targetRef:
    kind: MeshGateway
    name: my-gateway
    tags:
      port: http-80
  to:
    - targetRef:
        kind: Mesh
      rules:
        - default:
            backendRefs:
              - kind: MeshService
                name: frontend_demo_svc_8080
                weight: 1
          matches:
            - path:
                type: PathPrefix
                value: /
```

```yaml
# After
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: gw-to-frontend
  namespace: kong-mesh-system
spec:
  parentRefs:
    - group: gateway.networking.k8s.io
      kind: Gateway
      name: my-gateway
      sectionName: http-80
  rules:
    - backendRefs:
        - group: ""
          kind: Service
          name: frontend
          namespace: demo
          port: 8080
          weight: 1
      matches:
        - path:
            type: PathPrefix
            value: /
```

### Scenario: OPAPolicy → MeshOPA (Kong Mesh)

```yaml
# Before
apiVersion: kuma.io/v1alpha1
kind: OPAPolicy
metadata:
  name: my-opa-policy
  namespace: kong-mesh-system
spec:
  targetRef:
    kind: Mesh
  conf:
    policies:
      - inlineString: |
          package envoy.authz
          default allow = false
          allow { input.attributes.request.http.method == "GET" }
```

```yaml
# After
apiVersion: kuma.io/v1alpha1
kind: MeshOPA
metadata:
  name: my-opa-policy
  namespace: kong-mesh-system
spec:
  targetRef:
    kind: Mesh
  default:
    appendPolicies:
      - rego:
          inlineString: |
            package envoy.authz
            default allow = false
            allow { input.attributes.request.http.method == "GET" }
```

## Notes

- **Policy config is preserved as-is.** The tool only rewrites structural targeting fields.
  Fields inside `conf`/`default` are not modified unless auto-fixing a known deprecated field.
- **`TrafficRoute` is skipped with an error** — it requires manual migration to
  `MeshHTTPRoute` or `MeshTCPRoute` depending on the protocol.
- **Multiple sources** in a legacy policy are split into one output policy per source
  (named `<original>-0`, `<original>-1`, …) because `spec.targetRef` accepts a single reference.
- **Gateway API hostname `*`** — a bare `*` hostname on a `MeshGateway` listener is invalid
  in Gateway API. The migrated `Gateway` listener omits the hostname field (meaning "accept
  any hostname"), and a warning is emitted.
- **Gateway API backendRef ports** — `MeshService` names encoded as `kuma.io/service` tags
  (e.g. `backend_demo_svc_3001`) are parsed to extract `name`, `namespace`, and `port` for
  the Gateway API `backendRef`. Missing ports trigger a warning.
- **Kong Mesh upgrade constraint** — Kong Mesh supports upgrading at most **two minor
  versions** at a time. Plan your upgrade path accordingly (e.g. 2.8 → 2.10 → 2.12 → 2.14).
  Latest as of mid-2026: Kuma 2.14 / Kong Mesh 2.14 (2.13.x is the Kong Mesh LTS line).
- **Universal vs Kubernetes format** — Kuma resources exist in two YAML shapes. Kubernetes
  format uses `apiVersion`, `kind`, and `metadata.name`; Universal format uses `type` and a
  top-level `name`/`mesh` field. Both are fully supported in extract and migrate. When
  service names are Kubernetes-encoded (`backend_demo_svc_3001`), the tool parses them to
  extract name, namespace, and port; Universal free-form names are used as-is.
- **Rules API: `spec.targetRef` is optional** — the Rules scenario (`from[]` → `rules[]`)
  is triggered whenever the policy kind is in the affected set and `from[]` is present,
  regardless of whether `spec.targetRef` is set at the top level.
- **Universal Dataplane deprecations** — on Universal CPs, `Dataplane` resources are
  hand-authored and included in extraction. The tool warns about:
  - `transparentProxying.redirectPortInboundV6` — removed in Kuma 2.9
  - `transparentProxying.reachableServices` — service names must be updated to MeshService
    display names when `spec.meshServices.mode: Exclusive` is enabled (Kuma 2.10+)
- **Skip list is environment-aware** — on Kubernetes, `Dataplane`, `ZoneIngress`,
  `ZoneEgress`, and `Workload` are skipped (CP-managed, never hand-authored). On Universal
  these are extracted and scanned. The user-configured `skip` list always takes precedence.
- **TLS skip verify** — use `-k` / `--tls-skip-verify` (or `adminServer.tlsSkipVerify: true`
  in `~/.config/kuma-migrator.yaml`) for control planes with self-signed certificates.

## Development

```bash
make test      # run unit tests
make build     # compile binary to ./dist/kuma-migrator
make snapshot  # local GoReleaser dry-run (requires goreleaser)
make lint      # run golangci-lint
make clean     # remove ./dist/
```

## Requirements

- Go 1.24+

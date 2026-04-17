# kuma-migrator

A CLI tool that migrates Kuma and Kong Mesh policy YAML manifests across all supported
migration paths — from legacy `sources`/`destinations` policies through to the new
`MeshService`-based API, Rules API, and Gateway API CRDs.

## Background

Kuma and Kong Mesh have evolved their policy API substantially across versions 2.0–2.13.
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
- `MeshHealthCheck` `healthyPanicThreshold` moved to `MeshCircuitBreaker` *(warn, Kuma 2.10)*
- `MeshTrust` `spec.origin` deprecated → `status.origin` *(warn, Kuma 2.13)*
- `MeshTrafficPermission`/`MeshFaultInjection` `from[].targetRef.kind: MeshService` deprecated *(warn, Kuma 2.7)*
- `MeshTrafficPermission` `action: ALLOW/DENY` uppercase casing → `Allow`/`Deny` *(warn, Kong Mesh 2.1)*
- `MeshLoadBalancingStrategy` `hashPolicies[].type: SourceIP` → `Connection` *(warn, Kuma 2.10)*
- `Dataplane` `transparentProxying.redirectPortInboundV6` removed *(warn, Kuma 2.9)*
- `kuma.io/*` annotation values `"yes"`/`"no"` → `"true"`/`"false"` *(scanner, Kuma 2.9)*
- Legacy `kuma.io/service`-encoded addresses in Deployment/StatefulSet env vars *(scanner)*
- RFC 1035/1123 name validation for all resources *(warn)*

## Installation

### Homebrew (macOS and Linux)

Supported platforms: macOS Apple Silicon (`arm64`), macOS Intel (`amd64`),
Linux `amd64`, Linux `arm64`.

```bash
brew tap Kong/kuma-migrator
brew install --cask kuma-migrator
```

Or as a one-liner:

```bash
brew install --cask Kong/kuma-migrator/kuma-migrator
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
one YAML file per resource, organised by CP mode and policy type:

```
<output-dir>/
  global/               ← resources from the Global CP
    resiliency/
    routing/
    zero-trust/
    observability/
    mesh/               ← Mesh CRs (apply last — they enable Exclusive mode)
  zone-<zone-name>/     ← resources from a Zone CP (zone-origin only)
    resiliency/
    routing/
    ...
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
| All other policy types | Global CP | extracted normally | skipped unless `kuma.io/origin: zone` |

> `MeshGatewayInstance` and `MeshGatewayConfig` are strictly zone-local and are never synced
> to the Global CP. Extract them by running against each Zone CP.
>
> `MeshGateway` and route CRDs can be created on either the Global CP or a Zone CP.
> When synced from Global to Zone they carry `kuma.io/origin: global` and are filtered out
> on zone extraction. When created directly on a Zone CP they have no origin label and are kept.

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
Extracted 87 resource(s) to ./raw-policies/global
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

Output is written under `zone-<zone-name>/` (e.g. `raw-policies/zone-eu-west/`).
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
Read-only resources (Insights, computed objects) are automatically excluded.

### 2. Plan (dry run)

Preview all changes **without writing any output files**.
Run once per extracted directory:

```bash
kuma-migrator plan --input-dir ./raw-policies/global    --output-dir ./migrated/global
kuma-migrator plan --input-dir ./raw-policies/zone-eu-west --output-dir ./migrated/zone-eu-west
```

Writes `migration-plan.md` in each output directory. Review before proceeding.

### 3. Migrate

Transform policies and write migrated YAML files:

```bash
kuma-migrator migrate --input-dir ./raw-policies/global    --output-dir ./migrated/global
kuma-migrator migrate --input-dir ./raw-policies/zone-eu-west --output-dir ./migrated/zone-eu-west
```

The output mirrors the input subfolder structure. When the input already contains
a CP mode parent folder (e.g. `raw-policies/global/resiliency/`), the output
preserves it (`migrated/global/resiliency/`).

**Special case — Gateway API resources from the Global CP**: `MeshGateway`, `MeshHTTPRoute`,
`MeshTCPRoute`, and `MeshGatewayRoute` in the `global/` input folder are transformed into
Gateway API CRDs (`Gateway`, `HTTPRoute`, `TCPRoute`). Because these are Kubernetes-native
resources that must be applied to **zone clusters**, the migrator writes them to `all-zones/`
instead of `global/`:

```
migrated/
  global/           ← Kuma policy CRDs applied to the Global CP
  all-zones/        ← Gateway API CRDs to be applied to every Zone cluster
    routing/
      Gateway-my-gw.yaml
      HTTPRoute-my-route.yaml
  zone-eu-west/     ← zone-origin Kuma policies applied to that zone cluster
```

You can also point `--input-dir` at the root of the extracted tree to process all
CP modes in one pass:

```bash
kuma-migrator migrate --input-dir ./raw-policies --output-dir ./migrated
# writes to ./migrated/global/, ./migrated/all-zones/, ./migrated/zone-eu-west/, etc.
```

### 4. Apply (in order)

After upgrading your control planes, apply the migrated manifests in this order:

```bash
# 1. Global CP policies — resiliency, routing, zero-trust, observability
kubectl apply -f ./migrated/global/resiliency/
kubectl apply -f ./migrated/global/routing/
kubectl apply -f ./migrated/global/zero-trust/
kubectl apply -f ./migrated/global/observability/

# 2. Gateway API resources — apply to EACH zone cluster (repeat per zone context)
#    These were migrated from MeshGateway / MeshHTTPRoute / etc. on the Global CP.
#    They are Kubernetes-native CRDs and must be applied to zone clusters, not the Global CP.
kubectl --context <zone-eu-west-context> apply -f ./migrated/all-zones/routing/
kubectl --context <zone-us-east-context> apply -f ./migrated/all-zones/routing/

# 3. Zone-origin policies (if any were extracted from Zone CPs)
kubectl --context <zone-eu-west-context> apply -f ./migrated/zone-eu-west/resiliency/
kubectl --context <zone-eu-west-context> apply -f ./migrated/zone-eu-west/routing/
kubectl --context <zone-eu-west-context> apply -f ./migrated/zone-eu-west/zero-trust/
kubectl --context <zone-eu-west-context> apply -f ./migrated/zone-eu-west/observability/

# 4. Mesh CRs last — these enable spec.meshServices.mode: Exclusive
#    Applying them before all other policies are in place will break
#    any workload still addressed by a kuma.io/service tag.
kubectl apply -f ./migrated/global/mesh/
```

### 5. (Optional) Skip step 2 when MeshGateway was zone-local

If your `MeshGateway` and route CRDs were created directly on a Zone CP (not via the Global CP),
they will be extracted into `zone-<name>/routing/` and migrated there — no `all-zones/` directory
will be produced. The migration report will tell you which case applies.

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

| Flag | Short | Required | Description |
|---|---|---|---|
| `--input-dir` | `-i` | yes | Directory containing source policy YAML files |
| `--output-dir` | `-o` | yes | Directory for output files and the Markdown report |

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
- **Migrated Files** — compact table per `cpMode/subfolder` (e.g. `global/resiliency/`),
  with per-file warning blocks where relevant; `mesh/` noted as "apply last"
- **Already Migrated** — files passed through unchanged
- **Skipped Files** — non-policy YAML files
- **Action Items** (when present) — errors, workload service address mappings,
  deprecated annotations
- **Apply Checklist** — ordered, numbered steps with the correct `kubectl apply -f` paths;
  includes a dedicated step for `all-zones/` Gateway API resources when present
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
    kind: MeshService
    name: redis
    namespace: kong-mesh-system
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
  versions** at a time. Plan your upgrade path accordingly (e.g. 2.7 → 2.9 → 2.11 → 2.13).
- **Universal vs Kubernetes mode** — detected from tag format. Kubernetes-encoded values
  (`backend_demo_svc_3001`) are parsed to extract name and namespace; Universal free-form
  values are used as-is.

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

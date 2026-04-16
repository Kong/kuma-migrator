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

### Homebrew

```bash
brew install bcollard/kuma-migrator/kuma-migrator
```

### From source

```bash
git clone https://github.com/bcollard/kuma-migrator.git
cd kuma-migrator
make build
# binary at ./dist/kuma-migrator
```

## Usage

### Workflow

```
extract → plan → migrate
```

### Extract (optional)

Pull resources directly from a running control plane into a local directory,
one YAML file per resource:

```bash
# From a Kubernetes cluster (uses kubectl)
kuma-migrator extract --kube-context prod-global --output-dir ./raw-policies

# From a kumactl-managed context (~/.kumactl/config)
kuma-migrator extract --kumactl-context my-cp --output-dir ./raw-policies
```

**kubectl path** — discovers all `kuma.io/v1alpha1` CRDs from the kube API server,
then fetches every instance with `kubectl get <kind> -o yaml`. One file per resource.

**kumactl path** — resolves the context from `~/.kumactl/config` (or `$KUMACTL_CONFIG`),
queries `GET <cpURL>/_resources` to discover all writable resource types, lists all Mesh
names, then calls `kumactl get <type> [--mesh <mesh>] -o yaml` for each type. One file
per resource. Read-only resources (Insights, computed objects) are automatically excluded.

### Plan (dry run)

Preview all changes **without writing any output files**:

```bash
kuma-migrator plan \
  --input-dir  ./raw-policies \
  --output-dir ./plan
```

Writes `./plan/migration-plan.md` — a full Markdown report of every file and every
change that *would* be applied. Review this before committing to the migration.

### Migrate

Transform policies and write migrated files:

```bash
kuma-migrator migrate \
  --input-dir  ./raw-policies \
  --output-dir ./migrated
```

Writes the migrated YAML files to `--output-dir` (preserving original filenames) and
writes `./migrated/migration-report.md`.

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
- **Errors section** — files that could not be fully migrated (require manual attention)
- **Files to Migrate** — per-file breakdown with kind, name, scenario, and any warnings
- **Already Migrated** — files passed through unchanged
- **Skipped Files** — non-policy YAML files
- **Workload Service Address Mappings** — legacy `kuma.io/service` addresses found in env vars, with Kubernetes and mesh hostname replacements
- **Deprecated Annotations** — `kuma.io/*` annotations using `"yes"`/`"no"` values
- **MeshService Mode** — advisory to set `spec.meshServices.mode: Exclusive` on every Mesh
- **Migration Notes** — scenario reference table and next steps

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

### OPAPolicy → MeshOPA (Kong Mesh)

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

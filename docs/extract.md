# Extracting from a control plane

Pulling resources out of a running Kuma or Kong Mesh control plane, and the CP-mode rules that decide what lands where.

[← Back to the README](../README.md)

---

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

## Resource type placement

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

## Always extract from the Global CP first

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

## Also extract from Zone CPs

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

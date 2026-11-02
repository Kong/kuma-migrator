# extract from Kong Mesh 2.14.7 (universal)

Input objects applied to the CP that `kuma-migrator extract` did not write
(kinds on the default skip list excluded: AccessRoleBinding Zone Workload Secret GlobalSecret).

| Kind/name | Note |
|---|---|
| `HealthCheck/hc-web` | not extracted |

Extract log:

```text

  kuma-migrator  extract

  Context:         e2e-ucp-v2
  Control plane:   http://localhost:25681
  Environment:     universal
  CP mode:         zone (default)
  ⚠  Extracting from a Zone CP — only kuma.io/origin: zone resources will be kept.
     For a complete policy set, also run extract against the Global CP.
  ℹ  MeshGatewayInstance and MeshGatewayConfig are zone-local and will be extracted here.
  ℹ  MeshGateway and route CRDs (MeshHTTPRoute, MeshTCPRoute, MeshGatewayRoute):
     - Synced from Global CP with kuma.io/origin: global → skipped (extract from Global CP).
     - Created directly on this Zone CP with no origin label → extracted here.

  ·  Found 48 writable resource type(s)

  Meshes found:    default, payments

  ◆  mesh  default

  ◆  mesh  payments

  ✓  Extracted 67 resource(s) to $E2E_WORK/extract-universal

```

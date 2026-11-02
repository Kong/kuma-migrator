# extract from Kong Mesh 2.14.7 (universal)

Input objects applied to the CP that `kuma-migrator extract` did not write
(kinds on the default skip list excluded: AccessRoleBinding Zone Workload Secret GlobalSecret).

| Kind/name | Note |
|---|---|
| — | everything extracted |

Extract log:

```text

  kuma-migrator  extract

  Context:         e2e-ucp-v2
  Control plane:   http://localhost:25681
  Environment:     universal
  CP mode:         zone (default), not federated
  ℹ  This Zone CP is not connected to a Global CP: every resource in its store is its own.
     No kuma.io/origin filter is applied — all resources are extracted.

  ·  Found 48 writable resource type(s)

  Meshes found:    default, payments

  ◆  mesh  default

  ◆  mesh  payments

  ✓  Extracted 69 resource(s) to $E2E_WORK/extract-universal

```

# extract from Kong Mesh 2.14.7 (kubernetes)

Input objects applied to the CP that `kuma-migrator extract` did not write
(kinds on the default skip list excluded: Dataplane AccessRoleBinding ZoneEgress ZoneIngress Zone Workload Secret GlobalSecret).

| Kind/name | Note |
|---|---|
| — | everything extracted |

Extract log:

```text

  kuma-migrator  extract

  Context:         km214
  CP mode:         zone, not federated
  ℹ  This Zone CP is not connected to a Global CP: every resource in its store is its own.
     No kuma.io/origin filter is applied — all resources are extracted.

  ·  Found 48 kuma.io/v1alpha1 CRD(s)


  ✓  Extracted 66 resource(s) to $E2E_WORK/extract-kubernetes

```

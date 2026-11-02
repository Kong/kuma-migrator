# extract from Kong Mesh 2.14.7 (kubernetes)

Input objects applied to the CP that `kuma-migrator extract` did not write
(kinds on the default skip list excluded: Dataplane AccessRoleBinding ZoneEgress ZoneIngress Zone Workload Secret GlobalSecret).

| Kind/name | Note |
|---|---|
| `AccessAudit/audit` | not extracted |
| `AccessRole/mesh-operator` | not extracted |
| `CircuitBreaker/cb-redis` | not extracted |
| `ContainerPatch/sidecar-resources` | not extracted |
| `ExternalService/httpbin` | not extracted |
| `ExternalService/postgres` | not extracted |
| `FaultInjection/fi-backend` | not extracted |
| `HealthCheck/hc-web` | not extracted |
| `MeshService/legacy-db` | not extracted |
| `OPAPolicy/opa-backend` | not extracted |
| `ProxyTemplate/custom-template` | not extracted |
| `RateLimit/rl-backend` | not extracted |
| `Retry/retry-all` | not extracted |
| `Timeout/timeout-backend` | not extracted |
| `TrafficLog/log-all` | not extracted |
| `TrafficLog/log-backend` | not extracted |
| `TrafficPermission/allow-all-default` | not extracted |
| `TrafficPermission/frontend-to-backend` | not extracted |
| `TrafficPermission/payments-all` | not extracted |
| `TrafficRoute/backend-split` | not extracted |
| `TrafficTrace/trace-all` | not extracted |
| `VirtualOutbound/instances` | not extracted |

Extract log:

```text

  kuma-migrator  extract

  Context:         km214
  CP mode:         zone ()
  ⚠  Extracting from a Zone CP — only kuma.io/origin: zone resources will be kept.
     For a complete policy set, also run extract against the Global CP.
  ℹ  MeshGatewayInstance and MeshGatewayConfig are zone-local and will be extracted here.
  ℹ  MeshGateway and route CRDs (MeshHTTPRoute, MeshTCPRoute, MeshGatewayRoute):
     - Synced from Global CP with kuma.io/origin: global → skipped (extract from Global CP).
     - Created directly on this Zone CP with no origin label → extracted here.

  ·  Found 48 kuma.io/v1alpha1 CRD(s)


  ✓  Extracted 43 resource(s) to $E2E_WORK/extract-kubernetes

```

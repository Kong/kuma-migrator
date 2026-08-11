# Applying the migrated manifests

The order to apply in, why that order matters, and how to clean up the originals afterwards.

[← Back to the README](../README.md)

---

## Apply order

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

## When MeshGateway was zone-local

You can skip step 2 above if your `MeshGateway` and route CRDs were created directly on a
Zone CP rather than via the Global CP:
they will be extracted into `<context>-zone-ctx/<mesh>/routing/` and migrated there.
The migration report will tell you which case applies.

## Clean up the originals

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

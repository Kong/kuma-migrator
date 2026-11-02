# Transformation examples

Before-and-after YAML for each migration scenario.

[← Back to the README](../README.md)

---

## Scenario: Legacy

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
# After (--to-latest v2)
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  labels:
    kuma.io/mesh: default
  name: my-timeout
spec:
  targetRef:
    kind: Mesh
  to:
  - default:
      connectionTimeout: 5s
    targetRef:
      kind: MeshService
      name: backend
      namespace: demo
```

Under `--to-latest v3` the destination is selected by labels, because 3.0 selects real
resources by `labels` only:

```yaml
# After (--to-latest v3)
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  labels:
    kuma.io/mesh: default
  name: my-timeout
spec:
  targetRef:
    kind: Mesh
  to:
  - default:
      connectionTimeout: 5s
    targetRef:
      kind: MeshService
      labels:
        k8s.kuma.io/namespace: demo
        kuma.io/display-name: backend
```

## Scenario: Subset

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

## Scenario: Rules (Kuma 2.10+)

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

## Scenario: GW — MeshGatewayRoute → HTTPRoute

`MeshHTTPRoute` and `MeshTCPRoute` are passed through unchanged; only the built-in gateway's
`MeshGatewayRoute` is converted. Same output on both targets. The backend lives in another
namespace than the route, so a `ReferenceGrant` is generated in the backend namespace.

```yaml
# Before
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayRoute
metadata:
  name: edge
  namespace: gateways
  labels:
    kuma.io/mesh: default
spec:
  selectors:
    - match:
        kuma.io/service: edge_gateways_svc
  conf:
    http:
      rules:
        - matches:
            - path: {match: PREFIX, value: /}
          backends:
            - weight: 1
              destination:
                kuma.io/service: api_apps_svc_8080
```

```yaml
# After: HTTPRoute-gateways-edge.yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  labels:
    kuma.io/mesh: default
  name: edge
  namespace: gateways
spec:
  parentRefs:
  - group: gateway.networking.k8s.io
    kind: Gateway
    name: edge
  rules:
  - backendRefs:
    - group: ""
      kind: Service
      name: api
      namespace: apps
      port: 8080
      weight: 1
    matches:
    - path:
        type: PathPrefix
        value: /
```

```yaml
# After: ReferenceGrant-apps-gateways-edge.yaml
apiVersion: gateway.networking.k8s.io/v1beta1
kind: ReferenceGrant
metadata:
  name: gateways-edge
  namespace: apps
spec:
  from:
  - group: gateway.networking.k8s.io
    kind: HTTPRoute
    namespace: gateways
  to:
  - group: ""
    kind: Service
    name: api
```

## Scenario: Mesh under `--to-latest v3`

A `Mesh` using the legacy mTLS backend in permissive mode, with outbound passthrough on:

```yaml
# Before
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
  mtls:
    enabledBackend: ca-1
    backends:
      - name: ca-1
        type: builtin
        mode: PERMISSIVE
  networking:
    outbound:
      passthrough: true
  routing:
    zoneEgress: true
```

```yaml
# After (--to-latest v3): Mesh-default.yaml
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
  meshServices:
    mode: Exclusive
```

```yaml
# After (--to-latest v3): MeshTLS-kong-mesh-system-default-tls-permissive.yaml
apiVersion: kuma.io/v1alpha1
kind: MeshTLS
metadata:
  labels:
    kuma.io/mesh: default
  name: default-tls-permissive
  namespace: kong-mesh-system
spec:
  rules:
  - default:
      mode: Permissive
  targetRef:
    kind: Mesh
```

```yaml
# After (--to-latest v3): MeshPassthrough-kong-mesh-system-default-passthrough.yaml
apiVersion: kuma.io/v1alpha1
kind: MeshPassthrough
metadata:
  labels:
    kuma.io/mesh: default
  name: default-passthrough
  namespace: kong-mesh-system
spec:
  default:
    passthroughMode: All
  targetRef:
    kind: Mesh
```

`spec.mtls` and `routing.zoneEgress` are gone from the `Mesh`. The run prints a **blocking**
warning for `spec.mtls`: create a `MeshIdentity` (and `MeshTrust`) that selects every proxy
before upgrading, or the mesh serves plaintext on 3.0. Under `--to-latest v2` the same input
keeps `spec.mtls` and `routing.zoneEgress`, gains `meshServices.mode: Exclusive`, and gets
advisories only.

## Scenario: OPAPolicy → MeshOPA (Kong Mesh)

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

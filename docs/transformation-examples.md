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

## Scenario: GW — MeshHTTPRoute → HTTPRoute

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

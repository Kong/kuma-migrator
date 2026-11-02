---
title: Transformation examples
description: Before-and-after YAML for each migration scenario, as produced by the current release.
order: 100
nav_title: Examples
---

Every output below was produced by running `kuma-migrator migrate` on the input shown. Warnings
printed alongside are summarised in prose. The full behaviour per kind is on the
[policy pages](../policies/_index.md).

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
mesh: default
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
type: MeshTimeout
```

Under `--to-latest v3` the destination is selected by labels, because 3.0 selects real
resources by `labels` only:

```yaml
# After (--to-latest v3)
mesh: default
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
type: MeshTimeout
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
# After (--to-latest v2)
apiVersion: kuma.io/v1alpha1
kind: MeshTrafficPermission
metadata:
  name: allow-backend-to-redis
  namespace: kong-mesh-system
spec:
  from:
  - default:
      action: Allow
    targetRef:
      kind: MeshService
      labels:
        k8s.kuma.io/namespace: demo
        kuma.io/display-name: backend
  targetRef:
    kind: Dataplane
    name: redis
    namespace: kong-mesh-system
```

The `from[]` source lives in namespace `demo`, which differs from the policy namespace, so it is
selected by labels. The top-level `k8s.kuma.io/service-name: redis` carries no namespace, so the
policy namespace is used and the reference keeps the `name`/`namespace` form.

> **Warning:** `Dataplane` with `name` selects the `Dataplane` object of that name. On
> Kubernetes a Dataplane is named after its Pod (`redis-7d9c…`), not after the service. Check
> that the reference matches your Dataplanes before applying, or select them by label.

Under `--to-latest v3` the top-level reference becomes labels, because 3.0 selects by labels
only:

```yaml
# After (--to-latest v3), spec.targetRef
  targetRef:
    kind: Dataplane
    labels:
      app: redis
      k8s.kuma.io/namespace: kong-mesh-system
```

`app` is a best guess for the Pod label and is reported as a warning. Both targets warn that
`from[]` on `MeshTrafficPermission` is deprecated in favour of `rules[]` and is not converted;
see [Access control](../topics/access-control.md).

## Scenario: Rules

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
        idleTimeout: 30s
```

```yaml
# After (both targets)
apiVersion: kuma.io/v1alpha1
kind: MeshTimeout
metadata:
  name: backend-timeout
spec:
  rules:
  - default:
      idleTimeout: 30s
  targetRef:
    kind: Dataplane
    labels:
      app: backend
```

The `rules[]` entry applies to all inbound traffic selected by `spec.targetRef`. A `from[]`
entry whose source is not `kind: Mesh` is narrower than that, and the conversion warns about the
widened scope.

## Scenario: Gateway (MeshGatewayRoute to HTTPRoute)

[MeshHTTPRoute](policy:MeshHTTPRoute) and [MeshTCPRoute](policy:MeshTCPRoute) are passed through unchanged; only the built-in gateway's
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
keeps `spec.mtls` and `routing.zoneEgress`, gains `meshServices.mode: Exclusive`, drops
`networking.outbound.passthrough: true` (the 2.x default), and reports the 3.0 changes as
advisories. See [Meshes](../topics/meshes.md) and [mTLS and identity](../topics/mtls-identity.md).

## Scenario: ExternalService

Universal input from a `kumactl` extraction. Same output on both targets.

```yaml
# Before
type: ExternalService
mesh: default
name: httpbin
tags:
  kuma.io/service: httpbin
  kuma.io/protocol: http
networking:
  address: httpbin.org:443
  tls:
    enabled: true
```

```yaml
# After: MeshExternalService-httpbin.yaml
mesh: default
name: httpbin
spec:
  endpoints:
  - address: httpbin.org
    port: 443
  match:
    port: 443
    protocol: http
    type: HostnameGenerator
  tls:
    enabled: true
    verification:
      mode: Secured
type: MeshExternalService
```

The run warns that `verification.mode` defaults to `Secured`, that `match.port` copies the
endpoint port, and that the hostname clients use now comes from a `HostnameGenerator`. Under
`--to-latest v3` it also warns when the input has no `MeshIdentity` for the mesh: 3.0 requires
one for `MeshExternalService` traffic. See [External services](../topics/external-services.md).

## Scenario: OPAPolicy (Kong Mesh)

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
# After (--to-latest v2)
apiVersion: kuma.io/v1alpha1
kind: MeshOPA
metadata:
  name: my-opa-policy
  namespace: kong-mesh-system
spec:
  default:
    appendPolicies:
    - rego:
        inlineString: |-
          package envoy.authz
          default allow = false
          allow { input.attributes.request.http.method == "GET" }
  targetRef:
    kind: Mesh
```

Under `--to-latest v3` the rego source uses the `SecureDataSource` shape that Kong Mesh 3.0
requires:

```yaml
# After (--to-latest v3), spec.default
  default:
    appendPolicies:
    - rego:
        insecureInline:
          value: |-
            package envoy.authz
            default allow = false
            allow { input.attributes.request.http.method == "GET" }
        type: InsecureInline
```

See [MeshOPA](policy:MeshOPA).

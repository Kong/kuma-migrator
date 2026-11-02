---
title: Observability
description: Metrics, tracing and access logs from Mesh fields and legacy policies to MeshMetric, MeshTrace, MeshAccessLog and MeshOpenTelemetryBackend.
order: 90
---

Observability on 2.x is configured in three places: backends declared on the `Mesh`
(`metrics`, `tracing`, `logging`), legacy policies that select traffic for them
([TrafficLog](policy:TrafficLog), [TrafficTrace](policy:TrafficTrace)), and the targetRef
policies [MeshMetric](policy:MeshMetric), [MeshTrace](policy:MeshTrace) and
[MeshAccessLog](policy:MeshAccessLog). On 3.0 only the policies remain, and an OpenTelemetry
destination must be a separate [MeshOpenTelemetryBackend](policy:MeshOpenTelemetryBackend).

## What changed upstream

| Version | Change |
|---|---|
| 2.7 | `MeshMetric` `sidecar.regex` replaced by `sidecar.profiles.exclude` |
| 2.14.0 | `MeshOpenTelemetryBackend` introduced (kuma#15863). Inline `openTelemetry.endpoint` on `MeshMetric`/`MeshTrace`/`MeshAccessLog` deprecated in favour of `backendRef` ([CHANGELOG](https://github.com/kumahq/kuma/blob/master/CHANGELOG.md)) |
| 2.14 | `MeshAccessLog` OpenTelemetry `attributes[].key` validated more strictly (reserved `otel.` prefix, casing, placeholders) |
| 3.0 | `Mesh.spec.metrics`, `tracing`, `logging` removed; the control plane ignores them ([tracing](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshspectracing-removed), [logging](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshspeclogging-removed), [`mesh.proto`](https://github.com/kumahq/kuma/blob/master/api/mesh/v1alpha1/mesh.proto)) |
| 3.0 | `TrafficLog` and `TrafficTrace` removed ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#legacy-policy-resources-removed)) |
| 3.0 | Inline `openTelemetry.endpoint` removed from [`MeshMetric`](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshmetric-opentelemetry-backend-no-longer-accepts-an-inline-endpoint), [`MeshAccessLog`](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshaccesslog-opentelemetry-backend-no-longer-accepts-an-inline-endpoint) and [`MeshTrace`](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshtrace-opentelemetry-backend-no-longer-accepts-an-inline-endpoint); a policy still setting it fails validation |
| 3.0 | `backendRef` exports always go through `kuma-dp`; the `otelPipeEnabled` settings are removed ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#opentelemetry-backends-referenced-by-backendref-always-export-through-kuma-dp)) |
| 3.0 | `MeshMetric` Prometheus `ActiveMTLSBackend` secures the endpoint with the workload identity: remote scrapers need a client certificate the mesh trusts ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshmetric-activemtlsbackend-requires-a-client-certificate)) |
| 3.0 | MADS no longer starts on Kubernetes control planes; use `MeshMetric` with Prometheus Kubernetes service discovery ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#mads-restricted-to-universal-deployment-mode)) |
| 3.0 | `MeshMetric` no longer reports `kuma.proxy_role: gateway`; former gateways report `sidecar` ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#kumaiogateway-is-removed)) |
| 3.0 | `from[]` removed from `MeshAccessLog`; use `rules[]` ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#from-removed-from-meshaccesslog)) |

### backendRef constraints

A `backendRef` selects the `MeshOpenTelemetryBackend` by `labels` only (no `name`), is mutually
exclusive with an inline `endpoint`, and the backend must live in the system namespace. With
`protocol: grpc`, `endpoint.path` must be empty.

Do not switch to `backendRef` while any control plane or data plane is below 2.14:
`MeshOpenTelemetryBackend` does not exist there, and against an older data plane the control
plane skips the OpenTelemetry route without logging it. The inline `endpoint` keeps working up
to and including 2.14.

## What kuma-migrator does

| Input | `--to-latest v2` | `--to-latest v3` |
|---|---|---|
| `Mesh.metrics` | The `enabledBackend` becomes a mesh-wide `MeshMetric` `<mesh>-metrics`; no `enabledBackend`, no `MeshMetric` | Same |
| `Mesh.tracing` | Removed, no mesh-wide `MeshTrace`. Warning lists which backends a `TrafficTrace` references | Same |
| `Mesh.logging` | Removed, no mesh-wide `MeshAccessLog`. Warning lists which backends a `TrafficLog` references | Same |
| `TrafficLog` | `MeshAccessLog` (outbound shape). `conf.backend` (or the Mesh `defaultBackend`) is resolved against `Mesh.logging.backends` and inlined | Same, plus the delete-before-upgrade advisory |
| `TrafficTrace` | `MeshTrace` (selector shape). Backend and its sampling resolved against `Mesh.tracing.backends` | Same |
| `TrafficLog`/`TrafficTrace` whose `Mesh` is not in the input | Warning naming the backend to write by hand | Same |
| Inline `openTelemetry.endpoint` | Warning, including the "not before 2.14" constraint | Generated `MeshOpenTelemetryBackend` `<policy>-otel` in the system namespace ([how it is picked](meshes.md#system-namespace)), `protocol: grpc`, and a `backendRef.labels` selecting it by `kuma.io/display-name` |
| `MeshMetric` `sidecar.regex` | Rewritten to `sidecar.profiles.exclude[{type: Regex}]` | Same |
| `MeshAccessLog` OTel attribute keys that 2.14 rejects | Warning | Warning |
| `MeshMetric` `ActiveMTLSBackend` | — | Warning |
| `MeshAccessLog` `from[]` | Converted to `rules[]` (Rules scenario) | Same; a source narrower than `kind: Mesh` is warned |
| Pod annotations `prometheus.metrics.kuma.io/port` / `/path` | Warning: use `MeshMetric` | Same |

Code: [`mesh.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/mesh.go),
[`legacybackend.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/legacybackend.go),
[`v3_mesh.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/v3_mesh.go)
(`convertInlineOtelEndpointsForV3`).

Keep the `Mesh` document and every `TrafficLog`/`TrafficTrace` in the input directory.
`TrafficLog` and `TrafficTrace` reference a backend by name, and the `Mesh` is where its
definition lives.

On 2.x, `Mesh.logging` and `Mesh.tracing` backends only declare destinations: nothing is logged
or traced until a `TrafficLog`/`TrafficTrace` selects traffic
([traffic log docs](https://kuma.io/docs/latest/policies/traffic-log/),
[traffic trace docs](https://kuma.io/docs/latest/policies/traffic-trace/)). The migrator therefore
generates no mesh-wide `MeshAccessLog`/`MeshTrace` from the `Mesh`: the logging and tracing that
was active is exactly what the converted `TrafficLog`/`TrafficTrace` policies carry. A backend no
`TrafficLog`/`TrafficTrace` references was inactive, and the `Mesh` warning says so. See
[Meshes](meshes.md#logging-and-tracing).

## What it cannot do

- **Share backends.** One `MeshOpenTelemetryBackend` is generated per policy. Several policies
  pointing at the same collector can share one; merge them by hand.
- **Know the system namespace for certain.** It is inferred from the input, or set with
  `systemNamespace` in the config file. See [Meshes](meshes.md#system-namespace).
- **Issue scraper certificates** for `ActiveMTLSBackend`.

## Manual steps

1. Apply the `v2` companions and converted policies on 2.x.
2. Delete `TrafficLog`/`TrafficTrace` once their `MeshAccessLog`/`MeshTrace` replacements apply.
3. Upgrade every control plane and data plane to 2.14.
4. Create `MeshOpenTelemetryBackend`s in the system namespace and switch policies to
   `backendRef` (the `v3` output contains both).
5. Fix `MeshAccessLog` OpenTelemetry attribute keys the report lists.
6. Give remote Prometheus scrapers a mesh-trusted client certificate, or set `ActiveMTLSBackend`
   endpoints to `Disabled`. On Kubernetes, move from MADS to Prometheus service discovery.
7. Update dashboards and alerts that select `kuma.proxy_role: gateway`.

## Example

Input — two files: a `MeshTrace` with an inline OpenTelemetry endpoint, and a `TrafficLog` whose
backend is declared on a Universal `Mesh`:

```yaml
apiVersion: kuma.io/v1alpha1
kind: MeshTrace
metadata:
  name: otel-trace
  namespace: kong-mesh-system
  labels:
    kuma.io/mesh: default
spec:
  targetRef:
    kind: Mesh
  default:
    backends:
      - type: OpenTelemetry
        openTelemetry:
          endpoint: otel-collector.observability:4317
    sampling:
      overall: 50
---
type: TrafficLog
mesh: default
name: all-traffic
sources:
  - match:
      kuma.io/service: '*'
destinations:
  - match:
      kuma.io/service: '*'
conf:
  backend: file-log
```

```yaml
# Mesh, in the same input directory
type: Mesh
name: default
meshServices:
  mode: Exclusive
logging:
  backends:
    - name: file-log
      type: file
      conf:
        path: /dev/stdout
```

The `TrafficLog` becomes a `MeshAccessLog` with the backend inlined, on both targets:

```yaml
# observability/MeshAccessLog-all-traffic.yaml
apiVersion: kuma.io/v1alpha1
kind: MeshAccessLog
metadata:
  labels:
    kuma.io/mesh: default
  name: all-traffic
spec:
  targetRef:
    kind: Mesh
  to:
  - default:
      backends:
      - file:
          path: /dev/stdout
        type: File
    targetRef:
      kind: Mesh
```

The `Mesh.logging` block produces no `MeshAccessLog` of its own. The `Mesh` warning confirms the
backend is carried by the converted `TrafficLog`:

```text
⚠  Mesh "default": spec.logging removed from the Mesh output. … No mesh-wide MeshAccessLog was
   generated, because that would enable logging for every proxy in the mesh. Referenced backends,
   carried inline into the MeshAccessLog generated from each TrafficLog: file-log (1 TrafficLog).
```

With `--to-latest v2` the `MeshTrace` is copied unchanged, with this warning (shortened):

```text
⚠  MeshTrace "otel-trace": an inline openTelemetry.endpoint is deprecated in Kuma 2.14 and removed
   in 3.0 — define a MeshOpenTelemetryBackend resource and reference it via backendRef. … Do NOT
   make this change while any control plane or data plane is still below 2.14 …
```

With `--to-latest v3` it is split in two:

```yaml
# observability/MeshTrace-kong-mesh-system-otel-trace.yaml
apiVersion: kuma.io/v1alpha1
kind: MeshTrace
metadata:
  labels:
    kuma.io/mesh: default
  name: otel-trace
  namespace: kong-mesh-system
spec:
  default:
    backends:
    - openTelemetry:
        backendRef:
          kind: MeshOpenTelemetryBackend
          labels:
            kuma.io/display-name: otel-trace-otel
      type: OpenTelemetry
    sampling:
      overall: 50
  targetRef:
    kind: Mesh
```

```yaml
# other/MeshOpenTelemetryBackend-kong-mesh-system-otel-trace-otel.yaml
# (kong-mesh-system: the input MeshTrace lives there)
apiVersion: kuma.io/v1alpha1
kind: MeshOpenTelemetryBackend
metadata:
  labels:
    kuma.io/mesh: default
  name: otel-trace-otel
  namespace: kong-mesh-system
spec:
  endpoint:
    address: otel-collector.observability
    port: 4317
  protocol: grpc
```

## Related

- [Meshes](meshes.md)
- [Legacy policies](legacy-policies.md) — `TrafficLog`/`TrafficTrace` shapes
- [mTLS and workload identity](mtls-identity.md) — `ActiveMTLSBackend`

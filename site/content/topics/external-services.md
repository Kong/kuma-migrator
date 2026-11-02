---
title: External services
description: ExternalService to MeshExternalService, the SecureDataSource TLS shape, MeshPassthrough, and what Kuma 3.0 requires for traffic leaving the mesh.
order: 70
---

Traffic to destinations outside the mesh takes one of two paths: a declared destination
([ExternalService](policy:ExternalService) on older meshes,
[MeshExternalService](policy:MeshExternalService) since 2.8), routed through a zone egress, or
passthrough to the address the client dialed, controlled by `Mesh.networking.outbound.passthrough`
and [MeshPassthrough](policy:MeshPassthrough). Kuma 3.0 removes `ExternalService` and the
`Mesh` passthrough switch, and tightens both remaining paths.

## What changed upstream

| Version | Change |
|---|---|
| 2.8.0 | `MeshExternalService` introduced, with `HostnameGenerator` support ([CHANGELOG](https://github.com/kumahq/kuma/blob/master/CHANGELOG.md)). It needs a zone egress and mutual TLS ([docs](https://kuma.io/docs/latest/networking/meshexternalservice/)) |
| 2.14.0 | `MeshPassthrough` `appendMatch[]` wildcard validation: partial wildcards (`*foo.com`), `type: Domain` with `tcp`/`mysql`, wildcard domains on an L7 protocol with no port rejected on apply ([CHANGELOG](https://github.com/kumahq/kuma/blob/master/CHANGELOG.md), kuma#16570) |
| 2.14.6 | `MeshExternalService` TLS verification accepts both the old `DataSource` and the new `SecureDataSource` shape ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#upgrade-to-2146)). `MeshPassthrough` validated by filter chain (kuma#18988) |
| 3.0 | `ExternalService` removed: CRD, API and webhook ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#legacy-externalservice-resource-removed)) |
| 3.0 | `MeshExternalService` `tls.verification.{caCert,clientCert,clientKey}` must use `SecureDataSource`; `File` and `EnvVar` types are rejected. A stored old-shape resource is dropped from xDS with an error on the control plane ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshexternalservice-tls-verification-uses-the-securedatasource-shape)) |
| 3.0 | A client proxy without a `MeshIdentity` gets no `MeshExternalService` cluster: requests fail locally with `503 cluster_not_found` ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshexternalservice-clusters-require-a-meshidentity)) |
| 3.0 | A `kuma.io/zone` label on a `MeshExternalService` no longer restricts it to that zone; every zone's egress dials the endpoint directly ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#per-zone-meshexternalservice-routing-removed)) |
| 3.0 | `Mesh.routing.defaultForbidMeshExternalServiceAccess` and `routing.zoneEgress` removed; use `MeshTrafficPermission` for access control ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#routingdefaultforbidmeshexternalserviceaccess-removed-from-the-mesh-schema)) |
| 3.0 | A transparent-proxy data plane that no `MeshPassthrough` selects drops traffic to destinations outside the mesh ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#outbound-passthrough-defaults-to-none)) |
| 3.0 | `MeshPassthrough` resolves a non-wildcard `Domain` match itself and requires a `port` for it; a stored match without one stops applying ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshpassthrough-resolves-a-domain-match-itself-and-needs-a-port-for-it)) |
| 3.0 | `MeshPassthrough` rejects matches that resolve to the same Envoy filter chain ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshpassthrough-rejects-matches-that-resolve-to-the-same-envoy-filter-chain)) |

> **Note:** `UPGRADE.md` has two 3.0 entries on outbound passthrough. The older one
> ("`Mesh.spec.networking.outbound.passthrough` removed") says 3.0 behaves as if `passthrough`
> were `true`. The newer one ("Outbound passthrough defaults to `None`") replaces that default.
> `kuma-migrator` follows the newer entry.

## What kuma-migrator does

| Input | `--to-latest v2` | `--to-latest v3` |
|---|---|---|
| `ExternalService` | `MeshExternalService`: `networking.address` → `endpoints[]` + `match.port`; `kuma.io/protocol` tag → `match.protocol`; `match.type: HostnameGenerator`; `networking.tls` → `tls` with `verification.mode: Secured` | Same, with the TLS data sources in the `SecureDataSource` shape, plus the delete-before-upgrade advisory |
| `ExternalService` with a `unix://` address | Error | Error |
| `MeshExternalService` TLS `inlineString` / `secret` | Warning: rewrite on 2.14.6+ | Rewritten: `type: InsecureInline` + `insecureInline.value`, or `type: Secret` + `secretRef` |
| `MeshExternalService` TLS `inline` (base64) | Warning | Warning only. Rewriting means decoding credential material and writing it back in clear text, which is your decision |
| `MeshExternalService` and no `MeshIdentity` for its mesh in the input | — | Warning: clients get `503 cluster_not_found` on 3.0 |
| `MeshExternalService` with a `kuma.io/zone` label | — | Warning: per-zone routing removed |
| `Mesh.networking.outbound.passthrough` | `false` → `MeshPassthrough` `None`; `true` → removed, with a warning | `false` → `None`; `true` → `MeshPassthrough` `All`; unset → warning |
| `Mesh.routing.defaultForbidMeshExternalServiceAccess` | Warning | Removed, with a note to use `MeshTrafficPermission` |
| `MeshPassthrough` partial wildcard, `Domain` on `tcp`/`mysql`, L7 wildcard without port | Warning | Warning |
| `MeshPassthrough` `Domain` match without a `port` | — | Warning |
| `MeshPassthrough` matches that collide on one filter chain | Warning | Warning |

Code: [`externalservice.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/externalservice.go),
[`v3_mes.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/v3_mes.go),
[`v3_scan.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/v3_scan.go).

The converted `MeshExternalService` keeps the input format. A Universal `ExternalService` gives a
Universal `MeshExternalService` (`type`, `name`, `mesh`). A Kubernetes one gives a CRD in the
control-plane namespace, inferred from the input or set with `systemNamespace` in the config file
(see [Meshes](meshes.md#system-namespace)).

The `MeshIdentity` check looks only at the input directory. If the `MeshIdentity` exists on the
control plane but was not extracted, ignore the warning.

## What it cannot do

- **Pick the virtual port.** `match.port` is set to the endpoint port. Change it if clients should
  connect on a different port.
- **Know the hostname clients use.** The `HostnameGenerator` renders a name from the resource
  name (for example `httpbin.extsvc.mesh.local`). Clients that used the old `ExternalService`
  address need to switch to it.
- **Decode `inline` certificate material.** See above.
- **Decide the passthrough policy.** `passthroughMode: All` keeps 2.x behaviour; `Matched` with
  `appendMatch` is an allowlist you write.
- **Add ports to `Domain` matches.** The port the workload uses is not in the policy.

## Manual steps

1. Make sure each zone that serves `MeshExternalService` traffic runs a zone egress, and the mesh
   has mutual TLS.
2. Apply the converted `MeshExternalService`s on 2.x, move clients to the generated hostnames,
   then delete the `ExternalService`s.
3. Upgrade every control plane to 2.14.6 or later, then rewrite TLS data sources to the
   `SecureDataSource` shape (the `v3` output does it for `inlineString` and `secret`).
4. Create a `MeshIdentity` covering every client of a `MeshExternalService` (see
   [mTLS and workload identity](mtls-identity.md)).
5. Add a `MeshPassthrough` for transparent-proxy workloads that reach external destinations
   directly, with a `port` on every non-wildcard `Domain` match.
6. Replace `defaultForbidMeshExternalServiceAccess` with `MeshTrafficPermission` rules.
7. Remove `kuma.io/zone` labels from `MeshExternalService`s and check each zone's network path to
   the endpoints.

## Example

Input:

```yaml
apiVersion: kuma.io/v1alpha1
kind: ExternalService
mesh: default
metadata:
  name: httpbin
spec:
  tags:
    kuma.io/service: httpbin
    kuma.io/protocol: http
  networking:
    address: httpbin.org:443
    tls:
      enabled: true
      caCert:
        inlineString: |
          -----BEGIN CERTIFICATE-----
          MIIB...
          -----END CERTIFICATE-----
      serverName: httpbin.org
```

Output with `--to-latest v2`:

```yaml
# routing/MeshExternalService-kuma-system-httpbin.yaml
apiVersion: kuma.io/v1alpha1
kind: MeshExternalService
metadata:
  labels:
    kuma.io/mesh: default
  name: httpbin
  namespace: kuma-system
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
      caCert:
        inlineString: |
          -----BEGIN CERTIFICATE-----
          MIIB...
          -----END CERTIFICATE-----
      mode: Secured
      serverName: httpbin.org
```

Output with `--to-latest v3` — the same, with the CA in the `SecureDataSource` shape:

```yaml
    verification:
      caCert:
        insecureInline:
          value: |
            -----BEGIN CERTIFICATE-----
            MIIB...
            -----END CERTIFICATE-----
        type: InsecureInline
      mode: Secured
      serverName: httpbin.org
```

`v3` warnings (shortened):

```
⚠  spec.tls.verification.caCert.inlineString rewritten to the SecureDataSource shape (type:
   InsecureInline) that Kuma 3.0 requires. Apply it while every control plane runs 2.14.6 or
   later and before upgrading the global control plane to 3.0 …
⚠  no MeshIdentity for mesh default in the input set. Kuma 3.0 gives a client proxy a
   MeshExternalService cluster only when it has a workload identity … 503 cluster_not_found …
⚠  ExternalService "httpbin": ExternalService is removed in 3.0 … Delete it before upgrading,
   once its converted replacement is in place …
```

## Related

- [Meshes](meshes.md) — the `Mesh` passthrough and routing fields
- [mTLS and workload identity](mtls-identity.md)
- [Access control](access-control.md)
- [Multizone](multizone.md) — zone egress

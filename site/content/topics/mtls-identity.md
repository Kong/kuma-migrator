---
title: mTLS and workload identity
description: Moving a mesh from Mesh.mtls to MeshIdentity and MeshTrust before Kuma 3.0, and why kuma-migrator reports it instead of rewriting it.
order: 20
nav_title: mTLS & identity
---

On 2.x a mesh gets mutual TLS from `Mesh.spec.mtls`: a CA backend (`builtin`, `provided`, or a
Kong Mesh enterprise backend) issues every proxy a certificate. Kuma 2.12 added a second model,
[MeshIdentity](policy:MeshIdentity) + [MeshTrust](policy:MeshTrust), which issues SPIFFE
identities per workload. Kuma 3.0 keeps only the second one.

## What changed upstream

| Version | Change |
|---|---|
| 2.12.0 | `MeshIdentity` and `MeshTrust` introduced, with the `Bundled` and `Spire` providers ([CHANGELOG](https://github.com/kumahq/kuma/blob/master/CHANGELOG.md)) |
| 2.13 | `MeshTrust` `spec.origin` removed from the API and CRD; the value moves to `status.origin` ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md)) |
| 2.14.0 | `MeshIdentity` `Extension` provider; Kong Mesh registers `vault`, `acmpca`, `certmanager` on it. See [External PKI](external-pki.md) |
| 3.0 | `Mesh.mtls` removed from the API. It issues nothing: a mesh whose only identity source is `mtls` **serves and accepts plaintext**, and [MeshTLS](policy:MeshTLS) and [MeshTrafficPermission](policy:MeshTrafficPermission) stop applying to its proxies ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshmtls-no-longer-produces-mtls)) |
| 3.0 | The legacy mesh CA is no longer added to the `MeshTrust` bundle. Proxies still holding a legacy certificate are not trusted by `MeshIdentity` peers, with no rolling path back ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#the-legacy-mesh-ca-is-no-longer-added-to-the-meshtrust-bundle)) |
| 3.0 | `mtls.backends[].mode: PERMISSIVE` no longer makes inbounds permissive. Only `MeshTLS` decides; without one, inbounds are `Strict` ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#mtlsbackendsmode-permissive-no-longer-makes-inbounds-permissive)) |
| 3.0 | `MeshTLS` `from[]` removed; use `rules[]` ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#from-removed-from-meshtls)) |
| 3.0 | `MeshIdentity.spec.spiffeID` is immutable; the trust domain is rendered once and recorded in `status.trustDomain` ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshidentityspecspiffeid-is-immutable-and-its-trust-domain-no-longer-follows-the-zone)) |
| 3.0 | Kong Mesh `vault`, `acm`, `certmanager` CA backends removed ([Kong Mesh upgrade notes](https://developer.konghq.com/mesh/version-specific-upgrade-notes/)) |

The upgrade does not delete the `<mesh>.ca-builtin-cert-<backend>` and
`<mesh>.ca-builtin-key-<backend>` Secrets of a `builtin` backend. Delete them yourself once
nothing needs the old CA ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshmtls-backends-are-no-longer-provisioned-or-validated)).

The identity model is described in [MADR-074 (SPIFFE compliance)](https://github.com/kumahq/kuma/blob/master/docs/madr/decisions/074-spiffe-compliance.md)
and the [MeshIdentity docs](https://kuma.io/docs/latest/policies/meshidentity/).

### MeshTLS is orthogonal

`MeshTLS` is not an identity source. It sets the TLS version, cipher suites and the inbound
mode (`Strict` / `Permissive`) of connections that already use mTLS. It needs an identity source
underneath it: on 3.0 that is `MeshIdentity`. Moving to `MeshIdentity` does not require a
`MeshTLS` policy, and adding one does not issue certificates
([MeshTLS docs](https://kuma.io/docs/latest/policies/meshtls/)).

`spec.mtls` on its own is not deprecated in 2.x and is safe to keep until the cutover.

## What kuma-migrator does

| Input | `--to-latest v2` | `--to-latest v3` |
|---|---|---|
| `Mesh.spec.mtls` | Kept. Advisory: `MeshIdentity` migration required before 3.0 | Removed from the output. **Blocking** warning: create a `MeshIdentity` (and `MeshTrust`) that selects every proxy before upgrading |
| Enabled backend with `mode: PERMISSIVE` | Kept. Warning to add a `MeshTLS` `Permissive` before 3.0 | Companion `MeshTLS` `<mesh>-tls-permissive` with `rules[].default.mode: Permissive` |
| Kong Mesh `vault` / `acmpca` / `certmanager` backend | Advisory names the `MeshIdentity` `Extension` provider | Same, in the blocking warning |
| `MeshTLS` with `from[]` | Converted to `rules[]` (Rules scenario) | Same; a `from[]` source that is not `kind: Mesh` is warned, because `rules[]` widens it |
| `MeshTrust` `spec.origin` | Warning (removed in 2.13) | Warning |
| `MeshMetric` Prometheus `tls.mode: ActiveMTLSBackend` | — | Warning: on 3.0 remote scrapers need a client certificate the mesh trusts ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshmetric-activemtlsbackend-requires-a-client-certificate)) |

Code: [`v3_mesh.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/v3_mesh.go),
[`deprecation.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/deprecation.go)
(`warnMeshMtlsBackends`).

## What it cannot do

The tool never writes a `MeshIdentity`. The move is a CA cutover, not a field rename:

- **Trust domain.** `MeshIdentity` renders a trust domain from a template (default
  `{{ .Mesh }}.{{ .Zone }}.mesh.local`). The zone name is runtime data, and on 3.0 the rendered
  value is fixed at creation.
- **SPIFFE paths.** The default path is `/ns/{{ .Namespace }}/sa/{{ .ServiceAccount }}` on
  Kubernetes and `/workload/{{ .Workload }}` on Universal. Neither the service accounts nor the
  `kuma.io/workload` labels are in the `Mesh` manifest.
- **CA material.** A `builtin` backend's CA lives in a control-plane Secret; a `provided` backend
  references Secrets or files. A generated `Bundled` identity with `autogenerate` would mint a
  new CA, which is a new trust root, not a translation.
- **Rollout order.** Which workloads move first, and when `MeshTrafficPermission` `rules[]` (which
  match SPIFFE IDs) replace `from[]`, depends on the mesh. See [Access control](access-control.md).

## Manual steps

Run these on 2.14, mesh by mesh, before upgrading any control plane to 3.0.

1. Decide the trust domain and path templates. The defaults work for most Kubernetes meshes.
2. Create a `MeshIdentity` (with `meshTrustCreation: Enabled`, or a hand-written `MeshTrust`)
   selecting a first group of proxies with `spec.selector.dataplane.matchLabels`. While
   `Mesh.mtls` is still enabled and a `MeshTrust` exists, 2.x adds the legacy mesh CA to the
   trust bundle, so `MeshIdentity` proxies keep trusting legacy peers
   ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#the-legacy-mesh-ca-is-no-longer-added-to-the-meshtrust-bundle)).
3. Check `DataplaneInsight` `mTLS.issuedBackend` on the selected proxies, then widen the selector
   until it matches every proxy (`matchLabels: {}`).
4. If the enabled backend used `mode: PERMISSIVE`, apply a `MeshTLS` with
   `rules[].default.mode: Permissive` (the `v3` run generates it).
5. Rewrite `MeshTrafficPermission` `from[]` to SPIFFE `rules[]` (see
   [Access control](access-control.md)).
6. Give remote Prometheus scrapers of `ActiveMTLSBackend` endpoints a client certificate the mesh
   trusts, or set the mode to `Disabled`.
7. Upgrade. Apply the `v3` `Mesh` (without `mtls`). Delete the `ca-builtin-*` Secrets when no
   tooling needs them.

A `MeshIdentity` for the whole mesh, using the default templates and a generated CA:

```yaml
apiVersion: kuma.io/v1alpha1
kind: MeshIdentity
metadata:
  name: identity
  namespace: kong-mesh-system
  labels:
    kuma.io/mesh: default
spec:
  selector:
    dataplane:
      matchLabels: {}
  spiffeID:
    trustDomain: "{{ .Mesh }}.{{ .Zone }}.mesh.local"
    path: "/ns/{{ .Namespace }}/sa/{{ .ServiceAccount }}"
  provider:
    type: Bundled
    bundled:
      meshTrustCreation: Enabled
      insecureAllowSelfSigned: true
      certificateParameters:
        expiry: 24h
      autogenerate:
        enabled: true
```

This is the example from the [Kong Mesh MeshIdentity docs](https://developer.konghq.com/mesh/policies/meshidentity/).
To keep your own CA, use `bundled.ca.certificate` / `bundled.ca.privateKey` (`SecureDataSource`)
instead of `autogenerate`; for Vault, ACM Private CA or cert-manager see
[External PKI](external-pki.md).

## Example

Input:

```yaml
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
  meshServices:
    mode: Exclusive
  networking:
    outbound:
      passthrough: false
  mtls:
    enabledBackend: ca-1
    backends:
      - name: ca-1
        type: builtin
        mode: PERMISSIVE
        dpCert:
          rotation:
            expiration: 1d
```

`--to-latest v2` keeps `mtls` and extracts the passthrough setting:

```yaml
# mesh/Mesh-default.yaml
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
  meshServices:
    mode: Exclusive
  mtls:
    backends:
    - dpCert:
        rotation:
          expiration: 1d
      mode: PERMISSIVE
      name: ca-1
      type: builtin
    enabledBackend: ca-1
```

`--to-latest v3` drops `mtls` and adds the `MeshTLS` that keeps inbounds permissive (the
`MeshPassthrough` `None` is generated on both targets):

```yaml
# mesh/Mesh-default.yaml
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: default
spec:
  meshServices:
    mode: Exclusive
```

```yaml
# zero-trust/MeshTLS-kong-mesh-system-default-tls-permissive.yaml
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

The `v3` blocking warning (shortened):

```
⚠  Mesh "default": spec.mtls is REMOVED in 3.0 and has been dropped from the Mesh output.
   BLOCKING: before upgrading, create a MeshIdentity (and MeshTrust) that selects every proxy in
   this mesh, unless one already does. On 3.0 spec.mtls issues no certificates: a mesh whose only
   identity source is spec.mtls serves and accepts plaintext, and MeshTLS and
   MeshTrafficPermission stop applying to its proxies. …
```

> **v3:** Apply the `v3` `Mesh` only after the `MeshIdentity` covers every proxy. Applying it
> earlier on a 2.x control plane turns mTLS off for the mesh.

## Related

- [External PKI](external-pki.md) — Vault, ACM Private CA and cert-manager
- [Access control](access-control.md) — `MeshTrafficPermission` `rules[]` and SPIFFE IDs
- [Meshes](meshes.md)

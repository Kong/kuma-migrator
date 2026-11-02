---
title: External PKI
description: Moving Kong Mesh Vault, ACM Private CA and cert-manager mTLS backends to the MeshIdentity Extension provider before Kong Mesh 3.0.
order: 30
nav_title: External PKI
---

Kong Mesh adds three enterprise CA backends to `Mesh.spec.mtls.backends`: `vault`
(HashiCorp Vault PKI), `acmpca` (AWS Certificate Manager Private CA) and `certmanager`
(Kubernetes cert-manager). They plug into `Mesh.mtls`, which Kuma 3.0 removes, so they go with
it. Their replacement is a [MeshIdentity](policy:MeshIdentity) with the `Extension` provider.

This page covers the backend-specific part. The general cutover from `Mesh.mtls` to
`MeshIdentity` is on [mTLS and workload identity](mtls-identity.md).

## What changed upstream

| Version | Change |
|---|---|
| 2.12.0 | `MeshIdentity` introduced with the `Bundled` and `Spire` providers |
| 2.14.0 | `MeshIdentity` gains `spec.provider.type: Extension` with `spec.provider.extension.{name, config}` (kuma#15537). Kong Mesh 2.14.0 registers three extensions: `vault`, `acmpca`, `certmanager` ([Kong Mesh CHANGELOG](https://developer.konghq.com/mesh/changelog/)) |
| 3.0 | Enterprise CA backends `acm`, `certmanager` and `vault` removed. After the upgrade the control plane no longer understands `Mesh.mtls`: leftover backend configuration is dropped and those proxies get no certificates until a `MeshIdentity` covers them ([Kong Mesh upgrade notes](https://developer.konghq.com/mesh/version-specific-upgrade-notes/)) |

> **Note:** The `Extension` provider and the configuration of its `vault`, `acmpca` and
> `certmanager` extensions are not documented on developer.konghq.com or kuma.io yet. The
> [MeshIdentity page](https://developer.konghq.com/mesh/policies/meshidentity/) lists only
> `Bundled` and `Spire`, and the [Vault](https://developer.konghq.com/mesh/vault/),
> [ACM Private CA](https://developer.konghq.com/mesh/acm-private-ca-policy/) and
> [cert-manager](https://developer.konghq.com/mesh/cert-manager/) pages describe only the
> `Mesh.mtls` backends. The field names below come from the Kong Mesh 2.14 / 3.0 Go types for
> each extension. The Kong Mesh upgrade note calls them the `ACMPCA`, `CertManager` and `Vault`
> providers; in the API they are one provider type, `Extension`, selected by `extension.name`.

### Mapping

| `Mesh.mtls.backends[].type` | `spec.provider.extension.name` | Environments |
|---|---|---|
| `vault` | `vault` | Kubernetes and Universal |
| `acmpca` (`acm` in the 3.0 upgrade note) | `acmpca` | Kubernetes and Universal |
| `certmanager` | `certmanager` | **Kubernetes only**: the control plane registers the extension only when its environment is `kubernetes` |

## What kuma-migrator does

The tool detects the backend type on every `Mesh` and names the matching extension in the
`Mesh.mtls` warning. It never writes the `MeshIdentity`.

| Input | `--to-latest v2` | `--to-latest v3` |
|---|---|---|
| `Mesh` with a `vault`, `acmpca`/`acm` or `certmanager`/`cert-manager` backend | `mtls` kept. Advisory names `spec.provider.type: Extension` and the `extension.name` to use | `mtls` removed, **blocking** warning with the same mapping |

The mapping is `caBackendExtensionMap` in
[`deprecation.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/deprecation.go).

## What it cannot do

- **Copy the backend configuration.** The shapes differ: Vault's flat `conf.fromCp` becomes a
  `connection` block with a `Server`/`Agent` discriminator and an `auth` discriminator
  (`Token`/`TLS`/`AWS`); every data source changes from the flat `secret`/`inline`/`inlineString`
  to the discriminated `SecureDataSource`; ACM Private CA drops the inline `auth.awsCredentials`
  and uses the AWS default credential chain only.
- **Decide the identity.** The trust domain, SPIFFE path and selector are the same decisions as
  for any `MeshIdentity` (see [mTLS and workload identity](mtls-identity.md)).
- **Check the PKI.** The Vault role, the ACM Private CA policy and the cert-manager issuer must
  accept the SPIFFE URI SANs the new identity requests. The `Mesh.mtls` backends issued
  certificates for `kuma.io/service`-based names.

## Manual steps

1. Upgrade to Kong Mesh 2.14 (the `Extension` provider does not exist before it).
2. For each mesh, write a `MeshIdentity` with the matching extension (examples below), selecting a
   first group of proxies.
3. Update the PKI side so it issues the requested SPIFFE identities: Vault role
   `allowed_uri_sans`, ACM Private CA template and IAM permissions for the control plane, or the
   cert-manager issuer.
4. Check `DataplaneInsight` on the selected proxies, then widen the selector to every proxy.
5. Follow the remaining steps on [mTLS and workload identity](mtls-identity.md).
6. Remove the backend from `Mesh.mtls` (the `--to-latest v3` output does this) and the backend's
   Kong Mesh Secrets once nothing references them.

### Vault

```yaml
apiVersion: kuma.io/v1alpha1
kind: MeshIdentity
metadata:
  name: vault-identity
  namespace: kong-mesh-system
  labels:
    kuma.io/mesh: payments
spec:
  selector:
    dataplane:
      matchLabels: {}
  provider:
    type: Extension
    extension:
      name: vault
      config:
        connection:
          type: Server              # or Agent, with agent.address
          namespace: ""             # Vault Enterprise namespace, optional
          server:
            address: https://vault.example.com:8200
            tls:
              caCert:
                type: Secret
                secretRef:
                  kind: Secret
                  name: vault-ca
              serverName: vault.example.com
            auth:
              type: Token           # or TLS (clientCert/clientKey) or AWS (type: IAM|EC2, role, mount, iamServerIdHeader)
              token:
                type: Secret
                secretRef:
                  kind: Secret
                  name: vault-token
        pki:
          mount: kong-mesh-pki-payments   # default: kong-mesh-pki-<mesh>
          role: dataplane-proxies         # default: dataplanes
          certificateParameters:
            expiry: 24h                   # default 120h; capped by the role's max_ttl
        meshTrustCreation: Enabled        # default when unset
```

Field mapping from the `Mesh.mtls` backend:

| `vault` backend (`conf.fromCp`) | `extension.config` |
|---|---|
| `address` | `connection.server.address` |
| `agentAddress` | `connection.type: Agent`, `connection.agent.address` |
| `namespace` | `connection.namespace` |
| `pki` | `pki.mount` |
| `role` | `pki.role` |
| `tls.caCert`, `tls.skipVerify`, `tls.serverName` | `connection.server.tls.{caCert, skipVerify, serverName}` |
| `auth.token` | `connection.server.auth.type: Token`, `.token` |
| `auth.tls.{clientCert, clientKey}` | `connection.server.auth.type: TLS`, `.tls.{clientCert, clientKey}` |
| `auth.aws.{type, role, iamServerIdHeader}` | `connection.server.auth.type: AWS`, `.aws.{type, role, iamServerIdHeader, mount}` |
| `dpCert.rotation.expiration` | `pki.certificateParameters.expiry` |
| `commonName` | none (the identity is the SPIFFE ID) |

### ACM Private CA

```yaml
apiVersion: kuma.io/v1alpha1
kind: MeshIdentity
metadata:
  name: acmpca-identity
  namespace: kong-mesh-system
  labels:
    kuma.io/mesh: payments
spec:
  selector:
    dataplane:
      matchLabels: {}
  provider:
    type: Extension
    extension:
      name: acmpca
      config:
        arn: arn:aws:acm-pca:eu-west-1:123456789012:certificate-authority/12345678-1234-1234-1234-123456789012
        certificateParameters:
          expiry: 24h               # default 120h
        meshTrustCreation: Enabled  # default when unset
```

`arn` is the only required field. The control plane authenticates with the AWS default credential
chain (environment, shared config, instance or pod role) in the region parsed from the ARN. The
`caCert`, `commonName` and `auth.awsCredentials` fields of the `acmpca` backend have no
equivalent: move inline credentials to the control plane's AWS credential chain.

### cert-manager

```yaml
apiVersion: kuma.io/v1alpha1
kind: MeshIdentity
metadata:
  name: certmanager-identity
  namespace: kong-mesh-system
  labels:
    kuma.io/mesh: payments
spec:
  selector:
    dataplane:
      matchLabels: {}
  provider:
    type: Extension
    extension:
      name: certmanager
      config:
        issuerRef:
          name: my-ca-issuer        # required
          kind: Issuer              # default Issuer; or ClusterIssuer
          group: cert-manager.io    # default
        caCert:                     # used to create the MeshTrust; without it, write the MeshTrust yourself
          type: Secret
          secretRef:
            kind: Secret
            name: my-ca-cert
        certificateParameters:
          expiry: 24h               # default 120h
        meshTrustCreation: Enabled  # default when caCert is set
```

`issuerRef` and `caCert` map directly from the `certmanager` backend's `conf`; the data source
moves to the `SecureDataSource` shape. Kong Mesh 2.14 added the `list` verb on cert-manager
resources to the control plane's RBAC `Role`; check it if you manage RBAC yourself
([Kong Mesh upgrade notes](https://developer.konghq.com/mesh/version-specific-upgrade-notes/)).

### SecureDataSource

Every data source in the extension configs uses the discriminated shape:

| Old flat field | New |
|---|---|
| `secret: <name>` | `type: Secret`, `secretRef: {kind: Secret, name: <name>}` |
| `inlineString: <text>` | `type: InsecureInline`, `insecureInline: {value: <text>}` |
| `inline: <base64>` | `type: InsecureInline`, `insecureInline: {value: <decoded text>}` |
| `file: <path>` | `type: File`, `file: {path: <path>}` |
| — | `type: EnvVar`, `envVar: {name: <VAR>}` |

## Example

Input — a Kong Mesh `Mesh` on the `vault` backend:

```yaml
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: payments
spec:
  meshServices:
    mode: Exclusive
  mtls:
    enabledBackend: vault-1
    backends:
      - name: vault-1
        type: vault
        dpCert:
          rotation:
            expiration: 24h
        conf:
          fromCp:
            address: https://vault.example.com:8200
            agentAddress: ""
            namespace: ""
            pki: kong-mesh-pki-payments
            role: dataplane-proxies
            tls:
              caCert:
                secret: vault-ca
              skipVerify: false
              serverName: vault.example.com
            auth:
              token:
                secret: vault-token
```

With `--to-latest v2` the `Mesh` is written back unchanged, with this advisory (shortened):

```
⚠  Mesh "payments": spec.mtls is the legacy mTLS/identity model and is removed in Kuma 3.0. …
   This Mesh uses the Kong Mesh CA backend type(s) vault. Kong Mesh 2.14 added a matching
   MeshIdentity provider: spec.provider.type: Extension with spec.provider.extension.name set to
   vault. … certmanager is Kubernetes-only; on Universal the CP does not register it.
```

With `--to-latest v3` the backend is removed:

```yaml
# mesh/Mesh-payments.yaml
apiVersion: kuma.io/v1alpha1
kind: Mesh
metadata:
  name: payments
spec:
  meshServices:
    mode: Exclusive
```

```
⚠  Mesh "payments": spec.mtls is REMOVED in 3.0 and has been dropped from the Mesh output.
   BLOCKING: before upgrading, create a MeshIdentity (and MeshTrust) that selects every proxy in
   this mesh, unless one already does. … This Mesh uses the Kong Mesh CA backend type(s) vault,
   which are removed in Kong Mesh 3.0. Replace them with a MeshIdentity using
   spec.provider.type: Extension with spec.provider.extension.name set to vault. …
```

## Related

- [mTLS and workload identity](mtls-identity.md)
- [MeshIdentity](policy:MeshIdentity), [MeshTrust](policy:MeshTrust), [Mesh](policy:Mesh)

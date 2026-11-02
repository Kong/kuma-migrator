---
title: Target versions
description: Why the 2.x and 3.0 lines need different output, and what --to-latest v3 rewrites, warns about or refuses.
order: 70
nav_title: Target versions (--to-latest)
---

`migrate` (including its `--dry-run` mode) takes `--to-latest v2|v3`, defaulting to `v2`.

| Value | Target | What it does |
|---|---|---|
| `v2` (default) | latest 2.x (2.14.x) | Keeps the output applicable to a 2.x control plane. Fields removed in 3.0 but still accepted in 2.14 are reported as forward-looking advisories, never rewritten. |
| `v3` | 3.0 (Kuma 3.0.0/3.0.1, Kong Mesh 3.0.0/3.0.1) | Rewrites what the manifest carries enough information for, and reports the rest as blocking work. Checked entry by entry against the released 3.0.0 upgrade notes of Kuma and Kong Mesh. |

The two lines need different output, so this is an explicit choice rather than a default:
3.0 removes fields that 2.14 still requires, and some 3.0 replacements
(`MeshOpenTelemetryBackend`, `SecureDataSource`) do not exist before 2.14. A v3 report carries a
banner saying the output must not be applied to a 2.x control plane.

3.0 also rejects fields that are not in its schema (`spec.from: unknown field`) and
control-plane-owned labels whose value differs from the computed one. That is why `v3` deletes
fields rather than leaving them in place.

## Which target to use

| Situation | Target |
|---|---|
| Cleaning up a 2.x mesh, or moving between 2.x minors | `v2` |
| Preparing the last 2.x state before a 3.0 upgrade (`MeshOpenTelemetryBackend`, `meshServices.mode: Exclusive`, converted legacy policies) | `v2`, applied on 2.14 |
| Producing the manifests to keep under source control for 3.0, and the list of objects to delete before the upgrade | `v3` |
| Finding out how much manual work a 3.0 upgrade needs | `v3 --dry-run` |

Run both against the same extracted tree when you plan a 3.0 upgrade. The sequence is in
[Upgrade plans](upgrade-plans.md).

## What v3 rewrites automatically

| Resource | 3.0 change | v3 output |
|---|---|---|
| `Mesh` | `spec.mtls` removed | Field dropped, with a **blocking** warning: create a `MeshIdentity` (+ `MeshTrust`) that selects every proxy before upgrading. On 3.0 a mesh whose only identity source is `mtls` serves plaintext, and `MeshTrafficPermission`/`MeshTLS` stop applying. |
| `Mesh` | `mtls` backend `mode: PERMISSIVE` no longer makes inbounds permissive | Companion `MeshTLS` with `rules[].default.mode: Permissive` |
| `Mesh` | `routing.zoneEgress`, `routing.localityAwareLoadBalancing`, `routing.defaultForbidMeshExternalServiceAccess`, `skipCreatingInitialPolicies` removed | Fields dropped, each with a note on what replaces it |
| `Mesh` | a transparent-proxy data plane that no `MeshPassthrough` selects now drops external traffic | `networking.outbound.passthrough: true` → companion `MeshPassthrough` with `passthroughMode: All` (unset: warning only) |
| `Mesh` | `meshServices` has no effect on 3.0 zones | `meshServices.mode: Exclusive` is kept: 2.x zones read a missing field as `Disabled`, so it must be set before the Global CP upgrade. Drop it once every zone runs 3.0. |
| `MeshMetric`/`MeshTrace`/`MeshAccessLog` | inline `openTelemetry.endpoint` removed | Generated `MeshOpenTelemetryBackend` plus `backendRef.labels` pointing at it |
| Any policy | `targetRef`/`to[].targetRef` select real resources by `labels` only | `name` → `kuma.io/display-name`, `namespace` → `k8s.kuma.io/namespace`, `mesh` → `kuma.io/mesh`. `Dataplane{name}` is warned (a Dataplane name is not a label). |
| Any Kuma resource | control-plane-owned labels rejected when they differ from the computed value | `kuma.io/origin`, `kuma.io/zone`, `kuma.io/display-name`, `kuma.io/policy-role`, `k8s.kuma.io/namespace`, … removed from the resource's own labels (selectors are untouched; `kuma.io/mesh` stays) |
| `MeshHTTPRoute`/`MeshTCPRoute` | `backendRefs` labels only; `MeshServiceSubset` removed | `name`/`namespace` → labels; a subset that only names `kuma.io/service` → `MeshService` |
| Legacy `HealthCheck` | `healthyPanicThreshold` removed from `MeshHealthCheck` | Companion `MeshCircuitBreaker` carrying `outlierDetection.healthyPanicThreshold`. 3.0 ignores the threshold without outlier detection, so the companion sets `detectors.totalFailures.consecutive: 5`, which **turns on outlier ejection** for those destinations. Review it. |
| `MeshFaultInjection` | `from[]` removed | `rules[]` when every source is `kind: Mesh` (legacy `FaultInjection`: every source `kuma.io/service: '*'`) |
| `MeshExternalService` | TLS `DataSource` → `SecureDataSource` | `inlineString` → `insecureInline.value`, `secret` → `secretRef`. `inline` (base64) stays warn-only: rewriting it means decoding credential material. 2.14.6+ accepts both shapes. |
| `Dataplane` | `networking.gateway` (any type) ignored | Block removed; non-`kuma.io/` gateway tags moved to Dataplane labels |
| `Dataplane` | inbound `tags` removed; `kuma.io/protocol` tag no longer sets the protocol | Tag copied to `inbound[].protocol`; other tags moved to labels |
| `Dataplane` | outbounds must use `backendRef`; `reachableServices` removed; `reachableBackends` labels only | Converted where the `kuma.io/service` value decodes to name, namespace and port |
| `MeshService` | `selector.dataplaneTags` removed; `identities[]` SPIFFE only | `dataplaneTags` → `dataplaneLabels.matchLabels`; `type: ServiceTag` entries dropped |
| `MeshOPA` (Kong Mesh) | `targetRef.kind` `Mesh`/`Dataplane` only; `name`/`namespace`/`mesh` removed; `SecureDataSource` | `MeshService` → `Dataplane`, `MeshSubset{tags}` → `Dataplane{labels}`, `name` → label, data sources rewritten |
| `AccessRole`/`AccessAudit` (Kong Mesh) | legacy types and qualifiers removed | `rules[].types` renamed to the `Mesh*` successors; types with no successor removed; legacy `when[]` qualifiers removed without widening the grant |
| Gateway API output | `kuma.io/mesh` annotation no longer honoured | Written as a label (on both targets) |

## What v3 refuses

These produce an error. The original is written to the `_manual/` subtree, outside the apply tree
(see [Manual subtree](apply.md#manual-subtree)).

| Input | Why |
|---|---|
| Legacy `TrafficPermission` | 3.0 `MeshTrafficPermission` needs `rules[]` with SPIFFE IDs, which need a `MeshIdentity` and trust domain the manifest does not carry. Re-run with `--to-latest v2` for the `from[]` output. See [Access control](../topics/access-control.md). |
| Legacy `FaultInjection` with specific sources | `rules[].matches[]` matches SPIFFE IDs or SNI, not tags |
| Legacy selector with no service tag in a destination | 3.0 accepts no tag selector in `to[]` |
| `TrafficRoute`, `VirtualOutbound` | No single successor (both targets) |
| `MeshGatewayInstance` | The built-in gateway is removed; run your own gateway |

Documents that pass through as a kind 3.0 removes (`MeshGlobalRateLimit`, Universal
`ZoneIngress`/`ZoneEgress`) also go to `_manual/`.

## What v3 only warns about

The manifest does not carry what the fix needs:

- `MeshExternalService` with no `MeshIdentity` in the input for its mesh: clients get 503 `cluster_not_found`.
- `MeshExternalService` with a `kuma.io/zone` label: per-zone routing is removed.
- `MeshPassthrough` `Domain` match without a `port`, and matches that collide on one Envoy filter chain.
- `MeshMetric` `ActiveMTLSBackend`: remote scrapers need a mesh-trusted client certificate.
- `MeshTrafficPermission` `from[]` (3.0 rejects it: "policy must define rules") and empty `allow`/`deny` entries.
- `from[]` entries on `MeshTimeout`/`MeshCircuitBreaker`/`MeshAccessLog` whose source is not `kind: Mesh`: converting to `rules[]` widens the scope.
- `to[].targetRef.kind` not accepted by the policy on 3.0 (matrix in [Policy targeting](../topics/policy-targeting.md)).
- `MeshProxyPatch` patching cluster `circuitBreakers`: on 3.0 it overrides `MeshCircuitBreaker`.
- `Dataplane` `advertisedAddress`, redirect ports, `ipFamilyMode`, `probes`, and transparent-proxy Dataplanes with no `reachableBackends` (no outbounds on 3.0).
- `ZoneIngress`/`ZoneEgress`: replace with a zone-proxy `Dataplane` per mesh; cross-zone `MeshService` traffic needs a `MeshZoneAddress` (snippet included).
- `ContainerPatch` probes on admin port `9901`: use `9902`.
- Pod and pod-template annotations removed in 3.0: `kuma.io/ingress`, `kuma.io/egress`, `kuma.io/mesh` (use the label), `kuma.io/transparent-proxying-reachable-services`, `kuma.io/transparent-proxying-ebpf*`, virtual probes.
- `Mesh`/`Zone`/`MeshService`/`MeshExternalService`/`MeshMultiZoneService` names that are not lowercase RFC 1035 labels.
- `AccessRole` `when[].targetRef.kind` other than `Mesh`/`Dataplane`, and `tags`/`mesh` on qualifier targetRefs.
- `MeshLoadBalancingStrategy` `crossZone` on a `to[]` entry that is not a `MeshMultiZoneService`.
- `MeshHTTPRoute` with no catch-all rule: on 3.0 unmatched requests get a `404`.

## Checks that differ between the targets

- **`Dataplane` inbound tags** are only flagged under `v3`. They are mandatory in 2.x, so a v2
  advisory would fire on every Dataplane. 3.0 drops them *silently*, so anything selecting on
  them stops matching with nothing logged.
- **Inline `openTelemetry.endpoint`** is only warned about under `v2`, with an explicit warning
  *not* to migrate it until every control plane and data plane is on 2.14:
  `MeshOpenTelemetryBackend` does not exist before then, and the control plane silently skips
  the OTel route against an older data plane.
- **`reachableBackends` name refs** and **`MeshService` `dataplaneTags`** are rewritten under
  `v3` only. On 2.14 a labels selector also matches same-named services synced from other zones,
  so the rewrite would widen selection.
- **`Mesh.mtls`** is kept under `v2` with an advisory that the `MeshIdentity` migration is
  required before 3.0.
- **Kong Mesh enterprise CA backends** (`vault`, `acmpca`, `cert-manager`) are removed in 3.0.
  Both targets name the `MeshIdentity` `Extension` provider that replaces each one.
- **`MeshGlobalRateLimit`** warns under both targets. Under `v3` it is moved to `_manual/` and
  listed for deletion, never for apply.

## Upstream upgrade notes

Every v3 rule comes from one of these:

- Kuma: [`UPGRADE.md`](https://github.com/kumahq/kuma/blob/master/UPGRADE.md), section
  "Upgrade to `3.0.0`", and the
  [Kuma upgrade notes](https://kuma.io/docs/latest/production/upgrades-tuning/upgrade-notes/).
- Kong Mesh: [version-specific upgrade notes](https://developer.konghq.com/mesh/version-specific-upgrade-notes/).

The per-kind behaviour on each target is on the [policy pages](../policies/_index.md).

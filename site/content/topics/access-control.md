---
title: Access control
description: MeshTrafficPermission from[] versus SPIFFE rules[], MeshOPA, and the Kong Mesh RBAC changes in 3.0 — what kuma-migrator converts and what stays manual.
order: 80
---

Access control in a mesh has two layers. Service-to-service authorization decides which clients
may reach a workload: [TrafficPermission](policy:TrafficPermission),
[MeshTrafficPermission](policy:MeshTrafficPermission) and, on Kong Mesh, [MeshOPA](policy:MeshOPA)
(formerly [OPAPolicy](policy:OPAPolicy)). Control plane RBAC decides who may read and write
resources: Kong Mesh [AccessRole](policy:AccessRole) and [AccessAudit](policy:AccessAudit).
All of them change before or at 3.0.

## MeshTrafficPermission: two modes

`MeshTrafficPermission` has two client-matching models. 3.0 keeps only the second.

| | `from[]` | `rules[]` |
|---|---|---|
| Spec shape | `spec.targetRef` + `from[]`, each `{targetRef, default.action}` | `spec.targetRef` + `rules[]`, each `{default.{allow, deny, allowWithShadowDeny}}` |
| Client selector | Tag/label `targetRef` (`Mesh`, `MeshSubset`, `MeshServiceSubset`) | SPIFFE identity matchers (`spiffeID`, optional `sni`) |
| Identity source | `Mesh.spec.mtls`; SPIFFE derived from `kuma.io/service` | [MeshIdentity](policy:MeshIdentity) + [MeshTrust](policy:MeshTrust) (required) |
| Verbs | `action: Allow` / `Deny` / `AllowWithShadowDeny` per source | `allow[]` / `deny[]` / `allowWithShadowDeny[]` lists of matchers |
| Evaluation | Ordered: a later `from[]` entry overrides an earlier one | `deny` wins over `allow`/`allowWithShadowDeny`; order-independent |
| No policy | Permissive (a default allow-all policy is present) | **All requests denied** |
| Prerequisite | Mutual TLS enabled | `MeshIdentity` enabled |
| Status | Stable; `from[]` deprecated in 2.14 | Matchers since 2.12; `sni` since 2.14 |

Sources: [MeshTrafficPermission](https://kuma.io/docs/latest/policies/meshtrafficpermission/),
[MeshTrafficPermission (experimental)](https://kuma.io/docs/latest/policies/meshtrafficpermission_experimental/),
[MADR-074](https://github.com/kumahq/kuma/blob/master/docs/madr/decisions/074-spiffe-compliance.md).

> **Note:** `MeshTrafficPermission`'s `rules[]` is not the generic Rules API. For `MeshTimeout`,
> `MeshCircuitBreaker`, `MeshAccessLog` and others, `rules[]` is
> `[{matches: [...], default: {...}}]`. `MeshTrafficPermission` uses
> `[{default: {allow, deny, allowWithShadowDeny}}]` with SPIFFE/SNI matchers, no `matches[]` and
> no `targetRef` inside a rule.

### Why it is not a field mapping

`from[]` selects clients by tags such as `kuma.io/service: orders`. `rules[]` selects them by
SPIFFE ID, for example `spiffe://default.zone-1.mesh.local/ns/demo/sa/orders`. The trust domain
and the per-workload path come from the `MeshIdentity` templates and the workload's namespace and
service account; none of them are in the policy. The default posture also flips from permissive to
default-deny, so a rewrite that misses one client blocks it, and one that widens a matcher lets in
clients the old policy refused.

## What changed upstream

| Version | Change |
|---|---|
| 2.7 | `MeshTrafficPermission`/`MeshFaultInjection` `from[].targetRef.kind: MeshService` deprecated |
| 2.12 | `rules[]` SPIFFE matchers; `spiffeId` field spelled `spiffeID` |
| 2.13 | `MeshFaultInjection` `rules[]` API |
| 2.14 | `MeshTrafficPermission` `from[]` deprecated (kuma#16182); `MeshFaultInjection` `from[]` deprecated |
| 3.0 | `MeshTrafficPermission.spec.from` removed. A policy with only `from[]` fails with `policy must define rules`; `from` itself is rejected as `spec.from: unknown field` ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshtrafficpermissionspecfrom-removed), [unknown fields](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#resources-with-fields-that-are-not-in-the-schema-are-rejected)) |
| 3.0 | Empty match entries in `allow`/`deny`/`allowWithShadowDeny` rejected; each needs `spiffeID`, `sni` or both ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#empty-meshtrafficpermission-match-entries)) |
| 3.0 | `Mesh.mtls` issues nothing: on a mesh without `MeshIdentity`, `MeshTrafficPermission` stops applying ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#meshmtls-no-longer-produces-mtls)) |
| 3.0 | Legacy `TrafficPermission` removed; the default allow-all `TrafficPermission` is no longer created for new meshes ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#legacy-policy-resources-removed)) |
| Kong Mesh 2.13 | `OPAPolicy` does not work with the default dynamic-config OPA runtime; migrate to `MeshOPA` ([Kong Mesh upgrade notes](https://developer.konghq.com/mesh/version-specific-upgrade-notes/)) |
| Kong Mesh 3.0 | `OPAPolicy` removed; `MeshOPA` `targetRef` loses `name`/`namespace`/`mesh` and accepts only `Mesh`/`Dataplane`; `agentConfig` and `appendPolicies[].rego` use `SecureDataSource`; `kuma-dp` `--opa-config-path` and `KMESH_OPA_EXPERIMENTAL_USE_DYNAMIC_CONFIG` removed ([Kong Mesh upgrade notes](https://developer.konghq.com/mesh/version-specific-upgrade-notes/)) |
| Kong Mesh 3.0 | `AccessRole` `when[].targetRef` qualifiers accept only `Mesh` and `Dataplane`; legacy policy types and `sources`/`destinations`/`selectors` qualifiers removed from `AccessRole` and `AccessAudit` |
| Kong Mesh 3.0 | `GET /config` requires the `VIEW_CONTROL_PLANE_METADATA` access type (it checked `GENERATE_DATAPLANE_TOKEN` before). Roles narrowed by hand and an `admin` role created before 2.7.0 lack it |
| Kong Mesh 3.0 | A new control plane binds the `admin` role to fewer subjects (no unauthenticated callers). Existing bindings are unchanged |

`MeshFaultInjection` is the exception among inbound policies: a `rules[]` entry with no `matches`
applies to every client, so `from[]` entries that all use `kind: Mesh` convert mechanically.

## What kuma-migrator does

| Input | `--to-latest v2` | `--to-latest v3` |
|---|---|---|
| Legacy `TrafficPermission` | `MeshTrafficPermission` with `from[]` and `action: Allow`. `destinations` → `spec.targetRef`, `sources` → `from[]` | **Error**. Original written to `_manual/`. Re-run with `v2` for the `from[]` form |
| `MeshTrafficPermission` with `from[]` | Warning: deprecated, not converted, manual steps listed | Warning: rejected by 3.0 as it stands |
| `MeshTrafficPermission` `rules[]` with an empty match entry | Warning | Warning |
| `MeshTrafficPermission` `action: ALLOW`/`DENY` | Warning: use `Allow`/`Deny` | Same |
| `MeshTrafficPermission` `spiffeId` | Rewritten to `spiffeID` | Same |
| `from[].targetRef.kind: MeshService` | Warning | Warning |
| Legacy `FaultInjection` | `MeshFaultInjection` with `from[]` | `rules[]` when every source is `kuma.io/service: '*'`; error otherwise |
| `MeshFaultInjection` with `from[]` | Warning | `rules[]` when every entry is `kind: Mesh`; warning otherwise |
| `OPAPolicy` | `MeshOPA`: `conf.policies[]` → `default.appendPolicies[].rego`, `conf.agentConfig` → `default.agentConfig`, `targetRef` kept, with 3.0 advisories | Same, plus the 3.0 rewrites below |
| `MeshOPA` `targetRef.kind: MeshService` / `name` | Advisory | `kind: Dataplane`, `name` → `labels.app` (scope kept); refuses to overwrite a conflicting label |
| `MeshOPA` flat `DataSource` (`inlineString`/`inline`/`secret`) | Advisory | Rewritten to `SecureDataSource`, including base64 `inline` (rego is not credential material) |
| `AccessRole`/`AccessAudit` `rules[].types` with legacy kinds | Advisory | Renamed to the `Mesh*` successor; types with no successor removed |
| `AccessRole` `when[]` entry with only `sources`/`destinations`/`selectors` | Advisory | Entry removed. Other qualifiers stay, so the grant does not widen |
| `AccessRole` `when[].targetRef.kind` other than `Mesh`/`Dataplane` | Advisory | Warning; not converted (the label set is not in the manifest) |

`MeshTrafficPermission` is never in the Rules scenario's `from[]` → `rules[]` conversion
(`rulesAPIMigrationKinds`); the warning is `warnFromDeprecatedForRulesAPI` in
[`deprecation.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/deprecation.go).
OPA: [`opapolicy.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/opapolicy.go).
RBAC: [`v3_kongmesh.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/v3_kongmesh.go).

### `/config` and extraction

`extract --kumactl-context` reads `GET /config` to detect the control plane mode and environment.
On Kong Mesh 3.0 a token whose role lacks `VIEW_CONTROL_PLANE_METADATA` gets `401`/`403`. The
extractor then warns, names the access type, and continues with the mode unknown: no origin
filter, Kubernetes skip list. See [Extract](../guides/extract.md).

## What it cannot do

- **Write SPIFFE matchers.** Trust domain, namespace and service account are not in the policy.
- **Preserve source scope in `MeshFaultInjection` `rules[]`** when a source is narrower than
  `kind: Mesh`: `rules[].matches[]` matches SPIFFE IDs or SNI, not tags.
- **Rewrite `AccessRole` qualifiers to `Dataplane` labels.** Which labels select the same proxies
  is not in the role.
- **Change RBAC bindings.** `AccessRoleBinding` subjects and the `VIEW_CONTROL_PLANE_METADATA`
  access type are control plane state you edit.

## Manual steps

1. Migrate the mesh to `MeshIdentity` (see [mTLS and workload identity](mtls-identity.md)).
2. For each `MeshTrafficPermission`, find the SPIFFE ID of each client group in `from[]`. Check
   the workloads' identities in `DataplaneInsight` or `MeshService.spec.identities`.
3. Write `rules[]`: one `allow`, `deny` or `allowWithShadowDeny` matcher per `from[]` entry, by its
   `action`. Add an `allow` for every client that was allowed implicitly (default-deny). Replace
   ordering with explicit `deny` entries.
4. Roll out with `allowWithShadowDeny` first to find clients you missed, then switch to `allow`.
5. Remove `from[]` (3.0 rejects it), and remove empty match entries.
6. Kong Mesh: apply the converted `MeshOPA`, delete `OPAPolicy`, move any `--opa-config-path`
   file into `MeshOPA` `agentConfig`.
7. Kong Mesh: add `VIEW_CONTROL_PLANE_METADATA` to roles that read `/config`
   (`kumactl get access-roles -o yaml`), and rewrite `AccessRole` qualifiers to `Mesh`/`Dataplane`.

A `from[]` policy and its `rules[]` equivalent, assuming the default Kubernetes identity templates:

```yaml
# before (2.x)
spec:
  targetRef:
    kind: Dataplane
    labels:
      app: payments
  from:
    - targetRef:
        kind: MeshSubset
        tags:
          app: orders
      default:
        action: Allow
---
# after (2.14 with MeshIdentity, and 3.0)
spec:
  targetRef:
    kind: Dataplane
    labels:
      app: payments
  rules:
    - default:
        allow:
          - spiffeID:
              type: Exact
              value: spiffe://default.zone-1.mesh.local/ns/kuma-demo/sa/orders
```

## Example

Input — three files: a legacy `TrafficPermission`, a `MeshTrafficPermission` on `from[]`, and a
Kong Mesh `AccessRole`:

```yaml
type: TrafficPermission
mesh: default
name: web-to-backend
sources:
  - match:
      kuma.io/service: web_demo_svc_8080
destinations:
  - match:
      kuma.io/service: backend_demo_svc_3001
---
apiVersion: kuma.io/v1alpha1
kind: MeshTrafficPermission
metadata:
  name: allow-orders
  namespace: kong-mesh-system
  labels:
    kuma.io/mesh: default
spec:
  targetRef:
    kind: Dataplane
    labels:
      app: payments
  from:
    - targetRef:
        kind: MeshSubset
        tags:
          app: orders
      default:
        action: Allow
---
type: AccessRole
name: team-a-policies
rules:
  - types: ["TrafficPermission", "MeshTimeout"]
    mesh: default
    access: ["CREATE", "UPDATE", "DELETE"]
    when:
      - sources:
          match:
            kuma.io/service: web_demo_svc_8080
      - targetRef:
          kind: MeshService
          name: backend
```

With `--to-latest v2` the `TrafficPermission` becomes a `from[]` policy; the other two are copied
with advisories:

```yaml
# zero-trust/MeshTrafficPermission-web-to-backend.yaml
apiVersion: kuma.io/v1alpha1
kind: MeshTrafficPermission
metadata:
  labels:
    kuma.io/mesh: default
  name: web-to-backend
spec:
  from:
  - default:
      action: Allow
    targetRef:
      kind: MeshService
      name: web
      namespace: demo
  targetRef:
    kind: Dataplane
    name: backend
    namespace: demo
```

```text
⚠  MeshTrafficPermission "web-to-backend": from[].targetRef.kind MeshService is deprecated in
   Kuma 2.7+ — use kind: Dataplane with labels instead.
⚠  MeshTrafficPermission "web-to-backend": the from[] field is deprecated in favour of the rules[]
   API (Kuma 2.14, removed in 3.0) and is NOT auto-converted. …
```

> **Warning:** In this output `spec.targetRef` is `kind: Dataplane` with `name: backend`, which
> selects the single `Dataplane` resource named `backend`. On Kubernetes a `Dataplane` is named
> after its pod, so check that it selects the proxies you expect, or replace it with `labels`.

With `--to-latest v3` the `TrafficPermission` is refused and written to `_manual/`, the
`MeshTrafficPermission` is copied with a "rejected by 3.0" warning, and the `AccessRole` is
rewritten:

```yaml
# other/AccessRole-team-a-policies.yaml (v3)
name: team-a-policies
rules:
- access:
  - CREATE
  - UPDATE
  - DELETE
  mesh: default
  types:
  - MeshTrafficPermission
  - MeshTimeout
  when:
  - targetRef:
      kind: MeshService
      name: backend
type: AccessRole
```

```text
✗  TrafficPermission "web-to-backend" requires manual migration for Kuma 3.0: 3.0 removes
   MeshTrafficPermission spec.from … its replacement selects clients with
   rules[].default.{allow,deny,allowWithShadowDeny}[].spiffeID matches. Those need a MeshIdentity
   and its trust domain, which this manifest does not carry. …
⚠  AccessRole "team-a-policies": rules[0].types rewritten for 3.0. Renamed:
   TrafficPermission→MeshTrafficPermission.
⚠  AccessRole "team-a-policies": rules[0].when[1].targetRef.kind MeshService is rejected in 3.0 …
   Not converted: the label set is not in the manifest.
⚠  AccessRole "team-a-policies": rules[0].when[0] removed. They only set
   sources/destinations/selectors (or nothing), which 3.0 removes … so the grant does not widen.
```

A Kong Mesh `OPAPolicy` converted with `--to-latest v3`:

```yaml
# input
apiVersion: kuma.io/v1alpha1
kind: OPAPolicy
mesh: default
metadata:
  name: backend-authz
spec:
  targetRef:
    kind: MeshService
    name: backend
  conf:
    agentConfig:
      inlineString: |
        decision_logs:
          console: true
    policies:
      - inlineString: |
          package envoy.authz
          default allow = false
```

```yaml
# zero-trust/MeshOPA-backend-authz.yaml (v3)
apiVersion: kuma.io/v1alpha1
kind: MeshOPA
metadata:
  labels:
    kuma.io/mesh: default
  name: backend-authz
spec:
  default:
    agentConfig:
      insecureInline:
        value: |
          decision_logs:
            console: true
      type: InsecureInline
    appendPolicies:
    - rego:
        insecureInline:
          value: |-
            package envoy.authz
            default allow = false
        type: InsecureInline
  targetRef:
    kind: Dataplane
    labels:
      app: backend
```

Under `v2` the same input gives a `MeshOPA` with `inlineString` data sources and the original
`targetRef: {kind: MeshService, name: backend}`, plus advisories for both.

## Related

- [mTLS and workload identity](mtls-identity.md)
- [Policy targeting](policy-targeting.md)
- [Legacy policies](legacy-policies.md)
- [Kong Mesh RBAC](https://developer.konghq.com/mesh/rbac/),
  [AccessAudit](https://developer.konghq.com/mesh/access-audit/)

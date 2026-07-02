<!--
UPSTREAM CONTRIBUTION DRAFT — not part of the kuma-migrator product.

Target repo:  github.com/kumahq/kuma-website
Target file:  app/_src/policies/meshtrafficpermission_experimental.md
Suggested insertion point: after the "## Configuration" section (which lists the
  allow/deny/allowWithShadowDeny evaluation rules) and before "## Examples".

Rationale: the experimental page currently documents the SPIFFE rules[] model in
isolation. The stable page (meshtrafficpermission.md) links here, but neither page
explains how the two modes differ or how to move a from[] policy to rules[]. Users
upgrading hit this gap. The two sections below fill it. Written in the site's
Jekyll/Liquid conventions ({% tip %}, {% warning %}, /docs/{{ page.release }}/ links,
{% policy_yaml %}); a maintainer may want to wrap version-specific bits in
{% if_version %} guards to match the page's release scoping.

Verified against Kuma 2.14 (kong-ama KB, kuma @ 36ae6234, 2026-06-30):
  - stable actions Allow/Deny/AllowWithShadowDeny, ordered last-match-wins
    (meshtrafficpermission.md:105-109, 584-588)
  - experimental evaluation deny > allow/allowWithShadowDeny > default-deny
    (meshtrafficpermission_experimental.md "Configuration")
  - Match{spiffeID,sni} shape (MADR-074 §Decision; api/common matchers)
  - from field deprecated in 2.14 (CHANGELOG #16182); matchers since 2.12.0
-->

## Differences from the stable `MeshTrafficPermission`

This experimental API replaces the source `targetRef` selectors of the
[stable `MeshTrafficPermission`](/docs/{{ page.release }}/policies/meshtrafficpermission)
`from[]` field with **SPIFFE-identity matchers** under `rules[]`. The two are different
models, not just different syntax:

| | Stable (`from[]`) | Experimental (`rules[]`) |
| --- | --- | --- |
| Spec shape | `spec.targetRef` + `from[]`, each `{ targetRef, default.action }` | `spec.targetRef` + `rules[]`, each `{ default.{ allow, deny, allowWithShadowDeny } }` |
| Client selector | tag/label `targetRef` (`Mesh`, `MeshSubset`, `MeshServiceSubset`) | SPIFFE identity matchers (`spiffeID`, optional `sni`) |
| Identity source | `Mesh` mTLS backends; SPIFFE derived from `kuma.io/service` | [`MeshIdentity`](/docs/{{ page.release }}/policies/meshidentity) + [`MeshTrust`](/docs/{{ page.release }}/policies/meshtrust) |
| Verbs | `action: Allow` / `Deny` / `AllowWithShadowDeny` per source | `allow[]` / `deny[]` / `allowWithShadowDeny[]` lists of matchers |
| Evaluation | ordered — a later `from[]` entry overrides an earlier one | `deny` wins over `allow`/`allowWithShadowDeny`; order-independent |
| Default (no policy) | permissive (a default allow-all policy is present) | **all requests denied** |
| Prerequisite | [Mutual TLS](/docs/{{ page.release }}/policies/mutual-tls) enabled | `MeshIdentity` enabled |

{% warning %}
The default posture is inverted between the two APIs. With the stable API, traffic is
allowed unless a `Deny` rule matches; with this API, traffic is denied unless an `allow`
matcher matches. Review your rules against this before switching.
{% endwarning %}

## Migrating from the stable `from[]` API

There is no automatic conversion, because the `from[]` model selects clients by tags
(for example `kuma.io/service: orders`) while this API selects them by SPIFFE identity
(for example `spiffe://<trust-domain>/ns/<namespace>/sa/<service-account>`). The trust
domain and the per-workload identity path are not contained in the policy itself — they
come from `MeshIdentity`/`MeshTrust` and the workload's namespace and service account — so
each `from[]` entry has to be re-expressed by hand.

Migrate as follows:

1. Enable [`MeshIdentity`](/docs/{{ page.release }}/policies/meshidentity) (and the
   resulting [`MeshTrust`](/docs/{{ page.release }}/policies/meshtrust)) for the mesh.
2. For each `from[]` entry, translate its source `targetRef` into the SPIFFE identity of
   those clients and place it under `rules[].default.allow`, `deny`, or
   `allowWithShadowDeny` according to its `action`.
3. Because this API is default-deny, add an explicit `allow` for every client group that
   the stable policy permitted (including any that were allowed implicitly).
4. Replace `from[]` ordering with explicit `deny` matchers — `deny` always takes
   precedence, so ordering no longer matters.

For example, the stable policy:

```yaml
type: MeshTrafficPermission
name: allow-orders
mesh: default
spec:
  targetRef:
    kind: Dataplane
    labels:
      app: payments
  from:
    - targetRef:
        kind: MeshSubset
        tags:
          kuma.io/service: orders
      default:
        action: Allow
```

becomes, in this API:

{% policy_yaml %}
```yaml
type: MeshTrafficPermission
name: allow-orders
mesh: default
spec:
  targetRef:
    kind: Dataplane
    labels:
      app: payments
  rules:
    - default:
        allow:
          - spiffeID:
              type: Prefix
              value: "spiffe://default.mesh.local/ns/kuma-demo/sa/orders"
```
{% endpolicy_yaml %}

{% tip %}
The `spiffeID` value depends on your trust domain and how identities are issued
(namespace + service account on Kubernetes, workload identity on Universal). Confirm the
exact identities from `MeshIdentity`/`MeshTrust` and the workloads before applying, and
consider rolling the new rules out with `allowWithShadowDeny` first to catch any client you
missed before enforcing.
{% endtip %}

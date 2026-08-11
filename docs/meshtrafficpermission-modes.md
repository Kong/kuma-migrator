# MeshTrafficPermission: `from[]` vs `rules[]`

The two identity models behind `MeshTrafficPermission`, and why moving between them is an operator task rather than a field mapping.

[← Back to the README](../README.md)

---

`MeshTrafficPermission` has **two modes**. The migrator flags the deprecated `from[]` field
but does **not** auto-convert it, because the two modes use fundamentally different identity
models and a mechanical rewrite could silently widen access. The difference is only lightly
documented upstream (the stable policy page links to the experimental page; there is no
side-by-side comparison or migration guide), so it is summarised here.

| | Stable (`from[]`) | Experimental (`rules[]`) |
|---|---|---|
| Spec shape | `spec.targetRef` + `from[]`, each `{targetRef, default.action}` | `spec.targetRef` + `rules[]`, each `{default.{allow,deny,allowWithShadowDeny}}` |
| Client selector | tag/label `targetRef` (`Mesh`/`MeshSubset`/`MeshServiceSubset`) | **SPIFFE identity** matchers (`spiffeID`, optional `sni`) |
| Identity source | legacy `Mesh.spec.mtls` (builtin/provided CA); SPIFFE derived from `kuma.io/service` | **`MeshIdentity` + `MeshTrust`** (required) |
| Verbs | `action: Allow` / `Deny` / `AllowWithShadowDeny` per source | `allow[]` / `deny[]` / `allowWithShadowDeny[]` lists of matchers |
| Evaluation | ordered — later `from[]` entries override earlier (last match wins) | `deny` > `allow`/`allowWithShadowDeny` > default |
| Default posture (no policy) | permissive (Kuma ships a default allow-all policy) | **default-deny** |
| Prerequisite | Mutual TLS enabled | `MeshIdentity` enabled |
| Status / version | stable/GA | experimental; matchers since 2.12, `from[]` deprecated in 2.14 |

**Why it's not auto-convertible:** the `rules[]` API matches on SPIFFE identity strings
(`spiffe://<trust-domain>/ns/<ns>/sa/<sa>`), whereas `from[]` uses tag selectors like
`kuma.io/service: orders`. The trust domain (zone/runtime-derived) and the per-workload
identity path are **not present in the policy manifest**, and the posture flips from
permissive to default-deny — so translating `from[]` → `rules[]` is a guided operator task,
not a field mapping. See the [`meshtrafficpermission_experimental`](https://kuma.io/docs/latest/policies/meshtrafficpermission_experimental/)
docs and [`MeshIdentity`](https://kuma.io/docs/latest/policies/meshidentity/) / [`MeshTrust`](https://kuma.io/docs/latest/policies/meshtrust/).

> **Note — MTP's `rules[]` is not the generic Rules API.** For most policies
> (`MeshTimeout`, `MeshCircuitBreaker`, `MeshAccessLog`, …) `rules[]` has the shape
> `rules: [{ matches: [...], default: {...} }]`, where `matches[]` carries `targetRef`/tag-style
> selectors. **MeshTrafficPermission's `rules[]` is a different, special shape** —
> `rules: [{ default: { allow, deny, allowWithShadowDeny } } ]` with **SPIFFE/SNI** matchers,
> no `matches[]` and no `targetRef`. So "`rules[]` with targetRefs" is true for those other
> policies but **not** for MTP; MTP's `rules[]` has been SPIFFE-only in every released CRD
> (2.12 → 2.14; `sni` added in 2.14). The stable, targetRef-based form of MTP is the `from[]`
> field, not a `rules[]` variant.

---

*An expanded version of this comparison, written for the Kuma website, is drafted at [`contrib/meshtrafficpermission-stable-vs-experimental.md`](contrib/meshtrafficpermission-stable-vs-experimental.md).*

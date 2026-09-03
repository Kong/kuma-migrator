# Choosing a target version (`--to-latest`)

Why the 2.x and 3.0 lines need different output, and which checks change behaviour between them.

[← Back to the README](../README.md)

---

`plan` and `migrate` take `--to-latest v2|v3`, defaulting to `v2`.

| Value | Target | What it does |
|---|---|---|
| `v2` (default) | latest 2.x (2.14.x) | Keeps the output applicable to a 2.x control plane. Fields removed in 3.0 but still accepted in 2.14 are reported as forward-looking advisories, never rewritten. |
| `v3` | 3.0 | Rewrites what it can do safely for 3.0 and flags the rest as blocking work. |

The two lines need different output, so this is an explicit choice rather than a default:
3.0 removes fields that 2.14 still requires, and some 3.0 replacements
(`MeshOpenTelemetryBackend`, `SecureDataSource`) do not exist before 2.14. Applying v3 output
to a 2.13 control plane can fail silently.

A few checks are deliberately target-sensitive:

- **`Dataplane` inbound tags** are only flagged under `v3`. They are mandatory in 2.x, so a v2
  advisory would fire on every Dataplane and be unactionable. Under `v3` this matters a lot:
  3.0 drops them *silently* rather than rejecting them, so the manifest applies cleanly and
  anything selecting on those tags quietly stops matching.
- **`MeshOPA` `targetRef.name`** is rewritten to `labels["kuma.io/display-name"]` under `v3`,
  which preserves the policy's scope. Simply dropping the field — what an unaided 3.0 upgrade
  does — widens the policy to every service matching its `kind`.
- **Inline `openTelemetry.endpoint`** is reported under `v2` with an explicit warning *not* to
  migrate it until every control plane and data plane is on 2.14, because
  `MeshOpenTelemetryBackend` does not exist before then and the control plane silently skips
  the OTel route against an older data plane.
- **`Dataplane` `networking.gateway.type: BUILTIN`** is only flagged under `v3`. `DELEGATED`
  and `BUILTIN` are both valid gateway types in 2.x; on 3.0 the `BUILTIN` ordinal is `reserved`
  in the proto, so it's rejected at parse time rather than merely at admission.
- **`MeshOPA` `targetRef.kind: MeshService`** is auto-converted to `kind: Dataplane` under `v3`
  (only `Mesh`/`Dataplane` remain valid targetRef kinds for MeshOPA in 3.0). This runs *before*
  the `targetRef.name` rewrite above, so that rewrite lands under the label key matching the
  final kind (`labels["app"]` for Dataplane) rather than MeshService's `kuma.io/display-name`.
  Under `v2` this is a forward-looking advisory only — `MeshService` is still valid today.
- **`MeshOPA` `agentConfig`/`appendPolicies[].rego`** legacy flat `DataSource`
  (`secret`/`inline`/`inlineString`) is auto-converted to the discriminated `SecureDataSource`
  shape under `v3` — a `MeshOPA` still in the old shape is rejected at write time on 3.0.
  Auto-converted (unlike the `MeshExternalService` case below) because rego source and OPA
  agent config aren't credential material. Advisory-only under `v2`.
- **`MeshLoadBalancingStrategy` `crossZone`** is only flagged under `v3`: a 3.0-dev validator
  restriction (not yet in the 2.14 line) rejects `localityAwareness.crossZone` on any `to[]`
  entry that doesn't target a `MeshMultiZoneService`.
- **`MeshHTTPRoute`/`HTTPRoute` catch-all rules** are only checked under `v3`: on 3.0, a route
  with no catch-all rule blocks (rather than falls through on) any request that doesn't match
  one of its listed rules — easy to hit by accident when a route exists only to anchor another
  policy via a narrow match.
- **`MeshExternalService` `spec.tls.verification.*` `DataSource`** and **Kong Mesh
  `MeshGlobalRateLimit`** warn under *both* targets, but the wording changes: `v2` frames the
  3.0 removal as forward-looking, `v3` states it as already in effect. Neither is
  auto-converted under either target — the TLS `DataSource` case holds credential material
  (decoding `inline` means re-emitting it in the clear, an operator decision), and
  `MeshGlobalRateLimit` has no in-mesh replacement to convert to.

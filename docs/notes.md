# Notes and caveats

Behaviour worth knowing before you rely on the output.

[← Back to the README](../README.md)

---

- **Policy config is preserved as-is.** The tool only rewrites structural targeting fields.
  Fields inside `conf`/`default` are not modified unless auto-fixing a known deprecated field.
- **`TrafficRoute` is skipped with an error** — it requires manual migration to
  `MeshHTTPRoute` or `MeshTCPRoute` depending on the protocol.
- **Multiple sources** in a legacy policy are split into one output policy per source
  (named `<original>-0`, `<original>-1`, …) because `spec.targetRef` accepts a single reference.
- **Gateway API hostname `*`** — a bare `*` hostname on a `MeshGateway` listener is invalid
  in Gateway API. The migrated `Gateway` listener omits the hostname field (meaning "accept
  any hostname"), and a warning is emitted.
- **Gateway API backendRef ports** — `MeshService` names encoded as `kuma.io/service` tags
  (e.g. `backend_demo_svc_3001`) are parsed to extract `name`, `namespace`, and `port` for
  the Gateway API `backendRef`. The port is written as an integer. Missing or invalid ports
  trigger a warning. A backend in another namespace than the route gets a generated
  `ReferenceGrant`; when the route has no namespace, a warning is emitted instead.
- **Kong Mesh upgrade constraint** — Kong Mesh supports upgrading at most **two minor
  versions** at a time. Plan your upgrade path accordingly (e.g. 2.8 → 2.10 → 2.12 → 2.14).
  Latest as of 2026-10-30: Kuma 3.0.1 / Kong Mesh 3.0.1, with 2.14.7 and 2.13.12 (LTS) patch
  releases. Use `--to-latest v3` for the 3.0 line.
- **Universal vs Kubernetes format** — Kuma resources exist in two YAML shapes. Kubernetes
  format uses `apiVersion`, `kind`, and `metadata.name`; Universal format uses `type` and a
  top-level `name`/`mesh` field. Both are fully supported in extract and migrate. When
  service names are Kubernetes-encoded (`backend_demo_svc_3001`), the tool parses them to
  extract name, namespace, and port; Universal free-form names are used as-is.
- **Rules API: `spec.targetRef` is optional** — the Rules scenario (`from[]` → `rules[]`)
  is triggered whenever the policy kind is in the affected set and `from[]` is present,
  regardless of whether `spec.targetRef` is set at the top level.
- **Universal Dataplane deprecations** — on Universal CPs, `Dataplane` resources are
  hand-authored and included in extraction. The tool warns about:
  - `transparentProxying.redirectPortInboundV6` — removed in Kuma 2.9
  - `transparentProxying.reachableServices` — service names must be updated to MeshService
    display names when `spec.meshServices.mode: Exclusive` is enabled (Kuma 2.10+)
  - under `--to-latest v3`, everything 3.0 removes from `Dataplane` (`networking.gateway`,
    inbound and outbound `tags`, `reachableServices`, `advertisedAddress`, `probes`, …) — see
    [Choosing a target version](target-version.md)
- **Skip list is environment-aware** — on Kubernetes, `Dataplane`, `ZoneIngress`,
  `ZoneEgress`, and `Workload` are skipped (CP-managed, never hand-authored). On Universal
  these are extracted and scanned. `migrate` picks the same list per context from the
  `environment` recorded in `.kuma-migrator.json`. The user-configured `skip` list always takes
  precedence.
- **TLS skip verify** — use `-k` / `--tls-skip-verify` (or `adminServer.tlsSkipVerify: true`
  in `~/.config/kuma-migrator.yaml`) for control planes with self-signed certificates.

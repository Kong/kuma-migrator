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
  the Gateway API `backendRef`. Missing ports trigger a warning.
- **Kong Mesh upgrade constraint** — Kong Mesh supports upgrading at most **two minor
  versions** at a time. Plan your upgrade path accordingly (e.g. 2.8 → 2.10 → 2.12 → 2.14).
  Latest as of mid-2026: Kuma 2.14 / Kong Mesh 2.14 (2.13.x is the Kong Mesh LTS line).
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
- **Skip list is environment-aware** — on Kubernetes, `Dataplane`, `ZoneIngress`,
  `ZoneEgress`, and `Workload` are skipped (CP-managed, never hand-authored). On Universal
  these are extracted and scanned. The user-configured `skip` list always takes precedence.
- **TLS skip verify** — use `-k` / `--tls-skip-verify` (or `adminServer.tlsSkipVerify: true`
  in `~/.config/kuma-migrator.yaml`) for control planes with self-signed certificates.

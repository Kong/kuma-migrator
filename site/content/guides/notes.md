---
title: Notes and caveats
description: Behaviour worth knowing before you rely on the output.
order: 120
---

## What is rewritten

- **Targeting and structure change; configuration values do not.** Legacy `conf` bodies are
  converted field by field to the successor's `default` schema, and every input field that
  has no equivalent is reported. In policies that are already `Mesh*` kinds, fields inside
  `default` are only changed to fix a known deprecated field. See
  [Legacy policies](../topics/legacy-policies.md).
- **Unrelated fields are kept.** Transforms work on the parsed document and keep top-level
  fields they do not own, including Universal `type`, `name`, `mesh` and `labels`.
- **`TrafficRoute` and `VirtualOutbound` are not converted** on either target.
  `TrafficRoute` maps to [MeshHTTPRoute](policy:MeshHTTPRoute) or
  [MeshTCPRoute](policy:MeshTCPRoute) depending on the protocol, which the manifest does not
  say. `VirtualOutbound` has no single successor. Both are reported as errors.
- **Several sources become several policies.** `spec.targetRef` takes a single reference, so
  a legacy policy with several specific sources is split into `<name>-0`, `<name>-1`, ….
- **`from[]` and `to[]` in one policy become two.** Kuma 2.10+ does not accept `rules[]` and
  `to[]` together, so the `to[]` half is written as `<name>-outbound`. Apply both.
- **`rules[]` conversion ignores whether `spec.targetRef` is set.** Any affected kind with
  `from[]` is converted.

## Service names

- `kuma.io/service` values in the Kubernetes encoding (`backend_demo_svc_3001`) are decoded
  into name, namespace and port. Universal service names are free-form and used as they are.
- A top-level `MeshSubset` with a service tag becomes `kind: Dataplane`. Where the reference
  needs labels, `app: <service>` is used as a best guess for the Pod label and a warning is
  printed. Check it against your Pods.
- A partially migrated policy that already says `kind: MeshService` but still uses the
  Kuma-generated name (`echo_demo_svc_8000`) is decoded the same way.

## Gateway API output

- A bare `*` hostname on a `MeshGateway` listener is not valid Gateway API. The listener is
  written without a hostname, which accepts any hostname, and a warning is printed.
- Backend references decode the `kuma.io/service` value into Service name, namespace and
  port. A missing or invalid port is reported. A backend in another namespace than the route
  gets a generated `ReferenceGrant`; when the route has no namespace, a warning is printed
  instead.
- `gatewayClassName` is resolved from the `MeshGatewayInstance` that shares the gateway's
  `kuma.io/service` tag. When none is found, it is set to `REPLACE-WITH-YOUR-GATEWAYCLASS`
  and a warning is printed. See [Gateways](../topics/gateways.md).

## Environments and formats

- **Both YAML shapes are read.** Kubernetes format (`apiVersion`, `kind`, `metadata`) and
  Universal format (`type`, top-level `name`/`mesh`) are supported by `extract` and
  `migrate`.
- **The output keeps the input format.** Universal input gives Universal output (`type`,
  `name`, `mesh`, top-level `labels`, body under `spec`), including every generated companion
  (`MeshCircuitBreaker` from a `HealthCheck`, `MeshOpenTelemetryBackend`, `MeshTLS`, ...).
  Kubernetes input gives Kubernetes resources. Gateway API output (`Gateway`, `HTTPRoute`, ...)
  is always Kubernetes: it exists only there.
- **Companion policies go in the system namespace.** On Kubernetes, policies generated from a
  `Mesh` (`MeshMetric`, `MeshPassthrough`, `MeshTLS`, `MeshOpenTelemetryBackend`) are placed in
  the control-plane namespace: `systemNamespace` from the config file, else the namespace
  inferred from the input, else `kuma-system`. See [Meshes](../topics/meshes.md#system-namespace).
- **The skip list depends on the environment.** On Kubernetes, `Dataplane`, `ZoneIngress`,
  `ZoneEgress` and `Workload` are skipped: the control plane generates them. On Universal the
  first three are hand-written and are scanned. `migrate` uses the `environment` that
  `extract` recorded in `.kuma-migrator.json`. A `skip` list in the configuration file
  replaces the default for its environment.
- **Universal `Dataplane` checks.** The scan reports
  `transparentProxying.redirectPortInboundV6` (removed in 2.9) and
  `transparentProxying.reachableServices` (names must be `MeshService` display names once the
  mesh runs `meshServices.mode: Exclusive`). Under `--to-latest v3` it also handles everything
  3.0 removes from `Dataplane`. See [Dataplanes](../topics/dataplanes.md).

## Operations

- **Upgrade in steps of at most two minor versions**, for Kuma and Kong Mesh alike, for
  example 2.10 → 2.12 → 2.14 → 3.0. As of 2026-10-30 the latest releases are Kuma 3.0.1 and
  Kong Mesh 3.0.1, with 2.14.7 and 2.13.12 (LTS) patch releases. See
  [Upgrade plans](upgrade-plans.md).
- **Self-signed control plane certificates.** Use `-k` / `--tls-skip-verify`, or
  `adminServer.tlsSkipVerify: true` in `~/.config/kuma-migrator.yaml`.
- **Nothing is applied for you.** `migrate` writes files; applying them is a manual step that
  follows the report's Apply Checklist. See [Apply](apply.md).

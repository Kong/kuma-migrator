---
title: Dataplanes
description: Universal Dataplane fields and Kubernetes Pod annotations that change or disappear between 2.x and 3.0, and what kuma-migrator rewrites.
order: 110
---

On Kubernetes the control plane generates every [Dataplane](policy:Dataplane) from the Pod and
its `Service`, so the manifests to migrate are the Pod annotations. On Universal the `Dataplane`
is hand-written and is the manifest. Kuma 3.0 removes most of the 2.x tag-based `Dataplane`
model, and many removals are **silent**: the field is dropped on read, the resource still applies,
and traffic changes.

## What changed upstream

| Field or annotation | 3.0 change | Silent? |
|---|---|---|
| `networking.inbound[].tags` | Removed. Use `Dataplane` `labels` ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#inbound-tags-removed-from-dataplane)) | Yes: anything selecting on the tags stops matching |
| `kuma.io/protocol` inbound tag | No longer sets the protocol; set `inbound[].protocol` ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#kumaioprotocol-inbound-tag-no-longer-sets-the-protocol)) | Yes: the inbound becomes plain TCP and loses L7 policies |
| `networking.outbound[].tags` | Removed; outbounds use `backendRef` to a `MeshService`, `MeshExternalService` or `MeshMultiZoneService` ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#dataplane-outbounds-must-use-backendref)) | Stored ones lose their listener; new writes rejected |
| `transparentProxying.reachableServices`, `kuma.io/transparent-proxying-reachable-services` | Removed; use `reachableBackends` ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#reachableservices--kumaiotransparent-proxying-reachable-services-removed)) | — |
| `reachableBackends.refs[]` `name`/`namespace` | Labels only ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#reachablebackends-refs-select-backends-by-labels-only)) | Yes: refs resolve to nothing, the proxy gets no outbounds |
| No `reachableBackends` | A transparent-proxy data plane gets **no outbounds**, unless `defaults.restrictOutbound=false` ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#data-plane-proxies-without-reachablebackends-get-no-outbounds)) | Yes |
| `networking.advertisedAddress` | Removed; other proxies use `networking.address` ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#advertisedaddress-removed-from-dataplane-networking)) | Yes |
| `transparentProxying.redirectPortInbound` / `redirectPortOutbound` | Deprecated; read from `kuma-dp --transparent-proxy` instead. Removed in 3.1 ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#redirect-ports-and-ip-family-mode-removed-from-dataplane)) | — |
| `transparentProxying.ipFamilyMode` | Removed | Yes |
| `transparentProxying.directAccessServices`, `kuma.io/direct-access-services` | Only `*` is honoured; named services are ignored ([`direct_access_proxy_generator.go`](https://github.com/kumahq/kuma/blob/master/pkg/xds/generator/direct_access_proxy_generator.go)) | Yes: the proxy loses direct access to those services |
| `inbound[].protocol: kafka` | Rejected: Kafka is no longer a protocol ([kumahq/kuma#17831](https://github.com/kumahq/kuma/pull/17831)) | — |
| `probes`, `kuma.io/virtual-probes`, `kuma.io/virtual-probes-port` | Virtual probes removed; Application Probe Proxy only ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#virtual-probes-removed)) | Yes on Universal |
| `networking.gateway` (any type), `kuma.io/gateway` | Removed. See [Gateways](gateways.md) ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#kumaiogateway-is-removed)) | Yes |
| `kuma.io/ingress`, `kuma.io/egress` Pod annotations | Removed ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#kumaioingress-and-kumaioegress-pod-annotations-removed)) | — |
| `kuma.io/mesh` annotation | No longer honoured; use the label ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#kumaiomesh-annotation-no-longer-honored-on-kubernetes)) | Yes: the resource lands in the `default` mesh |
| `kuma.io/transparent-proxying-ebpf*` | eBPF transparent proxy removed ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#ebpf-transparent-proxy-removed)) | — |
| `kuma.io/` and `k8s.kuma.io/` labels | Reserved; an unknown one on a Universal `Dataplane` fails registration ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#reserved-label-prefixes)) | — |
| Sidecar probes on admin port `9901` | Kubernetes probes on the sidecar use the readiness port ([UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#kubernetes-probes-on-the-sidecar-always-use-the-readiness-port)) | — |

Earlier 2.x deprecations still worth checking:

| Annotation or field | Status |
|---|---|
| `transparentProxying.redirectPortInboundV6` | Removed in 2.9 |
| `kuma.io/*` annotation values `yes`/`no` | Use `true`/`false` (2.9) |
| `kuma.io/builtindns`, `kuma.io/builtindnsport` | Not supported and ignored; use `kuma.io/builtin-dns`, `kuma.io/builtin-dns-port` |
| `kuma.io/sidecar-injection` as an annotation | No effect; it must be a label |
| `prometheus.metrics.kuma.io/port`, `/path` | Deprecated; use [MeshMetric](policy:MeshMetric). The `aggregate-<name>-*` annotations are not deprecated |

## What kuma-migrator does

`Dataplane` is skipped on Kubernetes extractions (the control plane owns it) and extracted on
Universal. Pod annotations are scanned wherever they appear in the input: `metadata.annotations`,
pod templates (`Deployment`, `StatefulSet`, `DaemonSet`, `ReplicaSet`, `Job`) and `CronJob` job
templates. `extract` does not export workloads; add their manifests to the input directory to have
them scanned.

| Input | `--to-latest v2` | `--to-latest v3` |
|---|---|---|
| `inbound[].tags` | Nothing (inbound tags are required on 2.x) | Non-`kuma.io/` tags moved to `labels`; `kuma.io/service`/`kuma.io/protocol` dropped (reserved prefix); warned |
| `kuma.io/protocol` inbound tag | — | Copied to `inbound[].protocol` |
| `inbound[].protocol: kafka` (field or `kuma.io/protocol` tag) | Warning | Set to `tcp`; only the Kafka stats are lost |
| Named `directAccessServices` entries, on the `Dataplane` or in the `kuma.io/direct-access-services` annotation | Warning | With `*` in the list: reduced to `*` (the names were redundant). Without `*`: warning, not rewritten. Use `*` or drop direct access |
| `outbound[].tags` | — | Converted to `backendRef` when the `kuma.io/service` value decodes to name, namespace and port. Otherwise 3.0 rejects the `Dataplane` (`networking.outbound[0].backendRef: must be defined`), so it is written to `_manual/` with the `backendRef` to fill in |
| `reachableServices` | Warning | Converted to `reachableBackends` refs with labels where the value decodes; warned otherwise |
| `reachableBackends` refs with `name`/`namespace`, on the `Dataplane` or in the `kuma.io/reachable-backends` annotation | Warning | Rewritten to labels (v3 only: on 2.14 a label selector also matches same-named services from other zones) |
| Transparent proxy without `reachableBackends` | — | Warning: no outbounds on 3.0 |
| `advertisedAddress`, redirect ports, `ipFamilyMode`, `probes` | — | Warning |
| `networking.gateway` | — | Block removed; non-`kuma.io/` gateway tags moved to labels |
| `redirectPortInboundV6` | Warning | Warning |
| Removed Pod annotations (`kuma.io/ingress`, `kuma.io/egress`, `kuma.io/mesh`, `kuma.io/transparent-proxying-reachable-services`, `kuma.io/transparent-proxying-ebpf*`, `kuma.io/virtual-probes*`) | Deprecated ones warned | All warned |
| Deprecated Pod annotations (table above) | Warning | Warning |
| `ContainerPatch` | Copied unchanged | Copied; warning for probes on admin port `9901` (use `9902`) |

Code: [`v3_dataplane.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/v3_dataplane.go),
[`v3_scan.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/v3_scan.go),
[`deprecation.go`](https://github.com/Kong/kuma-migrator/blob/main/pkg/migrator/deprecation.go).

## What it cannot do

- **Find a MeshService port.** A Universal outbound `kuma.io/service: backend` does not say which
  `MeshService` port it maps to. The warning gives the `backendRef` to fill in.
- **Choose `reachableBackends`.** Which services a workload calls is not in its `Dataplane`. On
  3.0 a missing list means no outbounds.
- **Keep `kuma.io/` tags.** `kuma.io/service` and other reserved-prefix tags cannot become
  labels. Policies or `MeshService` selectors that matched on them must select on another label or
  on the `MeshService`.
- **Restart workloads.** Pods injected before 3.0 keep stale annotations and sidecar
  configuration until redeployed.

## Manual steps

1. Universal: on 2.14, set `inbound[].protocol` on every inbound and move non-reserved inbound
   tags to `labels`.
2. Universal: replace outbound `tags` with `backendRef`, and list `reachableBackends` refs
   by labels for every transparent-proxy data plane.
3. Make every Universal proxy reachable on `networking.address`.
4. Start `kuma-dp` with `--transparent-proxy` (or `--transparent-proxy-config`) and drop the
   redirect port fields before 3.1.
5. Kubernetes: replace removed and deprecated Pod annotations; switch `kuma.io/mesh` and
   `kuma.io/sidecar-injection` to labels.
6. Upgrade the control plane and all data planes together (CoreDNS is removed from the data
   plane, [UPGRADE](https://github.com/kumahq/kuma/blob/master/UPGRADE.md#coredns-removed-from-the-data-plane)),
   then re-inject every pod.

## Example

Input — a Universal `Dataplane`:

```yaml
type: Dataplane
mesh: default
name: web-01
networking:
  address: 10.0.0.12
  advertisedAddress: 192.168.1.12
  inbound:
    - port: 8080
      servicePort: 8081
      tags:
        kuma.io/service: web
        kuma.io/protocol: http
        version: v1
  outbound:
    - port: 33033
      tags:
        kuma.io/service: backend
  transparentProxying:
    redirectPortInbound: 15006
    redirectPortOutbound: 15001
    reachableServices:
      - backend
```

With `--to-latest v2` the document is copied unchanged, with one warning about
`reachableServices`.

With `--to-latest v3`:

```yaml
# other/Dataplane-web-01.yaml
labels:
  version: v1
mesh: default
name: web-01
networking:
  address: 10.0.0.12
  advertisedAddress: 192.168.1.12
  inbound:
  - port: 8080
    protocol: http
    servicePort: 8081
  outbound:
  - port: 33033
    tags:
      kuma.io/service: backend
  transparentProxying:
    reachableServices:
    - backend
    redirectPortInbound: 15006
    redirectPortOutbound: 15001
type: Dataplane
```

The inbound is converted; the outbound and `reachableServices` are not, because `backend` is a
Universal name with no port. The outbound alone makes 3.0 reject the `Dataplane`, so it is
written to `_manual/` instead of the apply tree. Warnings (shortened):

```text
⚠  3.0 no longer reads the inbound protocol from the kuma.io/protocol tag … Auto-set
   inbound[].protocol from the tag: inbound port 8080=http.
⚠  networking.inbound[].tags is removed in 3.0 and dropped silently on apply … Auto-moved to
   Dataplane labels: version=v1. Dropped, because kuma.io/ and k8s.kuma.io/ are reserved label
   prefixes: kuma.io/protocol=http, kuma.io/service=web. …
⚠  networking.outbound[].tags is removed in 3.0 … Not converted, because the MeshService port is
   not in the manifest: outbound[0] (port 33033): replace tags with
   `backendRef: {kind: MeshService, name: backend, port: <MeshService port>}`.
⚠  transparentProxying.reachableServices is removed in 3.0 … these entries could not be
   converted: [backend]. …
⚠  networking.advertisedAddress (192.168.1.12) is removed in 3.0 and dropped silently. …
⚠  transparentProxying.redirectPortInbound / redirectPortOutbound still works on 3.0 but is
   removed in 3.1. …
⚠  transparent proxying without reachableBackends. On 3.0 such a proxy gets no generated
   outbounds and cannot reach any service …
```

## Related

- [MeshService](meshservice.md) — `reachableBackends` and display names
- [Gateways, Gateway API and GAMMA](gateways.md) — `networking.gateway` and delegated gateways
- [Multizone](multizone.md) — zone proxies as `Dataplane`s

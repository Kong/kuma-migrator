package migrator

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestFixMeshService_DataplaneTags(t *testing.T) {
	in := `apiVersion: kuma.io/v1alpha1
kind: MeshService
metadata:
  name: redis
spec:
  selector:
    dataplaneTags:
      app: redis
  ports:
    - port: 6379
      appProtocol: tcp
`
	obj, out, ws := scanObj(t, in, TargetV3)
	if digPath(t, obj, "spec", "selector", "dataplaneTags") != nil ||
		digPath(t, obj, "spec", "selector", "dataplaneLabels", "matchLabels", "app") != "redis" {
		t.Errorf("expected dataplaneTags → dataplaneLabels.matchLabels:\n%s", out)
	}
	if !strings.Contains(joined(ws), "must exist as labels on the Dataplane") {
		t.Errorf("expected label-existence note, got %v", ws)
	}

	v2out, v2 := ScanForDeprecations([]byte(in), TargetV2)
	if string(v2out) != in {
		t.Errorf("v2 must not rewrite the selector, got:\n%s", v2out)
	}
	if !strings.Contains(joined(v2), "--to-latest v3") {
		t.Errorf("expected v2 advisory, got %v", v2)
	}
}

func TestFixMeshService_DataplaneTagsReservedNotConverted(t *testing.T) {
	in := `type: MeshService
mesh: default
name: redis
spec:
  selector:
    dataplaneTags:
      kuma.io/service: redis
`
	out, ws := ScanForDeprecations([]byte(in), TargetV3)
	if string(out) != in {
		t.Errorf("selector with a reserved tag must be left alone, got:\n%s", out)
	}
	if !strings.Contains(joined(ws), "would widen the selector") {
		t.Errorf("expected not-converted warning, got %v", ws)
	}
}

func TestFixMeshService_ServiceTagIdentities(t *testing.T) {
	in := `type: MeshService
mesh: default
name: redis
spec:
  identities:
    - type: ServiceTag
      value: redis
    - type: SpiffeID
      value: spiffe://default/redis
`
	obj, out, ws := scanObj(t, in, TargetV3)
	ids, _ := digPath(t, obj, "spec", "identities").([]interface{})
	if len(ids) != 1 || digPath(t, ids[0], "type") != "SpiffeID" {
		t.Errorf("ServiceTag identity must be dropped:\n%s", out)
	}
	if !strings.Contains(joined(ws), "ServiceTag is rejected in 3.0") {
		t.Errorf("expected identities warning, got %v", ws)
	}
	if v2out, _ := ScanForDeprecations([]byte(in), TargetV2); string(v2out) != in {
		t.Errorf("v2 must keep identities, got:\n%s", v2out)
	}
}

func TestWarnMeshPassthroughV3_DomainWithoutPort(t *testing.T) {
	in := `type: MeshPassthrough
mesh: default
name: mp
spec:
  targetRef:
    kind: Mesh
  default:
    passthroughMode: Matched
    appendMatch:
      - type: Domain
        value: api.example.com
        protocol: tls
      - type: Domain
        value: "*.example.com"
        protocol: tls
      - type: Domain
        value: ok.example.com
        protocol: tls
        port: 443
`
	_, ws := ScanForDeprecations([]byte(in), TargetV3)
	j := joined(ws)
	if !strings.Contains(j, `Domain "api.example.com"`) {
		t.Errorf("expected missing-port warning, got %v", ws)
	}
	if strings.Contains(j, `Domain "*.example.com"`) || strings.Contains(j, `Domain "ok.example.com"`) {
		t.Errorf("wildcard and ported domains must not be flagged: %v", ws)
	}
	_, v2 := ScanForDeprecations([]byte(in), TargetV2)
	if strings.Contains(joined(v2), "has no port. 3.0") {
		t.Errorf("missing-port check is v3 only: %v", v2)
	}
}

func TestWarnMeshPassthroughV3_FilterChainConflicts(t *testing.T) {
	in := `type: MeshPassthrough
mesh: default
name: mp
spec:
  targetRef:
    kind: Mesh
  default:
    appendMatch:
      - type: Domain
        value: a.example.com
        protocol: http
        port: 8080
      - type: Domain
        value: b.example.com
        protocol: grpc
        port: 8080
      - type: IP
        value: 10.0.0.1
        protocol: tcp
      - type: CIDR
        value: 10.0.0.1/32
        protocol: tcp
      - type: CIDR
        value: 10.1.0.0/24
        protocol: tcp
        port: 5432
      - type: CIDR
        value: 10.1.0.5/24
        protocol: mysql
        port: 5432
      - type: Domain
        value: c.example.com
        protocol: tls
        port: 8080
`
	for _, target := range []TargetVersion{TargetV2, TargetV3} {
		_, ws := ScanForDeprecations([]byte(in), target)
		j := joined(ws)
		for _, want := range []string{
			"protocols http and grpc produce the same Envoy filter chain for domains on port 8080",
			`matches "10.0.0.1" and "10.0.0.1/32" produce the same Envoy filter chain for 10.0.0.1/32 on all ports`,
			"protocols tcp and mysql produce the same Envoy filter chain for 10.1.0.0/24 on port 5432",
		} {
			if !strings.Contains(j, want) {
				t.Errorf("%v: expected %q, got %v", target, want, ws)
			}
		}
		if strings.Contains(j, "c.example.com") {
			t.Errorf("%v: tls next to http on one port is a distinct chain: %v", target, ws)
		}
	}
}

func TestWarnMeshPassthroughV3_NoConflictPortVsPortless(t *testing.T) {
	in := `type: MeshPassthrough
mesh: default
name: mp
spec:
  default:
    appendMatch:
      - type: Domain
        value: "*.otel.io"
        protocol: grpc
        port: 4317
      - type: IP
        value: 10.0.0.9
        protocol: http
`
	_, ws := ScanForDeprecations([]byte(in), TargetV3)
	if strings.Contains(joined(ws), "same Envoy filter chain") {
		t.Errorf("port-specific next to portless is not a conflict: %v", ws)
	}
}

func TestWarnZoneProxyRemoved(t *testing.T) {
	in := `type: ZoneIngress
name: zi-1
networking:
  address: 10.0.0.5
  port: 10001
  advertisedAddress: 203.0.113.10
  advertisedPort: 30001
`
	_, ws := ScanForDeprecations([]byte(in), TargetV3)
	j := joined(ws)
	for _, want := range []string{"BLOCKING", "ZoneIngress resource is removed in 3.0", "type: ZoneIngress",
		"MeshZoneAddress", "address: 203.0.113.10, port: 30001", "kubectl delete crd"} {
		if !strings.Contains(j, want) {
			t.Errorf("expected %q, got %v", want, ws)
		}
	}
	// v2: forward-looking advisory, not blocking; names the 2.14 replacement.
	fixedV2, v2 := ScanForDeprecations([]byte(in), TargetV2)
	if strings.Contains(joined(v2), "BLOCKING") {
		t.Errorf("v2 must not block on ZoneIngress: %v", v2)
	}
	if len(v2) != 1 {
		t.Fatalf("want one v2 advisory, got %v", v2)
	}
	for _, want := range []string{"ZoneIngress resource is removed in 3.0", "2.14 already supports",
		"meshes[].ingress / meshes[].egress", "networking.listeners[] of type ZoneIngress", "--to-latest v3"} {
		if !strings.Contains(v2[0], want) {
			t.Errorf("v2 advisory: expected %q, got %s", want, v2[0])
		}
	}
	if !sameYAML([]byte(in), fixedV2) {
		t.Errorf("v2 must not rewrite ZoneIngress:\n%s", fixedV2)
	}
	_, ev2 := ScanForDeprecations([]byte(`type: ZoneEgress
name: ze-1
networking: {address: 10.0.0.6, port: 10002}
`), TargetV2)
	if len(ev2) != 1 || !strings.Contains(ev2[0], "ZoneEgress \"ze-1\": the ZoneEgress resource is removed in 3.0") {
		t.Errorf("v2 ZoneEgress advisory: %v", ev2)
	}

	egress := `apiVersion: kuma.io/v1alpha1
kind: ZoneEgress
metadata:
  name: ze-1
spec:
  networking:
    address: 10.0.0.6
    port: 10002
`
	_, ews := ScanForDeprecations([]byte(egress), TargetV3)
	ej := joined(ews)
	if !strings.Contains(ej, "type: ZoneEgress, address: 10.0.0.6, port: 10002") {
		t.Errorf("expected ZoneEgress listener snippet, got %v", ews)
	}
	if strings.Contains(ej, "MeshZoneAddress") {
		t.Errorf("MeshZoneAddress advisory is for ZoneIngress only: %v", ews)
	}
}

func TestWarnContainerPatchAdminProbePort(t *testing.T) {
	in := `apiVersion: kuma.io/v1alpha1
kind: ContainerPatch
metadata:
  name: probes
  namespace: kuma-system
spec:
  sidecarPatch:
    - op: replace
      path: /readinessProbe/httpGet/port
      value: "9901"
    - op: add
      path: /env/-
      value: '{"name":"X","value":"9901"}'
`
	_, ws := ScanForDeprecations([]byte(in), TargetV3)
	j := joined(ws)
	if !strings.Contains(j, "sidecarPatch[0]") || !strings.Contains(j, "9902") {
		t.Errorf("expected probe-port warning on sidecarPatch[0], got %v", ws)
	}
	if strings.Contains(j, "sidecarPatch[1]") {
		t.Errorf("non-probe patch must not be flagged: %v", ws)
	}
	_, v2 := ScanForDeprecations([]byte(in), TargetV2)
	if strings.Contains(joined(v2), "9902") {
		t.Errorf("v2 must not flag 9901 probes: %v", v2)
	}
}

func TestFixWorkloadReachableAnnotations_V3(t *testing.T) {
	in := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: app
spec:
  template:
    metadata:
      annotations:
        kuma.io/reachable-backends: |
          refs:
          - kind: MeshService
            name: redis
            namespace: redis-system
            port: 6379
          - kind: MeshService
            name: local
        kuma.io/transparent-proxying-reachable-services: "backend_demo_svc_8080, legacy"
`
	obj, out, ws := scanObj(t, in, TargetV3)
	anns := digPath(t, obj, "spec", "template", "metadata", "annotations")
	raw, _ := digPath(t, anns, annotationReachableBackends).(string)
	var doc map[string]interface{}
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("annotation must stay valid YAML: %v\n%s", err, out)
	}
	refs, _ := doc["refs"].([]interface{})
	if len(refs) != 3 {
		t.Fatalf("expected 3 refs, got %v", refs)
	}
	if digPath(t, refs[0], "name") != nil || digPath(t, refs[0], "labels", labelK8sNamespace) != "redis-system" {
		t.Errorf("ref 0 must use labels, got %v", refs[0])
	}
	if digPath(t, refs[1], "labels", labelK8sNamespace) != "app" {
		t.Errorf("name-only ref must be pinned to the workload namespace, got %v", refs[1])
	}
	if digPath(t, refs[2], "labels", labelDisplayName) != "backend" || digPath(t, refs[2], "port") != float64(8080) {
		t.Errorf("reachable-services entry must become a ref, got %v", refs[2])
	}
	if got := digPath(t, anns, annotationReachableServices); got != "legacy" {
		t.Errorf("undecodable entry must remain on the old annotation, got %v", got)
	}
	j := joined(ws)
	for _, want := range []string{"spec.template.metadata.annotations", "pinned to the workload's namespace",
		"Auto-converted to kuma.io/reachable-backends", annotationReachableServices + `" (spec.template.metadata.annotations) is removed`} {
		if !strings.Contains(j, want) {
			t.Errorf("expected %q, got %v", want, ws)
		}
	}

	v2out, v2 := ScanForDeprecations([]byte(in), TargetV2)
	if string(v2out) != in {
		t.Errorf("v2 must not rewrite annotations, got:\n%s", v2out)
	}
	if !strings.Contains(joined(v2), "2.14 already accepts the labels form") {
		t.Errorf("expected v2 advisory, got %v", v2)
	}
}

func TestWarnDeprecatedAnnotations_V3AndPodTemplate(t *testing.T) {
	in := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  annotations:
    kuma.io/mesh: default
spec:
  template:
    metadata:
      annotations:
        kuma.io/virtual-probes: enabled
        kuma.io/ingress: enabled
        kuma.io/ingress-public-address: 1.2.3.4
        kuma.io/transparent-proxying-ebpf: enabled
        kuma.io/xds-transport-protocol-variant: DELTA_GRPC
        kuma.io/gateway: enabled
        kuma.io/application-probe-proxy-port: "0"
`
	_, v3 := ScanForDeprecations([]byte(in), TargetV3)
	j := joined(v3)
	for _, want := range []string{
		`"kuma.io/mesh" is no longer honored`,
		`"kuma.io/virtual-probes" (spec.template.metadata.annotations) is removed in 3.0`,
		`"kuma.io/ingress" (spec.template.metadata.annotations) is removed in 3.0`,
		"MeshZoneAddress",
		"eBPF",
		"Delta xDS",
		"traffic.kuma.io/exclude-inbound-ports",
		"no longer falls back to virtual probes",
	} {
		if !strings.Contains(j, want) {
			t.Errorf("expected %q, got %v", want, v3)
		}
	}

	_, v2 := ScanForDeprecations([]byte(in), TargetV2)
	j2 := joined(v2)
	if !strings.Contains(j2, "will be removed in a future release") {
		t.Errorf("v2 virtual-probes wording expected on the pod template, got %v", v2)
	}
	for _, notV2 := range []string{"kuma.io/mesh", "kuma.io/ingress", "ebpf", "xds-transport", "kuma.io/gateway", "falls back"} {
		if strings.Contains(j2, notV2) {
			t.Errorf("%q is v3-only and must not be flagged under v2: %v", notV2, v2)
		}
	}
}

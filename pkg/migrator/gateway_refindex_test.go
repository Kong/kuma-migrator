package migrator

import (
	"os"
	"path/filepath"
	"testing"

	"sigs.k8s.io/yaml"
)

// The Gateway is named after the MeshGateway ("edge"), while the instance and the
// route selector only carry the service tag ("edge-gw"). A route parentRef built
// from the tag names a Gateway that does not exist.
const refIdxMeshGateway = `
apiVersion: kuma.io/v1alpha1
kind: MeshGateway
metadata:
  name: edge
  labels:
    kuma.io/mesh: default
spec:
  selectors:
    - match:
        kuma.io/service: edge-gw
  conf:
    listeners:
      - port: 8080
        protocol: HTTP
        tags:
          port: http-8080
      - port: 8443
        protocol: HTTPS
        hostname: foo.example.com
        tags:
          port: https-8443
`

const refIdxMeshGatewayInstance = `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayInstance
metadata:
  name: edge-gw
  namespace: gw-ns
spec:
  replicas: 2
  tags:
    kuma.io/service: edge-gw
`

const refIdxRoute = `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayRoute
metadata:
  name: edge-route
spec:
  selectors:
    - match:
        kuma.io/service: edge-gw
        port: http-8080
  conf:
    http:
      rules:
        - matches:
            - path:
                match: PREFIX
                value: /
          backends:
            - destination:
                kuma.io/service: backend_demo_svc_3000
`

func refIndexOf(docs ...string) *GatewayRefIndex {
	idx := &GatewayRefIndex{}
	for _, d := range docs {
		idx.add([]byte(d))
	}
	return idx
}

func routeOut(t *testing.T, raw string, opts TransformOptions) (map[string]interface{}, [][]byte, []string) {
	t.Helper()
	docs, warnings, err := TransformMeshGatewayRouteWithOptions([]byte(raw), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var out map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out, docs, warnings
}

func parentRefsOf(out map[string]interface{}) []map[string]interface{} {
	var refs []map[string]interface{}
	for _, r := range out["spec"].(map[string]interface{})["parentRefs"].([]interface{}) {
		refs = append(refs, r.(map[string]interface{}))
	}
	return refs
}

func TestParentRefs_ResolveToConvertedGateway(t *testing.T) {
	opts := TransformOptions{Target: TargetV2, Gateways: refIndexOf(refIdxMeshGateway, refIdxMeshGatewayInstance)}
	out, docs, warnings := routeOut(t, refIdxRoute, opts)

	refs := parentRefsOf(out)
	if len(refs) != 1 {
		t.Fatalf("expected 1 parentRef, got %v", refs)
	}
	if refs[0]["name"] != "edge" {
		t.Errorf("parentRef name = %v, want edge (the MeshGateway name, not the service tag)", refs[0]["name"])
	}
	if refs[0]["sectionName"] != "http-8080" {
		t.Errorf("sectionName = %v, want http-8080", refs[0]["sectionName"])
	}
	// The route is placed in the Gateway's namespace, so parentRef.namespace is redundant.
	meta := out["metadata"].(map[string]interface{})
	if meta["namespace"] != "gw-ns" {
		t.Errorf("route namespace = %v, want gw-ns", meta["namespace"])
	}
	if _, ok := refs[0]["namespace"]; ok {
		t.Errorf("parentRef namespace should be dropped when equal to the route's, got %v", refs[0]["namespace"])
	}
	if hasWarning(warnings, "verify") {
		t.Errorf("resolved parentRef must not warn to verify: %v", warnings)
	}
	// With a known route namespace the cross-namespace backend gets its ReferenceGrant.
	if len(docs) != 2 {
		t.Errorf("expected route + ReferenceGrant, got %d docs", len(docs))
	}
}

func TestParentRefs_TCPRouteResolves(t *testing.T) {
	route := `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayRoute
metadata:
  name: tcp
spec:
  selectors:
    - match:
        kuma.io/service: edge-gw
  conf:
    tcp:
      rules:
        - backends:
            - destination:
                kuma.io/service: redis_gw-ns_svc_6379
`
	opts := TransformOptions{Gateways: refIndexOf(refIdxMeshGateway, refIdxMeshGatewayInstance)}
	out, _, _ := routeOut(t, route, opts)
	refs := parentRefsOf(out)
	if len(refs) != 1 || refs[0]["name"] != "edge" {
		t.Fatalf("parentRefs = %v, want one ref to Gateway edge", refs)
	}
	if _, ok := refs[0]["sectionName"]; ok {
		t.Errorf("no listener tags in the selector → no sectionName, got %v", refs[0]["sectionName"])
	}
}

func TestParentRefs_ListenerTagsPickEmittedListenerNames(t *testing.T) {
	// Two listeners share protocol+port: Gateway API needs unique listener names,
	// and the route's sectionName must name the one actually emitted.
	gw := `
apiVersion: kuma.io/v1alpha1
kind: MeshGateway
metadata:
  name: multi
spec:
  selectors:
    - match:
        kuma.io/service: multi-gw
  conf:
    listeners:
      - port: 80
        protocol: HTTP
        hostname: a.example.com
        tags:
          vhost: a
      - port: 80
        protocol: HTTP
        hostname: b.example.com
        tags:
          vhost: b
`
	route := `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayRoute
metadata:
  name: b-route
spec:
  selectors:
    - match:
        kuma.io/service: multi-gw
        vhost: b
  conf:
    http:
      rules:
        - backends:
            - destination:
                kuma.io/service: b_apps_svc_80
`
	docs, _, err := TransformMeshGateway([]byte(gw), TransformOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var g map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &g); err != nil {
		t.Fatal(err)
	}
	ls := g["spec"].(map[string]interface{})["listeners"].([]interface{})
	n0 := ls[0].(map[string]interface{})["name"]
	n1 := ls[1].(map[string]interface{})["name"]
	if n0 != "http-80" || n1 != "http-80-2" {
		t.Errorf("listener names = %v, %v; want http-80, http-80-2", n0, n1)
	}

	out, _, warnings := routeOut(t, route, TransformOptions{Gateways: refIndexOf(gw)})
	refs := parentRefsOf(out)
	if len(refs) != 1 || refs[0]["sectionName"] != "http-80-2" {
		t.Errorf("parentRefs = %v, want sectionName http-80-2", refs)
	}
	// The MeshGatewayInstance is absent: namespace unknown → omitted and reported.
	if _, ok := refs[0]["namespace"]; ok {
		t.Errorf("unknown Gateway namespace must be omitted, got %v", refs[0]["namespace"])
	}
	if _, ok := out["metadata"].(map[string]interface{})["namespace"]; ok {
		t.Errorf("route namespace must stay unset when the Gateway namespace is unknown")
	}
	if !hasWarning(warnings, `namespace of parent Gateway "multi" is unknown`) {
		t.Errorf("expected an unknown-namespace warning, got %v", warnings)
	}
}

func TestParentRefs_AmbiguousGatewaysEmitEach(t *testing.T) {
	second := `
apiVersion: kuma.io/v1alpha1
kind: MeshGateway
metadata:
  name: edge-b
spec:
  selectors:
    - match:
        kuma.io/service: edge-gw
  conf:
    listeners:
      - port: 8080
        protocol: HTTP
        tags:
          port: http-8080
`
	opts := TransformOptions{Gateways: refIndexOf(refIdxMeshGateway, second, refIdxMeshGatewayInstance)}
	out, _, warnings := routeOut(t, refIdxRoute, opts)
	refs := parentRefsOf(out)
	if len(refs) != 2 || refs[0]["name"] != "edge" || refs[1]["name"] != "edge-b" {
		t.Errorf("parentRefs = %v, want one per MeshGateway (edge, edge-b)", refs)
	}
	if !hasWarning(warnings, "selected by more than one MeshGateway") {
		t.Errorf("expected an ambiguity warning, got %v", warnings)
	}
}

func TestParentRefs_UnresolvedFallsBackAndWarns(t *testing.T) {
	out, _, warnings := routeOut(t, refIdxRoute, TransformOptions{})
	refs := parentRefsOf(out)
	if refs[0]["name"] != "edge-gw" {
		t.Errorf("fallback name = %v, want edge-gw", refs[0]["name"])
	}
	if !hasWarning(warnings, "no MeshGateway in the input set selects") {
		t.Errorf("expected a verify warning on fallback, got %v", warnings)
	}
}

func TestTransformMeshGateway_NamespaceFromInstance(t *testing.T) {
	for _, target := range []TargetVersion{TargetV2, TargetV3} {
		opts := TransformOptions{Target: target, Gateways: refIndexOf(refIdxMeshGatewayInstance)}
		docs, warnings, err := TransformMeshGateway([]byte(refIdxMeshGateway), opts)
		if err != nil {
			t.Fatal(err)
		}
		var g map[string]interface{}
		if err := yaml.Unmarshal(docs[0], &g); err != nil {
			t.Fatal(err)
		}
		if ns := g["metadata"].(map[string]interface{})["namespace"]; ns != "gw-ns" {
			t.Errorf("%s: Gateway namespace = %v, want gw-ns", target, ns)
		}
		if hasWarning(warnings, "metadata.namespace omitted") {
			t.Errorf("%s: namespace was resolvable; should not warn: %v", target, warnings)
		}
	}
}

func TestTransformMeshGateway_NoEmptyNamespace(t *testing.T) {
	docs, warnings, err := TransformMeshGateway([]byte(refIdxMeshGateway), TransformOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var g map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &g); err != nil {
		t.Fatal(err)
	}
	if ns, ok := g["metadata"].(map[string]interface{})["namespace"]; ok {
		t.Errorf("namespace must be omitted when unknown, got %q", ns)
	}
	if !hasWarning(warnings, "metadata.namespace omitted") {
		t.Errorf("expected a warning about the omitted namespace, got %v", warnings)
	}
}

func TestTransformMeshGateway_AmbiguousNamespaceWarns(t *testing.T) {
	other := `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayInstance
metadata:
  name: edge-gw
  namespace: aaa-ns
spec:
  tags:
    kuma.io/service: edge-gw
`
	opts := TransformOptions{Gateways: refIndexOf(refIdxMeshGatewayInstance, other)}
	_, warnings, err := TransformMeshGateway([]byte(refIdxMeshGateway), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(warnings, "more than one namespace") {
		t.Errorf("expected an ambiguous-namespace warning, got %v", warnings)
	}
}

func TestGatewayRefIndex_UniversalMeshGatewayAndDefaultInstanceTag(t *testing.T) {
	// Universal layout: everything at the top level.
	uni := `
type: MeshGateway
name: edge-uni
mesh: default
selectors:
  - match:
      kuma.io/service: edge-uni-gw
conf:
  listeners:
    - port: 80
      protocol: HTTP
`
	// No spec.tags: Kuma defaults the service tag to <name>_<namespace>_svc.
	inst := `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayInstance
metadata:
  name: plain
  namespace: ns1
spec: {}
`
	idx := refIndexOf(uni, inst)
	gws := idx.gatewaysFor("edge-uni-gw")
	if len(gws) != 1 || gws[0].name != "edge-uni" || gws[0].listeners[0].name != "http-80" {
		t.Errorf("universal MeshGateway not indexed: %+v", gws)
	}
	if got := idx.namespaces["plain_ns1_svc"]; len(got) != 1 || got[0] != "ns1" {
		t.Errorf("default instance tag not indexed: %v", idx.namespaces)
	}
	if tag, class := parseGatewayClassEntry([]byte(inst)); tag != "plain_ns1_svc" || class != "plain" {
		t.Errorf("GatewayClassIndex entry = (%q, %q), want (plain_ns1_svc, plain)", tag, class)
	}

	docs, _, err := TransformMeshGateway([]byte(uni), TransformOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var g map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &g); err != nil {
		t.Fatal(err)
	}
	if g["metadata"].(map[string]interface{})["name"] != "edge-uni" {
		t.Errorf("Universal MeshGateway name not carried: %v", g["metadata"])
	}
}

// End to end through the pre-pass: separate files, as extract writes them.
func TestBuildTransformOptions_GatewayRefsWired(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"mg.yaml":  refIdxMeshGateway,
		"mgi.yaml": refIdxMeshGatewayInstance,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	opts := BuildTransformOptions(dir, TargetV2)
	if opts.Gateways.empty() {
		t.Fatal("BuildTransformOptions did not build the Gateway reference index")
	}

	gwDocs, _, _, err := TransformDocumentWithOptions([]byte(refIdxMeshGateway), opts)
	if err != nil {
		t.Fatal(err)
	}
	routeDocs, _, _, err := TransformDocumentWithOptions([]byte(refIdxRoute), opts)
	if err != nil {
		t.Fatal(err)
	}
	var g, r map[string]interface{}
	if err := yaml.Unmarshal(gwDocs[0], &g); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(routeDocs[0], &r); err != nil {
		t.Fatal(err)
	}
	gMeta := g["metadata"].(map[string]interface{})
	rMeta := r["metadata"].(map[string]interface{})
	ref := parentRefsOf(r)[0]
	refNS, ok := ref["namespace"].(string)
	if !ok {
		refNS, _ = rMeta["namespace"].(string)
	}
	if ref["name"] != gMeta["name"] || refNS != gMeta["namespace"] {
		t.Errorf("parentRef %v/%v does not name the emitted Gateway %v/%v", refNS, ref["name"], gMeta["namespace"], gMeta["name"])
	}
	ls := g["spec"].(map[string]interface{})["listeners"].([]interface{})
	if ref["sectionName"] != ls[0].(map[string]interface{})["name"] {
		t.Errorf("sectionName %v is not an emitted listener name", ref["sectionName"])
	}
}

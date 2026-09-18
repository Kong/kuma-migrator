package migrator

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestTransformMeshGateway_Basic(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGateway
metadata:
  name: edge-gateway
  namespace: kuma-demo
  labels:
    kuma.io/mesh: default
spec:
  selectors:
    - match:
        kuma.io/service: edge-gateway_kuma-demo_svc
  conf:
    listeners:
      - port: 8080
        protocol: HTTP
        hostname: example.com
`
	docs, warnings, err := TransformMeshGateway([]byte(input), TransformOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}

	var gw map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &gw); err != nil {
		t.Fatalf("unmarshal Gateway: %v", err)
	}

	if gw["apiVersion"] != "gateway.networking.k8s.io/v1" {
		t.Errorf("expected apiVersion=gateway.networking.k8s.io/v1, got %v", gw["apiVersion"])
	}
	if gw["kind"] != "Gateway" {
		t.Errorf("expected kind=Gateway, got %v", gw["kind"])
	}

	meta := gw["metadata"].(map[string]interface{})
	if meta["name"] != "edge-gateway" {
		t.Errorf("expected name=edge-gateway, got %v", meta["name"])
	}
	if meta["namespace"] != "kuma-demo" {
		t.Errorf("expected namespace=kuma-demo, got %v", meta["namespace"])
	}
	// Default mesh — no annotation needed.
	if ann, ok := meta["annotations"]; ok {
		t.Errorf("unexpected annotations for default mesh: %v", ann)
	}

	spec := gw["spec"].(map[string]interface{})
	// No MeshGatewayInstance was indexed, so the GatewayClass name is unknown.
	// It must NOT fall back to Kuma's controllerName: gatewayClassName names a
	// GatewayClass object, and a Gateway pointing at a class that does not exist
	// is created successfully and then never reconciled.
	if spec["gatewayClassName"] != gatewayClassPlaceholder {
		t.Errorf("expected gatewayClassName=%s, got %v", gatewayClassPlaceholder, spec["gatewayClassName"])
	}
	if !hasWarning(warnings, "could not determine spec.gatewayClassName") {
		t.Errorf("expected an unresolved-class warning, got: %v", warnings)
	}

	listeners := spec["listeners"].([]interface{})
	if len(listeners) != 1 {
		t.Fatalf("expected 1 listener, got %d", len(listeners))
	}
	l := listeners[0].(map[string]interface{})
	if l["name"] != "http-8080" {
		t.Errorf("expected listener name=http-8080, got %v", l["name"])
	}
	if l["port"] != float64(8080) {
		t.Errorf("expected port=8080, got %v", l["port"])
	}
	if l["protocol"] != "HTTP" {
		t.Errorf("expected protocol=HTTP, got %v", l["protocol"])
	}
	if l["hostname"] != "example.com" {
		t.Errorf("expected hostname=example.com, got %v", l["hostname"])
	}

	// selectors warning expected.
	hasSelectorWarn := false
	for _, w := range warnings {
		if strings.Contains(w, "selectors") {
			hasSelectorWarn = true
		}
	}
	if !hasSelectorWarn {
		t.Error("expected warning about selectors")
	}
}

func TestTransformMeshGateway_NonDefaultMesh(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGateway
metadata:
  name: my-gw
  namespace: apps
  labels:
    kuma.io/mesh: prod
spec:
  conf:
    listeners:
      - port: 443
        protocol: HTTPS
        tls:
          mode: TERMINATE
          certificates:
            - secret: my-tls-cert
`
	docs, _, err := TransformMeshGateway([]byte(input), TransformOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var gw map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &gw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	meta := gw["metadata"].(map[string]interface{})
	ann, ok := meta["annotations"].(map[string]interface{})
	if !ok {
		t.Fatal("expected annotations for non-default mesh")
	}
	if ann["kuma.io/mesh"] != "prod" {
		t.Errorf("expected annotation kuma.io/mesh=prod, got %v", ann["kuma.io/mesh"])
	}

	spec := gw["spec"].(map[string]interface{})
	listeners := spec["listeners"].([]interface{})
	l := listeners[0].(map[string]interface{})
	if l["name"] != "https-443" {
		t.Errorf("expected listener name=https-443, got %v", l["name"])
	}

	tls := l["tls"].(map[string]interface{})
	if tls["mode"] != "Terminate" {
		t.Errorf("expected TLS mode=Terminate, got %v", tls["mode"])
	}
	certRefs := tls["certificateRefs"].([]interface{})
	if len(certRefs) != 1 {
		t.Fatalf("expected 1 certRef, got %d", len(certRefs))
	}
	cert := certRefs[0].(map[string]interface{})
	if cert["name"] != "my-tls-cert" {
		t.Errorf("expected cert name=my-tls-cert, got %v", cert["name"])
	}
	if cert["kind"] != "Secret" {
		t.Errorf("expected cert kind=Secret, got %v", cert["kind"])
	}
}

func TestTransformMeshGateway_TLSPassthrough(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGateway
metadata:
  name: tls-gw
  namespace: apps
spec:
  conf:
    listeners:
      - port: 443
        protocol: TLS
        tls:
          mode: PASSTHROUGH
`
	docs, _, err := TransformMeshGateway([]byte(input), TransformOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var gw map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &gw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	spec := gw["spec"].(map[string]interface{})
	listeners := spec["listeners"].([]interface{})
	l := listeners[0].(map[string]interface{})
	tls := l["tls"].(map[string]interface{})
	if tls["mode"] != "Passthrough" {
		t.Errorf("expected TLS mode=Passthrough, got %v", tls["mode"])
	}
}

func TestTransformMeshGateway_CrossMeshWarning(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGateway
metadata:
  name: cross-gw
  namespace: apps
spec:
  conf:
    listeners:
      - port: 8080
        protocol: HTTP
        crossMesh: true
`
	_, warnings, err := TransformMeshGateway([]byte(input), TransformOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "crossMesh") {
			found = true
		}
	}
	if !found {
		t.Error("expected crossMesh warning")
	}
}

func TestTransformMeshGatewayInstance(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayInstance
metadata:
  name: edge-gateway
  namespace: kuma-demo
  labels:
    kuma.io/mesh: default
spec:
  replicas: 3
  serviceType: LoadBalancer
  tags:
    kuma.io/service: edge-gateway_kuma-demo_svc
  resources:
    limits:
      cpu: 1000m
      memory: 1Gi
    requests:
      cpu: 100m
      memory: 128Mi
`
	docs, warnings, err := TransformMeshGatewayInstance([]byte(input), TargetV2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Expect 2 docs: GatewayClass + MeshGatewayConfig.
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs, got %d", len(docs))
	}

	var gc map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &gc); err != nil {
		t.Fatalf("unmarshal GatewayClass: %v", err)
	}
	if gc["kind"] != "GatewayClass" {
		t.Errorf("expected kind=GatewayClass, got %v", gc["kind"])
	}
	if gc["apiVersion"] != "gateway.networking.k8s.io/v1" {
		t.Errorf("expected gatewayAPIVersion, got %v", gc["apiVersion"])
	}

	gcMeta := gc["metadata"].(map[string]interface{})
	if gcMeta["name"] != "edge-gateway" {
		t.Errorf("expected GatewayClass name=edge-gateway, got %v", gcMeta["name"])
	}
	// GatewayClass is cluster-scoped — no namespace.
	if ns, ok := gcMeta["namespace"]; ok && ns != "" {
		t.Errorf("GatewayClass should not have namespace, got %v", ns)
	}

	gcSpec := gc["spec"].(map[string]interface{})
	if gcSpec["controllerName"] != "gateways.kuma.io/controller" {
		t.Errorf("unexpected controllerName: %v", gcSpec["controllerName"])
	}

	var mgc map[string]interface{}
	if err := yaml.Unmarshal(docs[1], &mgc); err != nil {
		t.Fatalf("unmarshal MeshGatewayConfig: %v", err)
	}
	if mgc["kind"] != "MeshGatewayConfig" {
		t.Errorf("expected kind=MeshGatewayConfig, got %v", mgc["kind"])
	}

	mgcMeta := mgc["metadata"].(map[string]interface{})
	if mgcMeta["name"] != "edge-gateway" {
		t.Errorf("expected MeshGatewayConfig name=edge-gateway, got %v", mgcMeta["name"])
	}
	if mgcMeta["namespace"] != "kuma-demo" {
		t.Errorf("expected namespace=kuma-demo, got %v", mgcMeta["namespace"])
	}

	// Spec should be preserved from MeshGatewayInstance.
	mgcSpec := mgc["spec"].(map[string]interface{})
	if mgcSpec["serviceType"] != "LoadBalancer" {
		t.Errorf("expected serviceType=LoadBalancer, got %v", mgcSpec["serviceType"])
	}

	// Warnings about migration.
	if len(warnings) < 2 {
		t.Errorf("expected at least 2 warnings, got %d", len(warnings))
	}
}

// MeshHTTPRoute/MeshTCPRoute must reach the output byte-identical: rewriting a valid,
// current policy into Gateway API changes its owner and its precedence class (a generated
// route ranks as a producer and ties with hand-written ones), and Kuma never reconciled
// Gateway API TCPRoute at all.
func TestTransformDocument_RoutesArePassedThrough(t *testing.T) {
	for _, kind := range []string{"MeshHTTPRoute", "MeshTCPRoute"} {
		t.Run(kind, func(t *testing.T) {
			input := `apiVersion: kuma.io/v1alpha1
kind: ` + kind + `
metadata:
  name: route-1
spec:
  targetRef:
    kind: Mesh
  to:
    - targetRef:
        kind: MeshService
        name: backend
      rules:
        - default:
            backendRefs: []
`
			for _, target := range []TargetVersion{TargetV2, TargetV3} {
				docs, _, scenario, err := TransformDocument([]byte(input), target)
				if err != nil {
					t.Fatalf("%v: unexpected error: %v", target, err)
				}
				if scenario != ScenarioPassthrough {
					t.Errorf("%v: scenario = %v, want ScenarioPassthrough", target, scenario)
				}
				if len(docs) != 1 {
					t.Fatalf("%v: expected 1 document, got %d", target, len(docs))
				}
				if string(docs[0]) != input {
					t.Errorf("%v: document was rewritten:\n%s", target, docs[0])
				}
			}
		})
	}
}

func TestDetectScenario_GatewayResources(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		scenario Scenario
	}{
		{"MeshGateway", "MeshGateway", ScenarioGateway},
		{"MeshGatewayInstance", "MeshGatewayInstance", ScenarioGatewayInstance},
		// MeshHTTPRoute and MeshTCPRoute are current Kuma policies on both 2.x and 3.0 —
		// Kuma's own Gateway API reconcilers compile HTTPRoute/GRPCRoute *into*
		// MeshHTTPRoute — so they are passed through, not rewritten to Gateway API.
		{"MeshHTTPRoute", "MeshHTTPRoute", ScenarioPassthrough},
		{"MeshTCPRoute", "MeshTCPRoute", ScenarioPassthrough},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte("apiVersion: kuma.io/v1alpha1\nkind: " + tc.kind + "\nmetadata:\n  name: test\n  namespace: default\nspec: {}\n")
			scenario, err := DetectScenario(raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if scenario != tc.scenario {
				t.Errorf("expected %v, got %v", tc.scenario, scenario)
			}
		})
	}
}

func TestTransformMeshGatewayInstance_V3_NoSuccessor(t *testing.T) {
	input := `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayInstance
metadata:
  name: edge-gateway
  namespace: kuma-demo
spec:
  replicas: 3
  serviceType: LoadBalancer
  tags:
    kuma.io/service: edge-gateway
`
	// v2 keeps the 2.x output: GatewayClass + MeshGatewayConfig.
	docs, _, err := TransformMeshGatewayInstance([]byte(input), TargetV2)
	if err != nil {
		t.Fatalf("v2: unexpected error: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("v2: expected GatewayClass + MeshGatewayConfig, got %d docs", len(docs))
	}

	// v3 has no successor: Kuma 3.0 removes the built-in gateway API in full —
	// resources, Go/proto types, CRDs and KDS sync — and keeps only the HTTPRoute
	// and GRPCRoute halves of the Gateway API integration. Emitting the 2.x pair
	// would produce a document the control plane rejects and a GatewayClass it
	// strips finalizers from on startup.
	docs, _, err = TransformMeshGatewayInstance([]byte(input), TargetV3)
	if err == nil {
		t.Fatal("v3: expected an error, got none")
	}
	if len(docs) != 0 {
		t.Errorf("v3: expected no output documents, got %d", len(docs))
	}
	for _, want := range []string{
		"MeshGatewayConfig", // names what is removed
		// The delegated-gateway replacement. kuma.io/gateway is *removed* in 3.0, not
		// renamed, so the error must point at the exclude-inbound-ports annotation that
		// carries the behaviour instead — recommending the old marking would leave the
		// gateway's traffic redirected into Envoy and subject to MeshTrafficPermission.
		"traffic.kuma.io/exclude-inbound-ports",
		`kuma.io/ignore: "true"`, // stops the fronting Service generating a MeshService
		"GRPCRoute",              // the Gateway API surface that survives alongside HTTPRoute
		"replicas=3",             // carries the settings forward
		"serviceType=LoadBalancer",
		"before upgrading", // says to delete the leftovers first
		"--to-latest v2",   // says how to get the old behaviour back
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("v3 error should mention %q: %v", want, err)
		}
	}
}

// Kuma 3.0 deletes the built-in gateway API outright — resources, types, CRDs and KDS sync
// — so a converted MeshGateway/MeshGatewayRoute is only half the migration: the source
// object has to be deleted before the upgrade, or the Helm CRD removal takes it silently.
// The converted Gateway API output itself stays valid, so this is a warning, not an error.
func TestTransformDocument_RemovedGatewaySourceNote_V3(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		kind  string
	}{
		{"MeshGateway", meshGatewayForClassResolution, "MeshGateway"},
		{"MeshGatewayRoute", meshGatewayRouteForRemovalNote, "MeshGatewayRoute"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, warnings, _, err := TransformDocument([]byte(tc.input), TargetV3)
			if err != nil {
				t.Fatalf("v3: unexpected error: %v", err)
			}
			found := false
			for _, w := range warnings {
				if strings.Contains(w, tc.kind) && strings.Contains(w, "before upgrading") {
					found = true
				}
			}
			if !found {
				t.Errorf("expected a v3 delete-the-source advisory for %s, got: %v", tc.kind, warnings)
			}

			// v2 keeps the built-in gateway API, so the advisory must stay quiet.
			_, warnings, _, err = TransformDocument([]byte(tc.input), TargetV2)
			if err != nil {
				t.Fatalf("v2: unexpected error: %v", err)
			}
			for _, w := range warnings {
				if strings.Contains(w, "before upgrading") {
					t.Errorf("unexpected v2 delete-the-source advisory for %s: %s", tc.kind, w)
				}
			}
		})
	}
}

const meshGatewayRouteForRemovalNote = `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayRoute
metadata:
  name: edge-route
spec:
  selectors:
    - match:
        kuma.io/service: edge-gateway_kuma-demo_svc
  conf:
    http:
      rules:
        - matches:
            - path:
                match: PREFIX
                value: /
          backends:
            - destination:
                kuma.io/service: backend_kuma-demo_svc_3001
`

const meshGatewayForClassResolution = `
apiVersion: kuma.io/v1alpha1
kind: MeshGateway
metadata:
  name: edge
spec:
  selectors:
    - match:
        kuma.io/service: edge-gateway_kuma-demo_svc
  conf:
    listeners:
      - port: 8080
        protocol: HTTP
`

const meshGatewayInstanceForClassResolution = `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayInstance
metadata:
  name: edge-gateway
  namespace: kuma-demo
spec:
  replicas: 2
  serviceType: LoadBalancer
  tags:
    kuma.io/service: edge-gateway_kuma-demo_svc
`

// gatewayClassOptsFrom builds the index the way BuildTransformOptions does,
// from the companion MeshGatewayInstance document.
func gatewayClassOptsFrom(t *testing.T, target TargetVersion, instanceDocs ...string) TransformOptions {
	t.Helper()
	idx := GatewayClassIndex{}
	for _, doc := range instanceDocs {
		tag, class := parseGatewayClassEntry([]byte(doc))
		if tag == "" || class == "" {
			t.Fatalf("failed to index MeshGatewayInstance: tag=%q class=%q", tag, class)
		}
		idx.add(tag, class)
	}
	return TransformOptions{Target: target, GatewayClasses: idx}
}

func TestTransformMeshGateway_ResolvesGatewayClassName(t *testing.T) {
	opts := gatewayClassOptsFrom(t, TargetV2, meshGatewayInstanceForClassResolution)

	docs, warnings, err := TransformMeshGateway([]byte(meshGatewayForClassResolution), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var gw map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &gw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// The MeshGateway and its MeshGatewayInstance share a kuma.io/service tag, and
	// the generated GatewayClass is named after the instance — so the Gateway must
	// reference "edge-gateway", not Kuma's controllerName.
	spec := gw["spec"].(map[string]interface{})
	if spec["gatewayClassName"] != "edge-gateway" {
		t.Errorf("gatewayClassName = %v, want edge-gateway", spec["gatewayClassName"])
	}
	if hasWarning(warnings, "could not determine spec.gatewayClassName") {
		t.Errorf("class was resolvable; should not warn: %v", warnings)
	}
}

func TestTransformMeshGateway_AmbiguousGatewayClassWarns(t *testing.T) {
	second := `
apiVersion: kuma.io/v1alpha1
kind: MeshGatewayInstance
metadata:
  name: another-gateway
  namespace: kuma-demo
spec:
  tags:
    kuma.io/service: edge-gateway_kuma-demo_svc
`
	opts := gatewayClassOptsFrom(t, TargetV2, meshGatewayInstanceForClassResolution, second)

	_, warnings, err := TransformMeshGateway([]byte(meshGatewayForClassResolution), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasWarning(warnings, "matches more than one MeshGatewayInstance") {
		t.Errorf("expected an ambiguity warning, got: %v", warnings)
	}
}

func TestTransformMeshGateway_V3_NoKumaGatewayClass(t *testing.T) {
	// Even when a MeshGatewayInstance is present, v3 must not point the Gateway at
	// a Kuma GatewayClass: 3.0's Gateway API integration is HTTPRoute-only, so no
	// Gateway or GatewayClass reconciler exists to act on it.
	opts := gatewayClassOptsFrom(t, TargetV3, meshGatewayInstanceForClassResolution)

	docs, warnings, err := TransformMeshGateway([]byte(meshGatewayForClassResolution), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var gw map[string]interface{}
	if err := yaml.Unmarshal(docs[0], &gw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	spec := gw["spec"].(map[string]interface{})
	if spec["gatewayClassName"] != gatewayClassPlaceholder {
		t.Errorf("gatewayClassName = %v, want %s", spec["gatewayClassName"], gatewayClassPlaceholder)
	}
	// The listener block is valid Gateway API on 3.0 and must survive — that is
	// the whole reason this conversion is not an error like MeshGatewayInstance.
	listeners := spec["listeners"].([]interface{})
	if len(listeners) != 1 {
		t.Fatalf("expected the listener to be preserved, got %d", len(listeners))
	}
	if l := listeners[0].(map[string]interface{}); l["port"] != float64(8080) {
		t.Errorf("listener port = %v, want 8080", l["port"])
	}
	if !hasWarning(warnings, "HTTPRoute-only") && !hasWarning(warnings, "reduces its Gateway API") {
		t.Errorf("expected a v3 explanation, got: %v", warnings)
	}
	// The delegated-gateway pointer must name the annotation that replaces the marking,
	// not the marking itself: 3.0 removes kuma.io/gateway outright and silently ignores a
	// pod that still carries it.
	if !hasWarning(warnings, "traffic.kuma.io/exclude-inbound-ports") {
		t.Errorf("expected the delegated-gateway pointer, got: %v", warnings)
	}
	if hasWarning(warnings, "kuma.io/gateway: enabled") {
		t.Errorf("v3 warning still recommends the removed kuma.io/gateway marking: %v", warnings)
	}
}

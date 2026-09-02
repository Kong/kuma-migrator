package migrator

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

const (
	gatewayAPIVersion = "gateway.networking.k8s.io/v1"

	// kumaGatewayController is the controllerName of Kuma's built-in gateway
	// controller. It belongs on GatewayClass.spec.controllerName — NOT on
	// Gateway.spec.gatewayClassName, which must name a GatewayClass object.
	kumaGatewayController = "gateways.kuma.io/controller"

	// gatewayClassPlaceholder is emitted when the GatewayClass a Gateway must
	// name cannot be determined. It is deliberately not a plausible class name:
	// an unresolvable gatewayClassName leaves the Gateway unreconciled either
	// way, so the value's job is to read as a to-do in `kubectl describe gateway`
	// rather than as a Kuma misconfiguration.
	gatewayClassPlaceholder = "REPLACE-WITH-YOUR-GATEWAYCLASS"
)

// GatewayClassIndex maps the kuma.io/service tag of a built-in gateway to the
// name of the GatewayClass generated for it.
//
// Gateway.spec.gatewayClassName must reference a GatewayClass by name, but a
// MeshGateway document does not contain that name: the GatewayClass is generated
// from the companion MeshGatewayInstance (whose own name becomes the class), and
// the two are linked only by the kuma.io/service tag — MeshGateway selects it
// through spec.selectors[].match, MeshGatewayInstance declares it in spec.tags.
type GatewayClassIndex map[string][]string

// add records a class for a service tag, keeping the list unique and ordered so
// an ambiguous lookup is reported deterministically.
func (idx GatewayClassIndex) add(serviceTag, className string) {
	if serviceTag == "" || className == "" {
		return
	}
	for _, existing := range idx[serviceTag] {
		if existing == className {
			return
		}
	}
	idx[serviceTag] = append(idx[serviceTag], className)
	sort.Strings(idx[serviceTag])
}

// parseGatewayClassEntry extracts the (kuma.io/service tag, GatewayClass name)
// pair a MeshGatewayInstance contributes, or ("", "") for any other document.
func parseGatewayClassEntry(raw []byte) (string, string) {
	var probe struct {
		Kind     string       `json:"kind"`
		Type     string       `json:"type"`
		Metadata KubeMetadata `json:"metadata"`
		Spec     struct {
			Tags map[string]string `json:"tags"`
		} `json:"spec"`
	}
	if err := yaml.Unmarshal(raw, &probe); err != nil {
		return "", ""
	}
	kind := probe.Kind
	if kind == "" {
		kind = probe.Type
	}
	if kind != "MeshGatewayInstance" {
		return "", ""
	}
	// TransformMeshGatewayInstance names the generated GatewayClass after the
	// instance, so the instance name is the class name.
	return probe.Spec.Tags["kuma.io/service"], probe.Metadata.Name
}

// ---- Old MeshGateway structs (input) ----------------------------------------

type oldMeshGateway struct {
	APIVersion string       `json:"apiVersion"`
	Kind       string       `json:"kind"`
	Metadata   KubeMetadata `json:"metadata"`
	Spec       oldMGWSpec   `json:"spec"`
}

type oldMGWSpec struct {
	Selectors []OldSelector       `json:"selectors,omitempty"`
	Tags      map[string]string   `json:"tags,omitempty"`
	Conf      oldMGWConf          `json:"conf"`
}

type oldMGWConf struct {
	Listeners []oldMGWListener `json:"listeners"`
}

type oldMGWListener struct {
	Port      uint32            `json:"port"`
	Protocol  string            `json:"protocol"`
	Hostname  string            `json:"hostname,omitempty"`
	Tags      map[string]string `json:"tags,omitempty"`
	CrossMesh bool              `json:"crossMesh,omitempty"`
	TLS       *oldMGWListenerTLS `json:"tls,omitempty"`
	Resources json.RawMessage   `json:"resources,omitempty"`
}

type oldMGWListenerTLS struct {
	Mode         string          `json:"mode"`
	Certificates []oldMGWCertRef `json:"certificates,omitempty"`
}

type oldMGWCertRef struct {
	Secret string `json:"secret,omitempty"`
}

// ---- Old MeshGatewayInstance struct (input) ----------------------------------

type oldMeshGatewayInstance struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Metadata   KubeMetadata    `json:"metadata"`
	Spec       json.RawMessage `json:"spec"` // preserved verbatim for MeshGatewayConfig
}

// ---- MeshGateway → Gateway --------------------------------------------------

// TransformMeshGateway converts a MeshGateway CRD into a Gateway API Gateway resource.
//
// The listener block (ports, protocols, hostnames, TLS certificateRefs) is valid
// Gateway API on both targets, so this conversion is always performed. Only
// spec.gatewayClassName is target-sensitive — see resolveGatewayClassName.
func TransformMeshGateway(raw []byte, opts TransformOptions) ([][]byte, []string, error) {
	var gw oldMeshGateway
	if err := yaml.Unmarshal(raw, &gw); err != nil {
		return nil, nil, fmt.Errorf("unmarshal MeshGateway: %w", err)
	}

	name := gw.Metadata.Name
	namespace := gw.Metadata.Namespace
	var warnings []string

	if len(gw.Spec.Selectors) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"Gateway %q: spec.selectors has no equivalent in Gateway API — the gateway workload is "+
				"managed by the gateway implementation, not selected by the Gateway resource. The tag is "+
				"still used here to resolve spec.gatewayClassName", name))
	}
	if len(gw.Spec.Tags) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"Gateway %q: spec.tags has no direct equivalent in Gateway API — remove or migrate to labels/annotations", name))
	}

	listeners := make([]interface{}, 0, len(gw.Spec.Conf.Listeners))
	for i, l := range gw.Spec.Conf.Listeners {
		lName := gatewayListenerName(l.Protocol, l.Port)
		listener := map[string]interface{}{
			"name":     lName,
			"port":     int(l.Port),
			"protocol": strings.ToUpper(l.Protocol),
		}
		// Bare "*" is invalid in Gateway API (spec requires *.domain.com format or no field
		// at all to mean "any hostname"). Omitting the field is the correct equivalent.
		if l.Hostname != "" && l.Hostname != "*" {
			listener["hostname"] = l.Hostname
		} else if l.Hostname == "*" {
			warnings = append(warnings, fmt.Sprintf(
				"Gateway %q listener %q: hostname=* is not valid in Gateway API — field omitted (listener will accept any hostname)", name, lName))
		}

		if l.TLS != nil {
			tlsSpec := map[string]interface{}{
				"mode": convertGWTLSMode(l.TLS.Mode),
			}
			var certRefs []interface{}
			for j, cert := range l.TLS.Certificates {
				if cert.Secret != "" {
					certRefs = append(certRefs, map[string]interface{}{
						"name":  cert.Secret,
						"kind":  "Secret",
						"group": "",
					})
				} else {
					warnings = append(warnings, fmt.Sprintf(
						"Gateway %q listener %q cert[%d]: non-secret datasource cannot be automatically migrated — migrate TLS certificate reference manually", name, lName, j))
				}
			}
			if len(certRefs) > 0 {
				tlsSpec["certificateRefs"] = certRefs
			}
			listener["tls"] = tlsSpec
		}

		if l.CrossMesh {
			warnings = append(warnings, fmt.Sprintf(
				"Gateway %q listener %q: crossMesh=true has no direct equivalent in Gateway API — "+
					"configure cross-mesh traffic separately via MeshGatewayConfig", name, lName))
		}
		if len(l.Resources) > 0 && string(l.Resources) != "null" {
			warnings = append(warnings, fmt.Sprintf(
				"Gateway %q listener %d: spec.conf.listeners[].resources has no equivalent in Gateway API listeners — "+
					"configure resource limits in the associated MeshGatewayConfig instead", name, i))
		}

		listeners = append(listeners, listener)
	}

	meta := map[string]interface{}{
		"name":      name,
		"namespace": namespace,
	}
	if ann := meshAnnotationFromLabels(gw.Metadata.Labels); len(ann) > 0 {
		meta["annotations"] = ann
	}

	className, classWarnings := resolveGatewayClassName(gw, name, opts)
	warnings = append(warnings, classWarnings...)

	output := map[string]interface{}{
		"apiVersion": gatewayAPIVersion,
		"kind":       "Gateway",
		"metadata":   meta,
		"spec": map[string]interface{}{
			"gatewayClassName": className,
			"listeners":        listeners,
		},
	}

	b, err := yaml.Marshal(output)
	if err != nil {
		return nil, warnings, fmt.Errorf("marshal Gateway: %w", err)
	}
	return [][]byte{b}, warnings, nil
}

// ---- MeshGatewayInstance → GatewayClass + MeshGatewayConfig ----------------

// TransformMeshGatewayInstance converts a MeshGatewayInstance into a Gateway API
// GatewayClass (cluster-scoped) plus a Kuma MeshGatewayConfig (namespaced).
//
// That output is 2.x-only. Kuma 3.0 removes the built-in gateway API entirely and
// its Gateway API integration is reduced to HTTPRoute (and, since kuma#18280,
// GRPCRoute) — see meshGatewayInstanceRemovedInV3.
func TransformMeshGatewayInstance(raw []byte, target TargetVersion) ([][]byte, []string, error) {
	var inst oldMeshGatewayInstance
	if err := yaml.Unmarshal(raw, &inst); err != nil {
		return nil, nil, fmt.Errorf("unmarshal MeshGatewayInstance: %w", err)
	}

	name := inst.Metadata.Name
	namespace := inst.Metadata.Namespace
	var warnings []string

	if target.IsV3() {
		return nil, nil, meshGatewayInstanceRemovedInV3(inst, name, namespace)
	}

	// GatewayClass is cluster-scoped (no namespace).
	gcMeta := map[string]interface{}{"name": name}
	if ann := meshAnnotationFromLabels(inst.Metadata.Labels); len(ann) > 0 {
		gcMeta["annotations"] = ann
	}
	gatewayClass := map[string]interface{}{
		"apiVersion": gatewayAPIVersion,
		"kind":       "GatewayClass",
		"metadata":   gcMeta,
		"spec": map[string]interface{}{
			"controllerName": "gateways.kuma.io/controller",
			"parametersRef": map[string]interface{}{
				"group":     "kuma.io",
				"kind":      "MeshGatewayConfig",
				"name":      name,
				"namespace": namespace,
			},
		},
	}

	// MeshGatewayConfig carries the deployment configuration (replicas, service type, etc.).
	mgcMeta := map[string]interface{}{
		"name":      name,
		"namespace": namespace,
	}
	if len(inst.Metadata.Labels) > 0 {
		mgcMeta["labels"] = inst.Metadata.Labels
	}
	var specObj interface{}
	if len(inst.Spec) > 0 {
		_ = json.Unmarshal(inst.Spec, &specObj)
	}
	meshGatewayConfig := map[string]interface{}{
		"apiVersion": kumaAPIVersion,
		"kind":       "MeshGatewayConfig",
		"metadata":   mgcMeta,
		"spec":       specObj,
	}

	warnings = append(warnings, fmt.Sprintf(
		"MeshGatewayInstance %q → GatewayClass %q (cluster-scoped) + MeshGatewayConfig %q (%s). "+
			"Update Gateway resources to use gatewayClassName: %s", name, name, name, namespace, name))
	warnings = append(warnings, fmt.Sprintf(
		"MeshGatewayInstance %q: the kuma.io/service tag is now auto-generated when the Gateway resource is applied — "+
			"verify the generated service name matches what your routes expect", name))

	gcBytes, err := yaml.Marshal(gatewayClass)
	if err != nil {
		return nil, warnings, fmt.Errorf("marshal GatewayClass: %w", err)
	}
	mgcBytes, err := yaml.Marshal(meshGatewayConfig)
	if err != nil {
		return nil, warnings, fmt.Errorf("marshal MeshGatewayConfig: %w", err)
	}
	return [][]byte{gcBytes, mgcBytes}, warnings, nil
}

// resolveGatewayClassName determines the value of Gateway.spec.gatewayClassName.
//
// This field must name a GatewayClass *object*. The previous implementation
// hardcoded Kuma's controllerName ("gateways.kuma.io/controller"), which is a
// different identifier entirely and names no object the tool creates — so the
// Gateway applied cleanly and was never reconciled. The GatewayClass actually
// generated for a built-in gateway is named after its MeshGatewayInstance, which
// lives in a separate document; GatewayClassIndex links the two through the
// shared kuma.io/service tag.
//
// Under v3 there is nothing to resolve: Kuma 3.0's Gateway API integration is
// reduced to HTTPRoute and GRPCRoute (kuma#18280 added the latter reconciler
// on 2026-09-01), so no Kuma GatewayClass exists and the operator has to point
// the Gateway at whatever gateway implementation they adopt.
func resolveGatewayClassName(gw oldMeshGateway, name string, opts TransformOptions) (string, []string) {
	if opts.Target.IsV3() {
		return gatewayClassPlaceholder, []string{fmt.Sprintf(
			"Gateway %q: Kuma 3.0 removes the built-in gateway API and reduces its Gateway API "+
				"integration to HTTPRoute/GRPCRoute — no Gateway or GatewayClass reconciler remains, so no Kuma "+
				"GatewayClass exists to reference. The listener block below is valid Gateway API and "+
				"carries over as-is; set spec.gatewayClassName to the GatewayClass of the gateway "+
				"implementation you adopt, and rejoin the workload to the mesh as a delegated gateway "+
				"(its pod labelled kuma.io/gateway: enabled). Left as %q so it cannot be applied unnoticed.",
			name, gatewayClassPlaceholder)}
	}

	tags := gatewayServiceTags(gw)
	var (
		matched []string
		seen    = map[string]bool{}
	)
	for _, tag := range tags {
		for _, class := range opts.GatewayClasses[tag] {
			if !seen[class] {
				seen[class] = true
				matched = append(matched, class)
			}
		}
	}

	switch len(matched) {
	case 1:
		return matched[0], nil
	case 0:
		hint := "no MeshGatewayInstance in the input set declares a matching kuma.io/service tag"
		if len(tags) == 0 {
			hint = "this MeshGateway declares no kuma.io/service tag in spec.selectors or spec.tags"
		}
		return gatewayClassPlaceholder, []string{fmt.Sprintf(
			"Gateway %q: could not determine spec.gatewayClassName — %s. It must name the GatewayClass "+
				"object generated from the companion MeshGatewayInstance (the tool names that class after "+
				"the instance), not Kuma's controllerName. Left as %q; set it before applying, or a Gateway "+
				"referencing a missing class is created and never reconciled.",
			name, hint, gatewayClassPlaceholder)}
	default:
		return matched[0], []string{fmt.Sprintf(
			"Gateway %q: its kuma.io/service tag matches more than one MeshGatewayInstance (%s). "+
				"Used %q for spec.gatewayClassName — confirm that is the intended gateway.",
			name, strings.Join(matched, ", "), matched[0])}
	}
}

// gatewayServiceTags collects the kuma.io/service values a MeshGateway is scoped
// by, from spec.selectors[].match and spec.tags.
func gatewayServiceTags(gw oldMeshGateway) []string {
	var out []string
	seen := map[string]bool{}
	add := func(v string) {
		if v == "" || v == "*" || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, v)
	}
	for _, sel := range gw.Spec.Selectors {
		add(sel.Match["kuma.io/service"])
	}
	add(gw.Spec.Tags["kuma.io/service"])
	return out
}

// meshGatewayInstanceRemovedInV3 explains why a MeshGatewayInstance has no
// mechanical successor on Kuma 3.0, and carries over the settings the manifest
// does hold so the replacement workload can be written by hand.
//
// Kuma 3.0 removes the built-in gateway API in full: MeshGateway,
// MeshGatewayRoute, MeshGatewayInstance *and* MeshGatewayConfig, including the
// meshgatewayconfigs.kuma.io CRD. Both halves of the 2.x output are therefore
// dead on 3.0 — the parametersRef target no longer exists, and the Gateway API
// integration is reduced to HTTPRoute and GRPCRoute (plugin_gateway.go registers
// the HTTPRoute reconciler plus, since kuma#18280, a GRPCRoute reconciler; the
// sole remaining GatewayClass code path strips finalizers from Kuma-controlled
// GatewayClasses left behind by the removal).
//
// The replacement is a delegated gateway: a Deployment and Service you own, with
// the pod labelled kuma.io/gateway: enabled so Kuma injects a sidecar. That
// cannot be synthesised from a MeshGatewayInstance — the manifest has no
// container image or pod spec — so this is reported rather than half-generated.
func meshGatewayInstanceRemovedInV3(inst oldMeshGatewayInstance, name, namespace string) error {
	carried := carriedGatewayInstanceSettings(inst)
	detail := ""
	if carried != "" {
		detail = fmt.Sprintf(" Settings to carry over to the Deployment/Service: %s.", carried)
	}

	return fmt.Errorf(
		"MeshGatewayInstance %q (%s) has no successor on Kuma 3.0: the built-in gateway API is removed "+
			"in full (MeshGateway, MeshGatewayRoute, MeshGatewayInstance and MeshGatewayConfig, including "+
			"the meshgatewayconfigs.kuma.io CRD), and the Gateway API integration is reduced to HTTPRoute — "+
			"no Gateway or GatewayClass reconciler remains, and the control plane strips finalizers from "+
			"Kuma-controlled GatewayClasses on startup. Replace it with a delegated gateway: a Deployment "+
			"and Service you manage, with the pod labelled kuma.io/gateway: enabled so the sidecar is "+
			"injected. That needs a container image and pod spec this manifest does not carry, so it cannot "+
			"be generated.%s Re-run with --to-latest v2 to keep the 2.x GatewayClass + MeshGatewayConfig "+
			"output", name, namespace, detail)
}

// carriedGatewayInstanceSettings renders the MeshGatewayInstance spec fields that
// have a direct equivalent on a hand-written Deployment/Service, so the operator
// does not have to re-read the original manifest.
func carriedGatewayInstanceSettings(inst oldMeshGatewayInstance) string {
	if len(inst.Spec) == 0 {
		return ""
	}
	var spec struct {
		Replicas    *int              `json:"replicas"`
		ServiceType string            `json:"serviceType"`
		Tags        map[string]string `json:"tags"`
	}
	if err := json.Unmarshal(inst.Spec, &spec); err != nil {
		return ""
	}

	var parts []string
	if spec.Replicas != nil {
		parts = append(parts, fmt.Sprintf("replicas=%d", *spec.Replicas))
	}
	if spec.ServiceType != "" {
		parts = append(parts, fmt.Sprintf("serviceType=%s", spec.ServiceType))
	}
	if len(spec.Tags) > 0 {
		keys := make([]string, 0, len(spec.Tags))
		for k := range spec.Tags {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		tags := make([]string, 0, len(keys))
		for _, k := range keys {
			tags = append(tags, fmt.Sprintf("%s=%s", k, spec.Tags[k]))
		}
		parts = append(parts, "tags{"+strings.Join(tags, ",")+"}")
	}
	return strings.Join(parts, ", ")
}

// ---- Helpers -----------------------------------------------------------------

// gatewayListenerName generates a stable listener name from protocol and port,
// matching the convention Kuma uses internally (e.g. "http-8080").
func gatewayListenerName(protocol string, port uint32) string {
	return fmt.Sprintf("%s-%d", strings.ToLower(protocol), port)
}

func convertGWTLSMode(mode string) string {
	switch strings.ToUpper(mode) {
	case "TERMINATE":
		return "Terminate"
	case "PASSTHROUGH":
		return "Passthrough"
	default:
		return mode
	}
}

// meshAnnotationFromLabels returns a Gateway API annotation map containing
// kuma.io/mesh if the source labels specify a non-default mesh.
// (Gateway API resources use annotations for mesh association, unlike old Kuma resources that used labels.)
func meshAnnotationFromLabels(labels map[string]string) map[string]interface{} {
	if mesh, ok := labels["kuma.io/mesh"]; ok && mesh != "" && mesh != "default" {
		return map[string]interface{}{"kuma.io/mesh": mesh}
	}
	return nil
}

package migrator

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

const gatewayAPIVersion = "gateway.networking.k8s.io/v1"

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
func TransformMeshGateway(raw []byte) ([][]byte, []string, error) {
	var gw oldMeshGateway
	if err := yaml.Unmarshal(raw, &gw); err != nil {
		return nil, nil, fmt.Errorf("unmarshal MeshGateway: %w", err)
	}

	name := gw.Metadata.Name
	namespace := gw.Metadata.Namespace
	var warnings []string

	if len(gw.Spec.Selectors) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"Gateway %q: spec.selectors has no equivalent in Gateway API — "+
				"the gateway pods are now managed by the GatewayClass controller", name))
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

	output := map[string]interface{}{
		"apiVersion": gatewayAPIVersion,
		"kind":       "Gateway",
		"metadata":   meta,
		"spec": map[string]interface{}{
			"gatewayClassName": "gateways.kuma.io/controller",
			"listeners":        listeners,
		},
	}

	warnings = append(warnings, fmt.Sprintf(
		"Gateway %q: ensure a GatewayClass named 'kuma' exists with controllerName: gateways.kuma.io/controller", name))

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
// its Gateway API integration is reduced to HTTPRoute — see
// meshGatewayInstanceRemovedInV3.
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

// meshGatewayInstanceRemovedInV3 explains why a MeshGatewayInstance has no
// mechanical successor on Kuma 3.0, and carries over the settings the manifest
// does hold so the replacement workload can be written by hand.
//
// Kuma 3.0 removes the built-in gateway API in full: MeshGateway,
// MeshGatewayRoute, MeshGatewayInstance *and* MeshGatewayConfig, including the
// meshgatewayconfigs.kuma.io CRD. Both halves of the 2.x output are therefore
// dead on 3.0 — the parametersRef target no longer exists, and the Gateway API
// integration is reduced to HTTPRoute (plugin_gateway.go registers only the
// HTTPRoute reconciler; the sole remaining GatewayClass code path strips
// finalizers from Kuma-controlled GatewayClasses left behind by the removal).
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

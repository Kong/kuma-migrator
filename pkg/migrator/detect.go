package migrator

import (
	"strings"

	"sigs.k8s.io/yaml"
)

// knownLegacyTypes is the set of non-Mesh-prefixed policy type names (Scenario A).
var knownLegacyTypes = map[string]bool{
	"Timeout":           true,
	"CircuitBreaker":    true,
	"Retry":             true,
	"TrafficPermission": true,
	"FaultInjection":    true,
	"RateLimit":         true,
	"HealthCheck":       true,
	"TrafficLog":        true,
	"TrafficTrace":      true,
	"TrafficRoute":      true,
	"ProxyTemplate":     true,
}

// probe is a minimal struct used only for scenario detection — never for transformation.
type probe struct {
	// Scenario A signals
	Type         string      `json:"type"`
	Sources      interface{} `json:"sources"`
	Destinations interface{} `json:"destinations"`
	// Scenario B / C signals
	APIVersion string     `json:"apiVersion"`
	Kind       string     `json:"kind"`
	Spec       *probeSpec `json:"spec"`
}

type probeSpec struct {
	TargetRef *probeRef  `json:"targetRef"`
	To        []probeEntry `json:"to"`
	From      []probeEntry `json:"from"`
}

type probeEntry struct {
	TargetRef *probeRef `json:"targetRef"`
}

type probeRef struct {
	Kind string            `json:"kind"`
	Name string            `json:"name,omitempty"`
	Tags map[string]string `json:"tags"`
}

// DetectScenario classifies a single YAML document into a migration scenario.
func DetectScenario(raw []byte) (Scenario, error) {
	var p probe
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return ScenarioUnknown, err
	}

	// Normalise: Universal format uses "type" instead of "kind".
	// All checks below use kind so both formats are handled uniformly.
	kind := p.Kind
	if kind == "" {
		kind = p.Type
	}

	// ExternalService.
	if kind == "ExternalService" {
		return ScenarioExternalService, nil
	}

	// Kong Mesh OPAPolicy → MeshOPA.
	if kind == "OPAPolicy" {
		return ScenarioOPAPolicy, nil
	}

	// Gateway resources → Gateway API CRDs.
	switch kind {
	case "MeshGateway":
		return ScenarioGateway, nil
	case "MeshGatewayInstance":
		return ScenarioGatewayInstance, nil
	case "MeshGatewayRoute":
		return ScenarioGatewayRoute, nil
	case "MeshHTTPRoute":
		return ScenarioHTTPRoute, nil
	case "MeshTCPRoute":
		return ScenarioTCPRoute, nil
	}

	// Mesh CRD — check if it has sections that need extracting.
	if kind == "Mesh" {
		if meshNeedsMigration(raw) {
			return ScenarioMesh, nil
		}
		return ScenarioPassthrough, nil
	}

	// Scenario A: has sources/destinations keys OR a known non-Mesh* type name.
	if p.Sources != nil || p.Destinations != nil || knownLegacyTypes[kind] {
		return ScenarioLegacy, nil
	}

	// Scenario D: new-style Mesh* policy with from[] still present (deprecated in 2.10).
	// Must not have service-identity tags (those become ScenarioSubset first, then the
	// from→rules migration is applied as a second pass inside transformScenarioSubset).
	if rulesAPIMigrationKinds[kind] && p.Spec != nil && len(p.Spec.From) > 0 {
		hasServiceTag := false
		for i := range p.Spec.From {
			if probeRefHasServiceTag(p.Spec.From[i].TargetRef) {
				hasServiceTag = true
				break
			}
		}
		if !hasServiceTag && !probeRefHasServiceTag(p.Spec.TargetRef) {
			return ScenarioRules, nil
		}
	}

	// Not a Kuma policy resource at all — skip.
	if kind == "" {
		return ScenarioSkipped, nil
	}

	// Non-Mesh, non-legacy Kubernetes resources (e.g. Deployment, Service) — skip.
	if !strings.HasPrefix(kind, "Mesh") {
		return ScenarioSkipped, nil
	}

	// Check all targetRef nodes for service-identity tags → Scenario B.
	// Also catch a top-level MeshSubset with only workload-selector tags: in Kuma 2.10+
	// MeshSubset is deprecated even without service tags, and ConvertTargetRef will
	// migrate it to Dataplane+labels.
	if p.Spec != nil {
		if probeRefHasServiceTag(p.Spec.TargetRef) || probeRefIsMeshSubset(p.Spec.TargetRef) || probeRefHasOldMeshServiceName(p.Spec.TargetRef) {
			return ScenarioSubset, nil
		}
		for i := range p.Spec.To {
			if probeRefHasServiceTag(p.Spec.To[i].TargetRef) || probeRefHasOldMeshServiceName(p.Spec.To[i].TargetRef) {
				return ScenarioSubset, nil
			}
		}
		for i := range p.Spec.From {
			if probeRefHasServiceTag(p.Spec.From[i].TargetRef) || probeRefHasOldMeshServiceName(p.Spec.From[i].TargetRef) {
				return ScenarioSubset, nil
			}
		}
	}

	// All targetRefs are already migrated (or resource has no targetRef).
	return ScenarioPassthrough, nil
}

// probeRefIsMeshSubset reports whether the ref uses the deprecated MeshSubset kind.
// Used to ensure non-service-tag MeshSubset at spec.targetRef is migrated to Dataplane.
func probeRefIsMeshSubset(ref *probeRef) bool {
	return ref != nil && ref.Kind == "MeshSubset"
}

// probeRefHasOldMeshServiceName reports whether the ref is a MeshService using a
// Kuma-generated internal name (e.g. "echo_demo_svc_8000"). These refs need the same
// normalisation as MeshSubset refs even though the kind is already correct.
func probeRefHasOldMeshServiceName(ref *probeRef) bool {
	if ref == nil || ref.Kind != "MeshService" || ref.Name == "" {
		return false
	}
	_, ns := ParseKumaServiceTag(ref.Name)
	return ns != ""
}

func probeRefHasServiceTag(ref *probeRef) bool {
	if ref == nil {
		return false
	}
	for k := range ref.Tags {
		if isServiceIdentityTag(k) {
			return true
		}
	}
	return false
}

// isServiceIdentityTag returns true for tag keys that identify a specific service.
func isServiceIdentityTag(key string) bool {
	return key == "kuma.io/service" || key == "k8s.kuma.io/service-name"
}

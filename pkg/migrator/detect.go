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
	Tags map[string]string `json:"tags"`
}

// DetectScenario classifies a single YAML document into a migration scenario.
func DetectScenario(raw []byte) (Scenario, error) {
	var p probe
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return ScenarioUnknown, err
	}

	// ExternalService (both Kubernetes-style and Universal-style).
	if p.Kind == "ExternalService" || p.Type == "ExternalService" {
		return ScenarioExternalService, nil
	}

	// Kong Mesh OPAPolicy → MeshOPA.
	if p.Kind == "OPAPolicy" || p.Type == "OPAPolicy" {
		return ScenarioOPAPolicy, nil
	}

	// Gateway resources → Gateway API CRDs.
	switch p.Kind {
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
	if p.Kind == "Mesh" {
		if meshNeedsMigration(raw) {
			return ScenarioMesh, nil
		}
		return ScenarioPassthrough, nil
	}

	// Scenario A: has sources/destinations keys OR a known non-Mesh* type name.
	if p.Sources != nil || p.Destinations != nil || knownLegacyTypes[p.Type] {
		return ScenarioLegacy, nil
	}

	// Scenario D: new-style Mesh* policy with from[] still present (deprecated in 2.10).
	// Must not have service-identity tags (those become ScenarioSubset first, then the
	// from→rules migration is applied as a second pass inside transformScenarioSubset).
	if rulesAPIMigrationKinds[p.Kind] && p.Spec != nil && len(p.Spec.From) > 0 {
		hasServiceTag := false
		for i := range p.Spec.From {
			if probeRefHasServiceTag(p.Spec.From[i].TargetRef) {
				hasServiceTag = true
				break
			}
		}
		if !hasServiceTag && p.Spec.TargetRef != nil && !probeRefHasServiceTag(p.Spec.TargetRef) {
			return ScenarioRules, nil
		}
	}

	// Not a Kuma policy resource at all.
	if p.Kind == "" && p.Type == "" {
		return ScenarioSkipped, nil
	}

	// Non-Mesh resources (e.g. Kubernetes Deployment, Service) — pass through.
	if !strings.HasPrefix(p.Kind, "Mesh") && p.Type == "" {
		return ScenarioSkipped, nil
	}

	// Check all targetRef nodes for service-identity tags → Scenario B.
	if p.Spec != nil {
		if probeRefHasServiceTag(p.Spec.TargetRef) {
			return ScenarioSubset, nil
		}
		for i := range p.Spec.To {
			if probeRefHasServiceTag(p.Spec.To[i].TargetRef) {
				return ScenarioSubset, nil
			}
		}
		for i := range p.Spec.From {
			if probeRefHasServiceTag(p.Spec.From[i].TargetRef) {
				return ScenarioSubset, nil
			}
		}
	}

	// All targetRefs are already migrated (or resource has no targetRef).
	return ScenarioPassthrough, nil
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

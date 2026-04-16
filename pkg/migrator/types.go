package migrator

import "encoding/json"

// Scenario represents the migration state of a single YAML policy document.
type Scenario int

const (
	ScenarioUnknown         Scenario = iota
	ScenarioLegacy                   // Legacy: sources/destinations with kuma.io/service tags
	ScenarioSubset                   // Intermediate: Mesh* type but MeshSubset with service-identity tags
	ScenarioPassthrough              // Fully migrated: MeshService kind throughout — pass through unchanged
	ScenarioSkipped                  // Not a recognised Kuma policy — pass through unchanged
	ScenarioMesh                     // Mesh CRD with extractable observability/passthrough sections
	ScenarioExternalService          // ExternalService CRD to be converted to MeshExternalService
	ScenarioGateway                  // MeshGateway → Gateway (gateway.networking.k8s.io)
	ScenarioGatewayInstance          // MeshGatewayInstance → GatewayClass + MeshGatewayConfig
	ScenarioHTTPRoute                // MeshHTTPRoute → HTTPRoute (gateway.networking.k8s.io)
	ScenarioTCPRoute                 // MeshTCPRoute → TCPRoute (gateway.networking.k8s.io)
	ScenarioRules                    // from[] deprecated in 2.10 → rules[] (MeshTimeout, MeshCircuitBreaker, etc.)
	ScenarioGatewayRoute             // MeshGatewayRoute → Gateway API HTTPRoute or TCPRoute
	ScenarioOPAPolicy                // Kong Mesh OPAPolicy → MeshOPA
)

func (s Scenario) String() string {
	switch s {
	case ScenarioLegacy:
		return "Legacy (sources/destinations)"
	case ScenarioSubset:
		return "Subset (MeshSubset → MeshService)"
	case ScenarioPassthrough:
		return "Passthrough (already migrated)"
	case ScenarioSkipped:
		return "skipped (non-policy)"
	case ScenarioMesh:
		return "Mesh (observability extracted)"
	case ScenarioExternalService:
		return "ExternalService → MeshExternalService"
	case ScenarioGateway:
		return "MeshGateway → Gateway"
	case ScenarioGatewayInstance:
		return "MeshGatewayInstance → GatewayClass + MeshGatewayConfig"
	case ScenarioHTTPRoute:
		return "MeshHTTPRoute → HTTPRoute"
	case ScenarioTCPRoute:
		return "MeshTCPRoute → TCPRoute"
	case ScenarioRules:
		return "Rules (from[] → rules[])"
	case ScenarioGatewayRoute:
		return "MeshGatewayRoute → HTTPRoute/TCPRoute"
	case ScenarioOPAPolicy:
		return "OPAPolicy → MeshOPA"
	default:
		return "unknown"
	}
}

// TargetRef mirrors github.com/kumahq/kuma/v2/api/common/v1alpha1.TargetRef
// with identical JSON tags, so sigs.k8s.io/yaml round-trips correctly.
type TargetRef struct {
	Kind        string            `json:"kind"`
	Name        *string           `json:"name,omitempty"`
	Namespace   *string           `json:"namespace,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Mesh        *string           `json:"mesh,omitempty"`
	SectionName *string           `json:"sectionName,omitempty"`
}

// PolicyEntry is a single element in the to[] or from[] arrays.
// Default is kept as raw JSON so policy-specific configuration is never lost.
type PolicyEntry struct {
	TargetRef TargetRef       `json:"targetRef"`
	Default   json.RawMessage `json:"default,omitempty"`
}

// KubePolicy is a Kubernetes-style policy document (Scenario B/C input; all output).
type KubePolicy struct {
	APIVersion string        `json:"apiVersion"`
	Kind       string        `json:"kind"`
	Metadata   KubeMetadata  `json:"metadata"`
	Spec       KubePolicySpec `json:"spec"`
}

// KubeMetadata holds the standard Kubernetes object metadata we care about.
type KubeMetadata struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// KubePolicySpec is the spec of a Kuma new-style policy.
type KubePolicySpec struct {
	TargetRef *TargetRef      `json:"targetRef,omitempty"`
	To        []PolicyEntry   `json:"to,omitempty"`
	From      []PolicyEntry   `json:"from,omitempty"`
	Rules     []RuleEntry     `json:"rules,omitempty"`
	Default   json.RawMessage `json:"default,omitempty"` // spec-level default (e.g. MeshMetric)
}

// RuleEntry is a single element in the rules[] array (Kuma 2.10+ Rules API).
// Unlike PolicyEntry it has no source targetRef — rules apply to all inbound traffic.
type RuleEntry struct {
	Default json.RawMessage `json:"default,omitempty"`
}

// UniversalPolicy is a flat Universal-style policy document (Scenario A input).
type UniversalPolicy struct {
	Type         string          `json:"type"`
	Name         string          `json:"name"`
	Mesh         string          `json:"mesh,omitempty"`
	Sources      []OldSelector   `json:"sources,omitempty"`
	Destinations []OldSelector   `json:"destinations,omitempty"`
	Conf         json.RawMessage `json:"conf,omitempty"`
}

// OldSelector is a single match clause in sources[] or destinations[].
type OldSelector struct {
	Match map[string]string `json:"match"`
}

package migrator

import (
	"encoding/json"
	"fmt"
	"strings"

	"sigs.k8s.io/yaml"
)

const kumaAPIVersion = "kuma.io/v1alpha1"

// TransformDocument transforms a single YAML document according to its scenario.
// Returns one or more output documents (multiple when a Scenario A policy with several
// specific sources must be split), collected warnings, and the detected scenario.
//
// After the scenario-specific transformation, ScanForDeprecations is applied to
// every output document so that deprecated fields (MeshMetric sidecar.regex,
// MeshHealthCheck healthyPanicThreshold, MeshTrust spec.origin) are caught even
// when the document was already fully migrated (ScenarioPassthrough pass-through).
func TransformDocument(raw []byte) ([][]byte, []string, Scenario, error) {
	scenario, err := DetectScenario(raw)
	if err != nil {
		return nil, nil, ScenarioUnknown, err
	}

	var docs [][]byte
	var warnings []string

	switch scenario {
	case ScenarioLegacy:
		var policy UniversalPolicy
		if err := yaml.Unmarshal(raw, &policy); err != nil {
			return nil, nil, scenario, fmt.Errorf("unmarshal legacy policy: %w", err)
		}
		outputs, w, err := transformScenarioLegacy(policy)
		if err != nil {
			return nil, nil, scenario, err
		}
		warnings = w
		for _, out := range outputs {
			b, err := yaml.Marshal(out)
			if err != nil {
				return nil, nil, scenario, fmt.Errorf("marshal output policy: %w", err)
			}
			docs = append(docs, b)
		}

	case ScenarioSubset:
		var policy KubePolicy
		if err := yaml.Unmarshal(raw, &policy); err != nil {
			return nil, nil, scenario, fmt.Errorf("unmarshal intermediate policy: %w", err)
		}
		out, w, err := transformScenarioSubset(policy)
		if err != nil {
			return nil, nil, scenario, err
		}
		warnings = w
		b, err := yaml.Marshal(out)
		if err != nil {
			return nil, nil, scenario, fmt.Errorf("marshal output policy: %w", err)
		}
		docs = [][]byte{b}

	case ScenarioMesh:
		var err error
		docs, warnings, err = TransformMesh(raw)
		if err != nil {
			return nil, nil, scenario, err
		}

	case ScenarioExternalService:
		var err error
		docs, warnings, err = TransformExternalService(raw)
		if err != nil {
			return nil, nil, scenario, err
		}

	case ScenarioRules:
		var err error
		docs, warnings, err = TransformFromToRules(raw)
		if err != nil {
			return nil, nil, scenario, err
		}

	case ScenarioGatewayRoute:
		var err error
		docs, warnings, err = TransformMeshGatewayRoute(raw)
		if err != nil {
			return nil, nil, scenario, err
		}

	case ScenarioOPAPolicy:
		var err error
		docs, warnings, err = TransformOPAPolicy(raw)
		if err != nil {
			return nil, nil, scenario, err
		}

	case ScenarioGateway:
		var err error
		docs, warnings, err = TransformMeshGateway(raw)
		if err != nil {
			return nil, nil, scenario, err
		}

	case ScenarioGatewayInstance:
		var err error
		docs, warnings, err = TransformMeshGatewayInstance(raw)
		if err != nil {
			return nil, nil, scenario, err
		}

	case ScenarioHTTPRoute:
		var err error
		docs, warnings, err = TransformMeshHTTPRoute(raw)
		if err != nil {
			return nil, nil, scenario, err
		}

	case ScenarioTCPRoute:
		var err error
		docs, warnings, err = TransformMeshTCPRoute(raw)
		if err != nil {
			return nil, nil, scenario, err
		}

	default: // ScenarioPassthrough, ScenarioSkipped, ScenarioUnknown
		docs = [][]byte{raw}
	}

	// Post-pass: scan every output document for deprecated fields.
	for i, doc := range docs {
		fixed, depWarns := ScanForDeprecations(doc)
		docs[i] = fixed
		warnings = append(warnings, depWarns...)
	}

	return docs, warnings, scenario, nil
}

// transformScenarioLegacy converts a legacy Universal-style policy into one or more
// Kubernetes-style KubePolicy documents.
func transformScenarioLegacy(policy UniversalPolicy) ([]KubePolicy, []string, error) {
	if policy.Type == "TrafficRoute" {
		return nil, nil, fmt.Errorf("TrafficRoute requires manual migration to MeshHTTPRoute or MeshTCPRoute")
	}

	newKind, err := OldTypeToNew(policy.Type)
	if err != nil {
		return nil, nil, err
	}

	// Universal policies have no namespace; leave it empty.
	const policyNamespace = ""

	// TrafficPermission is inverted: destinations → spec.targetRef, sources → from[].
	if policy.Type == "TrafficPermission" {
		return transformTrafficPermission(policy, newKind, policyNamespace)
	}

	return transformGenericLegacy(policy, newKind, policyNamespace)
}

// transformGenericLegacy handles all old-style policies except TrafficPermission.
// sources → spec.targetRef (topLevel=true → Dataplane), destinations → to[] (topLevel=false → MeshService).
func transformGenericLegacy(policy UniversalPolicy, newKind, policyNamespace string) ([]KubePolicy, []string, error) {
	var warnings []string

	// Build to[] from destinations.
	toEntries := make([]PolicyEntry, 0, len(policy.Destinations))
	for _, dest := range policy.Destinations {
		ref, warn := ConvertSelectorToTargetRef(dest, policyNamespace, false)
		toEntries = append(toEntries, PolicyEntry{TargetRef: ref, Default: policy.Conf})
		if warn != "" {
			warnings = append(warnings, warn)
		}
	}

	// Resolve source TargetRefs (top-level → Dataplane in 2.13.x).
	sourceRefs := make([]TargetRef, 0, len(policy.Sources))
	for _, src := range policy.Sources {
		ref, warn := ConvertSelectorToTargetRef(src, policyNamespace, true)
		sourceRefs = append(sourceRefs, ref)
		if warn != "" {
			warnings = append(warnings, warn)
		}
	}
	if len(sourceRefs) == 0 {
		sourceRefs = []TargetRef{{Kind: "Mesh"}}
	}

	// If all sources resolve to Mesh, emit a single policy.
	if allKind(sourceRefs, "Mesh") {
		if w := ValidateResourceName(policy.Name, newKind); w != "" {
			warnings = append(warnings, w)
		}
		return []KubePolicy{
			buildKubePolicy(policy.Name, policyNamespace, newKind, TargetRef{Kind: "Mesh"}, toEntries, nil),
		}, warnings, nil
	}

	// Single non-wildcard source — keep original name.
	if len(sourceRefs) == 1 {
		if w := ValidateResourceName(policy.Name, newKind); w != "" {
			warnings = append(warnings, w)
		}
		return []KubePolicy{
			buildKubePolicy(policy.Name, policyNamespace, newKind, sourceRefs[0], toEntries, nil),
		}, warnings, nil
	}

	// Multiple specific sources → split into one policy per source (§8).
	policies := make([]KubePolicy, 0, len(sourceRefs))
	for i, ref := range sourceRefs {
		name := fmt.Sprintf("%s-%d", policy.Name, i)
		if w := ValidateResourceName(name, newKind); w != "" {
			warnings = append(warnings, w)
		}
		policies = append(policies, buildKubePolicy(name, policyNamespace, newKind, ref, toEntries, nil))
	}
	return policies, warnings, nil
}

// transformTrafficPermission handles the inverted TrafficPermission semantics.
// destinations → spec.targetRef (topLevel=true → Dataplane), sources → from[] (topLevel=false → MeshService).
func transformTrafficPermission(policy UniversalPolicy, newKind, policyNamespace string) ([]KubePolicy, []string, error) {
	if len(policy.Destinations) == 0 {
		return nil, nil, fmt.Errorf("TrafficPermission %q has no destinations", policy.Name)
	}

	var warnings []string

	// Build from[] entries from sources; old TrafficPermission always means Allow.
	allowAction, _ := json.Marshal(map[string]string{"action": "Allow"})
	fromEntries := make([]PolicyEntry, 0, len(policy.Sources))
	for _, src := range policy.Sources {
		ref, warn := ConvertSelectorToTargetRef(src, policyNamespace, false)
		fromEntries = append(fromEntries, PolicyEntry{
			TargetRef: ref,
			Default:   json.RawMessage(allowAction),
		})
		if warn != "" {
			warnings = append(warnings, warn)
		}
	}

	// One policy per destination (usually just one).
	// Destinations map to spec.targetRef (top-level → Dataplane).
	policies := make([]KubePolicy, 0, len(policy.Destinations))
	for i, dest := range policy.Destinations {
		destRef, warn := ConvertSelectorToTargetRef(dest, policyNamespace, true)
		if warn != "" {
			warnings = append(warnings, warn)
		}
		name := policy.Name
		if len(policy.Destinations) > 1 {
			name = fmt.Sprintf("%s-%d", policy.Name, i)
		}
		policies = append(policies, buildKubePolicy(name, policyNamespace, newKind, destRef, nil, fromEntries))
	}
	return policies, warnings, nil
}

// transformScenarioSubset rewrites all targetRef entries that use service-identity tags,
// leaving the rest of the policy document (including Default configs) untouched.
func transformScenarioSubset(policy KubePolicy) (KubePolicy, []string, error) {
	ns := policy.Metadata.Namespace
	var warnings []string

	if policy.Spec.TargetRef != nil {
		converted, warn := ConvertTargetRef(*policy.Spec.TargetRef, ns, true)
		policy.Spec.TargetRef = &converted
		if warn != "" {
			warnings = append(warnings, warn)
		}
	}
	for i, entry := range policy.Spec.To {
		converted, warn := ConvertTargetRef(entry.TargetRef, ns, false)
		policy.Spec.To[i].TargetRef = converted
		if warn != "" {
			warnings = append(warnings, warn)
		}
	}
	for i, entry := range policy.Spec.From {
		converted, warn := ConvertTargetRef(entry.TargetRef, ns, false)
		policy.Spec.From[i].TargetRef = converted
		if warn != "" {
			warnings = append(warnings, warn)
		}
	}
	// Second pass: if this kind uses the Rules API and still has from[], migrate it.
	if rulesAPIMigrationKinds[policy.Kind] && len(policy.Spec.From) > 0 {
		w := applyFromToRules(&policy)
		warnings = append(warnings, w...)
	}

	return policy, warnings, nil
}

// buildKubePolicy constructs a new KubePolicy with the given fields.
func buildKubePolicy(name, namespace, kind string, targetRef TargetRef, to, from []PolicyEntry) KubePolicy {
	return KubePolicy{
		APIVersion: kumaAPIVersion,
		Kind:       kind,
		Metadata: KubeMetadata{
			Name:      name,
			Namespace: namespace,
		},
		Spec: KubePolicySpec{
			TargetRef: &targetRef,
			To:        to,
			From:      from,
		},
	}
}

// splitYAMLDocuments splits a multi-document YAML file on --- separators.
func splitYAMLDocuments(data []byte) [][]byte {
	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	parts := strings.Split(content, "\n---")

	var docs [][]byte
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = strings.TrimPrefix(part, "---")
		part = strings.TrimSpace(part)
		if part != "" {
			docs = append(docs, []byte(part))
		}
	}
	return docs
}

// allKind reports whether every TargetRef in refs has the given kind.
func allKind(refs []TargetRef, kind string) bool {
	for _, r := range refs {
		if r.Kind != kind {
			return false
		}
	}
	return true
}

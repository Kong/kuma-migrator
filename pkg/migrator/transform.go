package migrator

import (
	"encoding/json"
	"fmt"
	"strings"

	"sigs.k8s.io/yaml"
)

const (
	kumaAPIVersion = "kuma.io/v1alpha1"
	// meshLabel carries the mesh association on Kubernetes-style resources.
	meshLabel = "kuma.io/mesh"
)

// TransformDocument transforms a single YAML document according to its scenario.
// Returns one or more output documents (multiple when a Scenario A policy with several
// specific sources must be split), collected warnings, and the detected scenario.
//
// After the scenario-specific transformation, ScanForDeprecations is applied to
// every output document so that deprecated fields (MeshMetric sidecar.regex,
// MeshHealthCheck healthyPanicThreshold, MeshTrust spec.origin) are caught even
// when the document was already fully migrated (ScenarioPassthrough pass-through).
func TransformDocument(raw []byte, target TargetVersion) ([][]byte, []string, Scenario, error) {
	return TransformDocumentWithOptions(raw, TransformOptions{Target: target})
}

// TransformOptions carries context a single document cannot provide. Build it
// with BuildTransformOptions, which populates every index in one walk of the
// input tree. A zero value (or any nil index) is valid — the conversions that
// would have used it warn instead.
type TransformOptions struct {
	Target TargetVersion
	// MeshBackends resolves TrafficLog/TrafficTrace conf.backend references
	// against the Mesh resource that declares them.
	MeshBackends MeshBackendIndex
	// GatewayClasses resolves the GatewayClass a converted MeshGateway must name,
	// which lives on the companion MeshGatewayInstance.
	GatewayClasses GatewayClassIndex
}

// TransformDocumentWithOptions is TransformDocument with the cross-document
// context some conversions need (see TransformOptions).
func TransformDocumentWithOptions(raw []byte, opts TransformOptions) ([][]byte, []string, Scenario, error) {
	target := opts.Target
	scenario, err := DetectScenario(raw)
	if err != nil {
		return nil, nil, ScenarioUnknown, err
	}

	var docs [][]byte
	var warnings []string

	switch scenario {
	case ScenarioLegacy:
		policy, err := parseLegacyPolicy(raw)
		if err != nil {
			return nil, nil, scenario, err
		}
		outputs, w, err := transformScenarioLegacy(policy, opts)
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
		docs, warnings, err = TransformOPAPolicy(raw, target)
		if err != nil {
			return nil, nil, scenario, err
		}

	case ScenarioGateway:
		var err error
		docs, warnings, err = TransformMeshGateway(raw, opts)
		if err != nil {
			return nil, nil, scenario, err
		}

	case ScenarioGatewayInstance:
		var err error
		docs, warnings, err = TransformMeshGatewayInstance(raw, target)
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
		fixed, depWarns := ScanForDeprecations(doc, target)
		docs[i] = fixed
		warnings = append(warnings, depWarns...)
	}

	return docs, warnings, scenario, nil
}

// legacyPolicyDoc is the union of the two on-disk layouts a legacy policy can
// have. On Universal the body sits at the document root; on Kubernetes the legacy
// CRDs wrap it in spec, name it through metadata.name and identify it with kind
// rather than type. Reading only the Universal layout — which is what
// `extract --kumactl-context` produces — yields an empty policy for every legacy
// resource pulled with `extract --kube-context`.
type legacyPolicyDoc struct {
	// Universal layout (document root).
	UniversalPolicy `json:",inline"`

	// Kubernetes layout.
	Kind     string `json:"kind"`
	Metadata struct {
		Name      string            `json:"name"`
		Namespace string            `json:"namespace"`
		Labels    map[string]string `json:"labels"`
	} `json:"metadata"`
	Spec struct {
		Sources      []OldSelector   `json:"sources"`
		Destinations []OldSelector   `json:"destinations"`
		Selectors    []OldSelector   `json:"selectors"`
		Conf         json.RawMessage `json:"conf"`
	} `json:"spec"`
}

// parseLegacyPolicy reads a legacy policy in either layout into the flat
// UniversalPolicy the transforms operate on.
func parseLegacyPolicy(raw []byte) (UniversalPolicy, error) {
	var doc legacyPolicyDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return UniversalPolicy{}, fmt.Errorf("unmarshal legacy policy: %w", err)
	}

	policy := doc.UniversalPolicy
	if policy.Type == "" {
		policy.Type = doc.Kind
	}
	if policy.Name == "" {
		policy.Name = doc.Metadata.Name
	}
	if policy.Mesh == "" {
		policy.Mesh = doc.Metadata.Labels[meshLabel]
	}
	if len(policy.Sources) == 0 {
		policy.Sources = doc.Spec.Sources
	}
	if len(policy.Destinations) == 0 {
		policy.Destinations = doc.Spec.Destinations
	}
	if len(policy.Selectors) == 0 {
		policy.Selectors = doc.Spec.Selectors
	}
	if len(policy.Conf) == 0 {
		policy.Conf = doc.Spec.Conf
	}
	return policy, nil
}

// legacyPolicyShape describes how a legacy policy's selector fields map onto the
// targetRef API of its Mesh* successor.
type legacyPolicyShape int

const (
	// shapeOutbound: the policy configures the client side of a connection.
	// sources → spec.targetRef, destinations → to[].
	shapeOutbound legacyPolicyShape = iota
	// shapeInbound: the policy configures the server side. The mapping is
	// inverted — destinations → spec.targetRef, sources → from[].
	shapeInbound
	// shapeSelector: the policy has a single selectors[] list and no direction.
	// selectors → spec.targetRef, conf → spec.default.
	shapeSelector
)

// legacyShapes records the direction of every convertible legacy policy.
//
// FaultInjection and RateLimit are inbound policies just like TrafficPermission:
// the fault/limit is enforced by the sidecar of the service named in
// destinations, and sources selects the clients it applies to. Treating them as
// outbound (destinations → to[]) produces a policy that targets the wrong proxy.
//
// TrafficTrace and ProxyTemplate use selectors[] rather than sources/destinations.
var legacyShapes = map[string]legacyPolicyShape{
	"Timeout":           shapeOutbound,
	"CircuitBreaker":    shapeOutbound,
	"Retry":             shapeOutbound,
	"HealthCheck":       shapeOutbound,
	"TrafficLog":        shapeOutbound,
	"TrafficPermission": shapeInbound,
	"FaultInjection":    shapeInbound,
	"RateLimit":         shapeInbound,
	"TrafficTrace":      shapeSelector,
	"ProxyTemplate":     shapeSelector,
}

// transformScenarioLegacy converts a legacy Universal-style policy into one or more
// Kubernetes-style KubePolicy documents.
func transformScenarioLegacy(policy UniversalPolicy, opts TransformOptions) ([]KubePolicy, []string, error) {
	if policy.Type == "TrafficRoute" {
		return nil, nil, fmt.Errorf("TrafficRoute requires manual migration to MeshHTTPRoute or MeshTCPRoute")
	}
	if policy.Type == "VirtualOutbound" {
		return nil, nil, fmt.Errorf(
			"VirtualOutbound %q requires manual migration: its conf.host/conf.port templates render from "+
				"arbitrary Dataplane tags, which no single successor reproduces. Recreate the hostname with a "+
				"HostnameGenerator (spec.template over .DisplayName/.Namespace/.Zone/.Mesh plus the label "+
				"function — hostname only, no port templating) and the routing with MeshHTTPRoute/MeshTCPRoute, "+
				"as directed by the Kuma 3.0 upgrade notes", policy.Name)
	}

	newKind, err := OldTypeToNew(policy.Type)
	if err != nil {
		return nil, nil, err
	}

	// Universal policies have no namespace; leave it empty.
	const policyNamespace = ""

	// Convert the conf body before it is attached anywhere: legacy conf bodies
	// are not structurally compatible with the new default sections.
	conf, confWarnings, err := legacyDefaultSection(policy, opts)
	if err != nil {
		return nil, nil, err
	}

	var (
		policies []KubePolicy
		warnings []string
	)
	switch legacyShapes[policy.Type] {
	case shapeSelector:
		policies, warnings, err = transformSelectorLegacy(policy, newKind, policyNamespace, conf)
	case shapeInbound:
		policies, warnings, err = transformInboundLegacy(policy, newKind, policyNamespace, conf)
	default:
		policies, warnings, err = transformGenericLegacy(policy, newKind, policyNamespace, conf)
	}
	if err != nil {
		return nil, nil, err
	}

	// Carry the mesh association across. A legacy policy states its mesh in a
	// top-level `mesh` field; the Kubernetes-style output expresses it as a label.
	// Without this every converted policy lands in the "default" mesh.
	if policy.Mesh != "" {
		for i := range policies {
			if policies[i].Metadata.Labels == nil {
				policies[i].Metadata.Labels = map[string]string{}
			}
			policies[i].Metadata.Labels[meshLabel] = policy.Mesh
		}
	}

	return policies, append(confWarnings, warnings...), nil
}

// transformGenericLegacy handles outbound-shaped old-style policies.
// sources → spec.targetRef (topLevel=true → Dataplane), destinations → to[] (topLevel=false → MeshService).
func transformGenericLegacy(policy UniversalPolicy, newKind, policyNamespace string, conf json.RawMessage) ([]KubePolicy, []string, error) {
	var warnings []string

	// Build to[] from destinations. An empty destinations list means "every
	// destination" in the legacy API; without this the conf would have nowhere
	// to live and would be dropped silently.
	destinations := policy.Destinations
	if len(destinations) == 0 {
		destinations = []OldSelector{{}}
	}
	toEntries := make([]PolicyEntry, 0, len(destinations))
	for _, dest := range destinations {
		ref, warn := ConvertSelectorToTargetRef(dest, policyNamespace, false)
		toEntries = append(toEntries, PolicyEntry{TargetRef: ref, Default: conf})
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

// transformInboundLegacy handles the inverted (server-side) legacy policies:
// TrafficPermission, FaultInjection and RateLimit. destinations → spec.targetRef
// (topLevel=true → Dataplane), sources → from[] (topLevel=false → MeshService).
func transformInboundLegacy(policy UniversalPolicy, newKind, policyNamespace string, conf json.RawMessage) ([]KubePolicy, []string, error) {
	// Unlike the outbound shape, an empty destinations list is not defaulted to
	// the whole mesh here. These policies decide who may reach a service or how
	// hard it may be hit; silently widening spec.targetRef to kind: Mesh on
	// malformed input would relax a security or availability control. Kuma
	// rejects such a resource anyway, so error out and preserve the original.
	if len(policy.Destinations) == 0 {
		return nil, nil, fmt.Errorf(
			"%s %q has no destinations — set spec.targetRef manually rather than letting the policy "+
				"widen to the whole mesh", policy.Type, policy.Name)
	}

	var warnings []string

	sources := policy.Sources
	if len(sources) == 0 {
		sources = []OldSelector{{}}
	}

	// MeshRateLimit is the one inbound kind whose client selector cannot be
	// carried over at all: local rate limiting is enforced per inbound and its
	// from[] accepts only kind: Mesh. Upstream removed from[] entirely in 3.0 with
	// a mechanical rules[] equivalent for exactly this "all clients" case, so the
	// conversion emits rules[] — valid on 2.13+ and on 3.0.
	useRules := newKind == "MeshRateLimit"

	var fromEntries []PolicyEntry
	var ruleEntries []RuleEntry
	if useRules {
		for _, src := range policy.Sources {
			warnings = append(warnings, fmt.Sprintf(
				"RateLimit %q: source selector %v cannot be preserved — MeshRateLimit applies its limit to "+
					"every client of the targeted inbound (from[] accepts only kind: Mesh, and 3.0 replaces it "+
					"with the client-agnostic rules[]). The limit is now enforced for traffic from all clients, "+
					"not just this source. Review whether that is acceptable before applying.",
				policy.Name, src.Match))
		}
		ruleEntries = []RuleEntry{{Default: conf}}
	} else {
		fromEntries = make([]PolicyEntry, 0, len(sources))
		for _, src := range sources {
			ref, warn := ConvertSelectorToTargetRef(src, policyNamespace, false)
			if warn != "" {
				warnings = append(warnings, warn)
			}
			fromEntries = append(fromEntries, PolicyEntry{TargetRef: ref, Default: conf})
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
		if w := ValidateResourceName(name, newKind); w != "" {
			warnings = append(warnings, w)
		}
		out := buildKubePolicy(name, policyNamespace, newKind, destRef, nil, fromEntries)
		out.Spec.Rules = ruleEntries
		policies = append(policies, out)
	}
	return policies, warnings, nil
}

// transformSelectorLegacy handles the legacy policies scoped with selectors[]
// rather than sources/destinations: TrafficTrace and ProxyTemplate. Their
// successors (MeshTrace, MeshProxyPatch) have no to[]/from[] — the configuration
// sits at spec.default and spec.targetRef carries the selector.
func transformSelectorLegacy(policy UniversalPolicy, newKind, policyNamespace string, conf json.RawMessage) ([]KubePolicy, []string, error) {
	var warnings []string

	selectors := policy.Selectors
	if len(selectors) == 0 {
		selectors = []OldSelector{{}}
	}

	refs := make([]TargetRef, 0, len(selectors))
	for _, sel := range selectors {
		ref, warn := ConvertSelectorToTargetRef(sel, policyNamespace, true)
		refs = append(refs, ref)
		if warn != "" {
			warnings = append(warnings, warn)
		}
	}

	// All selectors resolve to the whole mesh — emit a single policy.
	if allKind(refs, "Mesh") {
		refs = []TargetRef{{Kind: "Mesh"}}
	}

	policies := make([]KubePolicy, 0, len(refs))
	for i, ref := range refs {
		name := policy.Name
		if len(refs) > 1 {
			name = fmt.Sprintf("%s-%d", policy.Name, i)
		}
		if w := ValidateResourceName(name, newKind); w != "" {
			warnings = append(warnings, w)
		}
		out := buildKubePolicy(name, policyNamespace, newKind, ref, nil, nil)
		out.Spec.Default = conf
		policies = append(policies, out)
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

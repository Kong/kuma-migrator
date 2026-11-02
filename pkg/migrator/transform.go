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
	// MeshIdentities records the meshes that have a MeshIdentity, which every
	// MeshExternalService needs on 3.0 (see warnMESRequiresMeshIdentity).
	MeshIdentities MeshIdentityIndex
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
		var err error
		docs, warnings, err = transformScenarioSubset(raw, target)
		if err != nil {
			return nil, nil, scenario, err
		}

	case ScenarioMesh:
		var err error
		docs, warnings, err = TransformMeshForTarget(raw, target)
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
		docs, warnings, err = TransformFromToRulesForTarget(raw, target)
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

	default: // ScenarioPassthrough, ScenarioSkipped, ScenarioUnknown
		docs = [][]byte{raw}
		if out, w, ok := transformPassthroughMeshForV3(raw, target); ok {
			docs, warnings = out, w
		}
	}

	// 3.0 removes the inline OpenTelemetry endpoint; generate MeshOpenTelemetryBackends.
	if target.IsV3() {
		var otelWarns []string
		docs, otelWarns = convertInlineOtelEndpointsForV3(docs)
		warnings = append(warnings, otelWarns...)
	}

	// The built-in gateway API is deleted outright in 3.0, so converting a MeshGateway or
	// MeshGatewayRoute is only half the job: the source object still has to go before the
	// upgrade, or the Helm CRD removal takes it with no record.
	if target.IsV3() {
		if note := removedGatewaySourceNote(scenario); note != "" {
			warnings = append(warnings, note)
		}
	}

	// Post-pass: scan every output document for deprecated fields.
	for i, doc := range docs {
		fixed, depWarns := ScanForDeprecations(doc, target)
		docs[i] = fixed
		warnings = append(warnings, depWarns...)
		warnings = append(warnings, warnMESRequiresMeshIdentity(fixed, opts)...)
	}

	// Guard: a generated document without kind/type or name lost its identity in a
	// conversion. Input passed through untouched is exempt — it is the user's own
	// document, not something this tool produced.
	for i, doc := range docs {
		passedThrough := (scenario == ScenarioPassthrough || scenario == ScenarioSkipped ||
			scenario == ScenarioUnknown) && string(doc) == string(raw)
		if passedThrough {
			continue
		}
		if err := unnamedOutputError(doc, i); err != nil {
			return nil, nil, scenario, err
		}
	}

	// Documents the target rejects as they stand are returned WITH the marker
	// error; see RejectedByTargetError for the contract.
	if err := rejectedByTarget(docs, target); err != nil {
		return docs, warnings, scenario, err
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
	target := opts.Target
	switch legacyShapes[policy.Type] {
	case shapeSelector:
		policies, warnings, err = transformSelectorLegacy(policy, newKind, policyNamespace, conf, target)
	case shapeInbound:
		policies, warnings, err = transformInboundLegacy(policy, newKind, policyNamespace, conf, target)
	default:
		policies, warnings, err = transformGenericLegacy(policy, newKind, policyNamespace, conf, target)
	}
	if err != nil {
		return nil, nil, err
	}

	// 3.0 removes MeshHealthCheck to[].default.healthyPanicThreshold; its home is
	// MeshCircuitBreaker outlierDetection. Move it into a companion policy.
	if target.IsV3() && newKind == "MeshHealthCheck" {
		var companions []KubePolicy
		for i := range policies {
			mcb, w, err := splitHealthyPanicThreshold(&policies[i])
			if err != nil {
				return nil, nil, err
			}
			warnings = append(warnings, w...)
			if mcb != nil {
				companions = append(companions, *mcb)
			}
		}
		policies = append(policies, companions...)
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
func transformGenericLegacy(policy UniversalPolicy, newKind, policyNamespace string, conf json.RawMessage, target TargetVersion) ([]KubePolicy, []string, error) {
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
		ref, warn, err := ConvertSelectorToTargetRefForTarget(dest, policyNamespace, false, target)
		if err != nil {
			return nil, nil, fmt.Errorf("%s %q destination: %w", policy.Type, policy.Name, err)
		}
		toEntries = append(toEntries, PolicyEntry{TargetRef: ref, Default: conf})
		if warn != "" {
			warnings = append(warnings, warn)
		}
	}

	// Resolve source TargetRefs (top-level → Dataplane in 2.13.x).
	sourceRefs := make([]TargetRef, 0, len(policy.Sources))
	for _, src := range policy.Sources {
		ref, warn, err := ConvertSelectorToTargetRefForTarget(src, policyNamespace, true, target)
		if err != nil {
			return nil, nil, fmt.Errorf("%s %q source: %w", policy.Type, policy.Name, err)
		}
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
func transformInboundLegacy(policy UniversalPolicy, newKind, policyNamespace string, conf json.RawMessage, target TargetVersion) ([]KubePolicy, []string, error) {
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

	// Kuma 3.0 removes spec.from from MeshTrafficPermission and MeshFaultInjection
	// (a policy still carrying it is rejected with "spec.from: unknown field"; an MTP
	// with no rules[] fails with "policy must define rules"). Their rules[] match
	// clients by SPIFFE ID / SNI, which the legacy tag selectors cannot be turned into.
	// The one mechanical case is a FaultInjection that applies to every client: a
	// MeshFaultInjection rules[] entry without matches[] means exactly that.
	if target.IsV3() {
		switch newKind {
		case "MeshTrafficPermission":
			return nil, nil, legacyInboundV3Error(policy, "MeshTrafficPermission",
				"rules[].default.{allow,deny,allowWithShadowDeny}[].spiffeID matches. Those need a "+
					"MeshIdentity and its trust domain, which this manifest does not carry. rules[] is "+
					"default-deny: write an allow entry for every client this TrafficPermission let in")
		case "MeshFaultInjection":
			if !allWildcardSources(policy.Sources) {
				return nil, nil, legacyInboundV3Error(policy, "MeshFaultInjection",
					"rules[].matches[] (spiffeID or sni) per client. A SPIFFE ID needs the client's "+
						"MeshIdentity trust domain, which this manifest does not carry. Only a FaultInjection "+
						"whose sources are all kuma.io/service: '*' converts automatically")
			}
			useRules = true
		}
	}

	var fromEntries []PolicyEntry
	var ruleEntries []RuleEntry
	if useRules {
		for _, src := range policy.Sources {
			if newKind != "MeshRateLimit" {
				// MeshFaultInjection only reaches here with wildcard sources: nothing is lost.
				break
			}
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
			if !target.IsV3() && meshSubsetFromKinds[newKind] {
				// 2.x: MeshService in from[] is deprecated since 2.7 for these kinds;
				// MeshSubset with the kuma.io/service tag is the documented form.
				fromEntries = append(fromEntries, PolicyEntry{TargetRef: fromSubsetRefV2(src), Default: conf})
				continue
			}
			ref, warn, err := ConvertSelectorToTargetRefForTarget(src, policyNamespace, false, target)
			if err != nil {
				return nil, nil, fmt.Errorf("%s %q source: %w", policy.Type, policy.Name, err)
			}
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
		destRef, warn, err := ConvertSelectorToTargetRefForTarget(dest, policyNamespace, true, target)
		if err != nil {
			return nil, nil, fmt.Errorf("%s %q destination: %w", policy.Type, policy.Name, err)
		}
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
func transformSelectorLegacy(policy UniversalPolicy, newKind, policyNamespace string, conf json.RawMessage, target TargetVersion) ([]KubePolicy, []string, error) {
	var warnings []string

	selectors := policy.Selectors
	if len(selectors) == 0 {
		selectors = []OldSelector{{}}
	}

	refs := make([]TargetRef, 0, len(selectors))
	for _, sel := range selectors {
		ref, warn, err := ConvertSelectorToTargetRefForTarget(sel, policyNamespace, true, target)
		if err != nil {
			return nil, nil, fmt.Errorf("%s %q selector: %w", policy.Type, policy.Name, err)
		}
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
//
// The document is handled as a generic map, so every field the converter does not
// rewrite survives: the Universal layout (top-level type/name/mesh/labels, the
// CP-written kri/creationTime/modificationTime) as well as Kubernetes metadata
// (annotations, …) and targetRef fields outside TargetRef (proxyTypes). Universal
// input therefore yields Universal output.
//
// Returns one document, or two when the from[] → rules[] second pass leaves both
// rules[] and to[] in the spec: Kuma rejects that combination ("fields 'to' must be
// empty when 'rules' is defined") on 2.x and 3.0 alike, so the policy is split the
// same way TransformFromToRules splits it — rules[] keeps the name, to[] moves to
// "<name>-outbound".
func transformScenarioSubset(raw []byte, target TargetVersion) ([][]byte, []string, error) {
	var obj map[string]interface{}
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		return nil, nil, fmt.Errorf("unmarshal policy: %w", err)
	}
	kind, _ := obj["kind"].(string)
	if kind == "" {
		kind, _ = obj["type"].(string)
	}
	name := extractNameFromObj(obj)
	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		return nil, nil, fmt.Errorf("%s %q: no spec to convert", kind, name)
	}
	ns := ""
	if md, ok := obj["metadata"].(map[string]interface{}); ok {
		ns, _ = md["namespace"].(string)
	}

	var warnings []string
	if tr, ok := spec["targetRef"].(map[string]interface{}); ok {
		out, warn, err := convertTargetRefMap(tr, func(ref TargetRef) (TargetRef, string, error) {
			return ConvertTargetRefForTarget(ref, ns, true, target)
		})
		if err != nil {
			return nil, nil, fmt.Errorf("%s %q spec.targetRef: %w", kind, name, err)
		}
		spec["targetRef"] = out
		if warn != "" {
			warnings = append(warnings, warn)
		}
	}
	to, _ := spec["to"].([]interface{})
	for i, entry := range to {
		em, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		tr, ok := em["targetRef"].(map[string]interface{})
		if !ok {
			continue
		}
		out, warn, err := convertTargetRefMap(tr, func(ref TargetRef) (TargetRef, string, error) {
			return ConvertTargetRefForTarget(ref, ns, false, target)
		})
		if err != nil {
			return nil, nil, fmt.Errorf("%s %q spec.to[%d].targetRef: %w", kind, name, i, err)
		}
		em["targetRef"] = out
		if warn != "" {
			warnings = append(warnings, warn)
		}
	}
	from, _ := spec["from"].([]interface{})
	for _, entry := range from {
		em, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		tr, ok := em["targetRef"].(map[string]interface{})
		if !ok {
			continue
		}
		// A from[] ref that cannot be expressed on 3.0 is not fatal here: from[] is
		// either rewritten to rules[] below (source refs dropped) or, for
		// MeshTrafficPermission/MeshFaultInjection, reported as rejected by the
		// target (RejectedByTargetError).
		out, warn, _ := convertTargetRefMap(tr, func(ref TargetRef) (TargetRef, string, error) {
			return fromRefForTarget(ref, kind, ns, target)
		})
		em["targetRef"] = out
		if warn != "" {
			warnings = append(warnings, warn)
		}
	}

	// Second pass: if this kind uses the Rules API and still has from[], migrate it.
	if rulesAPIMigrationKinds[kind] && len(from) > 0 {
		var widening []string
		if target.IsV3() {
			widening = fromScopeWideningWarnings(kind, name, fromSourceKinds(from))
		}
		w, _ := applyFromToRulesMap(obj, kind, name)
		warnings = append(warnings, w...)
		warnings = append(warnings, widening...)
	}

	objs := []map[string]interface{}{obj}
	if rulesAPIMigrationKinds[kind] {
		split, warn, err := splitRulesAndToMap(obj, kind, name)
		if err != nil {
			return nil, warnings, err
		}
		objs = split
		if warn != "" {
			warnings = append(warnings, warn)
		}
	}

	docs := make([][]byte, 0, len(objs))
	for _, o := range objs {
		b, err := yaml.Marshal(o)
		if err != nil {
			return nil, warnings, fmt.Errorf("marshal output policy: %w", err)
		}
		docs = append(docs, b)
	}
	return docs, warnings, nil
}

// targetRefKeys are the targetRef fields TargetRef models. convertTargetRefMap
// carries every other key (proxyTypes, …) over from the input unchanged.
var targetRefKeys = map[string]bool{
	"kind": true, "name": true, "namespace": true, "tags": true,
	"labels": true, "mesh": true, "sectionName": true,
}

// convertTargetRefMap runs a typed TargetRef conversion on a targetRef held as a
// generic map. When conversion changes nothing the input map is returned as is;
// otherwise the converted fields replace the modelled ones and every unmodelled
// key is preserved.
func convertTargetRefMap(in map[string]interface{}, conv func(TargetRef) (TargetRef, string, error)) (map[string]interface{}, string, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return in, "", err
	}
	var ref TargetRef
	if err := json.Unmarshal(b, &ref); err != nil {
		return in, "", fmt.Errorf("parse targetRef: %w", err)
	}
	converted, warn, err := conv(ref)
	if err != nil {
		return in, warn, err
	}
	cb, err := json.Marshal(converted)
	if err != nil {
		return in, warn, err
	}
	if string(cb) == mustJSON(ref) {
		return in, warn, nil
	}
	var out map[string]interface{}
	if err := json.Unmarshal(cb, &out); err != nil {
		return in, warn, err
	}
	for k, v := range in {
		if !targetRefKeys[k] {
			out[k] = v
		}
	}
	return out, warn, nil
}

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// splitWarning is the advisory emitted whenever a policy is split into a rules[]
// half and a to[] half.
func splitWarning(kind, name, outboundName string) string {
	return fmt.Sprintf(
		"%s %q: policy has both from[] and to[] — Kuma 2.10+ forbids rules[] and to[] "+
			"in the same spec. Split into two policies: %q (rules[], inbound) and %q (to[], outbound). "+
			"Apply both.",
		kind, name, name, outboundName)
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
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

// allWildcardSources reports whether every legacy source selects every client:
// no sources, an empty match, or a match that is exactly kuma.io/service: '*'.
func allWildcardSources(sources []OldSelector) bool {
	for _, src := range sources {
		switch len(src.Match) {
		case 0:
		case 1:
			if src.Match["kuma.io/service"] != "*" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// legacyInboundV3Error is the error returned for a legacy inbound policy whose
// Kuma 3.0 successor has no from[] and needs client identities the manifest does
// not hold. The migrator preserves the original document, as for TrafficRoute.
func legacyInboundV3Error(policy UniversalPolicy, newKind, rewrite string) error {
	return fmt.Errorf(
		"%s %q requires manual migration for Kuma 3.0: 3.0 removes %s spec.from (rejected as "+
			"\"spec.from: unknown field\"), and its replacement selects clients with %s. "+
			"Re-run with --to-latest v2 to get the from[]-based %s, which 2.x still accepts",
		policy.Type, policy.Name, newKind, rewrite, newKind)
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

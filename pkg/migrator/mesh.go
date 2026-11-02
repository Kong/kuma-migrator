package migrator

import (
	"encoding/json"
	"fmt"
	"strings"

	"sigs.k8s.io/yaml"
)

// ---- Old Mesh section structs (input parsing) --------------------------------

type oldMeshMetricsSection struct {
	EnabledBackend string              `json:"enabledBackend"`
	Backends       []oldMetricsBackend `json:"backends"`
}

type oldMetricsBackend struct {
	Name string          `json:"name"`
	Type string          `json:"type"` // "prometheus"
	Conf json.RawMessage `json:"conf"`
}

type oldPrometheusConf struct {
	SkipMTLS bool              `json:"skipMTLS"`
	Port     int               `json:"port,omitempty"`
	Path     string            `json:"path"`
	Tags     map[string]string `json:"tags,omitempty"`
}

type oldMeshTracingSection struct {
	DefaultBackend string              `json:"defaultBackend"`
	Backends       []oldTracingBackend `json:"backends"`
}

type oldTracingBackend struct {
	Name     string          `json:"name"`
	Type     string          `json:"type"`               // "zipkin", "datadog"
	Sampling *float64        `json:"sampling,omitempty"` // 0–100
	Conf     json.RawMessage `json:"conf"`
}

type oldZipkinConf struct {
	URL           string `json:"url"`
	TraceId128bit *bool  `json:"traceId128bit,omitempty"`
	APIVersion    string `json:"apiVersion,omitempty"`
	SharedSpan    *bool  `json:"sharedSpanContext,omitempty"`
}

type oldDatadogConf struct {
	Address      string `json:"address"`
	SplitService *bool  `json:"splitService,omitempty"`
}

type oldMeshLoggingSection struct {
	DefaultBackend string              `json:"defaultBackend"`
	Backends       []oldLoggingBackend `json:"backends"`
}

type oldLoggingBackend struct {
	Name string          `json:"name"`
	Type string          `json:"type"` // "file", "tcp"
	Conf json.RawMessage `json:"conf"`
}

type oldFileLogConf struct {
	Path string `json:"path"`
}

type oldTCPLogConf struct {
	Address string `json:"address"`
}

// ---- Detection ---------------------------------------------------------------

// meshNeedsMigration returns true when the Mesh CRD contains sections that
// must be extracted into standalone observability CRDs, OR when
// spec.meshServices.mode is not already set to "Exclusive".
func meshNeedsMigration(raw []byte) bool {
	// Support both Kubernetes-style (fields under spec) and Universal-style
	// (fields at top level, no spec wrapper).
	type meshProbe struct {
		Spec struct {
			Metrics      interface{}            `json:"metrics"`
			Tracing      interface{}            `json:"tracing"`
			Logging      interface{}            `json:"logging"`
			Networking   interface{}            `json:"networking"`
			MeshServices map[string]interface{} `json:"meshServices"`
		} `json:"spec"`
		// Universal-format: same fields at top level.
		Metrics      interface{}            `json:"metrics"`
		Tracing      interface{}            `json:"tracing"`
		Logging      interface{}            `json:"logging"`
		Networking   interface{}            `json:"networking"`
		MeshServices map[string]interface{} `json:"meshServices"`
	}
	var p meshProbe
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return false
	}

	// Resolve effective values: Kubernetes spec takes precedence; Universal top-level is fallback.
	metrics := p.Spec.Metrics
	if metrics == nil {
		metrics = p.Metrics
	}
	tracing := p.Spec.Tracing
	if tracing == nil {
		tracing = p.Tracing
	}
	logging := p.Spec.Logging
	if logging == nil {
		logging = p.Logging
	}
	networking := p.Spec.Networking
	if networking == nil {
		networking = p.Networking
	}
	meshServices := p.Spec.MeshServices
	if meshServices == nil {
		meshServices = p.MeshServices
	}

	if metrics != nil || tracing != nil || logging != nil {
		return true
	}
	// Check networking.outbound.passthrough specifically.
	if net, ok := networking.(map[string]interface{}); ok {
		if out, ok := net["outbound"].(map[string]interface{}); ok {
			if _, ok := out["passthrough"]; ok {
				return true
			}
		}
	}
	// meshServices.mode must be Exclusive for MeshService-based policies to work.
	if meshServices["mode"] != "Exclusive" {
		return true
	}
	return false
}

// ---- Main transformer --------------------------------------------------------

// TransformMesh transforms an old Mesh CRD into:
//  1. The cleaned Mesh CRD (metrics/tracing/logging/passthrough sections removed)
//  2. Zero or more companion CRDs (MeshMetric, MeshTrace, MeshAccessLog, MeshPassthrough)
//
// The cleaned Mesh is always the first document returned. TransformMesh targets
// the 2.x line; use TransformMeshForTarget for a 3.0 target.
func TransformMesh(raw []byte) ([][]byte, []string, error) {
	return TransformMeshForTarget(raw, TargetV2)
}

// TransformMeshForTarget is TransformMesh for an explicit target. Under v3 it
// also removes the Mesh fields 3.0 drops (mtls, routing.*, skipCreatingInitialPolicies)
// and emits the companion policies needed to keep their behaviour (see v3_mesh.go).
func TransformMeshForTarget(raw []byte, target TargetVersion) ([][]byte, []string, error) {
	return TransformMeshWithOptions(raw, TransformOptions{Target: target})
}

// TransformMeshWithOptions is TransformMeshForTarget with the cross-document
// context from the pre-pass: the system namespace the companions go in, and the
// TrafficLog/TrafficTrace references to the Mesh's logging/tracing backends.
func TransformMeshWithOptions(raw []byte, opts TransformOptions) ([][]byte, []string, error) {
	target := opts.Target
	// Parse as a generic map so all unrecognised fields are preserved.
	var obj map[string]interface{}
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		return nil, nil, fmt.Errorf("unmarshal Mesh: %w", err)
	}

	meshName, meshNamespace := extractMeshMeta(obj)

	// Companions follow the format of the Mesh: Universal input gets Universal
	// companions (type/name/mesh), Kubernetes input gets CRDs in the system namespace.
	cm := companionMeta{
		Mesh:      meshName,
		Namespace: resolveSystemNamespace(opts, meshNamespace),
		Universal: isUniversalDoc(obj),
	}

	spec, _ := obj["spec"].(map[string]interface{})
	if spec == nil {
		// Universal format: fields (meshServices, metrics, etc.) live at the top level
		// with no spec wrapper. Point spec at obj so the rest of the logic works uniformly.
		spec = obj
	}

	var companions [][]byte
	var warnings []string

	// ── metrics → MeshMetric ──────────────────────────────────────────────────
	// metrics.enabledBackend turns Prometheus metrics on for every proxy in the
	// mesh, so a mesh-wide MeshMetric is the equivalent.
	if rawMetrics, ok := spec["metrics"]; ok {
		b, _ := json.Marshal(rawMetrics)
		var m oldMeshMetricsSection
		if err := json.Unmarshal(b, &m); err == nil {
			crds, w := metricsToMeshMetric(m, cm)
			companions = append(companions, crds...)
			warnings = append(warnings, w...)
		}
		delete(spec, "metrics")
	}

	// ── tracing / logging ─────────────────────────────────────────────────────
	// On 2.x these sections only declare backends. Nothing is traced or logged
	// until a TrafficTrace/TrafficLog selects traffic and names a backend (or
	// falls back to defaultBackend), so no mesh-wide MeshTrace/MeshAccessLog is
	// generated: that would turn tracing/logging on for every proxy. The backend
	// is carried into the MeshTrace/MeshAccessLog converted from each
	// TrafficTrace/TrafficLog (resolveTracingBackendRef / resolveLoggingBackendRef).
	if rawTracing, ok := spec["tracing"]; ok {
		b, _ := json.Marshal(rawTracing)
		var t oldMeshTracingSection
		if err := json.Unmarshal(b, &t); err == nil {
			names := make([]string, 0, len(t.Backends))
			for _, be := range t.Backends {
				names = append(names, be.Name)
			}
			warnings = append(warnings, meshBackendsUsageWarning(meshName, "tracing", "TrafficTrace",
				"MeshTrace", names, t.DefaultBackend, opts))
		}
		delete(spec, "tracing")
	}

	if rawLogging, ok := spec["logging"]; ok {
		b, _ := json.Marshal(rawLogging)
		var l oldMeshLoggingSection
		if err := json.Unmarshal(b, &l); err == nil {
			names := make([]string, 0, len(l.Backends))
			for _, be := range l.Backends {
				names = append(names, be.Name)
			}
			warnings = append(warnings, meshBackendsUsageWarning(meshName, "logging", "TrafficLog",
				"MeshAccessLog", names, l.DefaultBackend, opts))
		}
		delete(spec, "logging")
	}

	// ── networking.outbound.passthrough → MeshPassthrough ─────────────────────
	passthroughSet := false
	if networking, ok := spec["networking"].(map[string]interface{}); ok {
		if outbound, ok := networking["outbound"].(map[string]interface{}); ok {
			if pt, exists := outbound["passthrough"]; exists {
				passthroughSet = true
				if ptBool, _ := pt.(bool); !ptBool {
					// passthrough=false → generate MeshPassthrough with passthroughMode: None
					if crd, ptWarns, err := buildMeshPassthroughMode(cm, "None"); err == nil {
						companions = append(companions, crd)
						warnings = append(warnings, ptWarns...)
						warnings = append(warnings,
							"Mesh.spec.networking.outbound.passthrough=false has been extracted to a MeshPassthrough CRD with passthroughMode: None")
					}
				} else {
					crds, w := passthroughTrueCompanion(cm, target)
					companions = append(companions, crds...)
					warnings = append(warnings, w...)
				}
				delete(outbound, "passthrough")
				if len(outbound) == 0 {
					delete(networking, "outbound")
				}
				if len(networking) == 0 {
					delete(spec, "networking")
				}
			}
		}
	}
	if !passthroughSet && target.IsV3() {
		warnings = append(warnings, passthroughUnsetV3Warning(meshName))
	}

	// ── meshServices.mode: Exclusive ─────────────────────────────────────────────
	// Without Exclusive mode, Kuma keeps generating legacy kuma.io/service tags
	// alongside MeshService resources, so new-style policies would not take effect.
	meshSvc, _ := spec["meshServices"].(map[string]interface{})
	if meshSvc == nil {
		meshSvc = map[string]interface{}{}
	}
	if meshSvc["mode"] != "Exclusive" {
		meshSvc["mode"] = "Exclusive"
		spec["meshServices"] = meshSvc
		warnings = append(warnings,
			"Mesh.spec.meshServices.mode set to Exclusive — required for MeshService-based policies to take effect (Kuma 2.9+). "+
				"Verify that all policies and workloads in this mesh have been migrated before applying."+
				meshServicesV3Note(target))
	} else if target.IsV3() {
		warnings = append(warnings, fmt.Sprintf(
			"Mesh %q: keep spec.meshServices.mode: Exclusive.%s", meshName, meshServicesV3Note(target)))
	}

	// ── 3.0 schema removals ───────────────────────────────────────────────────
	if target.IsV3() {
		crds, w := applyMeshRemovalsForV3(obj, spec, cm)
		companions = append(companions, crds...)
		warnings = append(warnings, w...)
	}

	// Marshal the cleaned Mesh CRD.
	cleaned, err := yaml.Marshal(obj)
	if err != nil {
		return nil, warnings, fmt.Errorf("marshal cleaned Mesh: %w", err)
	}

	return append([][]byte{cleaned}, companions...), warnings, nil
}

// ---- Companion CRD builders --------------------------------------------------

// metricsToMeshMetric converts the enabled Mesh metrics backend into a mesh-wide
// MeshMetric. Only metrics.enabledBackend is active on 2.x: the other entries in
// metrics.backends are declarations, and a Mesh without enabledBackend exposes
// no metrics, so neither produces a MeshMetric.
func metricsToMeshMetric(m oldMeshMetricsSection, cm companionMeta) ([][]byte, []string) {
	meshName := cm.Mesh
	var warnings []string
	var backends []map[string]interface{}

	if m.EnabledBackend == "" {
		if len(m.Backends) == 0 {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf(
			"Mesh %q: spec.metrics declares backends but sets no enabledBackend, so metrics were not "+
				"enabled on 2.x. No MeshMetric was generated and nothing is lost. Write a MeshMetric if "+
				"you want metrics.", meshName)}
	}
	var enabled []oldMetricsBackend
	for _, b := range m.Backends {
		if b.Name == m.EnabledBackend {
			enabled = append(enabled, b)
		}
	}
	if len(enabled) == 0 {
		return nil, []string{fmt.Sprintf(
			"Mesh %q: spec.metrics.enabledBackend %q is not declared in spec.metrics.backends — no "+
				"MeshMetric was generated; write one by hand.", meshName, m.EnabledBackend)}
	}
	if len(m.Backends) > len(enabled) {
		warnings = append(warnings, fmt.Sprintf(
			"Mesh %q: only the enabled metrics backend %q was converted to a MeshMetric; the other "+
				"spec.metrics.backends entries were not enabled on 2.x and were dropped.",
			meshName, m.EnabledBackend))
	}

	for _, b := range enabled {
		switch strings.ToLower(b.Type) {
		case "prometheus":
			var conf oldPrometheusConf
			_ = json.Unmarshal(b.Conf, &conf)

			promConf := map[string]interface{}{}
			if conf.Port != 0 {
				promConf["port"] = conf.Port
			}
			if conf.Path != "" {
				promConf["path"] = conf.Path
			}
			backends = append(backends, map[string]interface{}{
				"type":       "Prometheus",
				"prometheus": promConf,
			})

			if conf.SkipMTLS {
				warnings = append(warnings,
					"MeshMetric: 'skipMTLS' from Mesh.spec.metrics.conf has no direct equivalent in MeshMetric — "+
						"review your TLS configuration for the metrics endpoint")
			}
			if len(conf.Tags) > 0 {
				warnings = append(warnings,
					"MeshMetric: 'tags' from Mesh.spec.metrics.conf (used for securing metrics with TrafficPermission) "+
						"has no direct equivalent — use MeshMetric TLS mode and MeshTrafficPermission targeting the MeshService instead")
			}

		default:
			warnings = append(warnings,
				fmt.Sprintf("MeshMetric: unsupported backend type %q — migrate this backend manually", b.Type))
		}
	}

	if len(backends) == 0 {
		return nil, warnings
	}

	outName := meshName + "-metrics"
	if w := ValidateResourceName(outName, "MeshMetric"); w != "" {
		warnings = append(warnings, w)
	}
	crd := buildObservabilityCRD("MeshMetric", outName, cm, map[string]interface{}{
		"backends": backends,
	})
	b, err := yaml.Marshal(crd)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("MeshMetric marshal error: %v", err))
		return nil, warnings
	}
	return [][]byte{b}, warnings
}

// meshBackendsUsageWarning explains what happened to Mesh spec.logging or
// spec.tracing. section is "logging"/"tracing", policyKind the legacy policy that
// activates a backend (TrafficLog/TrafficTrace) and newKind the policy it is
// converted to (MeshAccessLog/MeshTrace).
//
// When the pre-pass indexed the input (opts.LegacyObservability != nil) the
// warning names the backends some TrafficLog/TrafficTrace references and the
// ones no policy references (inactive on 2.x, so nothing is lost). Without the
// index it states the rule.
func meshBackendsUsageWarning(meshName, section, policyKind, newKind string, backends []string,
	defaultBackend string, opts TransformOptions) string {
	head := fmt.Sprintf(
		"Mesh %q: spec.%s removed from the Mesh output. On 2.x these backends only declare where "+
			"data goes; a %s activates one for the traffic it selects. No mesh-wide %s was generated, "+
			"because that would enable %s for every proxy in the mesh.",
		meshName, section, policyKind, newKind, section)
	if opts.LegacyObservability == nil {
		return head + fmt.Sprintf(
			" The %s converted from each %s carries its backend inline, resolved from this Mesh when it "+
				"is in the same input. A backend no %s references was inactive, so dropping it loses nothing.",
			newKind, policyKind, policyKind)
	}

	used := opts.LegacyObservability.referencedBackends(meshName, policyKind, defaultBackend)
	var active, inactive []string
	for _, b := range backends {
		if used[b] > 0 {
			active = append(active, fmt.Sprintf("%s (%d %s)", b, used[b], policyKind))
		} else {
			inactive = append(inactive, b)
		}
	}
	msg := head
	if len(active) > 0 {
		msg += fmt.Sprintf(" Referenced backends, carried inline into the %s generated from each %s: %s.",
			newKind, policyKind, strings.Join(active, ", "))
	}
	if len(inactive) > 0 {
		msg += fmt.Sprintf(" No %s in the input references %s, so %s inactive on 2.x and nothing is lost "+
			"(if %s resources exist on the control plane but were not extracted, add them to the input).",
			policyKind, strings.Join(inactive, ", "), pluralIsWas(len(inactive)), policyKind)
	}
	return msg
}

func pluralIsWas(n int) string {
	if n == 1 {
		return "it was"
	}
	return "they were"
}

// buildMeshPassthroughMode builds a Mesh-wide MeshPassthrough with the given
// passthroughMode (None or All).
func buildMeshPassthroughMode(cm companionMeta, mode string) ([]byte, []string, error) {
	outName := cm.Mesh + "-passthrough"
	var warnings []string
	if w := ValidateResourceName(outName, "MeshPassthrough"); w != "" {
		warnings = append(warnings, w)
	}
	crd := buildObservabilityCRD("MeshPassthrough", outName, cm, map[string]interface{}{
		"passthroughMode": mode,
	})
	b, err := yaml.Marshal(crd)
	return b, warnings, err
}

// ---- Generic companion constructor --------------------------------------------

// defaultSystemNamespace is the Kuma OSS control plane namespace. Kong Mesh
// installs into kong-mesh-system; resolveSystemNamespace picks between them.
const defaultSystemNamespace = "kuma-system"

// companionMeta describes where a generated companion resource goes: its mesh,
// the Kubernetes system namespace, and whether it is written in Universal format.
type companionMeta struct {
	Mesh      string
	Namespace string
	Universal bool
}

// resolveSystemNamespace returns the namespace generated mesh-scoped companions
// (MeshMetric, MeshPassthrough, MeshTLS, MeshOpenTelemetryBackend,
// MeshExternalService) go in on Kubernetes: the config-file override or the value
// inferred by the pre-pass (opts.SystemNamespace), else the namespace the source
// document itself carries, else kuma-system.
func resolveSystemNamespace(opts TransformOptions, docNamespace string) string {
	if opts.SystemNamespace != "" {
		return opts.SystemNamespace
	}
	if docNamespace != "" {
		return docNamespace
	}
	return defaultSystemNamespace
}

// isUniversalDoc reports whether a decoded document uses the Universal layout
// (type instead of kind).
func isUniversalDoc(obj map[string]interface{}) bool {
	if k, _ := obj["kind"].(string); k != "" {
		return false
	}
	t, _ := obj["type"].(string)
	return t != ""
}

// buildCompanion builds a generated resource in the format cm asks for:
// Universal (type/name/mesh/spec) or Kubernetes (apiVersion/kind/metadata/spec,
// in cm.Namespace, mesh as the kuma.io/mesh label). An empty cm.Mesh leaves the
// mesh out; an empty cm.Namespace leaves the namespace out.
func buildCompanion(kind, name string, cm companionMeta, spec map[string]interface{}) map[string]interface{} {
	if cm.Universal {
		return universalDoc(kind, name, cm.Mesh, nil, spec)
	}
	meta := map[string]interface{}{
		"name": name,
	}
	if cm.Namespace != "" {
		meta["namespace"] = cm.Namespace
	}
	if cm.Mesh != "" {
		meta["labels"] = map[string]interface{}{meshLabel: cm.Mesh}
	}
	return map[string]interface{}{
		"apiVersion": kumaAPIVersion,
		"kind":       kind,
		"metadata":   meta,
		"spec":       spec,
	}
}

// universalDoc renders a Kuma resource in the Universal layout that kumactl and
// the API server use for new-style resources: top-level type, name, mesh and
// labels, with the body under spec:
//
//	type: MeshTimeout
//	name: backend
//	mesh: default
//	labels: {team: payments}
//	spec: {targetRef: ..., to: [...]}
//
// The kuma.io/mesh label is dropped from labels: on Universal the mesh is the
// top-level field. An empty mesh or labels leaves the key out. spec may be a map
// or a typed struct (KubePolicySpec keeps its field order that way).
func universalDoc(kind, name, mesh string, labels map[string]string, spec interface{}) map[string]interface{} {
	out := map[string]interface{}{
		"type": kind,
		"name": name,
		"spec": spec,
	}
	if mesh != "" {
		out["mesh"] = mesh
	}
	l := map[string]interface{}{}
	for k, v := range labels {
		if k != meshLabel {
			l[k] = v
		}
	}
	if len(l) > 0 {
		out["labels"] = l
	}
	return out
}

// kubePolicyAsUniversal re-renders a policy built as a KubePolicy in the
// Universal layout. The mesh comes from the kuma.io/mesh label; extraLabels
// (the source document's own top-level labels) are carried over beneath the
// policy's labels.
func kubePolicyAsUniversal(p KubePolicy, extraLabels map[string]string) map[string]interface{} {
	labels := copyStringMap(extraLabels)
	if labels == nil {
		labels = map[string]string{}
	}
	for k, v := range p.Metadata.Labels {
		labels[k] = v
	}
	return universalDoc(p.Kind, p.Metadata.Name, p.Metadata.Labels[meshLabel], labels, p.Spec)
}

// buildObservabilityCRD builds a mesh-wide companion policy (MeshMetric,
// MeshPassthrough) whose spec.default is defaultSection.
func buildObservabilityCRD(kind, name string, cm companionMeta, defaultSection map[string]interface{}) map[string]interface{} {
	return buildCompanion(kind, name, cm, map[string]interface{}{
		"targetRef": map[string]interface{}{"kind": "Mesh"},
		"default":   defaultSection,
	})
}

// ---- Helpers -----------------------------------------------------------------

// extractMeshMeta returns the name of a Mesh and the namespace its document
// carries ("" when none: Mesh is cluster-scoped, so it normally has none).
// Supports both Kubernetes-style (metadata.name) and Universal-style (top-level
// name).
func extractMeshMeta(obj map[string]interface{}) (name, namespace string) {
	if meta, ok := obj["metadata"].(map[string]interface{}); ok {
		if n, ok := meta["name"].(string); ok {
			name = n
		}
		if ns, ok := meta["namespace"].(string); ok {
			namespace = ns
		}
	}
	// Universal style: top-level "name" field.
	if name == "" {
		if n, ok := obj["name"].(string); ok {
			name = n
		}
	}
	return name, namespace
}

package migrator

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"sigs.k8s.io/yaml"
)

// TrafficLog and TrafficTrace do not carry their own configuration: conf.backend
// is the *name* of a backend declared on the Mesh resource
// (spec.logging.backends[] / spec.tracing.backends[]). MeshAccessLog and MeshTrace
// have no such indirection — the backend is declared inline in the policy.
//
// Converting these two kinds therefore needs a document the policy does not
// contain. MeshBackendIndex is built in a pre-pass over the whole input tree and
// threaded into the transform through TransformOptions. When the Mesh resource is
// not part of the input (a partial extract, or a single file), the conversion
// degrades to a warning naming the backend that has to be written by hand.

// indexedDocPattern matches the kind/type line of the resources the pre-pass
// indexes, in either layout. It is a cheap prefilter: without it the pre-pass
// unmarshals every YAML file in the tree.
var indexedDocPattern = regexp.MustCompile(`(?m)^\s*(kind|type)\s*:\s*["']?(Mesh|MeshGatewayInstance|MeshIdentity|TrafficLog|TrafficTrace)["']?\s*$`)

// System-namespace evidence for inferSystemNamespace. A resource stored in one of
// the two control-plane namespaces names it directly; a Kong Mesh-only kind means
// the default Kong Mesh install namespace.
var (
	kongMeshNamespacePattern = regexp.MustCompile(`(?m)^\s*namespace\s*:\s*["']?kong-mesh-system["']?\s*$`)
	kumaNamespacePattern     = regexp.MustCompile(`(?m)^\s*namespace\s*:\s*["']?kuma-system["']?\s*$`)
	kongMeshKindPattern      = regexp.MustCompile(`(?m)^\s*(kind|type)\s*:\s*["']?(OPAPolicy|MeshOPA|MeshGlobalRateLimit|AccessRole|AccessRoleBinding|AccessAudit)["']?\s*$`)
)

// kongMeshSystemNamespace is the namespace the Kong Mesh Helm chart installs into.
const kongMeshSystemNamespace = "kong-mesh-system"

// systemNamespaceEvidence accumulates what the pre-pass saw.
type systemNamespaceEvidence struct {
	kongMeshNS, kumaNS, kongMeshKind bool
}

func (e *systemNamespaceEvidence) scan(data []byte) {
	if !e.kongMeshNS && kongMeshNamespacePattern.Match(data) {
		e.kongMeshNS = true
	}
	if !e.kumaNS && kumaNamespacePattern.Match(data) {
		e.kumaNS = true
	}
	if !e.kongMeshKind && kongMeshKindPattern.Match(data) {
		e.kongMeshKind = true
	}
}

// inferSystemNamespace picks the control-plane namespace from the evidence: a
// document stored in kong-mesh-system, else one stored in kuma-system, else a
// Kong Mesh-only kind (kong-mesh-system). "" when there is no evidence, which
// resolveSystemNamespace turns into kuma-system.
func (e systemNamespaceEvidence) infer() string {
	switch {
	case e.kongMeshNS:
		return kongMeshSystemNamespace
	case e.kumaNS:
		return defaultSystemNamespace
	case e.kongMeshKind:
		return kongMeshSystemNamespace
	}
	return ""
}

// legacyBackendUse records, for one mesh, the backend each TrafficLog/TrafficTrace
// names in conf.backend ("" when it relies on the Mesh defaultBackend), counted
// per backend name.
type legacyBackendUse struct {
	TrafficLog   map[string]int
	TrafficTrace map[string]int
}

// LegacyObservabilityIndex maps a mesh name to the logging/tracing backend
// references of its TrafficLog/TrafficTrace resources. The pre-pass always sets
// it (possibly empty), so a non-nil index means "every input file was seen".
type LegacyObservabilityIndex map[string]*legacyBackendUse

// referencedBackends returns how many policyKind ("TrafficLog"/"TrafficTrace")
// resources of meshName reference each backend, with references that omit
// conf.backend attributed to defaultBackend.
func (idx LegacyObservabilityIndex) referencedBackends(meshName, policyKind, defaultBackend string) map[string]int {
	out := map[string]int{}
	use := idx[meshName]
	if use == nil {
		return out
	}
	refs := use.TrafficLog
	if policyKind == "TrafficTrace" {
		refs = use.TrafficTrace
	}
	for name, n := range refs {
		if name == "" {
			name = defaultBackend
		}
		if name != "" {
			out[name] += n
		}
	}
	return out
}

func (idx LegacyObservabilityIndex) add(kind, mesh, backend string) {
	use := idx[mesh]
	if use == nil {
		use = &legacyBackendUse{TrafficLog: map[string]int{}, TrafficTrace: map[string]int{}}
		idx[mesh] = use
	}
	if kind == "TrafficTrace" {
		use.TrafficTrace[backend]++
	} else {
		use.TrafficLog[backend]++
	}
}

// parseLegacyBackendUse returns the kind ("TrafficLog"/"TrafficTrace"), mesh and
// conf.backend of a legacy observability policy, or kind "" for any other document.
// Kubernetes keeps mesh at the top level (or as the kuma.io/mesh label) and conf
// under spec; Universal keeps both at the top level.
func parseLegacyBackendUse(raw []byte) (kind, mesh, backend string) {
	var probe struct {
		Kind     string `json:"kind"`
		Type     string `json:"type"`
		Mesh     string `json:"mesh"`
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
		Spec struct {
			Conf *legacyBackendRef `json:"conf"`
		} `json:"spec"`
		Conf *legacyBackendRef `json:"conf"`
	}
	if err := yaml.Unmarshal(raw, &probe); err != nil {
		return "", "", ""
	}
	kind = probe.Kind
	if kind == "" {
		kind = probe.Type
	}
	if kind != "TrafficLog" && kind != "TrafficTrace" {
		return "", "", ""
	}
	mesh = probe.Mesh
	if mesh == "" {
		mesh = probe.Metadata.Labels[meshLabel]
	}
	if mesh == "" {
		mesh = "default"
	}
	conf := probe.Spec.Conf
	if conf == nil {
		conf = probe.Conf
	}
	if conf != nil {
		backend = conf.Backend
	}
	return kind, mesh, backend
}

// meshBackends holds the observability backends declared by one Mesh resource.
type meshBackends struct {
	Logging               map[string]oldLoggingBackend
	Tracing               map[string]oldTracingBackend
	DefaultLoggingBackend string
	DefaultTracingBackend string
}

// MeshBackendIndex maps a mesh name to the backends its Mesh resource declares.
type MeshBackendIndex map[string]*meshBackends

// BuildTransformOptions walks inputDir once and builds every cross-document index
// the transforms need. Errors on individual files are ignored: the indexes are
// best-effort context, and every consumer degrades to a warning when a lookup
// misses.
func BuildTransformOptions(inputDir string, target TargetVersion) TransformOptions {
	backends := MeshBackendIndex{}
	classes := GatewayClassIndex{}
	identities := MeshIdentityIndex{}
	observability := LegacyObservabilityIndex{}
	var nsEvidence systemNamespaceEvidence

	_ = filepath.WalkDir(inputDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // best-effort scan
		}
		if ext := strings.ToLower(filepath.Ext(d.Name())); ext != ".yaml" && ext != ".yml" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		nsEvidence.scan(data)
		if !indexedDocPattern.Match(data) {
			return nil
		}
		for _, doc := range splitYAMLDocuments(data) {
			if name, b := parseMeshBackends(doc); name != "" && b != nil {
				backends[name] = b
			}
			if serviceTag, className := parseGatewayClassEntry(doc); serviceTag != "" {
				classes.add(serviceTag, className)
			}
			if mesh := parseMeshIdentityMesh(doc); mesh != "" {
				identities[mesh] = true
			}
			if kind, mesh, backend := parseLegacyBackendUse(doc); kind != "" {
				observability.add(kind, mesh, backend)
			}
		}
		return nil
	})

	opts := TransformOptions{
		Target:              target,
		SystemNamespace:     nsEvidence.infer(),
		LegacyObservability: observability,
	}
	if len(backends) > 0 {
		opts.MeshBackends = backends
	}
	if len(classes) > 0 {
		opts.GatewayClasses = classes
	}
	if len(identities) > 0 {
		opts.MeshIdentities = identities
	}
	return opts
}

// parseMeshBackends extracts the mesh name and its observability backends from a
// single document, or returns ("", nil) when the document is not a Mesh.
func parseMeshBackends(raw []byte) (string, *meshBackends) {
	var probe struct {
		Kind string `json:"kind"`
		Type string `json:"type"`
		Name string `json:"name"`
		Meta struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec struct {
			Logging *oldMeshLoggingSection `json:"logging"`
			Tracing *oldMeshTracingSection `json:"tracing"`
		} `json:"spec"`
		Logging *oldMeshLoggingSection `json:"logging"`
		Tracing *oldMeshTracingSection `json:"tracing"`
	}
	if err := yaml.Unmarshal(raw, &probe); err != nil {
		return "", nil
	}

	kind := probe.Kind
	if kind == "" {
		kind = probe.Type
	}
	if kind != "Mesh" {
		return "", nil
	}

	name := probe.Meta.Name
	if name == "" {
		name = probe.Name
	}
	if name == "" {
		return "", nil
	}

	// Kubernetes format nests under spec; Universal format is flat.
	logging := probe.Spec.Logging
	if logging == nil {
		logging = probe.Logging
	}
	tracing := probe.Spec.Tracing
	if tracing == nil {
		tracing = probe.Tracing
	}
	// A Mesh with no observability sections is still indexed: it lets a dangling
	// TrafficLog/TrafficTrace backend reference be reported as dangling rather
	// than as "the Mesh is not in the input set".
	out := &meshBackends{
		Logging: map[string]oldLoggingBackend{},
		Tracing: map[string]oldTracingBackend{},
	}
	if logging != nil {
		out.DefaultLoggingBackend = logging.DefaultBackend
		for _, b := range logging.Backends {
			out.Logging[b.Name] = b
		}
	}
	if tracing != nil {
		out.DefaultTracingBackend = tracing.DefaultBackend
		for _, b := range tracing.Backends {
			out.Tracing[b.Name] = b
		}
	}
	return name, out
}

// ---- Single-backend converters (shared with TransformMesh) -------------------

// tracingBackendToNew converts one Mesh spec.tracing backend into a MeshTrace
// default.backends[] entry.
func tracingBackendToNew(b oldTracingBackend) (map[string]interface{}, []string) {
	switch strings.ToLower(b.Type) {
	case "zipkin":
		var conf oldZipkinConf
		_ = json.Unmarshal(b.Conf, &conf)
		zipkinConf := map[string]interface{}{"url": conf.URL}
		if conf.TraceId128bit != nil {
			zipkinConf["traceId128bit"] = *conf.TraceId128bit
		}
		if conf.APIVersion != "" {
			zipkinConf["apiVersion"] = conf.APIVersion
		}
		if conf.SharedSpan != nil {
			zipkinConf["sharedSpanContext"] = *conf.SharedSpan
		}
		return map[string]interface{}{"type": "Zipkin", "zipkin": zipkinConf}, nil

	case "datadog":
		var conf oldDatadogConf
		_ = json.Unmarshal(b.Conf, &conf)
		ddConf := map[string]interface{}{"url": conf.Address}
		if conf.SplitService != nil {
			ddConf["splitService"] = *conf.SplitService
		}
		return map[string]interface{}{"type": "Datadog", "datadog": ddConf}, nil

	default:
		return nil, []string{fmt.Sprintf(
			"MeshTrace: unsupported backend type %q — migrate this backend manually", b.Type)}
	}
}

// loggingBackendToNew converts one Mesh spec.logging backend into a
// MeshAccessLog default.backends[] entry.
func loggingBackendToNew(b oldLoggingBackend) (map[string]interface{}, []string) {
	switch strings.ToLower(b.Type) {
	case "file":
		var conf oldFileLogConf
		_ = json.Unmarshal(b.Conf, &conf)
		return map[string]interface{}{
			"type": "File",
			"file": map[string]interface{}{"path": conf.Path},
		}, nil

	case "tcp":
		var conf oldTCPLogConf
		_ = json.Unmarshal(b.Conf, &conf)
		return map[string]interface{}{
			"type": "Tcp",
			"tcp":  map[string]interface{}{"address": conf.Address},
		}, nil

	default:
		return nil, []string{fmt.Sprintf(
			"MeshAccessLog: unsupported backend type %q — migrate this backend manually", b.Type)}
	}
}

// ---- Legacy default-section resolution ---------------------------------------

// legacyDefaultSection produces the `default` body for a legacy policy: either by
// running the registered conf converter, or — for TrafficLog and TrafficTrace — by
// resolving conf.backend against the Mesh resource.
func legacyDefaultSection(policy UniversalPolicy, opts TransformOptions) (json.RawMessage, []string, error) {
	switch policy.Type {
	case "TrafficPermission":
		// The legacy resource has no conf at all: its mere existence is the
		// permission. MeshTrafficPermission needs an explicit action on every
		// from[] entry, so synthesise the one the legacy resource implied.
		return json.RawMessage(`{"action":"Allow"}`), nil, nil
	case "TrafficLog":
		return resolveLoggingBackendRef(policy, opts)
	case "TrafficTrace":
		return resolveTracingBackendRef(policy, opts)
	default:
		return convertLegacyConf(policy.Type, policy.Name, policy.Conf)
	}
}

// legacyBackendRef is the shared shape of TrafficLog/TrafficTrace conf.
type legacyBackendRef struct {
	Backend string `json:"backend"`
}

// meshNameOf returns the mesh a legacy policy belongs to, defaulting to "default"
// the way the control plane does.
func meshNameOf(policy UniversalPolicy) string {
	if policy.Mesh != "" {
		return policy.Mesh
	}
	return "default"
}

func resolveLoggingBackendRef(policy UniversalPolicy, opts TransformOptions) (json.RawMessage, []string, error) {
	var ref legacyBackendRef
	if len(policy.Conf) > 0 {
		if err := json.Unmarshal(policy.Conf, &ref); err != nil {
			return nil, nil, fmt.Errorf("parse TrafficLog conf: %w", err)
		}
	}

	mesh := meshNameOf(policy)
	backends := opts.MeshBackends[mesh]

	name := ref.Backend
	if name == "" && backends != nil {
		name = backends.DefaultLoggingBackend
	}
	if name == "" {
		return nil, []string{fmt.Sprintf(
			"TrafficLog %q: no conf.backend set and the default logging backend of Mesh %q could not be "+
				"resolved — add default.backends[] to the generated MeshAccessLog by hand.",
			policy.Name, mesh)}, nil
	}

	if backends == nil {
		return nil, []string{fmt.Sprintf(
			"TrafficLog %q: conf.backend %q names a backend declared on Mesh %q (spec.logging.backends), "+
				"which is not in the input set — MeshAccessLog declares its backend inline, so copy that "+
				"backend definition into default.backends[] of the generated policy.",
			policy.Name, name, mesh)}, nil
	}

	backend, ok := backends.Logging[name]
	if !ok {
		return nil, []string{fmt.Sprintf(
			"TrafficLog %q: conf.backend %q is not declared in spec.logging.backends of Mesh %q — "+
				"the reference is dangling; set default.backends[] on the generated MeshAccessLog by hand.",
			policy.Name, name, mesh)}, nil
	}

	converted, warnings := loggingBackendToNew(backend)
	if converted == nil {
		return nil, warnings, nil
	}
	out, err := json.Marshal(map[string]interface{}{
		"backends": []interface{}{converted},
	})
	if err != nil {
		return nil, warnings, fmt.Errorf("marshal MeshAccessLog default: %w", err)
	}
	return out, warnings, nil
}

func resolveTracingBackendRef(policy UniversalPolicy, opts TransformOptions) (json.RawMessage, []string, error) {
	var ref legacyBackendRef
	if len(policy.Conf) > 0 {
		if err := json.Unmarshal(policy.Conf, &ref); err != nil {
			return nil, nil, fmt.Errorf("parse TrafficTrace conf: %w", err)
		}
	}

	mesh := meshNameOf(policy)
	backends := opts.MeshBackends[mesh]

	name := ref.Backend
	if name == "" && backends != nil {
		name = backends.DefaultTracingBackend
	}
	if name == "" {
		return nil, []string{fmt.Sprintf(
			"TrafficTrace %q: no conf.backend set and the default tracing backend of Mesh %q could not be "+
				"resolved — add default.backends[] to the generated MeshTrace by hand.",
			policy.Name, mesh)}, nil
	}

	if backends == nil {
		return nil, []string{fmt.Sprintf(
			"TrafficTrace %q: conf.backend %q names a backend declared on Mesh %q (spec.tracing.backends), "+
				"which is not in the input set — MeshTrace declares its backend inline, so copy that backend "+
				"definition into default.backends[] of the generated policy.",
			policy.Name, name, mesh)}, nil
	}

	backend, ok := backends.Tracing[name]
	if !ok {
		return nil, []string{fmt.Sprintf(
			"TrafficTrace %q: conf.backend %q is not declared in spec.tracing.backends of Mesh %q — "+
				"the reference is dangling; set default.backends[] on the generated MeshTrace by hand.",
			policy.Name, name, mesh)}, nil
	}

	converted, warnings := tracingBackendToNew(backend)
	if converted == nil {
		return nil, warnings, nil
	}

	body := map[string]interface{}{"backends": []interface{}{converted}}
	if backend.Sampling != nil {
		body["sampling"] = map[string]interface{}{"overall": int(*backend.Sampling)}
	}
	out, err := json.Marshal(body)
	if err != nil {
		return nil, warnings, fmt.Errorf("marshal MeshTrace default: %w", err)
	}
	return out, warnings, nil
}

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
var indexedDocPattern = regexp.MustCompile(`(?m)^\s*(kind|type)\s*:\s*["']?(Mesh|MeshGatewayInstance)["']?\s*$`)

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
		}
		return nil
	})

	opts := TransformOptions{Target: target}
	if len(backends) > 0 {
		opts.MeshBackends = backends
	}
	if len(classes) > 0 {
		opts.GatewayClasses = classes
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

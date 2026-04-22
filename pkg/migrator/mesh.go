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
	Type     string          `json:"type"` // "zipkin", "datadog"
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
	DefaultBackend string             `json:"defaultBackend"`
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
// The cleaned Mesh is always the first document returned.
func TransformMesh(raw []byte) ([][]byte, []string, error) {
	// Parse as a generic map so all unrecognised fields are preserved.
	var obj map[string]interface{}
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		return nil, nil, fmt.Errorf("unmarshal Mesh: %w", err)
	}

	meshName, meshNamespace := extractMeshMeta(obj)

	spec, _ := obj["spec"].(map[string]interface{})
	if spec == nil {
		// Universal format: fields (meshServices, metrics, etc.) live at the top level
		// with no spec wrapper. Point spec at obj so the rest of the logic works uniformly.
		spec = obj
	}

	var companions [][]byte
	var warnings []string

	// ── metrics → MeshMetric ──────────────────────────────────────────────────
	if rawMetrics, ok := spec["metrics"]; ok {
		b, _ := json.Marshal(rawMetrics)
		var m oldMeshMetricsSection
		if err := json.Unmarshal(b, &m); err == nil {
			crds, w := metricsToMeshMetric(m, meshName, meshNamespace)
			companions = append(companions, crds...)
			warnings = append(warnings, w...)
		}
		delete(spec, "metrics")
	}

	// ── tracing → MeshTrace ───────────────────────────────────────────────────
	if rawTracing, ok := spec["tracing"]; ok {
		b, _ := json.Marshal(rawTracing)
		var t oldMeshTracingSection
		if err := json.Unmarshal(b, &t); err == nil {
			crds, w := tracingToMeshTrace(t, meshName, meshNamespace)
			companions = append(companions, crds...)
			warnings = append(warnings, w...)
		}
		delete(spec, "tracing")
	}

	// ── logging → MeshAccessLog ───────────────────────────────────────────────
	if rawLogging, ok := spec["logging"]; ok {
		b, _ := json.Marshal(rawLogging)
		var l oldMeshLoggingSection
		if err := json.Unmarshal(b, &l); err == nil {
			crds, w := loggingToMeshAccessLog(l, meshName, meshNamespace)
			companions = append(companions, crds...)
			warnings = append(warnings, w...)
		}
		delete(spec, "logging")
	}

	// ── networking.outbound.passthrough → MeshPassthrough ─────────────────────
	if networking, ok := spec["networking"].(map[string]interface{}); ok {
		if outbound, ok := networking["outbound"].(map[string]interface{}); ok {
			if pt, exists := outbound["passthrough"]; exists {
				if ptBool, _ := pt.(bool); !ptBool {
					// passthrough=false → generate MeshPassthrough with passthroughMode: None
					if crd, ptWarns, err := buildMeshPassthrough(meshName, meshNamespace); err == nil {
						companions = append(companions, crd)
						warnings = append(warnings, ptWarns...)
						warnings = append(warnings,
							"Mesh.spec.networking.outbound.passthrough=false has been extracted to a MeshPassthrough CRD with passthroughMode: None")
					}
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
			"Mesh.spec.meshServices.mode set to Exclusive — required for MeshService-based policies to take effect (Kuma 2.6+). "+
				"Verify that all policies and workloads in this mesh have been migrated before applying.")
	}

	// Marshal the cleaned Mesh CRD.
	cleaned, err := yaml.Marshal(obj)
	if err != nil {
		return nil, warnings, fmt.Errorf("marshal cleaned Mesh: %w", err)
	}

	return append([][]byte{cleaned}, companions...), warnings, nil
}

// ---- Companion CRD builders --------------------------------------------------

func metricsToMeshMetric(m oldMeshMetricsSection, meshName, ns string) ([][]byte, []string) {
	var warnings []string
	var backends []map[string]interface{}

	for _, b := range m.Backends {
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
	crd := buildObservabilityCRD("MeshMetric", outName, ns, meshName, map[string]interface{}{
		"backends": backends,
	})
	b, err := yaml.Marshal(crd)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("MeshMetric marshal error: %v", err))
		return nil, warnings
	}
	return [][]byte{b}, warnings
}

func tracingToMeshTrace(t oldMeshTracingSection, meshName, ns string) ([][]byte, []string) {
	var warnings []string
	var backends []map[string]interface{}
	var sampling map[string]interface{}

	for _, b := range t.Backends {
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
			backends = append(backends, map[string]interface{}{
				"type":   "Zipkin",
				"zipkin": zipkinConf,
			})

		case "datadog":
			var conf oldDatadogConf
			_ = json.Unmarshal(b.Conf, &conf)
			ddConf := map[string]interface{}{"url": conf.Address}
			if conf.SplitService != nil {
				ddConf["splitService"] = *conf.SplitService
			}
			backends = append(backends, map[string]interface{}{
				"type":    "Datadog",
				"datadog": ddConf,
			})

		default:
			warnings = append(warnings,
				fmt.Sprintf("MeshTrace: unsupported backend type %q — migrate this backend manually", b.Type))
		}

		// Map old per-backend sampling (float 0–100) to new spec-level sampling.overall (int).
		if b.Sampling != nil && sampling == nil {
			overall := int(*b.Sampling)
			sampling = map[string]interface{}{"overall": overall}
		}
	}

	if len(backends) == 0 {
		return nil, warnings
	}

	defaultSection := map[string]interface{}{"backends": backends}
	if sampling != nil {
		defaultSection["sampling"] = sampling
	}

	outName := meshName + "-trace"
	if w := ValidateResourceName(outName, "MeshTrace"); w != "" {
		warnings = append(warnings, w)
	}
	crd := buildObservabilityCRD("MeshTrace", outName, ns, meshName, defaultSection)
	b, err := yaml.Marshal(crd)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("MeshTrace marshal error: %v", err))
		return nil, warnings
	}
	return [][]byte{b}, warnings
}

func loggingToMeshAccessLog(l oldMeshLoggingSection, meshName, ns string) ([][]byte, []string) {
	var warnings []string
	var backends []map[string]interface{}

	for _, b := range l.Backends {
		switch strings.ToLower(b.Type) {
		case "file":
			var conf oldFileLogConf
			_ = json.Unmarshal(b.Conf, &conf)
			backends = append(backends, map[string]interface{}{
				"type": "File",
				"file": map[string]interface{}{"path": conf.Path},
			})

		case "tcp":
			var conf oldTCPLogConf
			_ = json.Unmarshal(b.Conf, &conf)
			backends = append(backends, map[string]interface{}{
				"type": "Tcp",
				"tcp":  map[string]interface{}{"address": conf.Address},
			})

		default:
			warnings = append(warnings,
				fmt.Sprintf("MeshAccessLog: unsupported backend type %q — migrate this backend manually", b.Type))
		}
	}

	if len(backends) == 0 {
		return nil, warnings
	}

	outName := meshName + "-accesslog"
	if w := ValidateResourceName(outName, "MeshAccessLog"); w != "" {
		warnings = append(warnings, w)
	}
	crd := buildObservabilityCRD("MeshAccessLog", outName, ns, meshName, map[string]interface{}{
		"backends": backends,
	})
	b, err := yaml.Marshal(crd)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("MeshAccessLog marshal error: %v", err))
		return nil, warnings
	}
	return [][]byte{b}, warnings
}

func buildMeshPassthrough(meshName, ns string) ([]byte, []string, error) {
	outName := meshName + "-passthrough"
	var warnings []string
	if w := ValidateResourceName(outName, "MeshPassthrough"); w != "" {
		warnings = append(warnings, w)
	}
	crd := buildObservabilityCRD("MeshPassthrough", outName, ns, meshName, map[string]interface{}{
		"passthroughMode": "None",
	})
	b, err := yaml.Marshal(crd)
	return b, warnings, err
}

// ---- Generic CRD constructor -------------------------------------------------

// buildObservabilityCRD constructs a map representing any of the observability
// companion CRDs (MeshMetric, MeshTrace, MeshAccessLog, MeshPassthrough).
// defaultSection is the content of spec.default.
func buildObservabilityCRD(kind, name, namespace, meshName string, defaultSection map[string]interface{}) map[string]interface{} {
	meta := map[string]interface{}{
		"name":      name,
		"namespace": namespace,
		"labels": map[string]interface{}{
			"kuma.io/mesh": meshName,
		},
	}
	spec := map[string]interface{}{
		"targetRef": map[string]interface{}{"kind": "Mesh"},
		"default":   defaultSection,
	}
	return map[string]interface{}{
		"apiVersion": kumaAPIVersion,
		"kind":       kind,
		"metadata":   meta,
		"spec":       spec,
	}
}

// ---- Helpers -----------------------------------------------------------------

// extractMeshMeta returns the name and namespace from a Mesh CRD map.
// Supports both Kubernetes-style (metadata.name/namespace) and
// Universal-style (top-level name field, no namespace).
func extractMeshMeta(obj map[string]interface{}) (name, namespace string) {
	namespace = "kong-mesh-system" // default system namespace
	if meta, ok := obj["metadata"].(map[string]interface{}); ok {
		if n, ok := meta["name"].(string); ok {
			name = n
		}
		if ns, ok := meta["namespace"].(string); ok && ns != "" {
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

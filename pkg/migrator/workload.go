package migrator

import (
	"fmt"
	"regexp"
	"strings"

	"sigs.k8s.io/yaml"
)

// svcTagPattern matches a kuma.io/service-encoded token embedded in any string value.
//
// Format: <name>_<namespace>_svc_<port>
// Group 1 = the full svc token (e.g. "backend_demo_svc_3000")
// Group 2 = optional explicit port suffix immediately following (e.g. ":80")
var svcTagPattern = regexp.MustCompile(`([a-z0-9][a-z0-9_-]*_svc_[0-9]+)(:[0-9]+)?`)

// EnvVarHit records a single env var that embeds a legacy kuma.io/service address.
type EnvVarHit struct {
	WorkloadKind  string
	WorkloadName  string
	Namespace     string
	ContainerName string
	EnvVarName    string
	RawValue      string
	OldToken      string // the _svc_ token, e.g. "backend_demo_svc_3000"
	ExplicitPort  string // port suffix after the token, e.g. ":80"; empty if absent
	ServiceName   string // parsed: "backend"
	ServiceNS     string // parsed: "demo"
	ServicePort   string // port from the _svc_ encoding: "3000"
}

// NewK8sAddress returns the Kubernetes-native DNS address for this service.
// This is always safe to use regardless of mesh configuration.
func (h EnvVarHit) NewK8sAddress() string {
	addr := h.ServiceName + "." + h.ServiceNS + ".svc.cluster.local"
	if h.ExplicitPort != "" {
		addr += h.ExplicitPort
	} else if h.ServicePort != "" {
		addr += ":" + h.ServicePort
	}
	return addr
}

// NewMeshAddress returns the Kuma mesh hostname for this service.
//
// The HostnameGenerator pattern for a Kubernetes zone is:
//
//	{name}.{namespace}.svc.{zone}.mesh.local
//
// The zone name is deployment-specific and not known at migration time;
// <zone> is used as a placeholder that the user must fill in.
func (h EnvVarHit) NewMeshAddress() string {
	addr := h.ServiceName + "." + h.ServiceNS + ".svc.<zone>.mesh.local"
	if h.ExplicitPort != "" {
		addr += h.ExplicitPort
	} else if h.ServicePort != "" {
		addr += ":" + h.ServicePort
	}
	return addr
}

// MappingKey returns a deduplication key for the summary mapping table.
// Two hits with the same key map to the same old→new replacement.
func (h EnvVarHit) MappingKey() string {
	return h.OldToken + h.ExplicitPort
}

// FormatMapping returns one line for the summary mapping table.
func (h EnvVarHit) FormatMapping() string {
	old := h.OldToken + h.ExplicitPort
	return fmt.Sprintf("  %-42s  →  K8s: %-48s  Mesh: %s",
		old, h.NewK8sAddress(), h.NewMeshAddress())
}

// workloadProbe is a minimal Kubernetes object struct for workload scanning.
// It covers Deployment, StatefulSet, DaemonSet, Job, CronJob, and Pod.
type workloadProbe struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec workloadSpecProbe `json:"spec"`
}

type workloadSpecProbe struct {
	Template    *podTemplateProbe `json:"template"`    // Deployment / StatefulSet / DaemonSet / Job
	JobTemplate *jobTemplateProbe `json:"jobTemplate"` // CronJob
	// Bare Pod
	Containers     []containerProbe `json:"containers"`
	InitContainers []containerProbe `json:"initContainers"`
}

type podTemplateProbe struct {
	Spec struct {
		Containers     []containerProbe `json:"containers"`
		InitContainers []containerProbe `json:"initContainers"`
	} `json:"spec"`
}

type jobTemplateProbe struct {
	Spec struct {
		Template *podTemplateProbe `json:"template"`
	} `json:"spec"`
}

type containerProbe struct {
	Name string     `json:"name"`
	Env  []envProbe `json:"env"`
}

type envProbe struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

var knownWorkloadKinds = map[string]bool{
	"Deployment": true, "StatefulSet": true, "DaemonSet": true,
	"Job": true, "CronJob": true, "Pod": true, "ReplicaSet": true,
}

// ScanWorkloadEnvVars scans a single YAML document for env vars that contain
// legacy kuma.io/service-encoded addresses (e.g. "backend_demo_svc_3000:80").
//
// Returns nil, nil if the document is not a recognised Kubernetes workload kind.
// Returns one EnvVarHit per (container, env var, matched token) tuple.
func ScanWorkloadEnvVars(raw []byte) ([]EnvVarHit, error) {
	var w workloadProbe
	if err := yaml.Unmarshal(raw, &w); err != nil {
		return nil, err
	}
	if !knownWorkloadKinds[w.Kind] {
		return nil, nil
	}

	var hits []EnvVarHit
	for _, c := range collectContainers(&w) {
		for _, e := range c.Env {
			if e.Value == "" {
				continue
			}
			for _, m := range svcTagPattern.FindAllStringSubmatch(e.Value, -1) {
				token, explicitPort := m[1], m[2]
				name, ns := ParseKumaServiceTag(token)
				if name == "" {
					continue
				}
				svcPort := ""
				if idx := strings.LastIndex(token, "_svc_"); idx != -1 {
					svcPort = token[idx+5:]
				}
				hits = append(hits, EnvVarHit{
					WorkloadKind:  w.Kind,
					WorkloadName:  w.Metadata.Name,
					Namespace:     w.Metadata.Namespace,
					ContainerName: c.Name,
					EnvVarName:    e.Name,
					RawValue:      e.Value,
					OldToken:      token,
					ExplicitPort:  explicitPort,
					ServiceName:   name,
					ServiceNS:     ns,
					ServicePort:   svcPort,
				})
			}
		}
	}
	return hits, nil
}

// AnnotationHit records a single Kuma annotation that uses the deprecated "yes"/"no" syntax.
type AnnotationHit struct {
	Kind          string
	Name          string
	Namespace     string
	AnnotationKey string
	OldValue      string // "yes" or "no"
	NewValue      string // "true" or "false"
}

// annotationProbe is a minimal struct to extract metadata.annotations from any resource.
type annotationProbe struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name        string            `json:"name"`
		Namespace   string            `json:"namespace"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
}

// ScanKumaAnnotations scans a single YAML document for kuma.io/* annotations with
// deprecated boolean values ("yes" or "no"). These were deprecated in Kuma 2.9 in
// favour of "true"/"false".
//
// Returns one AnnotationHit per deprecated annotation found.
func ScanKumaAnnotations(raw []byte) ([]AnnotationHit, error) {
	var p annotationProbe
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	if len(p.Metadata.Annotations) == 0 {
		return nil, nil
	}

	var hits []AnnotationHit
	for k, v := range p.Metadata.Annotations {
		if !strings.HasPrefix(k, "kuma.io/") && !strings.HasPrefix(k, "k8s.kuma.io/") {
			continue
		}
		var newVal string
		switch v {
		case "yes":
			newVal = "true"
		case "no":
			newVal = "false"
		default:
			continue
		}
		hits = append(hits, AnnotationHit{
			Kind:          p.Kind,
			Name:          p.Metadata.Name,
			Namespace:     p.Metadata.Namespace,
			AnnotationKey: k,
			OldValue:      v,
			NewValue:      newVal,
		})
	}
	return hits, nil
}

// collectContainers gathers all regular and init containers from any supported workload shape.
func collectContainers(w *workloadProbe) []containerProbe {
	var out []containerProbe
	if w.Spec.Template != nil {
		out = append(out, w.Spec.Template.Spec.Containers...)
		out = append(out, w.Spec.Template.Spec.InitContainers...)
	}
	if w.Spec.JobTemplate != nil && w.Spec.JobTemplate.Spec.Template != nil {
		out = append(out, w.Spec.JobTemplate.Spec.Template.Spec.Containers...)
		out = append(out, w.Spec.JobTemplate.Spec.Template.Spec.InitContainers...)
	}
	out = append(out, w.Spec.Containers...)
	out = append(out, w.Spec.InitContainers...)
	return out
}

package migrator

import (
	"strings"

	"github.com/Kong/kuma-migrator/pkg/resource"
	"sigs.k8s.io/yaml"
)

// backendsSubfolder holds resources that policies reference by label and that
// must exist before those policies are applied. The Apply Checklist applies it
// first in each context.
const backendsSubfolder = "backends"

// outputSubfolder is the migrate output subdirectory for kind. It follows
// resource.KindSubfolder (the extract layout) except for kinds that policies
// reference through backendRef.labels:
//
//   - MeshOpenTelemetryBackend: MeshTrace, MeshAccessLog and MeshMetric select it
//     by labels. The 3.0 validators only check the shape of the reference
//     (validators.ValidateBackendResourceRef), not that a backend exists, so a
//     policy applied first is accepted and the control plane skips the dangling
//     backend ("MeshOpenTelemetryBackend not found, skipping backend") until the
//     backend is applied. Telemetry is lost in between. resource.KindSubfolder
//     puts it in other/, which the checklist applies after observability/.
func outputSubfolder(kind string) string {
	switch kind {
	case "MeshOpenTelemetryBackend":
		return backendsSubfolder
	}
	return resource.KindSubfolder(kind)
}

// outputDocFilename derives a file name for a single marshalled YAML document
// using its kind, namespace (if any), and name.
func outputDocFilename(doc []byte) string {
	kind, name := probeKindName(doc)
	if kind == "" || name == "" {
		return "unknown.yaml"
	}
	ns := probeNamespace(doc)
	if ns != "" {
		return sanitizeForFilename(kind+"-"+ns+"-"+name) + ".yaml"
	}
	return sanitizeForFilename(kind+"-"+name) + ".yaml"
}

// probeNamespace extracts metadata.namespace from a raw YAML document.
func probeNamespace(raw []byte) string {
	var obj map[string]interface{}
	if err := yaml.Unmarshal(raw, &obj); err != nil || obj == nil {
		return ""
	}
	meta, _ := obj["metadata"].(map[string]interface{})
	ns, _ := meta["namespace"].(string)
	return ns
}

// sanitizeForFilename replaces characters that are problematic in file names.
func sanitizeForFilename(s string) string {
	return strings.NewReplacer("/", "-", ":", "-", " ", "-").Replace(s)
}

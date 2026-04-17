package migrator

import (
	"strings"

	"sigs.k8s.io/yaml"
)

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

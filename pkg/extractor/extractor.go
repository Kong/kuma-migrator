package extractor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

// writeResourceFiles splits a multi-document YAML stream into individual files under outputDir.
// Documents whose kind contains "Insight" (case-insensitive) are silently skipped.
// Returns the number of files written.
func writeResourceFiles(data []byte, outputDir string) (int, error) {
	docs := splitYAMLDocs(data)
	count := 0
	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}

		var obj map[string]interface{}
		if err := yaml.Unmarshal([]byte(doc), &obj); err != nil || obj == nil {
			continue
		}

		kind, _ := obj["kind"].(string)
		if kind == "" {
			continue
		}
		if isInsightKind(kind) {
			continue
		}

		meta, _ := obj["metadata"].(map[string]interface{})
		name, _ := meta["name"].(string)
		ns, _ := meta["namespace"].(string)

		var filename string
		if ns != "" {
			filename = sanitize(kind+"-"+ns+"-"+name) + ".yaml"
		} else {
			filename = sanitize(kind+"-"+name) + ".yaml"
		}

		outPath := filepath.Join(outputDir, filename)
		if err := os.WriteFile(outPath, []byte(doc+"\n"), 0644); err != nil {
			fmt.Printf("  [WARN] write %s: %v\n", outPath, err)
			continue
		}
		fmt.Printf("  → %s\n", filename)
		count++
	}
	return count, nil
}

// splitYAMLDocs splits a byte slice on YAML document separators (---).
func splitYAMLDocs(data []byte) []string {
	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	parts := strings.Split(content, "\n---")
	var docs []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.TrimPrefix(p, "---")
		p = strings.TrimSpace(p)
		if p != "" {
			docs = append(docs, p)
		}
	}
	return docs
}

// isInsightKind reports whether the resource kind contains "Insight" (e.g. ZoneInsight,
// DataplaneInsight). These are control-plane-managed status objects, not user policies.
func isInsightKind(kind string) bool {
	return strings.Contains(strings.ToLower(kind), "insight")
}

// sanitize replaces characters that are problematic in file names.
func sanitize(s string) string {
	return strings.NewReplacer("/", "-", ":", "-", " ", "-").Replace(s)
}

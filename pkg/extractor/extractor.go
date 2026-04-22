package extractor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Kong/kuma-migrator/pkg/config"
	"github.com/Kong/kuma-migrator/pkg/resource"
	"github.com/Kong/kuma-migrator/pkg/ui"
	"sigs.k8s.io/yaml"
)

// loadSkipSet loads the user config and returns the skip set for the given
// deployment environment ("kubernetes", "universal", or "" for unknown).
// On error it logs a warning and falls back to an empty set (no additional skipping).
func loadSkipSet(env string) map[string]bool {
	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("  [WARN] could not load config (skip list ignored): %v\n", err)
		return map[string]bool{}
	}
	return cfg.SkipSetForEnv(env)
}

// gatewayLocalKinds is the set of resource types that may be created directly on a Zone
// or Standalone CP and therefore may NOT carry a kuma.io/origin: zone label.
//
// Two categories:
//
//  1. Strictly zone-local (never synced to Global CP):
//     MeshGatewayInstance, MeshGatewayConfig — these are always zone-created and have no
//     origin label.
//
//  2. Gateway policy kinds that can be created on either the Global CP or the Zone CP:
//     MeshGateway, MeshHTTPRoute, MeshTCPRoute, MeshGatewayRoute — when created on the
//     Global CP they are synced to zones WITH kuma.io/origin: global (skipped by the normal
//     origin filter). When created directly on the Zone CP they carry no origin label and
//     must be kept.
//
// On a Zone CP extraction these kinds are kept when the origin label is absent;
// resources explicitly labelled kuma.io/origin: global are still skipped.
var gatewayLocalKinds = map[string]bool{
	"MeshGatewayInstance": true,
	"MeshGatewayConfig":   true,
	"MeshGateway":         true,
	"MeshHTTPRoute":       true,
	"MeshTCPRoute":        true,
	"MeshGatewayRoute":    true,
}

// zoneOnlyKinds is the set of resource types that must be extracted from Zone or
// Standalone CPs only. They are skipped during Global CP extraction.
//
//   - MeshGatewayInstance: zone-local; never synced to the Global CP.
//   - MeshGatewayConfig: zone-local companion to MeshGatewayInstance; never synced to Global.
var zoneOnlyKinds = map[string]bool{
	"MeshGatewayInstance": true,
	"MeshGatewayConfig":   true,
}

// isGatewayLocalKind reports whether kind is a gateway resource that may lack
// kuma.io/origin labels on a Zone CP (see gatewayLocalKinds).
func isGatewayLocalKind(kind string) bool {
	return gatewayLocalKinds[kind]
}

// isZoneOnlyKind reports whether kind must be extracted from Zone/Standalone CPs only
// and should be skipped during Global CP extraction (see zoneOnlyKinds).
func isZoneOnlyKind(kind string) bool {
	return zoneOnlyKinds[kind]
}

// ZoneOriginSkip records a resource that was skipped on a Global CP because it
// carries kuma.io/origin: zone. These are zone-created policies synced (read-only)
// to the Global CP and must be extracted from the originating zone instead.
type ZoneOriginSkip struct {
	Kind     string
	Name     string
	ZoneName string // value of kuma.io/zone label; empty when label is absent
}

// writeResourceFiles splits a multi-document YAML stream into individual files under outputDir.
// Documents whose kind contains "Insight" or is in skipSet are silently skipped.
// On a Zone CP (cpMode == CPModeZone) only resources with kuma.io/origin: zone are kept,
// with the exception of gateway-local kinds which may lack the label (see gatewayLocalKinds).
// On a Global CP (cpMode == CPModeGlobal) resources with kuma.io/origin: zone are skipped and
// appended to skips (when non-nil) so callers can surface them to the user.
// Files are written into per-kind subfolders under <outputDir>/<cpModeDir>/<meshName>/<sub>/
// (context-first layout). Global-scoped resources go to <outputDir>/<cpModeDir>/global/<sub>/.
//
//   - cpModeDir:    the CP mode directory label (e.g. "global", "zone-eu-west"); empty means flat output.
//   - meshName:     the mesh these resources belong to; when non-empty, files are written under
//     <outputDir>/<meshName>/<cpModeDir>/<sub>/ so each mesh has its own top-level directory.
//   - meshFilter:   when non-empty, resources whose meshName does not match are skipped.
//     Resources with no mesh association (global-scoped) are never filtered out.
//   - outputFormat: "kubernetes" converts Universal-format resources (type/name/mesh) to
//     Kubernetes format (apiVersion/kind/metadata) before writing. "universal" writes as-is.
//   - skips:        when non-nil, zone-origin resources skipped on Global CP are appended here.
//
// Returns the number of files written.
func writeResourceFiles(data []byte, outputDir string, skipSet map[string]bool, cpMode, cpModeDir, meshName, meshFilter, outputFormat string, skips *[]ZoneOriginSkip) (int, error) {
	docs := splitYAMLDocs(data)
	count := 0
	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}
		n, err := writeSingleResourceDoc(doc, outputDir, skipSet, cpMode, cpModeDir, meshName, meshFilter, outputFormat, skips)
		if err != nil {
			return count, err
		}
		count += n
	}
	return count, nil
}

// writeSingleResourceDoc writes one YAML document to disk, or expands a list document
// into its individual items. Returns the number of files written.
//
// Supported formats:
//   - Kubernetes-style: kind/apiVersion + metadata.name  (kubectl/kumactl Kubernetes output)
//   - Universal-style: type + name at top level          (kumactl Universal output)
//   - Kubernetes list: kind ends in "List", items[]      (kumactl -o yaml on Kube returns MeshMetricList)
//   - Universal list:  top-level items[], no kind/type   ({total: N, items: [...]})
//
// When outputFormat is "kubernetes", Universal-format documents (type/name/mesh at top level)
// are converted to Kubernetes format (apiVersion/kind/metadata) before writing. This applies
// to both standalone documents and items within list responses (e.g. Konnect API responses).
//
// When cpModeDir is non-empty, the file is written to <outputDir>/<cpModeDir>/<meshName>/<sub>/
// for mesh-scoped resources, or <outputDir>/<cpModeDir>/global/<sub>/ for global-scoped ones.
// When meshFilter is non-empty, resources whose meshName does not match are skipped (resources
// with no mesh association are always kept).
//
// On a Global CP, resources with kuma.io/origin: zone are skipped and appended to skips (when
// non-nil). These are zone-created policies synced read-only to the Global CP.
func writeSingleResourceDoc(doc string, outputDir string, skipSet map[string]bool, cpMode, cpModeDir, meshName, meshFilter, outputFormat string, skips *[]ZoneOriginSkip) (int, error) {
	var obj map[string]interface{}
	if err := yaml.Unmarshal([]byte(doc), &obj); err != nil || obj == nil {
		return 0, nil
	}

	kind, _ := obj["kind"].(string)
	if kind == "" {
		kind, _ = obj["type"].(string) // Universal format uses "type" instead of "kind"
	}

	// List documents — recurse into items rather than writing the whole list as one file.
	// Handles: Kubernetes MeshMetricList, Universal {total: N, items: [...]}
	if kind == "" || strings.HasSuffix(kind, "List") {
		items, _ := obj["items"].([]interface{})
		count := 0
		for _, item := range items {
			itemMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			itemBytes, err := yaml.Marshal(itemMap)
			if err != nil {
				continue
			}
			n, _ := writeSingleResourceDoc(strings.TrimSpace(string(itemBytes)), outputDir, skipSet, cpMode, cpModeDir, meshName, meshFilter, outputFormat, skips)
			count += n
		}
		return count, nil
	}

	// Convert Universal-format documents to Kubernetes format when requested.
	// A Universal document has "type" but no "kind" at the top level.
	// universalToKubernetes maps type→kind, name→metadata.name, mesh→metadata.labels,
	// preserves spec/status, and drops CP-internal fields (kri, creationTime, modificationTime).
	if outputFormat == "kubernetes" {
		if _, hasKind := obj["kind"]; !hasKind {
			if _, hasType := obj["type"]; hasType {
				obj = universalToKubernetes(obj)
				kind, _ = obj["kind"].(string)
				if converted, err := yaml.Marshal(obj); err == nil {
					doc = strings.TrimSpace(string(converted))
				}
			}
		}
	}

	if isInsightKind(kind) || skipSet[kind] {
		return 0, nil
	}

	meta, _ := obj["metadata"].(map[string]interface{})
	name, _ := meta["name"].(string)
	if name == "" {
		name, _ = obj["name"].(string) // Universal format: name is at top level
	}
	ns, _ := meta["namespace"].(string)

	// Labels: Kubernetes format has metadata.labels; Universal format has top-level labels.
	labels, _ := meta["labels"].(map[string]interface{})
	if labels == nil {
		labels, _ = obj["labels"].(map[string]interface{})
	}

	// Skip MeshService CRs that were auto-generated by Kuma from Kubernetes Services.
	// These carry kuma.io/env: kubernetes and do not need to be extracted or re-applied.
	if kind == "MeshService" {
		if env, _ := labels["kuma.io/env"].(string); env == "kubernetes" {
			return 0, nil
		}
	}

	// On a Global CP, skip zone-only kinds (MeshGateway, MeshGatewayInstance, etc.).
	// They may appear on the global CP via KDS sync but must be extracted from zone CPs.
	if cpMode == CPModeGlobal && isZoneOnlyKind(kind) {
		return 0, nil
	}

	// On a Global CP, skip resources with kuma.io/origin: zone.
	// These are zone-created policies synced read-only to the Global CP; they must be
	// extracted from the originating zone instead (identified by kuma.io/zone label).
	if cpMode == CPModeGlobal {
		if origin, _ := labels["kuma.io/origin"].(string); origin == CPModeZone {
			zoneName, _ := labels["kuma.io/zone"].(string)
			if skips != nil {
				*skips = append(*skips, ZoneOriginSkip{Kind: kind, Name: name, ZoneName: zoneName})
			}
			return 0, nil
		}
	}

	if cpMode == CPModeZone {
		origin, _ := labels["kuma.io/origin"].(string)
		switch {
		case origin == CPModeZone:
			// explicit zone-origin → keep
		case origin == "" && isGatewayLocalKind(kind):
			// gateway-local kinds may lack origin label on zone CPs → keep
		default:
			return 0, nil
		}
	}

	// Apply mesh filter: skip if a specific mesh was requested and this resource
	// belongs to a different mesh. Resources with no mesh association are always kept.
	if meshFilter != "" && meshName != "" && meshName != meshFilter {
		return 0, nil
	}

	var filename string
	if ns != "" {
		filename = sanitize(kind+"-"+ns+"-"+name) + ".yaml"
	} else {
		filename = sanitize(kind+"-"+name) + ".yaml"
	}

	sub := resource.KindSubfolder(kind)
	// Compute output directory (context-first layout):
	//   <outputDir>/<cpModeDir>/<meshName>/<sub>   when both cpModeDir and meshName are set
	//   <outputDir>/<cpModeDir>/global/<sub>        when cpModeDir is set but no mesh (global-scoped resources)
	//   <outputDir>/<meshName>/<sub>               when only meshName is set (no CP-mode dir)
	//   <outputDir>/<sub>                          otherwise (flat / legacy)
	var dir string
	switch {
	case cpModeDir != "" && meshName != "":
		dir = filepath.Join(outputDir, cpModeDir, MeshDirPrefix+meshName, sub)
	case cpModeDir != "":
		dir = filepath.Join(outputDir, cpModeDir, GlobalScopedDir, sub)
	case meshName != "":
		dir = filepath.Join(outputDir, MeshDirPrefix+meshName, sub)
	default:
		dir = filepath.Join(outputDir, sub)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		ui.Warn(fmt.Sprintf("mkdir %s: %v", dir, err))
		return 0, nil
	}
	outPath := filepath.Join(dir, filename)
	if err := os.WriteFile(outPath, []byte(doc+"\n"), 0644); err != nil {
		ui.Warn(fmt.Sprintf("write %s: %v", outPath, err))
		return 0, nil
	}
	ui.FileWritten(sub, filename)
	return 1, nil
}


// universalToKubernetes converts a Universal-format resource map to Kubernetes format.
//
// Universal format (Konnect REST API and kumactl Universal output):
//
//	{ type, name, mesh, spec, status, labels, kri, creationTime, modificationTime }
//
// Kubernetes format:
//
//	{ apiVersion: kuma.io/v1alpha1, kind, metadata: { name, labels }, spec, status }
//
// CP-internal fields (kri, creationTime, modificationTime) are dropped.
// Top-level labels and the mesh name are merged into metadata.labels.
func universalToKubernetes(obj map[string]interface{}) map[string]interface{} {
	typeName, _ := obj["type"].(string)
	name, _ := obj["name"].(string)
	mesh, _ := obj["mesh"].(string)

	metaLabels := map[string]interface{}{}
	if existing, ok := obj["labels"].(map[string]interface{}); ok {
		for k, v := range existing {
			metaLabels[k] = v
		}
	}
	if mesh != "" {
		metaLabels["kuma.io/mesh"] = mesh
	}

	metadata := map[string]interface{}{"name": name}
	if len(metaLabels) > 0 {
		metadata["labels"] = metaLabels
	}

	result := map[string]interface{}{
		"apiVersion": "kuma.io/v1alpha1",
		"kind":       typeName,
		"metadata":   metadata,
	}

	// Copy remaining fields verbatim (spec, status, conf, …).
	// Drop Universal-specific top-level fields already promoted or CP-internal.
	drop := map[string]bool{
		"type": true, "name": true, "mesh": true, "labels": true,
		"kri": true, "creationTime": true, "modificationTime": true,
	}
	for k, v := range obj {
		if !drop[k] {
			result[k] = v
		}
	}
	return result
}

// printZoneOriginSkips prints a terminal summary of resources skipped on a Global CP
// because they carry kuma.io/origin: zone, telling the user which zone to target.
func printZoneOriginSkips(skips []ZoneOriginSkip) {
	if len(skips) == 0 {
		return
	}
	fmt.Println()
	ui.Warn("Zone-origin resources skipped on Global CP — extract from their zone instead:")
	for _, s := range skips {
		zone := s.ZoneName
		if zone == "" {
			zone = "(zone label absent — check kuma.io/zone label)"
		}
		ui.WarnIndented(fmt.Sprintf("%s/%s  →  zone: %s", s.Kind, s.Name, zone))
	}
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

// PrintCPModeInfo prints the CP mode banner and relevant notices.
// Called from both ExtractViaKubectl and ExtractViaKumactl.
func PrintCPModeInfo(cpMode, zoneName string, zones []string) {
	switch cpMode {
	case CPModeZone:
		ui.KV("CP mode:", fmt.Sprintf("zone (%s)", zoneName))
		ui.Warn("Extracting from a Zone CP — only kuma.io/origin: zone resources will be kept.")
		ui.WarnIndented("For a complete policy set, also run extract against the Global CP.")
		ui.Info("MeshGatewayInstance and MeshGatewayConfig are zone-local and will be extracted here.")
		ui.Info("MeshGateway and route CRDs (MeshHTTPRoute, MeshTCPRoute, MeshGatewayRoute):")
		ui.InfoIndented("- Synced from Global CP with kuma.io/origin: global → skipped (extract from Global CP).")
		ui.InfoIndented("- Created directly on this Zone CP with no origin label → extracted here.")
	case CPModeGlobal:
		ui.KV("CP mode:", "global")
		if len(zones) > 0 {
			ui.KV("Attached zones:", strings.Join(zones, ", "))
		}
		ui.Info("MeshGateway and route CRDs created on the Global CP are extracted here.")
		ui.Info("MeshGatewayInstance and MeshGatewayConfig are zone-local and skipped here.")
		ui.InfoIndented("Run extract against each Zone CP to capture gateway instances.")
	case CPModeStandalone:
		ui.KV("CP mode:", "standalone")
	default:
		ui.KV("CP mode:", "unknown")
		ui.Warn("Could not detect CP mode — extracting all resources.")
	}
}

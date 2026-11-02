package extractor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// zoneOnlyKinds is the set of resource types that must be extracted from Zone or
// Standalone CPs only. They are skipped during Global CP extraction.
//
//   - MeshGatewayInstance: zone-local; never synced to the Global CP.
//   - MeshGatewayConfig: zone-local companion to MeshGatewayInstance; never synced to Global.
var zoneOnlyKinds = map[string]bool{
	"MeshGatewayInstance": true,
	"MeshGatewayConfig":   true,
}

// isZoneOnlyKind reports whether kind must be extracted from Zone/Standalone CPs only
// and should be skipped during Global CP extraction (see zoneOnlyKinds).
func isZoneOnlyKind(kind string) bool {
	return zoneOnlyKinds[kind]
}

// ZoneOriginSkip records a resource skipped on a Global CP because it carries
// kuma.io/origin: zone. These are zone-created policies synced (read-only) to
// the Global CP and must be extracted from the originating zone instead.
type ZoneOriginSkip struct {
	Kind     string
	Name     string
	ZoneName string // value of kuma.io/zone label; empty when label is absent
}

// extractNotes collects what the per-resource filters did, for the summary
// printed after extraction. All methods accept a nil receiver (no recording).
type extractNotes struct {
	// ZoneOriginSkips: Global CP, resources labelled kuma.io/origin: zone.
	ZoneOriginSkips []ZoneOriginSkip
	// GlobalCopies: federated Zone CP, resources labelled kuma.io/origin: global
	// (KDS copies of Global resources) that were skipped.
	GlobalCopies int
	// KeptUnlabelled: federated Zone CP, kind → number of resources kept that
	// carry no kuma.io/origin label.
	KeptUnlabelled map[string]int
	// Generated: kind → number of CP-generated resources skipped
	// (kuma.io/managed-by, see cpGeneratedManagedBy).
	Generated map[string]int
}

func (n *extractNotes) zoneOriginSkip(s ZoneOriginSkip) {
	if n != nil {
		n.ZoneOriginSkips = append(n.ZoneOriginSkips, s)
	}
}

func (n *extractNotes) globalCopy() {
	if n != nil {
		n.GlobalCopies++
	}
}

func (n *extractNotes) keptUnlabelled(kind string) {
	if n == nil {
		return
	}
	if n.KeptUnlabelled == nil {
		n.KeptUnlabelled = map[string]int{}
	}
	n.KeptUnlabelled[kind]++
}

func (n *extractNotes) generated(kind string) {
	if n == nil {
		return
	}
	if n.Generated == nil {
		n.Generated = map[string]int{}
	}
	n.Generated[kind]++
}

// zoneOriginDecision is the per-resource origin filter on a federated Zone CP.
//
//   - origin global → skip: a KDS copy of a Global resource. The KDS
//     global-to-zone mapper sets kuma.io/origin: global on every resource it
//     syncs (kuma pkg/kds/context/context.go, DefaultContext globalMappers), so
//     a synced copy is always labelled. Extract it from the Global CP.
//   - origin zone → keep.
//   - no origin label → keep. The resource was written on this zone. Kuma 2.x
//     labels a resource only when it passes the defaulting webhook
//     (Kubernetes) or the API server (Universal), and the Helm chart registers
//     that webhook for new-style policies, Mesh, Dataplane, MeshGateway and the
//     zone proxies only (kuma deployments/charts/kuma/templates/
//     cp-webhooks-and-secrets.yaml, mesh.defaulter.kuma-admission.kuma.io). The
//     legacy policies, ExternalService, ProxyTemplate, MeshGatewayRoute,
//     OPAPolicy, AccessRole and AccessAudit are never labelled on Kubernetes.
//
// unlabelled reports a kept resource without the label, for the summary.
func zoneOriginDecision(origin string) (keep, unlabelled bool) {
	switch origin {
	case CPModeGlobal:
		return false, false
	case "":
		return true, true
	default:
		return true, false
	}
}

// cpGeneratedManagedBy lists the kuma.io/managed-by values the control plane
// writes on resources it generates and reconciles itself (kuma 2.14):
//
//   - k8s-controller: MeshService generated from a Kubernetes Service
//     (pkg/plugins/runtime/k8s/controllers/meshservice_controller.go), also
//     MeshZoneAddress and Workload.
//   - meshservice-generator: MeshService generated from Dataplane inbounds on
//     Universal (pkg/core/resources/apis/meshservice/generate/generator.go).
//   - workload-generator: Workload on Universal.
//
// The CP recreates these, so re-applying a copy fights the controller.
// A hand-written MeshService has no such label and is kept. kuma.io/env is
// not a usable signal: the defaulting webhook sets it on every MeshService
// created on a zone, hand-written or not (labels.Compute, ProvidedByZoneFlag).
var cpGeneratedManagedBy = map[string]bool{
	"k8s-controller":        true,
	"meshservice-generator": true,
	"workload-generator":    true,
}

// isCPGenerated reports whether labels mark a resource as generated by the CP.
func isCPGenerated(labels map[string]interface{}) bool {
	v, _ := labels["kuma.io/managed-by"].(string)
	return cpGeneratedManagedBy[v]
}

// resourceMesh returns the mesh a resource belongs to, "" for a global-scoped
// resource:
//
//   - a Mesh belongs to itself, so it is written under mesh-<name>/;
//   - the kuma.io/mesh label (new-style resources, and every Universal
//     resource converted to Kubernetes format);
//   - the top-level mesh field: Universal format, and the Kubernetes CRDs of
//     the legacy kinds (TrafficPermission, MeshGatewayRoute, …), which store
//     the mesh there and carry no label.
func resourceMesh(obj map[string]interface{}, kind, name string, labels map[string]interface{}) string {
	if kind == "Mesh" {
		return name
	}
	if m, _ := labels["kuma.io/mesh"].(string); m != "" {
		return m
	}
	m, _ := obj["mesh"].(string)
	return m
}

// serverSideMetadata lists the metadata fields the API server writes. They
// are dropped on extract:
//
//   - resourceVersion: a stale value makes `kubectl apply` fail with a
//     conflict once the object changed on the server;
//   - uid, creationTimestamp, generation, selfLink, managedFields,
//     deletionTimestamp, deletionGracePeriodSeconds: server-owned, rejected
//     or ignored on create;
//   - ownerReferences: on Kubernetes the CP's owner-reference mutating webhook
//     adds the Mesh owner itself on create, with the UID of the Mesh in the
//     target cluster (kuma pkg/plugins/runtime/k8s/webhooks/
//     owner_reference_mutator.go). A copied reference carries the source
//     cluster's UID: on any other cluster, or once the Mesh was recreated,
//     the garbage collector deletes the object as orphaned. 3.0 also rejects
//     a Mesh owner that disagrees with the resource's mesh (UPGRADE.md, "A
//     resource's Mesh ownerReference must match its mesh").
var serverSideMetadata = []string{
	"resourceVersion", "uid", "creationTimestamp", "generation", "selfLink",
	"managedFields", "ownerReferences", "deletionTimestamp", "deletionGracePeriodSeconds",
}

// lastAppliedAnnotation is written by client-side `kubectl apply`. A copy
// pins the old applied state onto the object on the next apply.
const lastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// stripServerSideFields removes what the server computes from an extracted
// object so the file can be re-applied as written:
//
//   - status (both formats): computed by the CP; migrate never reads it;
//   - Kubernetes metadata: serverSideMetadata and lastAppliedAnnotation;
//   - Universal: creationTime, modificationTime and kri, all CP-computed.
//
// Labels and annotations users set are kept, including the CP-computed
// kuma.io/* labels: the zone origin filter and the migrator read them, and the
// CP recomputes them on apply anyway.
func stripServerSideFields(obj map[string]interface{}) {
	delete(obj, "status")
	delete(obj, "creationTime")
	delete(obj, "modificationTime")
	delete(obj, "kri")
	meta, ok := obj["metadata"].(map[string]interface{})
	if !ok {
		return
	}
	for _, f := range serverSideMetadata {
		delete(meta, f)
	}
	if ann, ok := meta["annotations"].(map[string]interface{}); ok {
		delete(ann, lastAppliedAnnotation)
		if len(ann) == 0 {
			delete(meta, "annotations")
		}
	}
}

// writeResourceFiles splits a multi-document YAML stream into individual files under outputDir.
// See writeResourceObject for the filters applied to each document.
// Returns the number of files written.
func writeResourceFiles(data []byte, outputDir string, skipSet map[string]bool, cpMode, cpModeDir, meshName, meshFilter, outputFormat string, notes *extractNotes) (int, error) {
	docs := splitYAMLDocs(data)
	count := 0
	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}
		n, err := writeSingleResourceDoc(doc, outputDir, skipSet, cpMode, cpModeDir, meshName, meshFilter, outputFormat, notes)
		if err != nil {
			return count, err
		}
		count += n
	}
	return count, nil
}

// writeSingleResourceDoc parses one YAML document and hands it to writeResourceObject.
func writeSingleResourceDoc(doc string, outputDir string, skipSet map[string]bool, cpMode, cpModeDir, meshName, meshFilter, outputFormat string, notes *extractNotes) (int, error) {
	var obj map[string]interface{}
	if err := yaml.Unmarshal([]byte(doc), &obj); err != nil || obj == nil {
		return 0, nil
	}
	return writeResourceObject(obj, outputDir, skipSet, cpMode, cpModeDir, meshName, meshFilter, outputFormat, notes)
}

// writeResourceObject writes one resource to disk, or expands a list into its
// items. Returns the number of files written. Both extraction paths (kubectl
// and kumactl) go through it.
//
// Supported formats:
//   - Kubernetes-style: kind/apiVersion + metadata.name  (kubectl/kumactl Kubernetes output)
//   - Universal-style: type + name at top level          (kumactl Universal output)
//   - Kubernetes list: kind ends in "List", items[]      (kumactl -o yaml on Kube returns MeshMetricList)
//   - Universal list:  top-level items[], no kind/type   ({total: N, items: [...]})
//
// When outputFormat is "kubernetes", Universal-format documents are converted
// to Kubernetes format (universalToKubernetes).
//
// Filters, in order:
//   - Insight kinds and skipSet kinds;
//   - CP-generated resources (isCPGenerated), counted in notes;
//   - Global CP: zone-only kinds; kuma.io/origin: zone (recorded in notes);
//   - federated Zone CP: kuma.io/origin: global (zoneOriginDecision);
//   - meshFilter: resources of another mesh. Global-scoped ones are kept.
//
// meshName is the mesh the caller fetched the resource for (kumactl --mesh);
// when empty the mesh is read from the resource (resourceMesh). Files go to
// <outputDir>/<cpModeDir>/mesh-<mesh>/<sub>/, or
// <outputDir>/<cpModeDir>/global-scoped-resources/<sub>/ without a mesh.
// Server-written fields are removed first (stripServerSideFields).
func writeResourceObject(obj map[string]interface{}, outputDir string, skipSet map[string]bool, cpMode, cpModeDir, meshName, meshFilter, outputFormat string, notes *extractNotes) (int, error) {
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
			n, _ := writeResourceObject(itemMap, outputDir, skipSet, cpMode, cpModeDir, meshName, meshFilter, outputFormat, notes)
			count += n
		}
		return count, nil
	}

	// Convert Universal-format documents to Kubernetes format when requested.
	// A Universal document has "type" but no "kind" at the top level.
	if outputFormat == "kubernetes" {
		if _, hasKind := obj["kind"]; !hasKind {
			if _, hasType := obj["type"]; hasType {
				obj = universalToKubernetes(obj)
				kind, _ = obj["kind"].(string)
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
	if meshName == "" {
		meshName = resourceMesh(obj, kind, name, labels)
	}

	if isCPGenerated(labels) {
		notes.generated(kind)
		return 0, nil
	}

	// On a Global CP, skip zone-only kinds (MeshGatewayInstance, MeshGatewayConfig).
	if cpMode == CPModeGlobal && isZoneOnlyKind(kind) {
		return 0, nil
	}

	// On a Global CP, skip resources with kuma.io/origin: zone.
	// These are zone-created policies synced read-only to the Global CP; they must be
	// extracted from the originating zone instead (identified by kuma.io/zone label).
	if cpMode == CPModeGlobal {
		if origin, _ := labels["kuma.io/origin"].(string); origin == CPModeZone {
			zoneName, _ := labels["kuma.io/zone"].(string)
			notes.zoneOriginSkip(ZoneOriginSkip{Kind: kind, Name: name, ZoneName: zoneName})
			return 0, nil
		}
	}

	// Apply mesh filter: skip if a specific mesh was requested and this resource
	// belongs to a different mesh. Resources with no mesh association are always kept.
	if meshFilter != "" && meshName != "" && meshName != meshFilter {
		return 0, nil
	}

	// On a federated Zone CP drop the copies synced from Global. Callers pass ""
	// instead of CPModeZone for a non-federated zone (originFilterMode), which
	// keeps everything.
	if cpMode == CPModeZone {
		origin, _ := labels["kuma.io/origin"].(string)
		keep, unlabelled := zoneOriginDecision(origin)
		if !keep {
			notes.globalCopy()
			return 0, nil
		}
		if unlabelled {
			notes.keptUnlabelled(kind)
		}
	}

	stripServerSideFields(obj)
	out, err := yaml.Marshal(obj)
	if err != nil {
		ui.Warn(fmt.Sprintf("marshal %s/%s: %v", kind, name, err))
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
	//   <outputDir>/<cpModeDir>/mesh-<mesh>/<sub>              mesh-scoped
	//   <outputDir>/<cpModeDir>/global-scoped-resources/<sub>  no mesh
	//   <outputDir>/mesh-<mesh>/<sub>                          no CP-mode dir
	//   <outputDir>/<sub>                                      flat
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
	if err := os.WriteFile(outPath, out, 0644); err != nil {
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

// printExtractNotes prints what the filters did, after ExtractDone.
func printExtractNotes(notes *extractNotes) {
	if notes == nil {
		return
	}
	if len(notes.KeptUnlabelled) > 0 {
		fmt.Println()
		ui.Info(fmt.Sprintf("%d resource(s) without a kuma.io/origin label kept (created on this zone): %s",
			sumCounts(notes.KeptUnlabelled), formatCounts(notes.KeptUnlabelled)))
		ui.InfoIndented("Kuma 2.x labels only the kinds its defaulting webhook covers; legacy policies,")
		ui.InfoIndented("ExternalService, ProxyTemplate, MeshGatewayRoute and Kong Mesh RBAC kinds are never labelled.")
		ui.InfoIndented("Copies synced from Global always carry kuma.io/origin: global.")
	}
	if notes.GlobalCopies > 0 {
		fmt.Println()
		ui.Info(fmt.Sprintf("%d resource(s) synced from the Global CP (kuma.io/origin: global) skipped — extract them from the Global CP.",
			notes.GlobalCopies))
	}
	if len(notes.Generated) > 0 {
		fmt.Println()
		ui.Info(fmt.Sprintf("%d resource(s) generated by the control plane skipped (kuma.io/managed-by): %s",
			sumCounts(notes.Generated), formatCounts(notes.Generated)))
		ui.InfoIndented("The CP recreates them from Kubernetes Services or Dataplanes.")
	}
	printZoneOriginSkips(notes.ZoneOriginSkips)
}

func sumCounts(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// formatCounts renders kind → count as "Kind1 2, Kind2 1", sorted by kind.
func formatCounts(m map[string]int) string {
	kinds := make([]string, 0, len(m))
	for k := range m {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	parts := make([]string, len(kinds))
	for i, k := range kinds {
		parts[i] = fmt.Sprintf("%s %d", k, m[k])
	}
	return strings.Join(parts, ", ")
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
	PrintCPInfo(CPInfo{Mode: cpMode, ZoneName: zoneName, Federated: true}, zones)
}

// PrintCPInfo prints the CP mode banner, including whether a zone CP is
// federated (connected to a Global CP) and the origin filter that follows.
func PrintCPInfo(info CPInfo, zones []string) {
	cpMode, zoneName := info.Mode, info.ZoneName
	if info.IsNonFederatedZone() {
		label := "zone, not federated"
		if zoneName != "" {
			label = fmt.Sprintf("zone (%s), not federated", zoneName)
		}
		ui.KV("CP mode:", label)
		ui.Info("This Zone CP is not connected to a Global CP: every resource in its store is its own.")
		ui.InfoIndented("No kuma.io/origin filter is applied — all resources are extracted.")
		return
	}
	switch cpMode {
	case CPModeZone:
		ui.KV("CP mode:", fmt.Sprintf("zone (%s), federated", zoneName))
		ui.Warn("Extracting from a federated Zone CP — copies synced from Global (kuma.io/origin: global) are skipped.")
		ui.WarnIndented("For a complete policy set, also run extract against the Global CP.")
		ui.Info("Resources created on this zone are extracted: kuma.io/origin: zone, or no origin label")
		ui.InfoIndented("(Kuma 2.x does not label legacy policies, ExternalService, ProxyTemplate, MeshGatewayRoute, …).")
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
		ui.Info("standalone is removed in Kuma 3.0: rename KUMA_MODE / controlPlane.mode to zone before upgrading.")
	default:
		ui.KV("CP mode:", "unknown")
		ui.Warn("Could not detect CP mode — extracting all resources.")
	}
}

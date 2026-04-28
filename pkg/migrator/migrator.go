package migrator

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Kong/kuma-migrator/pkg/config"
	"github.com/Kong/kuma-migrator/pkg/resource"
	"github.com/Kong/kuma-migrator/pkg/ui"
	"sigs.k8s.io/yaml"
)

// ---- Public report types ----------------------------------------------------

// DocChange captures what happened to a single YAML document within a file.
type DocChange struct {
	InputKind      string   // e.g. "Timeout", "MeshGatewayRoute"
	InputName      string   // resource name
	InputNamespace string   // metadata.namespace (empty for cluster-scoped or Universal resources)
	Scenario       Scenario // detected migration scenario
	Warnings       []string // per-document warnings
	ErrMsg         string   // non-empty if transformation failed
}

// FileReport captures the transformation results for a single YAML file.
type FileReport struct {
	FileName        string
	Label           string // e.g. "MIGRATED LEGACY", "ALREADY MIGRATED", "SKIP", "ERROR"
	Subfolder       string // output subdirectory (e.g. "resiliency", "mesh")
	CPModeDir       string // CP mode prefix from input path (e.g. "global", "zone", "standalone")
	OutputCPModeDir string // effective output CP mode dir (may differ from CPModeDir for GW resources from global)
	MeshDir         string // mesh directory from input path (e.g. "default", "prod"); empty for flat/legacy layout
	InputRelPath    string // input file path relative to inputDir
	OutputRelPath   string   // primary output file path relative to outputDir (first doc, for stdout)
	OutputRelPaths  []string // all output file paths relative to outputDir (all docs, for report)
	HasError        bool
	Changes         []DocChange
	EnvVarHits      []EnvVarHit
	AnnotHits       []AnnotationHit
}

// MigrationReport is the top-level result of a plan or apply run.
type MigrationReport struct {
	Mode      string // "plan" or "apply"
	InputDir  string
	OutputDir string
	Files     []FileReport

	// Aggregates (computed from Files).
	TotalFiles       int
	MigratedCount    int
	AlreadyDoneCount int
	SkippedCount     int
	ErrorCount       int

	// Deduped service address mappings across all files.
	AddressMappings []EnvVarHit
	// Deduped annotation hits across all files.
	AnnotationHits []AnnotationHit
}

// ---- Public entry points ----------------------------------------------------

// Plan reads all YAML files from inputDir, runs every transformation in dry-run
// mode (no output files are written), and writes a Markdown plan report to
// outputDir/migration-plan.md.
//
// The plan shows every change that would be made and all warnings, letting you
// review before committing.
//
// meshFilter, when non-empty, restricts processing to files under the named
// mesh subdirectory (e.g. "default"). Files without a mesh directory prefix
// are always processed.
func Plan(inputDir, outputDir, meshFilter string) error {
	ui.Header("plan")
	report, err := runMigration(inputDir, outputDir, false, meshFilter)
	if err != nil {
		return err
	}
	report.Mode = "plan"
	printReportToStdout(report)
	return WriteMarkdownReport(report, filepath.Join(outputDir, "migration-plan.md"))
}

// Migrate reads all YAML files from inputDir, transforms them, writes the results
// to outputDir, and writes a Markdown report to outputDir/migration-report.md.
//
// meshFilter, when non-empty, restricts processing to files under the named
// mesh subdirectory (e.g. "default"). Files without a mesh directory prefix
// are always processed.
func Migrate(inputDir, outputDir, meshFilter string) error {
	ui.Header("migrate")
	report, err := runMigration(inputDir, outputDir, true, meshFilter)
	if err != nil {
		return err
	}
	report.Mode = "apply"
	printReportToStdout(report)
	return WriteMarkdownReport(report, filepath.Join(outputDir, "migration-report.md"))
}

// ---- Internal engine --------------------------------------------------------

// isKindSubfolder returns true when s is a recognised per-kind output subdirectory
// name. These names are produced by resource.KindSubfolder and appear as the final
// path component before the YAML filename in the extract output layout.
// Used during path detection to tell kind-subfolders apart from context/mesh dirs.
func isKindSubfolder(s string) bool {
	switch s {
	case "resiliency", "routing", "zero-trust", "mesh", "observability", "other",
		"gateway", "workload":
		return true
	}
	return false
}

func runMigration(inputDir, outputDir string, writeFiles bool, meshFilter string) (*MigrationReport, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output directory %q: %w", outputDir, err)
	}

	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	skipSet := cfg.SkipSet()

	report := &MigrationReport{
		InputDir:  inputDir,
		OutputDir: outputDir,
	}
	allHits := map[string]EnvVarHit{}

	err = filepath.WalkDir(inputDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if ext := strings.ToLower(filepath.Ext(d.Name())); ext != ".yaml" && ext != ".yml" {
			return nil
		}

		// Detect context directory and mesh directory from the relative path.
		//
		// Context-first layout (produced by current extract):
		//   <inputDir>/<ctxDir>/<meshName>/<sub>/file.yaml  → cpModeDir=<ctxDir>, meshDir=<meshName>
		//   <inputDir>/<ctxDir>/global/<sub>/file.yaml       → cpModeDir=<ctxDir>, meshDir=""
		//
		// Legacy layout (flat, or pre-context-dir extract output):
		//   <inputDir>/<sub>/file.yaml                       → cpModeDir="", meshDir=""
		//   <inputDir>/<anyDir>/<sub>/file.yaml              → cpModeDir=<anyDir>, meshDir=""
		//
		// Detection rule: a path component is a kind-subfolder (leaf) if isKindSubfolder
		// returns true. The first non-kind-subfolder component is cpModeDir; the second
		// non-kind-subfolder component that is not the reserved "global-scoped-resources"
		// directory is meshDir. Mesh dirs may carry the "mesh-" prefix (current extract
		// output); the prefix is stripped to obtain the plain Kuma mesh name used for
		// filtering and FileReport.MeshDir.
		meshDir := ""
		cpModeDir := ""
		if rel, relErr := filepath.Rel(inputDir, path); relErr == nil {
			parts := strings.SplitN(filepath.ToSlash(rel), "/", 5)
			if len(parts) >= 2 {
				first := parts[0]
				if !isKindSubfolder(first) {
					cpModeDir = first
					if len(parts) >= 3 {
						second := parts[1]
						if !isKindSubfolder(second) && second != "global-scoped-resources" {
							// Strip the "mesh-" prefix added by the extractor so that
							// meshDir holds the plain Kuma mesh name for filtering.
							meshDir = strings.TrimPrefix(second, "mesh-")
						}
					}
				}
			}
		}

		// Apply mesh filter: skip files that belong to a different named mesh.
		// Files without a mesh directory prefix (old layout) are always processed.
		if meshFilter != "" && meshDir != "" && meshDir != meshFilter {
			return nil
		}

		report.TotalFiles++
		fr := processFile(path, outputDir, cpModeDir, meshDir, writeFiles, skipSet)
		if rel, relErr := filepath.Rel(inputDir, path); relErr == nil {
			fr.InputRelPath = filepath.ToSlash(rel)
		}
		report.Files = append(report.Files, fr)

		for _, h := range fr.EnvVarHits {
			allHits[h.MappingKey()] = h
		}
		report.AnnotationHits = append(report.AnnotationHits, fr.AnnotHits...)

		switch fr.Label {
		case labelMigratedLegacy, labelMigratedSubset, labelMigratedRules, labelMigratedMesh, labelMigratedES, labelMigratedGW, labelMigratedOPA:
			report.MigratedCount++
		case labelAlreadyDone:
			report.AlreadyDoneCount++
		case labelSkipped, labelSkippedEmpty:
			report.SkippedCount++
		case labelError, labelPartialError:
			report.ErrorCount++
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk input directory %q: %w", inputDir, err)
	}

	// Deduplicate and sort address mappings.
	keys := make([]string, 0, len(allHits))
	for k := range allHits {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		report.AddressMappings = append(report.AddressMappings, allHits[k])
	}

	// Deduplicate annotation hits.
	seen := map[string]bool{}
	var dedupedAnnot []AnnotationHit
	for _, h := range report.AnnotationHits {
		k := h.Kind + "/" + h.Namespace + "/" + h.Name + "/" + h.AnnotationKey
		if !seen[k] {
			seen[k] = true
			dedupedAnnot = append(dedupedAnnot, h)
		}
	}
	report.AnnotationHits = dedupedAnnot

	return report, nil
}

// Label constants — used to classify file results consistently across stdout
// output and the Markdown report.
const (
	labelMigratedLegacy    = "MIGRATED LEGACY"
	labelMigratedSubset    = "MIGRATED SUBSET"
	labelMigratedRules    = "MIGRATED RULES"
	labelMigratedMesh = "MIGRATED MESH"
	labelMigratedES   = "MIGRATED ES"
	labelMigratedGW   = "MIGRATED GW"
	labelMigratedOPA  = "MIGRATED OPA"
	labelAlreadyDone  = "ALREADY MIGRATED"
	labelSkipped      = "SKIP"
	labelSkippedEmpty = "SKIP (empty)"
	labelError        = "ERROR"
	labelPartialError = "PARTIAL ERROR"
)

// isGatewayAPIOutputKind reports whether kind is a Gateway API output kind that must
// be applied to zone clusters (not to the Global CP).
func isGatewayAPIOutputKind(kind string) bool {
	switch kind {
	case "Gateway", "HTTPRoute", "TCPRoute", "GatewayClass", "ReferenceGrant":
		return true
	}
	return false
}

func processFile(inputPath, outputDir, cpModeDir, meshDir string, writeFile bool, skipSet map[string]bool) FileReport {
	name := filepath.Base(inputPath)
	fr := FileReport{FileName: name, CPModeDir: cpModeDir, OutputCPModeDir: cpModeDir, MeshDir: meshDir}

	data, err := os.ReadFile(inputPath)
	if err != nil {
		fr.Label = labelError
		fr.HasError = true
		fr.Changes = []DocChange{{ErrMsg: err.Error()}}
		return fr
	}

	docs := splitYAMLDocuments(data)
	if len(docs) == 0 {
		fr.Label = labelSkippedEmpty
		return fr
	}

	var outputDocs [][]byte
	foundA, foundB, foundC, foundD, foundMesh, foundES, foundGW, foundOPA, foundSkipped := false, false, false, false, false, false, false, false, false

	for _, doc := range docs {
		kind, name2 := probeKindName(doc)
		dc := DocChange{InputKind: kind, InputName: name2, InputNamespace: probeNamespace(doc)}

		if skipSet[kind] {
			dc.Scenario = ScenarioSkipped
			fr.Changes = append(fr.Changes, dc)
			foundSkipped = true
			continue
		}

		results, warnings, scenario, tErr := TransformDocument(doc)
		dc.Scenario = scenario
		dc.Warnings = warnings
		if tErr != nil {
			dc.ErrMsg = tErr.Error()
			fr.HasError = true
			outputDocs = append(outputDocs, doc) // preserve original on error
		} else {
			outputDocs = append(outputDocs, results...)
		}

		switch scenario {
		case ScenarioLegacy:
			foundA = true
		case ScenarioSubset:
			foundB = true
		case ScenarioPassthrough:
			foundC = true
		case ScenarioRules:
			foundD = true
		case ScenarioMesh:
			foundMesh = true
		case ScenarioExternalService:
			foundES = true
		case ScenarioGateway, ScenarioGatewayInstance, ScenarioHTTPRoute, ScenarioTCPRoute, ScenarioGatewayRoute:
			foundGW = true
		case ScenarioOPAPolicy:
			foundOPA = true
		case ScenarioSkipped:
			foundSkipped = true
		}

		fr.Changes = append(fr.Changes, dc)

		if hits, scanErr := ScanWorkloadEnvVars(doc); scanErr == nil {
			fr.EnvVarHits = append(fr.EnvVarHits, hits...)
		}
		if annotHits, annotErr := ScanKumaAnnotations(doc); annotErr == nil {
			fr.AnnotHits = append(fr.AnnotHits, annotHits...)
		}
	}

	// Determine the primary subfolder from the first output document.
	if len(outputDocs) > 0 {
		kind, _ := probeKindName(outputDocs[0])
		fr.Subfolder = resource.KindSubfolder(kind)
	}

	for _, doc := range outputDocs {
		kind, _ := probeKindName(doc)
		sub := resource.KindSubfolder(kind)
		// Compute output directory (context-first layout, mirrors extract output):
		//   <outputDir>/<cpModeDir>/<meshDir>/<sub>   when context+mesh present; GW API → global/
		//   <outputDir>/<cpModeDir>/global/<sub>       when context set but no mesh (or GW API redirect)
		//   <outputDir>/<sub>                          otherwise (flat / legacy)
		var dir string
		if cpModeDir != "" && meshDir != "" {
			if isGatewayAPIOutputKind(kind) {
				// Gateway API resources (Gateway, HTTPRoute, …) are Kubernetes-native and
				// must be applied to zone clusters, not to the Global CP. Redirect them to
				// the global-scoped-resources/ sub-directory so it is clear where they belong.
				dir = filepath.Join(outputDir, cpModeDir, "global-scoped-resources", sub)
				fr.OutputCPModeDir = "global-scoped-resources"
			} else {
				dir = filepath.Join(outputDir, cpModeDir, "mesh-"+meshDir, sub)
			}
		} else if cpModeDir != "" {
			dir = filepath.Join(outputDir, cpModeDir, "global-scoped-resources", sub)
		} else {
			dir = filepath.Join(outputDir, sub)
		}
		fname := outputDocFilename(doc)

		// Record output paths for display and apply checklist.
		if rel, relErr := filepath.Rel(outputDir, filepath.Join(dir, fname)); relErr == nil {
			relSlash := filepath.ToSlash(rel)
			if fr.OutputRelPath == "" {
				fr.OutputRelPath = relSlash
			}
			fr.OutputRelPaths = append(fr.OutputRelPaths, relSlash)
		}

		if writeFile {
			if err := os.MkdirAll(dir, 0755); err != nil {
				fr.Label = labelError
				fr.HasError = true
				return fr
			}
			if err := os.WriteFile(filepath.Join(dir, fname), append(doc, '\n'), 0644); err != nil {
				fr.Label = labelError
				fr.HasError = true
				return fr
			}
		}
	}

	switch {
	case fr.HasError:
		fr.Label = labelPartialError
	case foundA:
		fr.Label = labelMigratedLegacy
	case foundB:
		fr.Label = labelMigratedSubset
	case foundD:
		fr.Label = labelMigratedRules
	case foundMesh:
		fr.Label = labelMigratedMesh
	case foundES:
		fr.Label = labelMigratedES
	case foundGW:
		fr.Label = labelMigratedGW
	case foundOPA:
		fr.Label = labelMigratedOPA
	case foundC:
		fr.Label = labelAlreadyDone
	case foundSkipped:
		fr.Label = labelSkipped
	default:
		fr.Label = labelSkippedEmpty
	}

	return fr
}

// probeKindName extracts the kind and name from a raw YAML document without
// a full unmarshal into a typed struct.
func probeKindName(raw []byte) (kind, name string) {
	var obj map[string]interface{}
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		return "", ""
	}
	if k, ok := obj["kind"].(string); ok && k != "" {
		kind = k
	} else if t, ok := obj["type"].(string); ok && t != "" {
		kind = t
	}
	name = extractNameFromObj(obj)
	return kind, name
}

// ---- Stdout printer ---------------------------------------------------------

func printReportToStdout(r *MigrationReport) {
	for _, fr := range r.Files {
		mesh := fr.MeshDir
		switch fr.Label {
		case labelError:
			ui.FileError(mesh, fr.FileName)
		case labelPartialError:
			ui.FilePartialError(mesh, fr.FileName)
		case labelMigratedLegacy:
			ui.FileMigrated("LEGACY", mesh, fr.FileName)
		case labelMigratedSubset:
			ui.FileMigrated("SUBSET", mesh, fr.FileName)
		case labelMigratedRules:
			ui.FileMigrated("RULES", mesh, fr.FileName)
		case labelMigratedMesh:
			ui.FileMigrated("MESH", mesh, fr.FileName)
		case labelMigratedES:
			ui.FileMigrated("EXTERNAL SERVICE", mesh, fr.FileName)
		case labelMigratedGW:
			ui.FileMigrated("GATEWAY", mesh, fr.FileName)
		case labelMigratedOPA:
			ui.FileMigrated("OPA", mesh, fr.FileName)
		case labelAlreadyDone:
			ui.FileAlreadyMigrated(mesh, fr.FileName)
		case labelSkipped:
			ui.FileSkipped(mesh, fr.FileName, "no recognised Kuma policy documents")
		default:
			ui.FileSkipped(mesh, fr.FileName, "empty after parsing")
		}
		ui.DocRelPaths(fr.InputRelPath, fr.OutputRelPath)

		for _, dc := range fr.Changes {
			if dc.ErrMsg != "" {
				ui.DocError(dc.ErrMsg)
			}
			for _, w := range dc.Warnings {
				ui.DocWarn(w)
			}
		}

		if len(fr.EnvVarHits) > 0 {
			ui.DocWorkload("legacy Kuma service addresses found in env vars:")
			for _, h := range fr.EnvVarHits {
				ui.DocWorkloadHit(fmt.Sprintf("%s/%s (ns: %s) · container %q · %s=%q",
					h.WorkloadKind, h.WorkloadName, h.Namespace,
					h.ContainerName, h.EnvVarName, h.RawValue))
			}
		}

		if len(fr.AnnotHits) > 0 {
			ui.DocAnnotation("deprecated boolean annotation values found:")
			for _, h := range fr.AnnotHits {
				ui.DocAnnotationHit(fmt.Sprintf("%s/%s (ns: %s) · %s=%q → %q",
					h.Kind, h.Name, h.Namespace, h.AnnotationKey, h.OldValue, h.NewValue))
			}
		}
	}

	ui.Summary(r.TotalFiles, r.MigratedCount, r.AlreadyDoneCount, r.SkippedCount, r.ErrorCount)

	if len(r.AddressMappings) > 0 {
		ui.SectionHeader("Service address mapping")
		ui.SectionNote("Update env vars in your Deployments/StatefulSets:")
		ui.SectionNote("replace the old kuma.io/service address with one of the new addresses below.")
		ui.SectionNote("Replace <zone> with your actual Kuma zone name for the mesh hostname.")
		for _, h := range r.AddressMappings {
			ui.SectionItem(h.FormatMapping())
		}
	}

	if len(r.AnnotationHits) > 0 {
		ui.SectionHeader("Deprecated annotation values")
		ui.SectionNote("Update 'yes'/'no' to 'true'/'false' in your manifests:")
		for _, h := range r.AnnotationHits {
			ns := h.Namespace
			if ns == "" {
				ns = "<cluster-scoped>"
			}
			ui.SectionItem(fmt.Sprintf("%s/%s (ns: %s) · %s: %q → %q",
				h.Kind, h.Name, ns, h.AnnotationKey, h.OldValue, h.NewValue))
		}
	}
}

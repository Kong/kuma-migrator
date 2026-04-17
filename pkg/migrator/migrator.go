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
	FileName      string
	Label         string // e.g. "MIGRATED LEGACY", "ALREADY MIGRATED", "SKIP", "ERROR"
	Subfolder     string // output subdirectory (e.g. "resiliency", "mesh")
	CPModeDir     string // CP mode prefix from input path (e.g. "global", "zone", "standalone")
	OutputCPModeDir string // effective output CP mode dir (may differ from CPModeDir for GW resources from global)
	HasError      bool
	Changes    []DocChange
	EnvVarHits []EnvVarHit
	AnnotHits  []AnnotationHit
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
func Plan(inputDir, outputDir string) error {
	report, err := runMigration(inputDir, outputDir, false)
	if err != nil {
		return err
	}
	report.Mode = "plan"
	printReportToStdout(report)
	return WriteMarkdownReport(report, filepath.Join(outputDir, "migration-plan.md"))
}

// Migrate reads all YAML files from inputDir, transforms them, writes the results
// to outputDir, and writes a Markdown report to outputDir/migration-report.md.
func Migrate(inputDir, outputDir string) error {
	report, err := runMigration(inputDir, outputDir, true)
	if err != nil {
		return err
	}
	report.Mode = "apply"
	printReportToStdout(report)
	return WriteMarkdownReport(report, filepath.Join(outputDir, "migration-report.md"))
}

// ---- Internal engine --------------------------------------------------------

func runMigration(inputDir, outputDir string, writeFiles bool) (*MigrationReport, error) {
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

		// Detect CP mode from the first directory component of the relative path.
		// e.g. "<inputDir>/global/resiliency/file.yaml"    → cpModeDir = "global"
		//      "<inputDir>/zone-eu-west/resiliency/file.yaml" → cpModeDir = "zone-eu-west"
		cpModeDir := ""
		if rel, relErr := filepath.Rel(inputDir, path); relErr == nil {
			parts := strings.SplitN(filepath.ToSlash(rel), "/", 2)
			if len(parts) >= 2 {
				first := parts[0]
				switch {
				case first == "global", first == "standalone", first == "unknown":
					cpModeDir = first
				case first == "zone" || strings.HasPrefix(first, "zone-"):
					cpModeDir = first
				}
			}
		}

		report.TotalFiles++
		fr := processFile(path, outputDir, cpModeDir, writeFiles, skipSet)
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

func processFile(inputPath, outputDir, cpModeDir string, writeFile bool, skipSet map[string]bool) FileReport {
	name := filepath.Base(inputPath)
	fr := FileReport{FileName: name, CPModeDir: cpModeDir, OutputCPModeDir: cpModeDir}

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

	if writeFile {
		for _, doc := range outputDocs {
			kind, _ := probeKindName(doc)
			sub := resource.KindSubfolder(kind)
			// Gateway API output kinds (Gateway, HTTPRoute, TCPRoute, GatewayClass, …) are
			// Kubernetes-native resources applied to zone clusters, never to the Global CP.
			// When the source file came from the global/ input folder, redirect the output to
			// all-zones/ so it is clear where to apply.
			effectiveCPModeDir := cpModeDir
			if cpModeDir == "global" && isGatewayAPIOutputKind(kind) {
				effectiveCPModeDir = "all-zones"
			}
			var dir string
			if effectiveCPModeDir != "" {
				dir = filepath.Join(outputDir, effectiveCPModeDir, sub)
			} else {
				dir = filepath.Join(outputDir, sub)
			}
			if err := os.MkdirAll(dir, 0755); err != nil {
				fr.Label = labelError
				fr.HasError = true
				return fr
			}
			fname := outputDocFilename(doc)
			if err := os.WriteFile(filepath.Join(dir, fname), append(doc, '\n'), 0644); err != nil {
				fr.Label = labelError
				fr.HasError = true
				return fr
			}
			// Track that output went to all-zones (for report apply-path accuracy).
			if effectiveCPModeDir != cpModeDir {
				fr.OutputCPModeDir = effectiveCPModeDir
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
		switch fr.Label {
		case labelError:
			fmt.Printf("[ERROR]            %s\n", fr.FileName)
		case labelPartialError:
			fmt.Printf("[PARTIAL ERROR]    %s (some documents could not be migrated)\n", fr.FileName)
		case labelMigratedLegacy:
			fmt.Printf("[MIGRATED LEGACY]  %s\n", fr.FileName)
		case labelMigratedSubset:
			fmt.Printf("[MIGRATED SUBSET]  %s\n", fr.FileName)
		case labelMigratedRules:
			fmt.Printf("[MIGRATED RULES]   %s\n", fr.FileName)
		case labelMigratedMesh:
			fmt.Printf("[MIGRATED MESH]    %s\n", fr.FileName)
		case labelMigratedES:
			fmt.Printf("[MIGRATED ES]      %s\n", fr.FileName)
		case labelMigratedGW:
			fmt.Printf("[MIGRATED GW]      %s\n", fr.FileName)
		case labelMigratedOPA:
			fmt.Printf("[MIGRATED OPA]     %s\n", fr.FileName)
		case labelAlreadyDone:
			fmt.Printf("[ALREADY MIGRATED] %s\n", fr.FileName)
		case labelSkipped:
			fmt.Printf("[SKIP]             %s: no recognised Kuma policy documents\n", fr.FileName)
		default:
			fmt.Printf("[SKIP]             %s: empty after parsing\n", fr.FileName)
		}

		for _, dc := range fr.Changes {
			if dc.ErrMsg != "" {
				fmt.Printf("  [ERROR] %s\n", dc.ErrMsg)
			}
			for _, w := range dc.Warnings {
				fmt.Printf("  [WARN] %s\n", w)
			}
		}

		if len(fr.EnvVarHits) > 0 {
			fmt.Printf("  [WORKLOAD] legacy Kuma service addresses found in env vars:\n")
			for _, h := range fr.EnvVarHits {
				fmt.Printf("    %s/%s (ns: %s) · container %q · %s=%q\n",
					h.WorkloadKind, h.WorkloadName, h.Namespace,
					h.ContainerName, h.EnvVarName, h.RawValue)
			}
		}

		if len(fr.AnnotHits) > 0 {
			fmt.Printf("  [ANNOTATION] deprecated boolean annotation values found:\n")
			for _, h := range fr.AnnotHits {
				fmt.Printf("    %s/%s (ns: %s) · %s=%q → %q\n",
					h.Kind, h.Name, h.Namespace, h.AnnotationKey, h.OldValue, h.NewValue)
			}
		}
	}

	fmt.Printf("\nSummary: %d file(s) processed — %d migrated, %d already migrated, %d skipped, %d error(s)\n",
		r.TotalFiles, r.MigratedCount, r.AlreadyDoneCount, r.SkippedCount, r.ErrorCount)

	if len(r.AddressMappings) > 0 {
		fmt.Println()
		fmt.Println("Service address mapping — update env vars in your Deployments/StatefulSets:")
		fmt.Println("  (replace the old kuma.io/service address with one of the new addresses below)")
		fmt.Println("  Replace <zone> with your actual Kuma zone name for the mesh hostname.")
		fmt.Println()
		for _, h := range r.AddressMappings {
			fmt.Println(h.FormatMapping())
		}
	}

	if len(r.AnnotationHits) > 0 {
		fmt.Println()
		fmt.Println("Deprecated annotation values — update 'yes'/'no' to 'true'/'false' in your manifests:")
		for _, h := range r.AnnotationHits {
			ns := h.Namespace
			if ns == "" {
				ns = "<cluster-scoped>"
			}
			fmt.Printf("  %s/%s (ns: %s) · %s: %q → %q\n",
				h.Kind, h.Name, ns, h.AnnotationKey, h.OldValue, h.NewValue)
		}
	}
}

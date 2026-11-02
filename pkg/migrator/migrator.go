package migrator

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/Kong/kuma-migrator/pkg/config"
	"github.com/Kong/kuma-migrator/pkg/extractor"
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
	InputMesh      string   // mesh of the source object (Universal `mesh` or the kuma.io/mesh label); may be empty
	Warnings       []string // per-document warnings
	ErrMsg         string   // non-empty if transformation failed
	// ManualRelPath is set when the document was written to the ManualDir
	// subtree instead of the apply tree (relative to outputDir).
	ManualRelPath string
	// Rejected is set when the transform returned a *RejectedByTargetError: the
	// output exists but the target control plane rejects it, so it went to
	// ManualDir. ErrMsg carries the reason.
	Rejected bool
	// Rewritten is set for a non-policy document (ScenarioSkipped) whose output
	// differs from the input, i.e. the deprecation scan changed it.
	Rewritten bool
	// MeshMTLS is set for a Mesh document that carries spec.mtls (top-level
	// mtls in Universal format). The Apply Checklist uses it under v3.
	MeshMTLS bool
}

// FileReport captures the transformation results for a single YAML file.
type FileReport struct {
	FileName        string
	Label           string   // e.g. "MIGRATED LEGACY", "ALREADY MIGRATED", "SKIP", "ERROR"
	Subfolder       string   // output subdirectory (e.g. "resiliency", "mesh")
	CPModeDir       string   // CP mode prefix from input path (e.g. "global", "zone", "standalone")
	OutputCPModeDir string   // effective output CP mode dir (may differ from CPModeDir for GW resources from global)
	MeshDir         string   // mesh directory from input path (e.g. "default", "prod"); empty for flat/legacy layout
	InputRelPath    string   // input file path relative to inputDir
	OutputRelPath   string   // primary output file path relative to outputDir (first doc, for stdout)
	OutputRelPaths  []string // all output file paths relative to outputDir (all docs, for report)
	// ManualRelPaths are the files written under ManualDir (v3 only). They are
	// not in OutputRelPaths, so no apply step lists them.
	ManualRelPaths []string
	// UniversalOutput is set when an apply-tree document is in Universal
	// format (type/name/mesh), which kubectl cannot apply.
	UniversalOutput bool
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
	Target    TargetVersion // --to-latest: which major version the output targets
	Files     []FileReport

	// Aggregates (computed from Files).
	TotalFiles       int
	MigratedCount    int
	AlreadyDoneCount int
	ScannedCount     int // non-policy files the deprecation scan rewrote or flagged
	ManualCount      int // files whose documents all went to ManualDir without error
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
func Plan(inputDir, outputDir, meshFilter string, target TargetVersion) error {
	ui.Header("migrate --dry-run")
	report, err := runMigration(inputDir, outputDir, false, meshFilter, target)
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
func Migrate(inputDir, outputDir, meshFilter string, target TargetVersion) error {
	ui.Header("migrate")
	report, err := runMigration(inputDir, outputDir, true, meshFilter, target)
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
		"gateway", "workload", backendsSubfolder:
		return true
	}
	return false
}

func runMigration(inputDir, outputDir string, writeFiles bool, meshFilter string, target TargetVersion) (*MigrationReport, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output directory %q: %w", outputDir, err)
	}

	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	// The skip list depends on the CP environment of each context directory
	// (see skipPolicy). Cache the metadata lookup per context.
	skipK8s := cfg.SkipSetForEnv(extractor.CPEnvKubernetes)
	skipUniversal := cfg.SkipSetForEnv(extractor.CPEnvUniversal)
	envByContext := map[string]string{}
	contextEnv := func(cpModeDir string) string {
		if env, ok := envByContext[cpModeDir]; ok {
			return env
		}
		env := ""
		if meta := extractor.ReadContextMeta(inputDir, cpModeDir); meta != nil {
			env = meta.Environment
		}
		envByContext[cpModeDir] = env
		return env
	}

	report := &MigrationReport{
		InputDir:  inputDir,
		OutputDir: outputDir,
		Target:    target,
	}
	allHits := map[string]EnvVarHit{}

	// Pre-pass: some conversions need a document other than the one being
	// transformed — TrafficLog/TrafficTrace reference a backend declared on the
	// Mesh, and a MeshGateway must name the GatewayClass generated from its
	// companion MeshGatewayInstance. One walk builds every index.
	opts := BuildTransformOptions(inputDir, target)
	if cfg.SystemNamespace != "" {
		opts.SystemNamespace = cfg.SystemNamespace
	}

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
		skip := skipPolicy{env: contextEnv(cpModeDir), kubernetes: skipK8s, universal: skipUniversal}
		fr := processFile(path, outputDir, cpModeDir, meshDir, writeFiles, skip, opts)
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
		case labelScanned:
			report.ScannedCount++
		case labelManual:
			report.ManualCount++
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
	labelMigratedLegacy = "MIGRATED LEGACY"
	labelMigratedSubset = "MIGRATED SUBSET"
	labelMigratedRules  = "MIGRATED RULES"
	labelMigratedMesh   = "MIGRATED MESH"
	labelMigratedES     = "MIGRATED ES"
	labelMigratedGW     = "MIGRATED GW"
	labelMigratedOPA    = "MIGRATED OPA"
	labelAlreadyDone    = "ALREADY MIGRATED"
	// labelScanned: not a policy, but the deprecation scan rewrote it or warned
	// about it. Written to the output tree like any other document.
	labelScanned = "SCANNED"
	// labelManual: every document of the file is a kind the target removes and
	// went to ManualDir without a conversion error (e.g. ZoneIngress,
	// MeshGatewayConfig, MeshGlobalRateLimit under v3).
	labelManual       = "MANUAL"
	labelSkipped      = "SKIP"
	labelSkippedEmpty = "SKIP (empty)"
	labelError        = "ERROR"
	labelPartialError = "PARTIAL ERROR"
)

// ManualDir is the per-context subdirectory that holds, under --to-latest v3,
// every document that must not be applied to a 3.0 control plane: originals
// whose conversion failed (TrafficRoute, VirtualOutbound, MeshGatewayInstance, ...)
// and objects whose kind 3.0 removes with no conversion (MeshGlobalRateLimit,
// ZoneIngress, ZoneEgress). It sits next to the mesh-*/ and
// global-scoped-resources/ directories:
//
//	<outputDir>/<ctx>/_manual/mesh-<mesh>/<sub>/<file>
//	<outputDir>/<ctx>/_manual/global-scoped-resources/<sub>/<file>
//	<outputDir>/_manual/<sub>/<file>                       (flat layout)
//
// The Apply Checklist never lists it: its kubectl steps apply leaf <sub>/
// directories (kubectl -f on a directory is not recursive), and its kumactl
// steps list files from FileReport.OutputRelPaths, which excludes these. The
// report lists the files under "Delete Before Upgrading to 3.0" and
// "Action Items -> Errors".
const ManualDir = "_manual"

// skipPolicy picks the skip list for a document.
//
// The environment recorded by extract in .kuma-migrator.json wins. When it is
// absent (older extract output, /config unreadable, or no context directory),
// the document shape decides: a document with `type` and no `kind` is Universal
// format and gets the Universal list; anything else gets the Kubernetes list.
// The shape is a reliable signal for kumactl Universal-format extracts. A
// Universal CP extracted with --output-format kubernetes and no recorded
// environment falls back to the Kubernetes list, which skips more, never less.
//
// An explicit per-environment `skip` list in the user config always wins over
// the built-in defaults (config.SkipSetForEnv).
type skipPolicy struct {
	env        string // extractor.CPEnvKubernetes, extractor.CPEnvUniversal or ""
	kubernetes map[string]bool
	universal  map[string]bool
}

func (p skipPolicy) skips(kind string, universalShape bool) bool {
	env := p.env
	if env == "" && universalShape {
		env = extractor.CPEnvUniversal
	}
	if env == extractor.CPEnvUniversal {
		return p.universal[kind]
	}
	return p.kubernetes[kind]
}

// isUniversalShape reports whether a raw document is in Kuma's Universal format
// (`type` instead of `kind`).
func isUniversalShape(raw []byte) bool {
	var obj map[string]interface{}
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		return false
	}
	k, _ := obj["kind"].(string)
	t, _ := obj["type"].(string)
	return k == "" && t != ""
}

// probeMesh returns the mesh of a raw document: the Universal top-level `mesh`
// field, else the kuma.io/mesh label.
func probeMesh(raw []byte) string {
	var obj map[string]interface{}
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	if m, ok := obj["mesh"].(string); ok && m != "" {
		return m
	}
	if meta, ok := obj["metadata"].(map[string]interface{}); ok {
		if labels, ok := meta["labels"].(map[string]interface{}); ok {
			if m, ok := labels[meshLabel].(string); ok {
				return m
			}
		}
	}
	return ""
}

// isGatewayAPIOutputKind reports whether kind is a Gateway API output kind that must
// be applied to zone clusters (not to the Global CP).
func isGatewayAPIOutputKind(kind string) bool {
	switch kind {
	case "Gateway", "HTTPRoute", "TCPRoute", "GatewayClass", "ReferenceGrant":
		return true
	}
	return false
}

// transformDocument is the per-document transform processFile calls. It is a
// variable so tests can substitute a fake (e.g. one returning a
// *RejectedByTargetError) without building an input that triggers it.
var transformDocument = TransformDocumentWithOptions

// sameYAML reports whether two YAML documents hold the same data, ignoring
// formatting and key order.
func sameYAML(a, b []byte) bool {
	var oa, ob interface{}
	if err := yaml.Unmarshal(a, &oa); err != nil {
		return false
	}
	if err := yaml.Unmarshal(b, &ob); err != nil {
		return false
	}
	return reflect.DeepEqual(oa, ob)
}

// meshHasMTLS reports whether a Mesh document sets mtls (spec.mtls on
// Kubernetes, top-level mtls in Universal format).
func meshHasMTLS(raw []byte) bool {
	var obj map[string]interface{}
	if err := yaml.Unmarshal(raw, &obj); err != nil {
		return false
	}
	if spec, ok := obj["spec"].(map[string]interface{}); ok && spec["mtls"] != nil {
		return true
	}
	return obj["mtls"] != nil
}

// outputDoc is one document to write, with the DocChange it came from.
type outputDoc struct {
	raw    []byte
	change int  // index into FileReport.Changes
	manual bool // v3: goes under ManualDir, not into the apply tree
}

func processFile(inputPath, outputDir, cpModeDir, meshDir string, writeFile bool, skip skipPolicy, opts TransformOptions) FileReport {
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

	v3 := opts.Target.IsV3()
	var outputDocs []outputDoc
	foundA, foundB, foundC, foundD, foundMesh, foundES, foundGW, foundOPA, foundSkipped := false, false, false, false, false, false, false, false, false
	foundScanned, foundManual := false, false

	for _, doc := range docs {
		kind, name2 := probeKindName(doc)
		dc := DocChange{InputKind: kind, InputName: name2, InputNamespace: probeNamespace(doc), InputMesh: probeMesh(doc)}
		if kind == "Mesh" {
			dc.MeshMTLS = meshHasMTLS(doc)
		}

		if skip.skips(kind, isUniversalShape(doc)) {
			dc.Scenario = ScenarioSkipped
			fr.Changes = append(fr.Changes, dc)
			foundSkipped = true
			continue
		}

		results, warnings, scenario, tErr := transformDocument(doc, opts)
		dc.Scenario = scenario
		dc.Warnings = warnings
		changeIdx := len(fr.Changes)
		var rejected *RejectedByTargetError
		manualNoErr := false // a document went to ManualDir without an error
		switch {
		case tErr != nil && errors.As(tErr, &rejected):
			// The transform produced output, but the target control plane rejects
			// it. Keep it out of the apply tree, like an errored conversion, but
			// keep the converted output (it is closer to the end state than the
			// original). Without output, keep the original.
			dc.ErrMsg = tErr.Error()
			dc.Rejected = true
			fr.HasError = true
			if len(results) == 0 {
				results = [][]byte{doc}
			}
			for _, res := range results {
				outputDocs = append(outputDocs, outputDoc{raw: res, change: changeIdx, manual: true})
			}
		case tErr != nil:
			dc.ErrMsg = tErr.Error()
			fr.HasError = true
			// Preserve the original. Under v3 it goes to ManualDir: it is either a
			// kind 3.0 removes or a document the migrator could not make valid, and
			// the apply tree must only hold what a 3.0 CP accepts. Under v2 it stays
			// in the apply tree: it came from the 2.x CP it is applied back to, so
			// re-applying it unchanged is a no-op.
			outputDocs = append(outputDocs, outputDoc{raw: doc, change: changeIdx, manual: v3})
		default:
			for _, res := range results {
				// v3: a result that is still a removed kind (MeshGlobalRateLimit,
				// ZoneIngress, ZoneEgress, MeshGatewayConfig pass through
				// unconverted) is not applicable.
				outKind, _ := probeKindName(res)
				manual := v3 && isRemovedInV3Kind(outKind)
				manualNoErr = manualNoErr || manual
				outputDocs = append(outputDocs, outputDoc{raw: res, change: changeIdx, manual: manual})
			}
		}
		if v3 {
			converted := tErr == nil && len(results) > 0
			if converted {
				outKind, _ := probeKindName(results[0])
				converted = outKind != kind
			}
			if note := v3DeleteBeforeUpgradeNote(kind, name2, converted); note != "" {
				dc.Warnings = append(dc.Warnings, note)
			}
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
		case ScenarioGateway, ScenarioGatewayInstance, ScenarioGatewayRoute:
			foundGW = true
		case ScenarioOPAPolicy:
			foundOPA = true
		case ScenarioSkipped:
			// Not a policy, but every document still goes through
			// ScanForDeprecations, which can rewrite it (e.g. Dataplane inbound
			// tags under v3) or warn about it (pod-template annotations).
			if tErr == nil {
				dc.Rewritten = len(results) != 1 || !sameYAML(doc, results[0])
			}
			if dc.Rewritten || len(dc.Warnings) > 0 {
				foundScanned = true
			} else {
				foundSkipped = true
			}
		}
		if manualNoErr {
			foundManual = true
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
		kind, _ := probeKindName(outputDocs[0].raw)
		fr.Subfolder = outputSubfolder(kind)
	}

	for _, od := range outputDocs {
		doc := od.raw
		kind, _ := probeKindName(doc)
		sub := outputSubfolder(kind)
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
		if od.manual {
			dir = manualDirFor(outputDir, cpModeDir, dir)
		}
		fname := outputDocFilename(doc)

		// Record output paths for display and apply checklist. Manual files are
		// kept out of OutputRelPaths so no apply step lists them.
		if rel, relErr := filepath.Rel(outputDir, filepath.Join(dir, fname)); relErr == nil {
			relSlash := filepath.ToSlash(rel)
			if fr.OutputRelPath == "" {
				fr.OutputRelPath = relSlash
			}
			if od.manual {
				fr.ManualRelPaths = append(fr.ManualRelPaths, relSlash)
				if fr.Changes[od.change].ManualRelPath == "" {
					fr.Changes[od.change].ManualRelPath = relSlash
				}
			} else {
				fr.OutputRelPaths = append(fr.OutputRelPaths, relSlash)
				if isUniversalShape(doc) {
					fr.UniversalOutput = true
				}
			}
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
	case foundManual && len(fr.OutputRelPaths) == 0:
		fr.Label = labelManual
	case foundScanned:
		fr.Label = labelScanned
	case foundC:
		fr.Label = labelAlreadyDone
	case foundSkipped:
		fr.Label = labelSkipped
	default:
		fr.Label = labelSkippedEmpty
	}

	return fr
}

// manualDirFor moves an apply-tree directory under ManualDir, keeping the rest
// of its path: <outputDir>/<ctx>/<rest> -> <outputDir>/<ctx>/_manual/<rest>, or
// <outputDir>/<rest> -> <outputDir>/_manual/<rest> for the flat layout.
func manualDirFor(outputDir, cpModeDir, dir string) string {
	base := outputDir
	if cpModeDir != "" {
		base = filepath.Join(outputDir, cpModeDir)
	}
	rest, err := filepath.Rel(base, dir)
	if err != nil {
		return filepath.Join(base, ManualDir)
	}
	return filepath.Join(base, ManualDir, rest)
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

// targetLine names the release line a target describes, for short messages.
func targetLine(t TargetVersion) string {
	if t.IsV3() {
		return "3.0"
	}
	return "2.x"
}

// scannedReason is the stdout detail for a labelScanned file.
func scannedReason(fr FileReport) string {
	for _, dc := range fr.Changes {
		if dc.Rewritten {
			return "not a policy; rewritten by the deprecation scan"
		}
	}
	return "not a policy; copied unchanged, see warnings"
}

// skippedReason is the stdout detail for a labelSkipped file: either the skip
// list kept it out of the output, or it is not a Kuma policy and was copied
// unchanged.
func skippedReason(fr FileReport) string {
	if len(fr.OutputRelPaths) == 0 && len(fr.ManualRelPaths) == 0 {
		return "on the skip list; not written"
	}
	return "not a Kuma policy; copied unchanged"
}

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
		case labelScanned:
			ui.FileScanned(mesh, fr.FileName, scannedReason(fr))
		case labelManual:
			ui.FileManual(mesh, fr.FileName, fmt.Sprintf("kind removed in %s; kept under %s/, not applied", targetLine(r.Target), ManualDir))
		case labelSkipped:
			ui.FileSkipped(mesh, fr.FileName, skippedReason(fr))
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

	ui.Summary(ui.SummaryCounts{
		Total:       r.TotalFiles,
		Migrated:    r.MigratedCount,
		AlreadyDone: r.AlreadyDoneCount,
		Scanned:     r.ScannedCount,
		Manual:      r.ManualCount,
		Skipped:     r.SkippedCount,
		Errors:      r.ErrorCount,
	})

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

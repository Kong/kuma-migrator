package migrator

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Kong/kuma-migrator/pkg/extractor"
)

// WriteMarkdownReport renders the MigrationReport as a Markdown file and
// writes it to path.
func WriteMarkdownReport(r *MigrationReport, path string) error {
	var b strings.Builder
	writeMarkdown(&b, r)
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("write report %q: %w", path, err)
	}
	fmt.Printf("\nReport written to: %s\n", path)
	return nil
}

func writeMarkdown(b *strings.Builder, r *MigrationReport) {
	isPlan := r.Mode == "plan"
	title := "Migration Plan"
	if !isPlan {
		title = "Migration Report"
	}

	// ── Header ────────────────────────────────────────────────────────────────
	line(b, "# Kuma Migrator — "+title)
	line(b, "")
	linef(b, "Generated: %s", time.Now().Format("2006-01-02 15:04:05"))
	linef(b, "Input:     `%s`", r.InputDir)
	linef(b, "Output:    `%s`", r.OutputDir)
	line(b, "")
	if isPlan {
		line(b, "> **Dry run** — no files have been written.")
		line(b, "> Run `kuma-migrator migrate` with the same flags to apply these changes.")
		line(b, "")
	}

	// ── Summary ───────────────────────────────────────────────────────────────
	line(b, "## Summary")
	line(b, "")
	line(b, "| | |")
	line(b, "|---|---:|")
	linef(b, "| Files processed | %d |", r.TotalFiles)
	linef(b, "| Migrated | %d |", r.MigratedCount)
	linef(b, "| Already migrated | %d |", r.AlreadyDoneCount)
	linef(b, "| Skipped (non-policy) | %d |", r.SkippedCount)
	linef(b, "| Errors | %d |", r.ErrorCount)
	line(b, "")

	// ── Classify files ────────────────────────────────────────────────────────
	var errFiles, migratedFiles, alreadyFiles, skippedFiles []FileReport
	for _, fr := range r.Files {
		switch fr.Label {
		case labelError, labelPartialError:
			errFiles = append(errFiles, fr)
		case labelAlreadyDone:
			alreadyFiles = append(alreadyFiles, fr)
		case labelSkipped, labelSkippedEmpty:
			skippedFiles = append(skippedFiles, fr)
		default:
			migratedFiles = append(migratedFiles, fr)
		}
	}

	// ── Migrated files ────────────────────────────────────────────────────────
	if len(migratedFiles) > 0 || len(errFiles) > 0 {
		if isPlan {
			line(b, "## Files That Would Be Migrated")
		} else {
			line(b, "## Migrated Files")
		}
		line(b, "")
		line(b, "> Grouped by output subfolder. Apply **`mesh/`** last — those `Mesh` CRs")
		line(b, "> enable `spec.meshServices.mode: Exclusive` which disables legacy routing.")
		line(b, "")

		subfolderOrder := []string{"resiliency", "routing", "zero-trust", "observability", "other", "mesh"}

		bySubfolder := map[string][]FileReport{}
		for _, fr := range migratedFiles {
			sub := fr.Subfolder
			if sub == "" {
				sub = "other"
			}
			outDir := fr.OutputCPModeDir
			if outDir == "" {
				outDir = fr.CPModeDir
			}
			key := sub
			if outDir != "" {
				key = outDir + "/" + sub
			}
			bySubfolder[key] = append(bySubfolder[key], fr)
		}
		// Errors go into their own subfolder group.
		if len(errFiles) > 0 {
			bySubfolder["⚠ errors"] = errFiles
		}

		// Collect distinct CP mode dirs in preferred order:
		// global/standalone first, then all-zones, then zone-* dirs (sorted), then unknown, then flat (no mode).
		cpModeDirsSeen := map[string]bool{}
		var cpModeDirs []string
		for _, fr := range migratedFiles {
			d := fr.OutputCPModeDir
			if d == "" {
				d = fr.CPModeDir
			}
			if !cpModeDirsSeen[d] {
				cpModeDirsSeen[d] = true
				cpModeDirs = append(cpModeDirs, d)
			}
		}
		// Sort so global/standalone come before all-zones, zone-* are alphabetical.
		sortCPModeDirs(cpModeDirs)

		// Build ordered key list: iterate cp modes × subfolders to preserve intended order.
		var orderedKeys []string
		for _, modeDir := range cpModeDirs {
			for _, sub := range subfolderOrder {
				key := sub
				if modeDir != "" {
					key = modeDir + "/" + sub
				}
				if _, ok := bySubfolder[key]; ok {
					orderedKeys = append(orderedKeys, key)
				}
			}
		}

		writeSubfolderTables(b, orderedKeys, bySubfolder, errFiles)
	}

	// ── Already migrated ──────────────────────────────────────────────────────
	if len(alreadyFiles) > 0 {
		line(b, "## Already Migrated")
		line(b, "")
		line(b, "These files already use the new API and are passed through unchanged.")
		line(b, "")
		for _, fr := range alreadyFiles {
			linef(b, "- `%s`", fr.FileName)
		}
		line(b, "")
	}

	// ── Skipped ───────────────────────────────────────────────────────────────
	if len(skippedFiles) > 0 {
		line(b, "## Skipped")
		line(b, "")
		line(b, "No recognised Kuma policy documents found in these files.")
		line(b, "")
		for _, fr := range skippedFiles {
			linef(b, "- `%s`", fr.FileName)
		}
		line(b, "")
	}

	// ── Action Items ──────────────────────────────────────────────────────────
	hasActionItems := len(errFiles) > 0 || len(r.AddressMappings) > 0 || len(r.AnnotationHits) > 0
	if hasActionItems {
		line(b, "## Action Items")
		line(b, "")
		line(b, "> Address all items below before applying the migrated manifests.")
		line(b, "")

		if len(errFiles) > 0 {
			line(b, "### Errors")
			line(b, "")
			line(b, "These files could not be fully migrated and require manual attention.")
			line(b, "")
			for _, fr := range errFiles {
				linef(b, "#### `%s`", fr.FileName)
				line(b, "")
				for _, dc := range fr.Changes {
					if dc.ErrMsg != "" {
						linef(b, "- %s", dc.ErrMsg)
					}
				}
				line(b, "")
			}
		}

		if len(r.AddressMappings) > 0 {
			line(b, "### Workload Service Address Mappings")
			line(b, "")
			line(b, "Legacy `kuma.io/service`-encoded addresses found in env vars — update these")
			line(b, "in your Deployments and StatefulSets. Replace `<zone>` with your Kuma zone name.")
			line(b, "")
			line(b, "| Old address | Kubernetes address | Mesh address |")
			line(b, "|---|---|---|")
			for _, h := range r.AddressMappings {
				linef(b, "| `%s` | `%s` | `%s` |",
					h.OldToken+h.ExplicitPort, h.NewK8sAddress(), h.NewMeshAddress())
			}
			line(b, "")
		}

		if len(r.AnnotationHits) > 0 {
			line(b, "### Deprecated Annotations")
			line(b, "")
			line(b, "These resources use `\"yes\"`/`\"no\"` annotation values deprecated in Kuma 2.9.")
			line(b, "Update them to `\"true\"`/`\"false\"` in your manifests.")
			line(b, "")
			line(b, "| Resource | Namespace | Annotation | Old | New |")
			line(b, "|---|---|---|---|---|")
			for _, h := range r.AnnotationHits {
				ns := h.Namespace
				if ns == "" {
					ns = "*(cluster-scoped)*"
				}
				linef(b, "| `%s/%s` | `%s` | `%s` | `%s` | `%s` |",
					h.Kind, h.Name, ns, h.AnnotationKey, h.OldValue, h.NewValue)
			}
			line(b, "")
		}
	}

	// ── Apply Checklist ───────────────────────────────────────────────────────
	writeApplyChecklist(b, r, isPlan)

	// ── Deletable originals ───────────────────────────────────────────────────
	writeDeletableOriginals(b, r, isPlan)
}

// writeSubfolderTables writes one compact table per subfolder key, followed by
// per-file notes for any file that has warnings, env-var hits, or annotation hits.
// Keys are in the form "cpMode/subfolder" (e.g. "global/resiliency") or just
// "subfolder" when no CP mode prefix is present.
func writeSubfolderTables(b *strings.Builder, order []string, bySubfolder map[string][]FileReport, errFiles []FileReport) {
	for _, key := range order {
		files, ok := bySubfolder[key]
		if !ok {
			continue
		}

		linef(b, "### `%s/`", key)
		line(b, "")
		// Show the mesh/ advisory for any key whose last component is "mesh".
		lastComponent := key
		if i := strings.LastIndex(key, "/"); i >= 0 {
			lastComponent = key[i+1:]
		}
		if lastComponent == "mesh" {
			line(b, "> **Apply last.** These `Mesh` CRs enable `spec.meshServices.mode: Exclusive`.")
			line(b, "")
		}

		line(b, "| File | Input kind | Scenario | Notes |")
		line(b, "|---|---|---|---|")
		for _, fr := range files {
			for i, dc := range fr.Changes {
				if dc.InputKind == "" {
					continue
				}
				notes := notesCell(dc, i == 0 && fr.HasError)
				fname := ""
				if i == 0 {
					fname = fr.FileName
				}
				linef(b, "| `%s` | `%s` | %s | %s |", fname, dc.InputKind, dc.Scenario, notes)
			}
		}
		line(b, "")

		// Per-file detail blocks — only for files that have something to say.
		for _, fr := range files {
			writeFileNotes(b, fr)
		}
	}

	// Errors section.
	if len(errFiles) > 0 {
		line(b, "### Errors")
		line(b, "")
		line(b, "> These files could not be fully migrated — see **Action Items** below.")
		line(b, "")
		line(b, "| File | Error |")
		line(b, "|---|---|")
		for _, fr := range errFiles {
			for _, dc := range fr.Changes {
				if dc.ErrMsg != "" {
					linef(b, "| `%s` | %s |", fr.FileName, dc.ErrMsg)
				}
			}
		}
		line(b, "")
	}
}

// notesCell returns the content for the Notes column of a file table row.
func notesCell(dc DocChange, isError bool) string {
	if isError && dc.ErrMsg != "" {
		return "⚠ error — see Action Items"
	}
	if len(dc.Warnings) == 1 {
		return "⚠ " + dc.Warnings[0]
	}
	if len(dc.Warnings) > 1 {
		return fmt.Sprintf("⚠ %d warnings — see below", len(dc.Warnings))
	}
	return "—"
}

// writeFileNotes writes the detail block for a single file if it has warnings,
// env-var hits, or annotation hits. Nothing is written for clean files.
func writeFileNotes(b *strings.Builder, fr FileReport) {
	var allWarnings []string
	for _, dc := range fr.Changes {
		allWarnings = append(allWarnings, dc.Warnings...)
	}
	hasNotes := len(allWarnings) > 0 || len(fr.EnvVarHits) > 0 || len(fr.AnnotHits) > 0
	if !hasNotes {
		return
	}

	linef(b, "**`%s`**", fr.FileName)
	line(b, "")

	if len(allWarnings) > 0 {
		for _, w := range allWarnings {
			linef(b, "- ⚠ %s", w)
		}
		line(b, "")
	}

	if len(fr.EnvVarHits) > 0 {
		line(b, "Legacy service addresses in env vars:")
		line(b, "")
		line(b, "| Workload | Container | Env var | Value |")
		line(b, "|---|---|---|---|")
		for _, h := range fr.EnvVarHits {
			linef(b, "| `%s/%s` (ns: `%s`) | `%s` | `%s` | `%s` |",
				h.WorkloadKind, h.WorkloadName, h.Namespace,
				h.ContainerName, h.EnvVarName, h.RawValue)
		}
		line(b, "")
	}

	if len(fr.AnnotHits) > 0 {
		line(b, "Deprecated annotations:")
		line(b, "")
		for _, h := range fr.AnnotHits {
			linef(b, "- `%s/%s`: `%s: \"%s\"` → `\"%s\"`",
				h.Kind, h.Name, h.AnnotationKey, h.OldValue, h.NewValue)
		}
		line(b, "")
	}
}

// requiresKubeDelete returns true for scenarios where the migrated resource has
// a different kind than the original — meaning the old K8s resource must be
// explicitly deleted (kubectl delete) after applying the new one.
// Subset/Rules/Passthrough update the same kind in-place via kubectl apply and
// are excluded.
func requiresKubeDelete(s Scenario) bool {
	switch s {
	case ScenarioLegacy,
		ScenarioExternalService,
		ScenarioGateway, ScenarioGatewayInstance, ScenarioHTTPRoute, ScenarioTCPRoute, ScenarioGatewayRoute,
		ScenarioOPAPolicy:
		return true
	}
	return false
}

// writeDeletableOriginals lists original K8s resources that must be deleted
// after the migrated replacements are applied. Only resources whose kind changes
// during migration are listed (kind-preserving scenarios like Subset and Rules
// are updated in-place via kubectl apply and do not need an explicit delete).
func writeDeletableOriginals(b *strings.Builder, r *MigrationReport, isPlan bool) {
	type deletableEntry struct {
		Kind      string
		Name      string
		Namespace string
		File      string
	}
	var entries []deletableEntry
	for _, fr := range r.Files {
		for _, dc := range fr.Changes {
			if dc.ErrMsg != "" || !requiresKubeDelete(dc.Scenario) {
				continue
			}
			entries = append(entries, deletableEntry{
				Kind:      dc.InputKind,
				Name:      dc.InputName,
				Namespace: dc.InputNamespace,
				File:      fr.FileName,
			})
		}
	}
	if len(entries) == 0 {
		return
	}

	if isPlan {
		line(b, "## Original Resources to Delete (Preview)")
	} else {
		line(b, "## Original Resources to Delete")
	}
	line(b, "")
	line(b, "These resources changed kind during migration. After applying the migrated files,")
	line(b, "delete the originals from Kubernetes — `kubectl apply` alone will not remove them.")
	line(b, "")
	line(b, "| Source file | Kind | Name | Namespace |")
	line(b, "|---|---|---|---|")
	for _, e := range entries {
		ns := e.Namespace
		if ns == "" {
			ns = "*(cluster-scoped or Universal)*"
		}
		linef(b, "| `%s` | `%s` | `%s` | %s |", e.File, e.Kind, e.Name, ns)
	}
	line(b, "")
	line(b, "<details>")
	line(b, "<summary>kubectl delete commands</summary>")
	line(b, "")
	line(b, "```bash")
	for _, e := range entries {
		if e.Namespace != "" {
			linef(b, "kubectl delete %s %s -n %s", e.Kind, e.Name, e.Namespace)
		} else {
			linef(b, "kubectl delete %s %s", e.Kind, e.Name)
		}
	}
	line(b, "```")
	line(b, "</details>")
	line(b, "")
}

// writeApplyChecklist writes the numbered apply checklist. In plan mode this is
// a short three-step list; in migrate mode it is the full ordered procedure.
func writeApplyChecklist(b *strings.Builder, r *MigrationReport, isPlan bool) {
	line(b, "## Apply Checklist")
	line(b, "")

	if isPlan {
		line(b, "This is a dry run. Once you are satisfied with this plan:")
		line(b, "")
		line(b, "1. Fix any errors and address warnings above.")
		line(b, "2. Run the migration:")
		line(b, "   ```bash")
		linef(b, "   kuma-migrator migrate --input-dir %s --output-dir <output-dir>", r.InputDir)
		line(b, "   ```")
		line(b, "3. Follow the Apply Checklist in the generated `migration-report.md`.")
		line(b, "")
		return
	}

	line(b, "Follow these steps **in order**.")
	line(b, "")

	n := 1

	if r.ErrorCount > 0 {
		linef(b, "**%d. Fix errors** — %d file(s) in **Action Items → Errors** above could not be automatically migrated.", n, r.ErrorCount)
		line(b, "")
		n++
	}

	if len(r.AddressMappings) > 0 {
		linef(b, "**%d. Update workload env vars** — replace legacy `kuma.io/service` addresses listed in **Action Items → Workload Service Address Mappings** above.", n)
		line(b, "")
		n++
	}

	if len(r.AnnotationHits) > 0 {
		linef(b, "**%d. Fix deprecated annotations** — update `\"yes\"`/`\"no\"` values to `\"true\"`/`\"false\"` as listed in **Action Items → Deprecated Annotations** above.", n)
		line(b, "")
		n++
	}

	linef(b, "**%d. Upgrade the Global Control Plane** to the target Kuma / Kong Mesh version.", n)
	line(b, "")
	n++

	linef(b, "**%d. Upgrade Zone Control Planes.** Kong Mesh supports at most two minor versions per upgrade step (e.g. 2.7 → 2.9 → 2.11).", n)
	line(b, "")
	n++

	if hasLabel(r, labelMigratedGW) {
		linef(b, "**%d. Install Gateway API CRDs** on every Kubernetes cluster (Global CP and each Zone).", n)
		line(b, "")
		line(b, "   Standard channel (GatewayClass, Gateway, HTTPRoute, GRPCRoute):")
		line(b, "   ```bash")
		line(b, "   kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/latest/download/standard-install.yaml")
		line(b, "   ```")
		if hasTCPRouteOutput(r) {
			line(b, "   This migration includes `TCPRoute` — also install the experimental channel (superset of standard):")
			line(b, "   ```bash")
			line(b, "   kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/latest/download/experimental-install.yaml")
			line(b, "   ```")
		}
		line(b, "   > Do not apply Gateway API CRDs to Universal (non-Kubernetes) zones.")
		line(b, "")
		n++
	}

	// Build apply steps from actual output directories found in the report.
	applyDirs := collectApplyDirs(r)

	// Separate mesh dirs (apply last) from the rest.
	var nonMeshDirs, meshDirs []applyDirInfo
	for _, d := range applyDirs {
		if d.Subfolder == "mesh" {
			meshDirs = append(meshDirs, d)
		} else {
			nonMeshDirs = append(nonMeshDirs, d)
		}
	}

	if len(nonMeshDirs) > 0 {
		linef(b, "**%d. Apply policies** (resiliency, routing, zero-trust, observability) in the order below:", n)
		line(b, "")
		n++
		var prevCPDir string
		for _, d := range nonMeshDirs {
			if d.CPModeDir != prevCPDir {
				prevCPDir = d.CPModeDir
				writeContextHeader(b, d)
			}
			writeApplyDirCommands(b, d, r.OutputDir)
		}
	}

	if len(meshDirs) > 0 {
		linef(b, "**%d. Apply `Mesh` CRs last:**", n)
		line(b, "")
		line(b, "   > These CRs set `spec.meshServices.mode: Exclusive`, which disables legacy `kuma.io/service`")
		line(b, "   > routing. **Do not apply until all policies and workload env vars are migrated.**")
		line(b, "")
		n++
		var prevCPDir string
		for _, d := range meshDirs {
			if d.CPModeDir != prevCPDir {
				prevCPDir = d.CPModeDir
				writeContextHeader(b, d)
			}
			writeApplyDirCommands(b, d, r.OutputDir)
		}
		line(b, "   > If any `Mesh` CRs were not in the input directory, patch them manually:")
		line(b, "   > ```bash")
		line(b, "   > kubectl patch mesh <name> --type merge -p '{\"spec\":{\"meshServices\":{\"mode\":\"Exclusive\"}}}'")
		line(b, "   > ```")
		line(b, "")
	}

	linef(b, "**%d. Verify traffic health.** Check service-to-service connectivity across all meshes.", n)
	line(b, "   Monitor your observability stack for errors before proceeding.")
	line(b, "")
	n++

	linef(b, "**%d. Delete the original policy files** once the migrated policies are confirmed working.", n)
	linef(b, "   The originals are in `%s` and were not modified.", r.InputDir)
	line(b, "")
	n++

	linef(b, "**%d. Plan your next upgrade** if you have not yet reached the target version.", n)
	line(b, "   Re-run `kuma-migrator extract` + `plan` + `migrate` for each minor-version step.")
	line(b, "")
}

// writeContextHeader writes the context label line before a group of apply commands.
// For kumactl contexts it adds a use-context reminder; for kubectl contexts it's plain.
func writeContextHeader(b *strings.Builder, d applyDirInfo) {
	if d.Tool == "kumactl" {
		label := "kumactl"
		if d.IsKonnect {
			label = "kumactl — Konnect-hosted CP"
		}
		linef(b, "   **Context `%s`** (%s):", d.CPModeDir, label)
		ctxName := d.Context
		if ctxName == "" {
			ctxName = d.CPModeDir // fallback
		}
		linef(b, "   > Set the active context first: `kumactl config use-context %s`", ctxName)
	} else {
		linef(b, "   **Context `%s`**:", d.CPModeDir)
	}
	line(b, "")
}

// writeApplyDirCommands writes the apply commands for a single output directory.
// For kumactl contexts each file is listed individually since kumactl does not
// accept a directory as the -f argument. For kubectl a single directory apply is used.
// When the extraction tool is unknown (legacy/flat layout) kubectl is assumed.
func writeApplyDirCommands(b *strings.Builder, d applyDirInfo, outputDir string) {
	if d.Tool == "kumactl" {
		line(b, "   ```bash")
		for _, f := range d.Files {
			linef(b, "   kumactl apply -f %s", filepath.Join(outputDir, f))
		}
		line(b, "   ```")
	} else {
		line(b, "   ```bash")
		linef(b, "   kubectl apply -f %s/", d.FullDir)
		line(b, "   ```")
	}
	line(b, "")
}

// ── Apply dir helpers ─────────────────────────────────────────────────────────

// applyDirInfo represents a single output directory to be applied.
type applyDirInfo struct {
	FullDir   string   // absolute path to the directory
	RelDir    string   // relative to outputDir
	CPModeDir string   // first path component (context label)
	Subfolder string   // last path component ("resiliency", "mesh", etc.)
	Tool      string   // "kubectl", "kumactl", or "" (unknown/legacy — defaults to kubectl)
	IsKonnect bool     // true when the CP is Konnect-hosted (from extraction metadata)
	Context   string   // original kumactl/kubectl context name (from extraction metadata)
	Files     []string // output file paths relative to outputDir, in this dir (for kumactl)
}

// collectApplyDirs returns all distinct output directories derived from the
// report's output file paths, sorted for correct application order:
// global/standalone CPs before zone CPs; mesh/ subfolder last within each CP.
// Tool metadata is read from .kuma-migrator.json files written by kuma-migrator extract.
func collectApplyDirs(r *MigrationReport) []applyDirInfo {
	subOrder := map[string]int{
		"resiliency": 0, "routing": 1, "zero-trust": 2, "observability": 3,
		"gateway": 4, "workload": 4, "other": 4, "mesh": 99,
	}
	subRank := func(s string) int {
		if rank, ok := subOrder[s]; ok {
			return rank
		}
		return 50
	}

	// Cache metadata per cpModeDir to avoid repeated file reads.
	metaCache := map[string]*extractor.ContextMeta{}
	getMeta := func(cpModeDir string) *extractor.ContextMeta {
		if m, ok := metaCache[cpModeDir]; ok {
			return m
		}
		m := extractor.ReadContextMeta(r.InputDir, cpModeDir)
		metaCache[cpModeDir] = m
		return m
	}

	// Collect distinct dirs and their files.
	dirMap := map[string]*applyDirInfo{} // relDir → info
	var dirOrder []string

	for _, fr := range r.Files {
		for _, relPath := range fr.OutputRelPaths {
			relDir := filepath.ToSlash(filepath.Dir(relPath))
			if relDir == "." {
				continue
			}
			if _, seen := dirMap[relDir]; !seen {
				parts := strings.SplitN(relDir, "/", 5)
				cpModeDir := ""
				if len(parts) > 0 {
					cpModeDir = parts[0]
				}
				subfolder := path.Base(relDir)

				tool := ""
				isKonnect := false
				contextName := ""
				if meta := getMeta(cpModeDir); meta != nil {
					tool = meta.Tool
					isKonnect = meta.IsKonnect
					contextName = meta.Context
				}

				info := &applyDirInfo{
					FullDir:   filepath.Join(r.OutputDir, relDir),
					RelDir:    relDir,
					CPModeDir: cpModeDir,
					Subfolder: subfolder,
					Tool:      tool,
					IsKonnect: isKonnect,
					Context:   contextName,
				}
				dirMap[relDir] = info
				dirOrder = append(dirOrder, relDir)
			}
			dirMap[relDir].Files = append(dirMap[relDir].Files, relPath)
		}
	}

	result := make([]applyDirInfo, 0, len(dirOrder))
	for _, d := range dirOrder {
		result = append(result, *dirMap[d])
	}

	sort.SliceStable(result, func(i, j int) bool {
		a, b := result[i], result[j]
		ra, rb := applyDirCPRank(a.CPModeDir), applyDirCPRank(b.CPModeDir)
		if ra != rb {
			return ra < rb
		}
		if a.CPModeDir != b.CPModeDir {
			return a.CPModeDir < b.CPModeDir
		}
		sa, sb := subRank(a.Subfolder), subRank(b.Subfolder)
		if sa != sb {
			return sa < sb
		}
		return a.RelDir < b.RelDir
	})

	return result
}

// applyDirCPRank orders CP mode directory labels: global/standalone first,
// then zone CPs, then unknown/flat layouts.
func applyDirCPRank(d string) int {
	lower := strings.ToLower(d)
	switch {
	case strings.Contains(lower, "global") || strings.Contains(lower, "standalone"):
		return 0
	case strings.Contains(lower, "zone"):
		return 1
	case d == "":
		return 3
	default:
		return 2
	}
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// hasLabel reports whether any file in the report carries the given label.
func hasLabel(r *MigrationReport, label string) bool {
	for _, fr := range r.Files {
		if fr.Label == label {
			return true
		}
	}
	return false
}

// hasTCPRouteOutput reports whether any migrated document produced a TCPRoute,
// which requires the Gateway API experimental channel.
func hasTCPRouteOutput(r *MigrationReport) bool {
	for _, fr := range r.Files {
		for _, dc := range fr.Changes {
			if dc.Scenario == ScenarioTCPRoute || dc.Scenario == ScenarioGatewayRoute {
				return true
			}
		}
	}
	return false
}

// sortCPModeDirs sorts CP mode directory names so that global/standalone appear
// first, then all-zones, then zone-* dirs alphabetically, then unknown, then "" (no prefix).
func sortCPModeDirs(dirs []string) {
	rank := func(d string) int {
		switch d {
		case "global":
			return 0
		case "standalone":
			return 1
		case "all-zones":
			return 2
		case "unknown":
			return 4
		case "":
			return 5
		}
		if strings.HasPrefix(d, "zone") {
			return 3
		}
		return 6
	}
	sort.Slice(dirs, func(i, j int) bool {
		ri, rj := rank(dirs[i]), rank(dirs[j])
		if ri != rj {
			return ri < rj
		}
		return dirs[i] < dirs[j]
	})
}

func line(b *strings.Builder, s string) {
	b.WriteString(s)
	b.WriteByte('\n')
}

func linef(b *strings.Builder, format string, args ...interface{}) {
	b.WriteString(fmt.Sprintf(format, args...))
	b.WriteByte('\n')
}

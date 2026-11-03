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
	linef(b, "Target:    %s", r.Target.Describe())
	line(b, "")
	if r.Target.IsV3() {
		line(b, "> Output targets **3.0**. It is not safe to apply to a 2.x control plane:")
		line(b, "> fields this run rewrote or removed are still required there.")
		line(b, "")
		writeV3TopologyNote(b, r)
	}
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
	linef(b, "| Scanned (non-policy, rewritten or flagged) | %d |", r.ScannedCount)
	if r.ManualCount > 0 {
		linef(b, "| Kept under `%s/` (kind removed) | %d |", ManualDir, r.ManualCount)
	}
	linef(b, "| Skipped (non-policy) | %d |", r.SkippedCount)
	linef(b, "| Errors | %d |", r.ErrorCount)
	line(b, "")

	// ── Classify files ────────────────────────────────────────────────────────
	var errFiles, migratedFiles, alreadyFiles, scannedFiles, manualFiles, skippedFiles []FileReport
	for _, fr := range r.Files {
		switch fr.Label {
		case labelError, labelPartialError:
			errFiles = append(errFiles, fr)
		case labelAlreadyDone:
			alreadyFiles = append(alreadyFiles, fr)
		case labelScanned:
			scannedFiles = append(scannedFiles, fr)
		case labelManual:
			manualFiles = append(manualFiles, fr)
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

		subfolderOrder := []string{backendsSubfolder, "resiliency", "routing", "zero-trust", "observability", "other", "mesh"}

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
	// A Passthrough scenario means the STRUCTURE is already on the new API —
	// it says nothing about whether ScanForDeprecations (which runs on every
	// document regardless of scenario) found anything else to flag, e.g. a
	// MeshTrafficPermission already using targetRef/from[] with only
	// namespace/workload tags still carries the from[]-deprecated-for-rules[]
	// advisory. List those warnings here so passing through unchanged doesn't
	// also mean silently dropping them.
	if len(alreadyFiles) > 0 {
		line(b, "## Already Migrated")
		line(b, "")
		line(b, "These files already use the new API and are passed through unchanged.")
		line(b, "")
		for _, fr := range alreadyFiles {
			linef(b, "- `%s`%s", fr.FileName, alreadyMigratedSuffix(fr))
		}
		line(b, "")
		for _, fr := range alreadyFiles {
			writeFileNotes(b, fr)
		}
	}

	// ── Scanned non-policy resources ──────────────────────────────────────────
	// Not policies (Dataplane, workloads, ...), but ScanForDeprecations rewrote
	// or flagged them. They used to be listed under "Skipped", which read as
	// "nothing happened" while the output file had changed.
	if len(scannedFiles) > 0 {
		line(b, "## Scanned Non-Policy Resources")
		line(b, "")
		line(b, "These files hold no policy, but the deprecation scan rewrote or flagged them.")
		line(b, "They are written to the output tree and applied with the rest.")
		line(b, "")
		for _, fr := range scannedFiles {
			linef(b, "- `%s` — %s%s", fr.FileName, scannedReason(fr), alreadyMigratedSuffix(fr))
		}
		line(b, "")
		for _, fr := range scannedFiles {
			writeFileNotes(b, fr)
		}
	}

	// ── Kept under _manual/ ───────────────────────────────────────────────────
	// Every file written under ManualDir, whatever put it there, with the
	// reason. Errored and rejected documents are also detailed under Action
	// Items → Errors; this is the one place that lists the whole subtree.
	if entries := collectManualEntries(r); len(entries) > 0 {
		linef(b, "## Kept Under `%s/`", ManualDir)
		line(b, "")
		line(b, "No apply step uses these files. Each needs a decision before the upgrade: see the reason")
		if r.Target.IsV3() {
			line(b, "below, **Action Items → Errors**, and **Delete Before Upgrading to 3.0**.")
		} else {
			line(b, "below and **Action Items → Errors**.")
		}
		line(b, "")
		line(b, "| Source file | Kind | Name | Kept at | Reason |")
		line(b, "|---|---|---|---|---|")
		for _, e := range entries {
			linef(b, "| `%s` | `%s` | `%s` | `%s` | %s |", e.File, e.Kind, e.Name, e.Path, mdCell(e.Reason))
		}
		line(b, "")
		for _, fr := range manualFiles {
			writeFileNotes(b, fr)
		}
	}

	// ── Skipped ───────────────────────────────────────────────────────────────
	if len(skippedFiles) > 0 {
		line(b, "## Skipped")
		line(b, "")
		line(b, "No Kuma policy and nothing to change in these files. Each is either copied unchanged")
		line(b, "to the output tree or, when its kind is on the skip list, not written.")
		line(b, "")
		for _, fr := range skippedFiles {
			linef(b, "- `%s` — %s%s", fr.FileName, skippedReason(fr), alreadyMigratedSuffix(fr))
		}
		line(b, "")
		for _, fr := range skippedFiles {
			writeFileNotes(b, fr)
		}
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
						switch {
						case dc.Rejected && dc.ManualRelPath != "":
							linef(b, "  - Converted output kept at `%s`. The target control plane rejects it as written, so it is not part of any apply step.", dc.ManualRelPath)
						case dc.ManualRelPath != "":
							linef(b, "  - Original kept at `%s`. It is not part of any apply step.", dc.ManualRelPath)
						}
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

	// ── v3: objects of kinds 3.0 removes ─────────────────────────────────────
	if r.Target.IsV3() {
		writeV3DeleteBeforeUpgrade(b, r, isPlan)
	}
}

// manualEntry is one file written under ManualDir.
type manualEntry struct {
	File, Kind, Name, Path, Reason string
}

// collectManualEntries lists every file under ManualDir with why it is there.
func collectManualEntries(r *MigrationReport) []manualEntry {
	var out []manualEntry
	for _, fr := range r.Files {
		file := fr.InputRelPath
		if file == "" {
			file = fr.FileName
		}
		for _, dc := range fr.Changes {
			paths := dc.ManualRelPaths
			if len(paths) == 0 && dc.ManualRelPath != "" {
				paths = []string{dc.ManualRelPath}
			}
			for _, p := range paths {
				out = append(out, manualEntry{File: file, Kind: dc.InputKind, Name: dc.InputName, Path: p, Reason: manualReason(dc, r.Target)})
			}
		}
	}
	return out
}

// manualReason says why a document went to ManualDir.
func manualReason(dc DocChange, target TargetVersion) string {
	switch {
	case dc.DeleteBeforeV3:
		return "default policy to delete before upgrading: 3.0 applies it to every proxy (kumahq/kuma#18857)"
	case dc.Rejected:
		// RejectedByTargetError.Error() already reads "rejected by the <target>
		// control plane as converted (...): <kind> "<name>": <reasons>".
		return dc.ErrMsg
	case dc.ErrMsg != "":
		return "not converted, original kept: " + dc.ErrMsg
	default:
		return "kind removed in " + targetLine(target) + ", no conversion"
	}
}

// mdCell makes s safe for one Markdown table cell.
func mdCell(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ").Replace(s)
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
				linef(b, "| `%s` | `%s` | %s | %s |", fname, dc.InputKind, scenarioCell(dc), notes)
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
// alreadyMigratedSuffix returns an inline "⚠ N warning(s) — see below" marker
// for a file's list-item line when ScanForDeprecations found something worth
// flagging, or "" for a clean file. Mirrors notesCell's wording for the
// migrated-files table, so the same warning count reads the same way whether
// the file appears there or in "Already Migrated"/"Skipped".
func alreadyMigratedSuffix(fr FileReport) string {
	n := 0
	for _, dc := range fr.Changes {
		n += len(dc.Warnings)
	}
	switch n {
	case 0:
		return ""
	case 1:
		return " — ⚠ 1 warning — see below"
	default:
		return fmt.Sprintf(" — ⚠ %d warnings — see below", n)
	}
}

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
// Subset/Rules/Passthrough rewrite the same object in place (kubectl replace,
// see FileReport.ReplaceRelPaths) and are excluded.
func requiresKubeDelete(s Scenario) bool {
	switch s {
	case ScenarioLegacy,
		ScenarioExternalService,
		ScenarioGateway, ScenarioGatewayInstance, ScenarioGatewayRoute,
		ScenarioOPAPolicy:
		return true
	}
	return false
}

// writeDeletableOriginals lists original K8s resources that must be deleted
// after the migrated replacements are applied. Only resources whose kind changes
// during migration are listed (kind-preserving scenarios like Subset and Rules
// are rewritten in place and do not need an explicit delete).
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
			// Under v3, removed kinds are listed by writeV3DeleteBeforeUpgrade,
			// which must happen before the upgrade rather than after applying.
			if r.Target.IsV3() && (isRemovedInV3Kind(dc.InputKind) || dc.DeleteBeforeV3) {
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

// v3Deletion is one source object of a kind 3.0 removes.
type v3Deletion struct {
	Kind, Name, Namespace, Mesh string
	File                        string // input file, relative to the input dir
	CPModeDir                   string
	Converted                   bool   // a replacement is in the apply tree
	ManualRelPath               string // copy under ManualDir, when not converted
	DefaultPolicy               bool   // a 2.x default policy 3.0 keeps but must be deleted (DocChange.DeleteBeforeV3)
}

// collectV3Deletions lists every input document whose kind 3.0 removes, whether
// or not it was converted, and the default policies flagged DeleteBeforeV3.
func collectV3Deletions(r *MigrationReport) []v3Deletion {
	var out []v3Deletion
	for _, fr := range r.Files {
		for _, dc := range fr.Changes {
			if !isRemovedInV3Kind(dc.InputKind) && !dc.DeleteBeforeV3 {
				continue
			}
			// A skip-listed document (e.g. a CP-managed ZoneIngress on
			// Kubernetes) produces no output at all; the operator chose not to
			// migrate it. Non-skip-listed removed kinds always end up either
			// converted or under ManualDir.
			if dc.Scenario == ScenarioSkipped && dc.ManualRelPath == "" {
				continue
			}
			mesh := dc.InputMesh
			if mesh == "" {
				mesh = fr.MeshDir
			}
			file := fr.InputRelPath
			if file == "" {
				file = fr.FileName
			}
			out = append(out, v3Deletion{
				Kind:          dc.InputKind,
				Name:          dc.InputName,
				Namespace:     dc.InputNamespace,
				Mesh:          mesh,
				File:          file,
				CPModeDir:     fr.CPModeDir,
				Converted:     dc.ErrMsg == "" && dc.ManualRelPath == "",
				ManualRelPath: dc.ManualRelPath,
				DefaultPolicy: dc.DeleteBeforeV3,
			})
		}
	}
	return out
}

// writeV3DeleteBeforeUpgrade lists, under --to-latest v3, the source objects of
// kinds 3.0 removes, with the delete command for the tool each context was
// extracted with. These must go before the control plane is upgraded.
func writeV3DeleteBeforeUpgrade(b *strings.Builder, r *MigrationReport, isPlan bool) {
	entries := collectV3Deletions(r)
	if len(entries) == 0 {
		return
	}

	line(b, "## Delete Before Upgrading to 3.0")
	line(b, "")
	line(b, "3.0 removes these kinds. The 3.0 control plane rejects them and does not migrate stored")
	line(b, "objects, so delete them on the 2.x control plane before upgrading. Objects without a")
	linef(b, "conversion are copied under `%s/` for reference; no apply step uses that directory.", ManualDir)
	if hasDefaultPolicy(entries) {
		line(b, "The table also lists 2.x default policies that 3.0 keeps but applies to every proxy.")
	}
	if isPlan {
		line(b, "")
		line(b, "> Dry run: the paths below are where `migrate` would write.")
	}
	line(b, "")
	line(b, "| Source file | Kind | Name | Namespace / mesh | Replacement |")
	line(b, "|---|---|---|---|---|")
	for _, e := range entries {
		scope := "—"
		switch {
		case e.Namespace != "":
			scope = "ns `" + e.Namespace + "`"
		case e.Mesh != "":
			scope = "mesh `" + e.Mesh + "`"
		}
		repl := "converted (see the apply steps)"
		if e.DefaultPolicy {
			repl = "none — delete it; 3.0 applies it to every proxy"
			if e.ManualRelPath != "" {
				repl += ", copy at `" + e.ManualRelPath + "`"
			}
		} else if !e.Converted {
			repl = "none — manual migration"
			if e.ManualRelPath != "" {
				repl += ", copy at `" + e.ManualRelPath + "`"
			}
		}
		linef(b, "| `%s` | `%s` | `%s` | %s | %s |", e.File, e.Kind, e.Name, scope, repl)
	}
	line(b, "")

	// Commands, per write tool of each context (kubectl for a Kubernetes-backed
	// CP even when it was extracted with kumactl; see resolveContextTool).
	toolFor := newContextToolResolver(r)
	if anyKumactlOnKubernetes(r, entries) {
		line(b, "> Some contexts were extracted with `kumactl` from a Kubernetes-backed control plane.")
		line(b, "> `kumactl` cannot delete resources there, so their commands use `kubectl`: run them")
		line(b, "> against the cluster that hosts that control plane.")
		line(b, "")
	}
	line(b, "<details>")
	line(b, "<summary>delete commands</summary>")
	line(b, "")
	line(b, "```bash")
	for _, e := range entries {
		linef(b, "%s", v3DeleteCommand(e, toolFor(e.CPModeDir).Tool))
	}
	line(b, "```")
	line(b, "</details>")
	line(b, "")
	if hasDefaultPolicy(entries) {
		line(b, "`mesh-gateways-timeout-all-<mesh>` is a default policy. On Kubernetes the 2.14 Mesh controller")
		line(b, "recreates it on the next Mesh reconcile: first add `MeshTimeout` (or `*`) to the Mesh's")
		line(b, "`spec.skipCreatingInitialPolicies`, then delete it. 3.0 ignores that field. On Universal deleting")
		line(b, "it is enough. Do not drop `proxyTypes` from it instead: without the filter it sets a 5s")
		line(b, "`streamIdleTimeout` on every sidecar.")
		line(b, "")
	}
	if hasKind(entries, "MeshGlobalRateLimit") {
		line(b, "After upgrading, Helm leaves the `meshglobalratelimits.kuma.io` CRD in place. Remove it with")
		line(b, "`kubectl delete crd meshglobalratelimits.kuma.io`, and drop the `ratelimit.*` / `global.ratelimit.*`")
		line(b, "Helm overrides.")
		line(b, "")
	}
}

func hasDefaultPolicy(entries []v3Deletion) bool {
	for _, e := range entries {
		if e.DefaultPolicy {
			return true
		}
	}
	return false
}

func hasKind(entries []v3Deletion, kind string) bool {
	for _, e := range entries {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

// v3DeleteCommand renders the delete command for one removed-kind object.
func v3DeleteCommand(e v3Deletion, tool string) string {
	if tool == "kumactl" && !kubernetesOnlyRemovedKinds[e.Kind] {
		typ, ok := kumactlDeleteType[e.Kind]
		if !ok {
			typ = strings.ToLower(e.Kind)
		}
		cmd := fmt.Sprintf("kumactl delete %s %s", typ, e.Name)
		if e.Mesh != "" && !globalScopedRemovedKinds[e.Kind] {
			cmd += " --mesh " + e.Mesh
		}
		return cmd
	}
	cmd := fmt.Sprintf("kubectl delete %s %s", e.Kind, e.Name)
	if e.Namespace != "" {
		cmd += " -n " + e.Namespace
	}
	return cmd
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
		linef(b, "   Their originals, or the converted output the target rejects, are under `<context>/%s/`", ManualDir)
		linef(b, "   (see **Kept Under `%s/`**). No step of this checklist applies that directory.", ManualDir)
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

	v3 := r.Target.IsV3()
	toolFor := newContextToolResolver(r)
	meshes := collectMeshes(r)

	// v3: meshServices.mode must be Exclusive before the Global CP is upgraded
	// (UPGRADE.md "meshServices removed from the Mesh schema"): a 2.x zone reads
	// a missing field as Disabled and a 3.0 zone rejects any other mode. It also
	// comes before the MeshIdentity cutover: on 2.14 a MeshIdentity in a mesh
	// that is not Exclusive stays DependenciesReady=False (MeshServicesDisabled)
	// and never becomes ready (observed on Kong Mesh 2.14.7).
	if v3 {
		linef(b, "**%d. Set `meshServices.mode: Exclusive` on every mesh, on the 2.x Global control plane, before upgrading it.**", n)
		line(b, "   3.0 makes unified naming unconditional and its zones reject a mesh with any other mode; a 2.x")
		line(b, "   zone reads a missing field as `Disabled` and deletes every generated `MeshService`. `Exclusive`")
		line(b, "   stops legacy `kuma.io/service` routing, so workloads must already use the new addresses.")
		line(b, "   This comes before the `MeshIdentity` cutover: on 2.14 a `MeshIdentity` in a mesh that is not")
		line(b, "   `Exclusive` reports `MeshServicesDisabled` and never becomes ready.")
		line(b, "   These commands change only `meshServices` and keep `spec.mtls`; do not apply the v3 `Mesh` files here.")
		line(b, "")
		writeMeshExclusiveCommands(b, meshes, toolFor)
		n++
	}

	// v3: the MeshIdentity cutover must be finished on 2.x. 3.0 stops producing
	// mTLS from Mesh.mtls and drops the legacy CA from the trust bundle, with no
	// rolling path back (UPGRADE.md "Mesh.mtls no longer produces mTLS").
	if v3 {
		if names := meshesWithMTLS(meshes); len(names) > 0 {
			linef(b, "**%d. Finish the `MeshIdentity` cutover on the 2.x control plane** for every mesh that still uses `spec.mtls`: %s.", n, strings.Join(names, ", "))
			line(b, "   The mesh must already run `meshServices.mode: Exclusive` (previous step); until then a")
			line(b, "   `MeshIdentity` stays `DependenciesReady=False` with reason `MeshServicesDisabled`.")
			line(b, "   Create a `MeshIdentity` and `MeshTrust` that select every proxy of the mesh and confirm")
			line(b, "   traffic is mTLS'd by them (see the `Mesh` warnings above). 3.0 ignores `spec.mtls`, so a")
			line(b, "   mesh without a `MeshIdentity` serves plaintext after the upgrade.")
			line(b, "   > Do **not** apply the `--to-latest v3` `Mesh` files yet: they drop `spec.mtls`, which turns")
			line(b, "   > mTLS off on a 2.x control plane. They are applied after the upgrade.")
			line(b, "")
			n++
		}
	}

	if v3 && len(collectV3Deletions(r)) > 0 {
		linef(b, "**%d. Delete the resources 3.0 removes** listed under **Delete Before Upgrading to 3.0** below, on the 2.x control plane.", n)
		line(b, "   The 3.0 control plane rejects them and does not migrate stored ones. If one still serves")
		line(b, "   traffic (ExternalService, MeshGateway*, OPAPolicy), first apply its replacement on 2.14 from a")
		line(b, "   `--to-latest v2` run of the same input, verify it, then delete it. Nothing under")
		linef(b, "   `%s/` is applied by any step of this checklist.", ManualDir)
		line(b, "")
		n++
	}

	if v3 {
		if pre := preUpgradeApplyDirs(r); len(pre) > 0 {
			linef(b, "**%d. Upgrade every control plane to Kong Mesh 2.14.6 or later, then apply these `MeshOPA` / `MeshExternalService` files**, still on 2.x and before the Global upgrade:", n)
			line(b, "   They use the 3.0 `SecureDataSource` shape. 3.0 rejects the old shape and nothing converts it")
			line(b, "   in transit, so once the Global runs 3.0 a zone receives these resources only in the new")
			line(b, "   shape. A zone below 2.14.6 drops the new fields: `MeshOPA` fails to load and its proxies stop")
			line(b, "   receiving configuration, and `MeshExternalService` loses its TLS verification settings.")
			line(b, "   2.14.6 reads both shapes. Apply a resource created on a zone on that zone. The later")
			line(b, "   apply step lists these files again; applying them twice is harmless.")
			line(b, "")
			var prevCPDir string
			for _, d := range pre {
				if d.CPModeDir != prevCPDir {
					prevCPDir = d.CPModeDir
					writeContextHeader(b, d)
				}
				writeApplyFileCommands(b, d, r.OutputDir)
			}
			n++
		}
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
		if hasSubfolder(nonMeshDirs, backendsSubfolder) {
			linef(b, "**%d. Apply backends, then policies** in the order below. `%s/` comes first in each context:", n, backendsSubfolder)
			line(b, "   policies select a `MeshOpenTelemetryBackend` by label, and until it exists the control plane")
			line(b, "   skips that backend and the telemetry is lost.")
		} else {
			linef(b, "**%d. Apply policies** (resiliency, routing, zero-trust, observability) in the order below:", n)
		}
		line(b, "")
		writeReplaceNote(b, nonMeshDirs)
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
		if v3 {
			linef(b, "**%d. Apply the `Mesh` CRs** once every control plane runs 3.0 and the `MeshIdentity` cutover is done:", n)
			line(b, "")
			line(b, "   > These files have `spec.mtls` and the other fields 3.0 removed dropped. 3.0 ignores those")
			line(b, "   > fields, so applying them now only makes the stored `Mesh` match what 3.0 uses.")
		} else {
			linef(b, "**%d. Apply `Mesh` CRs last:**", n)
			line(b, "")
			line(b, "   > These CRs set `spec.meshServices.mode: Exclusive`, which disables legacy `kuma.io/service`")
			line(b, "   > routing. **Do not apply until all policies and workload env vars are migrated.**")
		}
		line(b, "")
		writeReplaceNote(b, meshDirs)
		n++
		var prevCPDir string
		for _, d := range meshDirs {
			if d.CPModeDir != prevCPDir {
				prevCPDir = d.CPModeDir
				writeContextHeader(b, d)
			}
			writeApplyDirCommands(b, d, r.OutputDir)
		}
		if !v3 {
			line(b, "   > If any `Mesh` CRs were not in the input directory, set the mode on them directly:")
			line(b, "   > ```bash")
			line(b, "   > "+meshExclusiveCommand("<name>", toolFor(meshDirs[0].CPModeDir)))
			line(b, "   > ```")
			line(b, "")
		}
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
	line(b, "   Re-run `kuma-migrator extract` + `migrate --dry-run` + `migrate` for each minor-version step.")
	line(b, "")
}

// ── Mesh helpers ──────────────────────────────────────────────────────────────

// meshEntry is one Mesh document of the input.
type meshEntry struct {
	Name      string
	CPModeDir string
	MTLS      bool // carries spec.mtls
}

// collectMeshes lists the Mesh documents of the input, in report order.
func collectMeshes(r *MigrationReport) []meshEntry {
	var out []meshEntry
	seen := map[string]bool{}
	for _, fr := range r.Files {
		for _, dc := range fr.Changes {
			if dc.InputKind != "Mesh" || dc.InputName == "" {
				continue
			}
			key := fr.CPModeDir + "/" + dc.InputName
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, meshEntry{Name: dc.InputName, CPModeDir: fr.CPModeDir, MTLS: dc.MeshMTLS})
		}
	}
	return out
}

// meshesWithMTLS returns the backquoted names of the meshes that set spec.mtls.
func meshesWithMTLS(meshes []meshEntry) []string {
	var names []string
	seen := map[string]bool{}
	for _, m := range meshes {
		if m.MTLS && !seen[m.Name] {
			seen[m.Name] = true
			names = append(names, "`"+m.Name+"`")
		}
	}
	return names
}

// meshExclusiveCommand sets meshServices.mode: Exclusive on one Mesh without
// touching its other fields. kumactl has no patch command, so the kumactl form
// round-trips the stored Universal resource through yq (v4).
func meshExclusiveCommand(name string, t contextTool) string {
	if t.Tool == "kumactl" {
		return fmt.Sprintf(`kumactl get mesh %s -o yaml | yq '.meshServices.mode = "Exclusive"' | kumactl apply -f -`, name)
	}
	return fmt.Sprintf(`kubectl patch mesh %s --type merge -p '{"spec":{"meshServices":{"mode":"Exclusive"}}}'`, name)
}

// writeMeshExclusiveCommands writes, per context, the command that sets
// meshServices.mode: Exclusive on each Mesh of the input, plus the command for
// meshes that are not in the input.
func writeMeshExclusiveCommands(b *strings.Builder, meshes []meshEntry, toolFor func(string) contextTool) {
	byCtx := map[string][]meshEntry{}
	var ctxs []string
	for _, m := range meshes {
		if _, ok := byCtx[m.CPModeDir]; !ok {
			ctxs = append(ctxs, m.CPModeDir)
		}
		byCtx[m.CPModeDir] = append(byCtx[m.CPModeDir], m)
	}
	sort.SliceStable(ctxs, func(i, j int) bool {
		ri, rj := applyDirCPRank(ctxs[i]), applyDirCPRank(ctxs[j])
		if ri != rj {
			return ri < rj
		}
		return ctxs[i] < ctxs[j]
	})
	for _, ctx := range ctxs {
		t := toolFor(ctx)
		if ctx != "" {
			writeContextHeader(b, applyDirInfo{CPModeDir: ctx, Tool: t.Tool, IsKonnect: t.IsKonnect,
				Context: t.Context, KumactlOnKubernetes: t.KumactlOnKubernetes})
		}
		line(b, "   ```bash")
		for _, m := range byCtx[ctx] {
			linef(b, "   %s", meshExclusiveCommand(m.Name, t))
		}
		line(b, "   ```")
		line(b, "")
	}
	other := contextTool{Tool: "kubectl"}
	if len(ctxs) > 0 {
		other = toolFor(ctxs[0])
	}
	if len(meshes) > 0 {
		line(b, "   > Meshes that are not in the input need the same change. List them and set the mode on each:")
	} else {
		line(b, "   > No `Mesh` was in the input. List the meshes and set the mode on each:")
	}
	line(b, "   > ```bash")
	if other.Tool == "kumactl" {
		line(b, "   > kumactl get meshes")
	} else {
		line(b, "   > kubectl get meshes")
	}
	line(b, "   > "+meshExclusiveCommand("<name>", other))
	line(b, "   > ```")
	line(b, "")
}

// ── Write tool per context ────────────────────────────────────────────────────

// contextTool is the CLI the checklist uses to write to one context's control
// plane.
type contextTool struct {
	Tool      string // "kubectl" or "kumactl"
	IsKonnect bool
	Context   string // context name recorded by extract
	// KumactlOnKubernetes: extracted with kumactl, but the CP is Kubernetes-backed,
	// so writes go through kubectl.
	KumactlOnKubernetes bool
}

// resolveContextTool picks the write tool for a context from its extract
// metadata. kumactl refuses to create, update or delete resources on a
// Kubernetes-backed control plane (they are CRDs, owned by the Kubernetes API),
// so a context extracted with kumactl from such a CP is written with kubectl.
// kumactl stays for Universal CPs and for Konnect, whose hosted Global CP has no
// Kubernetes API and is written with kumactl (Konnect docs, "apply resources to
// Konnect"). Without metadata (older or hand-made input) kubectl is assumed.
func resolveContextTool(meta *extractor.ContextMeta) contextTool {
	if meta == nil {
		return contextTool{Tool: "kubectl"}
	}
	t := contextTool{Tool: meta.Tool, IsKonnect: meta.IsKonnect, Context: meta.Context}
	if t.Tool != "kumactl" {
		t.Tool = "kubectl"
		return t
	}
	if !meta.IsKonnect && meta.Environment == extractor.CPEnvKubernetes {
		t.Tool = "kubectl"
		t.KumactlOnKubernetes = true
	}
	return t
}

// newContextToolResolver returns a cached resolveContextTool lookup by context
// directory.
func newContextToolResolver(r *MigrationReport) func(cpModeDir string) contextTool {
	cache := map[string]contextTool{}
	return func(cpModeDir string) contextTool {
		if t, ok := cache[cpModeDir]; ok {
			return t
		}
		t := resolveContextTool(extractor.ReadContextMeta(r.InputDir, cpModeDir))
		cache[cpModeDir] = t
		return t
	}
}

// anyKumactlOnKubernetes reports whether any delete entry belongs to a context
// extracted with kumactl from a Kubernetes-backed CP.
func anyKumactlOnKubernetes(r *MigrationReport, entries []v3Deletion) bool {
	toolFor := newContextToolResolver(r)
	for _, e := range entries {
		if toolFor(e.CPModeDir).KumactlOnKubernetes {
			return true
		}
	}
	return false
}

// hasSubfolder reports whether any apply dir is the given kind subfolder.
func hasSubfolder(dirs []applyDirInfo, sub string) bool {
	for _, d := range dirs {
		if d.Subfolder == sub {
			return true
		}
	}
	return false
}

// writeContextHeader writes the context label line before a group of apply commands.
// For kumactl contexts it adds the command that selects the context
// (`kumactl config control-planes switch --name`; kumactl 2.14 and 3.0 have no
// `config use-context`); for kubectl contexts it's plain.
func writeContextHeader(b *strings.Builder, d applyDirInfo) {
	switch {
	case d.KumactlOnKubernetes:
		linef(b, "   **Context `%s`** (kubectl — extracted with kumactl from a Kubernetes-backed control plane):", d.CPModeDir)
		ctxName := d.Context
		if ctxName == "" {
			ctxName = d.CPModeDir
		}
		linef(b, "   > `kumactl` cannot create, update or delete resources on a Kubernetes-backed control plane, so use")
		linef(b, "   > `kubectl` against the cluster that runs the control plane of kumactl context `%s`.", ctxName)
		if d.UniversalFiles {
			line(b, "   > These files are in Universal format (`type`/`name`/`mesh`), which `kubectl` cannot apply.")
			line(b, "   > Re-run `kuma-migrator extract` with `--output-format kubernetes`, then `migrate` again.")
		}
	case d.Tool == "kumactl":
		label := "kumactl"
		if d.IsKonnect {
			label = "kumactl — Konnect-hosted CP"
		}
		linef(b, "   **Context `%s`** (%s):", d.CPModeDir, label)
		ctxName := d.Context
		if ctxName == "" {
			ctxName = d.CPModeDir // fallback
		}
		linef(b, "   > Set the active context first: `kumactl config control-planes switch --name %s`", ctxName)
	default:
		linef(b, "   **Context `%s`**:", d.CPModeDir)
	}
	line(b, "")
}

// writeApplyDirCommands writes the apply commands for a single output directory.
// For kumactl contexts each file is listed individually since kumactl before
// 2.14 does not accept a directory as the -f argument; `kumactl apply` already replaces the
// whole spec and label set of an existing resource (its upsert does Get,
// SetSpec, Update), so it needs no replace form. For kubectl a single directory
// apply is used, unless the directory holds documents that rewrite an existing
// object (applyDirInfo.Replace): then those are listed with `kubectl replace`
// and the rest with `kubectl apply`, one file per line, because a directory
// apply would also apply the rewritten ones. When the extraction tool is
// unknown (legacy/flat layout) kubectl is assumed.
func writeApplyDirCommands(b *strings.Builder, d applyDirInfo, outputDir string) {
	line(b, "   ```bash")
	switch {
	case d.Tool == "kumactl":
		for _, f := range d.Files {
			linef(b, "   kumactl apply -f %s", filepath.Join(outputDir, f))
		}
	case len(d.Replace) == 0:
		linef(b, "   kubectl apply -f %s/", d.FullDir)
	default:
		for _, f := range d.Files {
			if d.Replace[f] {
				linef(b, "   kubectl replace -f %s --save-config", filepath.Join(outputDir, f))
			}
		}
		for _, f := range d.Files {
			if !d.Replace[f] {
				linef(b, "   kubectl apply -f %s", filepath.Join(outputDir, f))
			}
		}
	}
	line(b, "   ```")
	line(b, "")
}

// preUpgradeApplyDirs is collectApplyDirs narrowed to FileReport.PreUpgradeRelPaths.
func preUpgradeApplyDirs(r *MigrationReport) []applyDirInfo {
	pre := map[string]bool{}
	for _, fr := range r.Files {
		for _, p := range fr.PreUpgradeRelPaths {
			pre[p] = true
		}
	}
	if len(pre) == 0 {
		return nil
	}
	var out []applyDirInfo
	for _, d := range collectApplyDirs(r) {
		var files []string
		for _, f := range d.Files {
			if pre[f] {
				files = append(files, f)
			}
		}
		if len(files) > 0 {
			d.Files = files
			out = append(out, d)
		}
	}
	return out
}

// writeApplyFileCommands writes one apply command per file of d (never the
// whole directory), with kubectl replace for files in d.Replace.
func writeApplyFileCommands(b *strings.Builder, d applyDirInfo, outputDir string) {
	line(b, "   ```bash")
	for _, f := range d.Files {
		full := filepath.Join(outputDir, f)
		switch {
		case d.Tool == "kumactl":
			linef(b, "   kumactl apply -f %s", full)
		case d.Replace[f]:
			linef(b, "   kubectl replace -f %s --save-config", full)
		default:
			linef(b, "   kubectl apply -f %s", full)
		}
	}
	line(b, "   ```")
	line(b, "")
}

// writeReplaceNote explains `kubectl replace` once per apply step, when any of
// its kubectl directories holds a document that rewrites an existing object.
func writeReplaceNote(b *strings.Builder, dirs []applyDirInfo) {
	found := false
	for _, d := range dirs {
		if d.Tool != "kumactl" && len(d.Replace) > 0 {
			found = true
			break
		}
	}
	if !found {
		return
	}
	line(b, "   > `kubectl replace` is used for files that rewrite an object already on the control plane (same")
	line(b, "   > kind, namespace and name as an input document). `kubectl apply` merges into the live object and,")
	line(b, "   > when the object has no `kubectl.kubernetes.io/last-applied-configuration` annotation (made by")
	line(b, "   > the control plane, Helm or `kubectl create`), keeps every field the new file drops: a converted")
	line(b, "   > `from[]` policy then carries both `rules[]` and the old `to[]` and is rejected, a converted `Mesh`")
	line(b, "   > keeps its embedded backends. `kubectl apply --server-side --force-conflicts` does not remove them")
	line(b, "   > either: fields owned by another field manager stay. `--save-config` records the new content, so a")
	line(b, "   > later `kubectl apply` of the same file merges correctly. `kubectl replace` fails with `NotFound` if")
	line(b, "   > the object was deleted since `extract`; use `kubectl create -f <file>` for that file.")
	line(b, "")
}

// ── Apply dir helpers ─────────────────────────────────────────────────────────

// applyDirInfo represents a single output directory to be applied.
type applyDirInfo struct {
	FullDir   string   // absolute path to the directory
	RelDir    string   // relative to outputDir
	CPModeDir string   // first path component (context label)
	Subfolder string   // last path component ("resiliency", "mesh", etc.)
	Tool      string   // write tool: "kubectl" or "kumactl" (resolveContextTool)
	IsKonnect bool     // true when the CP is Konnect-hosted (from extraction metadata)
	Context   string   // original kumactl/kubectl context name (from extraction metadata)
	Files     []string // output file paths relative to outputDir, in this dir
	// Replace holds the Files that rewrite an object present in the input
	// (FileReport.ReplaceRelPaths); kubectl contexts replace them.
	Replace map[string]bool
	// KumactlOnKubernetes: extracted with kumactl from a Kubernetes-backed CP;
	// Tool is kubectl.
	KumactlOnKubernetes bool
	// UniversalFiles: at least one file in the dir is in Universal format.
	UniversalFiles bool
}

// collectApplyDirs returns all distinct output directories derived from the
// report's output file paths, sorted for correct application order:
// global/standalone CPs before zone CPs; mesh/ subfolder last within each CP.
// Tool metadata is read from .kuma-migrator.json files written by kuma-migrator extract.
func collectApplyDirs(r *MigrationReport) []applyDirInfo {
	subOrder := map[string]int{
		backendsSubfolder: -1, // referenced by label from observability policies
		"resiliency":      0, "routing": 1, "zero-trust": 2, "observability": 3,
		"gateway": 4, "workload": 4, "other": 4, "mesh": 99,
	}
	subRank := func(s string) int {
		if rank, ok := subOrder[s]; ok {
			return rank
		}
		return 50
	}

	toolFor := newContextToolResolver(r)

	// Collect distinct dirs and their files.
	dirMap := map[string]*applyDirInfo{} // relDir → info
	var dirOrder []string

	for _, fr := range r.Files {
		replaced := map[string]bool{}
		for _, p := range fr.ReplaceRelPaths {
			replaced[p] = true
		}
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

				t := toolFor(cpModeDir)
				info := &applyDirInfo{
					FullDir:             filepath.Join(r.OutputDir, relDir),
					RelDir:              relDir,
					CPModeDir:           cpModeDir,
					Subfolder:           subfolder,
					Tool:                t.Tool,
					IsKonnect:           t.IsKonnect,
					Context:             t.Context,
					KumactlOnKubernetes: t.KumactlOnKubernetes,
				}
				dirMap[relDir] = info
				dirOrder = append(dirOrder, relDir)
			}
			dirMap[relDir].Files = append(dirMap[relDir].Files, relPath)
			if replaced[relPath] {
				if dirMap[relDir].Replace == nil {
					dirMap[relDir].Replace = map[string]bool{}
				}
				dirMap[relDir].Replace[relPath] = true
			}
			if fr.UniversalOutput {
				dirMap[relDir].UniversalFiles = true
			}
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

// scenarioCell is the Scenario column of the migrated-files tables. A
// passthrough document the scan pass changed is not "already migrated".
func scenarioCell(dc DocChange) string {
	if dc.Scenario == ScenarioPassthrough && dc.Rewritten {
		return "Passthrough, rewritten for the target"
	}
	return dc.Scenario.String()
}

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
			if dc.Scenario == ScenarioGatewayRoute {
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

// writeV3TopologyNote lists extracted control planes whose deployment 3.0
// rejects: `standalone` mode, and a Global CP on Kubernetes (environment or
// store). Fed by each context's .kuma-migrator.json; contexts without one are
// not checked. Called only for a v3 target.
func writeV3TopologyNote(b *strings.Builder, r *MigrationReport) {
	var blocking, check []extractor.ContextDir
	for _, c := range extractor.ListContextMetas(r.InputDir) {
		switch c.Meta.V3Topology() {
		case extractor.V3TopologyStandalone, extractor.V3TopologyKubernetesGlobal:
			blocking = append(blocking, c)
		case extractor.V3TopologyCheckMode:
			check = append(check, c)
		}
	}
	if len(blocking) == 0 && len(check) == 0 {
		return
	}
	const upgradeMD = "https://github.com/kumahq/kuma/blob/master/UPGRADE.md"
	label := func(c extractor.ContextDir) string {
		dir := c.Dir
		if dir == "" {
			dir = "."
		}
		return fmt.Sprintf("`%s` (context `%s`)", dir, c.Meta.Context)
	}

	line(b, "## Control Plane Topology")
	line(b, "")
	if len(blocking) > 0 {
		line(b, "> **Blocking.** These control planes cannot be upgraded to 3.0 as deployed. Change the")
		line(b, "> deployment on 2.x first; the migrated policies do not depend on it.")
		line(b, "")
	}
	for _, c := range blocking {
		if c.Meta.V3Topology() == extractor.V3TopologyStandalone {
			linef(b, "### %s: `standalone` mode", label(c))
			line(b, "")
			line(b, "3.0 removes `standalone`: `kuma-cp` fails config validation at startup and the Helm chart fails")
			line(b, "at template time. `standalone` and `zone` already behave the same, so the change is a rename:")
			line(b, "")
			line(b, "1. Set `KUMA_MODE=zone` (Universal) or `controlPlane.mode: zone` (Helm), and change any other")
			line(b, "   runtime configuration that still says `standalone`.")
			line(b, "2. Restart the control plane on 2.x and confirm it starts in `zone` mode.")
			line(b, "")
			line(b, "The extract directory keeps its `-standalone-ctx` name; nothing in it has to change.")
			linef(b, "See [`standalone` mode removed](%s#standalone-mode-removed).", upgradeMD)
			line(b, "")
			continue
		}
		linef(b, "### %s: Global control plane on Kubernetes", label(c))
		line(b, "")
		line(b, "3.0 `kuma-cp` rejects `mode=global` with `environment=kubernetes` and with `store.type=kubernetes`.")
		line(b, "A Global control plane must run with `environment=universal` on a non-Kubernetes store such as")
		line(b, "PostgreSQL. It can still be deployed in a Kubernetes cluster. Zone control planes on Kubernetes are")
		line(b, "unaffected. Before upgrading:")
		line(b, "")
		line(b, "1. Deploy a new Global `kuma-cp` with `environment=universal` backed by PostgreSQL (Helm:")
		line(b, "   `controlPlane.mode: global`, `controlPlane.environment: universal`, PostgreSQL credentials in")
		line(b, "   `controlPlane.secrets`).")
		line(b, "2. Recreate this Global's resources on it with `kumactl apply`, which takes Universal YAML: extract")
		line(b, "   the current Global with `--kumactl-context <ctx> --output-format universal` and migrate that output.")
		line(b, "   kuma-migrator does not extract `Secret`, `GlobalSecret` or `AccessRoleBinding`: copy those")
		line(b, "   separately. Without the Mesh CA secrets the new Global generates new CAs.")
		line(b, "3. Point every Zone control plane at the new Global (`controlPlane.kdsGlobalAddress` /")
		line(b, "   `KUMA_MULTIZONE_ZONE_GLOBAL_ADDRESS`).")
		line(b, "4. Decommission the Kubernetes-native Global.")
		line(b, "")
		linef(b, "See [Global control plane on Kubernetes is no longer supported](%s#global-control-plane-on-kubernetes-is-no-longer-supported)", upgradeMD)
		line(b, "and [Universal on Kubernetes using Helm](https://kuma.io/docs/latest/production/cp-deployment/multi-zone/).")
		line(b, "")
	}
	if len(check) > 0 {
		line(b, "### Zone control planes to check")
		line(b, "")
		line(b, "These zones are not connected to a Global CP. A 2.x `kuma-cp` started with `KUMA_MODE=standalone`")
		line(b, "reports `zone` through its API, so the configured value cannot be read from here. If it still says")
		linef(b, "`standalone`, rename it to `zone` before upgrading ([`standalone` mode removed](%s#standalone-mode-removed)).", upgradeMD)
		line(b, "")
		for _, c := range check {
			linef(b, "- %s", label(c))
		}
		line(b, "")
	}
}

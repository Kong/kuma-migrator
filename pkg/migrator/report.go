package migrator

import (
	"fmt"
	"os"
	"strings"
	"time"
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
	title := "Migration Plan"
	if r.Mode == "apply" {
		title = "Migration Report"
	}

	line(b, "# Kuma Migrator — "+title)
	line(b, "")
	linef(b, "Generated: %s", time.Now().Format("2006-01-02 15:04:05"))
	line(b, "")
	linef(b, "**Input directory:** `%s`", r.InputDir)
	linef(b, "**Output directory:** `%s`", r.OutputDir)
	if r.Mode == "plan" {
		line(b, "")
		line(b, "> **Dry run** — no files have been written. Run `kuma-migrator apply` to apply these changes.")
	}
	line(b, "")

	// ── Summary table ──────────────────────────────────────────────────────────
	line(b, "## Summary")
	line(b, "")
	line(b, "| Metric | Count |")
	line(b, "|---|---:|")
	linef(b, "| Files processed | %d |", r.TotalFiles)
	linef(b, "| Files migrated | %d |", r.MigratedCount)
	linef(b, "| Already migrated | %d |", r.AlreadyDoneCount)
	linef(b, "| Skipped (non-policy) | %d |", r.SkippedCount)
	linef(b, "| Errors | %d |", r.ErrorCount)
	line(b, "")

	// ── Errors first ──────────────────────────────────────────────────────────
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

	if len(errFiles) > 0 {
		line(b, "## Errors")
		line(b, "")
		line(b, "> These files could not be fully migrated and require manual attention.")
		line(b, "")
		for _, fr := range errFiles {
			linef(b, "### `%s`", fr.FileName)
			line(b, "")
			for _, dc := range fr.Changes {
				if dc.ErrMsg != "" {
					linef(b, "- **Error:** %s", dc.ErrMsg)
				}
			}
			line(b, "")
		}
	}

	// ── Files to migrate ──────────────────────────────────────────────────────
	if len(migratedFiles) > 0 {
		line(b, "## Files to Migrate")
		line(b, "")
		for _, fr := range migratedFiles {
			writeFileSection(b, fr)
		}
	}

	// ── Already migrated ──────────────────────────────────────────────────────
	if len(alreadyFiles) > 0 {
		line(b, "## Already Migrated")
		line(b, "")
		line(b, "These files are already using the new API and will be passed through unchanged.")
		line(b, "")
		for _, fr := range alreadyFiles {
			linef(b, "- `%s`", fr.FileName)
		}
		line(b, "")
	}

	// ── Skipped ───────────────────────────────────────────────────────────────
	if len(skippedFiles) > 0 {
		line(b, "## Skipped Files")
		line(b, "")
		line(b, "These files contain no recognised Kuma policy documents.")
		line(b, "")
		for _, fr := range skippedFiles {
			linef(b, "- `%s`", fr.FileName)
		}
		line(b, "")
	}

	// ── Service address mappings ───────────────────────────────────────────────
	if len(r.AddressMappings) > 0 {
		line(b, "## Workload Service Address Mappings")
		line(b, "")
		line(b, "Legacy `kuma.io/service`-encoded addresses were found in env vars.")
		line(b, "Update these in your Deployments, StatefulSets, etc.")
		line(b, "Replace `<zone>` with your actual Kuma zone name for the mesh hostname.")
		line(b, "")
		line(b, "| Old Address | Kubernetes Address | Mesh Address |")
		line(b, "|---|---|---|")
		for _, h := range r.AddressMappings {
			linef(b, "| `%s` | `%s` | `%s` |",
				h.OldToken+h.ExplicitPort, h.NewK8sAddress(), h.NewMeshAddress())
		}
		line(b, "")
	}

	// ── Deprecated annotations ─────────────────────────────────────────────────
	if len(r.AnnotationHits) > 0 {
		line(b, "## Deprecated Annotations")
		line(b, "")
		line(b, "These resources use `\"yes\"`/`\"no\"` annotation values deprecated in Kuma 2.9.")
		line(b, "Update them to `\"true\"`/`\"false\"` in your manifests.")
		line(b, "")
		line(b, "| Resource | Namespace | Annotation | Old Value | New Value |")
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

	// ── MeshService advisory ───────────────────────────────────────────────────
	line(b, "## MeshService Mode — Required Action")
	line(b, "")
	line(b, "> **Stop using `kuma.io/service` tags.** From Kuma 2.6+ the canonical service")
	line(b, "> identity is the `MeshService` resource (auto-generated from Kubernetes `Service`")
	line(b, "> objects), `MeshExternalService`, and `MeshMultizoneService`. All policies")
	line(b, "> produced by this tool target these resources — they will **not** work until")
	line(b, "> every `Mesh` in your cluster has `spec.meshServices.mode: Exclusive`.")
	line(b, "")
	line(b, "### What `Exclusive` mode does")
	line(b, "")
	line(b, "| Mode | Behaviour |")
	line(b, "|---|---|")
	line(b, "| `ReachableBackends` *(default)* | Kuma generates **both** legacy `kuma.io/service` tags **and** `MeshService` resources. Policies can match either. |")
	line(b, "| `Exclusive` | Kuma generates **only** `MeshService` / `MeshExternalService` / `MeshMultizoneService`. Legacy tag matching is disabled. |")
	line(b, "")
	line(b, "### How to enable it")
	line(b, "")
	line(b, "kuma-migrator has automatically added `spec.meshServices.mode: Exclusive` to")
	line(b, "every `Mesh` CRD it processed. If any `Mesh` resources were **not** included")
	line(b, "in the input directory, patch them manually:")
	line(b, "")
	line(b, "```yaml")
	line(b, "# patch.yaml")
	line(b, "spec:")
	line(b, "  meshServices:")
	line(b, "    mode: Exclusive")
	line(b, "```")
	line(b, "")
	line(b, "```bash")
	line(b, "kubectl patch mesh <name> --type merge --patch-file patch.yaml")
	line(b, "```")
	line(b, "")
	line(b, "> **Before switching to Exclusive mode**, confirm that *all* workload policies")
	line(b, "> and env-var service addresses in this mesh have been migrated. Enabling")
	line(b, "> Exclusive mode before migration is complete will break traffic for any")
	line(b, "> workload still addressed by a `kuma.io/service` tag.")
	line(b, "")

	// ── Migration notes ────────────────────────────────────────────────────────
	line(b, "## Migration Notes")
	line(b, "")
	line(b, "### Scenario Reference")
	line(b, "")
	line(b, "| Scenario | Description |")
	line(b, "|---|---|")
	line(b, "| Legacy | Old-style `sources`/`destinations` policies → new `targetRef`/`to`/`from` style |")
	line(b, "| Subset | New-style Mesh* kind with `MeshSubset` service-identity tags → `Dataplane`/`MeshService` |")
	line(b, "| Passthrough | Already migrated — passed through unchanged |")
	line(b, "| Rules | New-style Mesh* kind with deprecated `from[]` → new `rules[]` API (Kuma 2.10+) |")
	line(b, "| Mesh | `Mesh` CRD with embedded observability/passthrough → standalone companion CRDs |")
	line(b, "| ExternalService | `ExternalService` → `MeshExternalService` |")
	line(b, "| GW | Gateway resources (`MeshGateway`, `MeshGatewayInstance`, `MeshGatewayRoute`, `MeshHTTPRoute`, `MeshTCPRoute`) → Gateway API CRDs |")
	line(b, "| OPA | Kong Mesh `OPAPolicy` → `MeshOPA` |")
	line(b, "")
	if r.Mode == "plan" {
		line(b, "### Next Steps")
		line(b, "")
		line(b, "1. Review this plan and address any warnings or errors.")
		line(b, "2. Run `kuma-migrator migrate --input-dir <input> --output-dir <output>` to apply the migration.")
		line(b, "3. Review the migrated files in the output directory.")
		line(b, "4. Apply the migrated files to your cluster using `kubectl apply -f <output-dir>`.")
		line(b, "5. Verify that every `Mesh` resource has `spec.meshServices.mode: Exclusive` (kuma-migrator sets this automatically on processed Mesh CRDs).")
		line(b, "6. Monitor your services and remove the old policies once the new ones are confirmed working.")
		line(b, "")
	}
}

func writeFileSection(b *strings.Builder, fr FileReport) {
	linef(b, "### `%s` — %s", fr.FileName, fr.Label)
	line(b, "")

	// Per-document table.
	hasDocs := false
	for _, dc := range fr.Changes {
		if dc.InputKind != "" {
			hasDocs = true
			break
		}
	}
	if hasDocs {
		line(b, "| Kind | Name | Scenario |")
		line(b, "|---|---|---|")
		for _, dc := range fr.Changes {
			if dc.InputKind == "" {
				continue
			}
			linef(b, "| `%s` | `%s` | %s |", dc.InputKind, dc.InputName, dc.Scenario)
		}
		line(b, "")
	}

	// Collect all warnings across docs.
	var allWarnings []string
	for _, dc := range fr.Changes {
		allWarnings = append(allWarnings, dc.Warnings...)
	}
	if len(allWarnings) > 0 {
		line(b, "**Warnings:**")
		line(b, "")
		for _, w := range allWarnings {
			linef(b, "- %s", w)
		}
		line(b, "")
	}

	// Env-var hits for this file.
	if len(fr.EnvVarHits) > 0 {
		line(b, "**Legacy service addresses in env vars:**")
		line(b, "")
		line(b, "| Workload | Container | Env Var | Value |")
		line(b, "|---|---|---|---|")
		for _, h := range fr.EnvVarHits {
			linef(b, "| `%s/%s` (ns: `%s`) | `%s` | `%s` | `%s` |",
				h.WorkloadKind, h.WorkloadName, h.Namespace,
				h.ContainerName, h.EnvVarName, h.RawValue)
		}
		line(b, "")
	}

	// Annotation hits for this file.
	if len(fr.AnnotHits) > 0 {
		line(b, "**Deprecated annotations:**")
		line(b, "")
		for _, h := range fr.AnnotHits {
			linef(b, "- `%s/%s`: `%s: \"%s\"` → `\"%s\"`",
				h.Kind, h.Name, h.AnnotationKey, h.OldValue, h.NewValue)
		}
		line(b, "")
	}
}

func line(b *strings.Builder, s string) {
	b.WriteString(s)
	b.WriteByte('\n')
}

func linef(b *strings.Builder, format string, args ...interface{}) {
	b.WriteString(fmt.Sprintf(format, args...))
	b.WriteByte('\n')
}

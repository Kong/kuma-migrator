package cmd

import (
	"fmt"
	"os"

	"github.com/Kong/kuma-migrator/pkg/migrator"
	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Transform policy files, writing them (or preview with --dry-run)",
	Long: `Reads Kuma/Kong Mesh policy YAML files from --input-dir, transforms them
to the current API, writes the results to --output-dir, and writes a Markdown
report to --output-dir/migration-report.md.

Run with --dry-run first to preview every change, warning, and required
manual action without writing any output YAML files — only a
migration-plan.md report is written in that case.`,
	Example: `  # Preview every change first
  kuma-migrator migrate --input-dir old-kuma-configs --output-dir new-kuma-configs --dry-run

  # Then run it for real
  kuma-migrator migrate --input-dir old-kuma-configs --output-dir new-kuma-configs`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := os.Stat(migrateInputDir); os.IsNotExist(err) {
			return fmt.Errorf("input directory does not exist: %s", migrateInputDir)
		}
		target, err := migrator.ParseTargetVersion(migrateToLatest)
		if err != nil {
			return err
		}
		if migrateDryRun {
			return migrator.Plan(migrateInputDir, migrateOutputDir, migrateMesh, target)
		}
		return migrator.Migrate(migrateInputDir, migrateOutputDir, migrateMesh, target)
	},
}

var migrateInputDir string
var migrateOutputDir string
var migrateMesh string
var migrateToLatest string
var migrateDryRun bool

func init() {
	migrateCmd.Flags().StringVarP(&migrateInputDir, "input-dir", "i", "", "directory containing source policy YAML files (required)")
	migrateCmd.Flags().StringVarP(&migrateOutputDir, "output-dir", "o", "", "directory to write migrated YAML files and the migration-report.md (required)")
	migrateCmd.Flags().StringVar(&migrateMesh, "mesh", "", "restrict migration to the named Kuma mesh (default: all meshes)")
	migrateCmd.Flags().StringVar(&migrateToLatest, "to-latest", "v2", toLatestFlagUsage)
	migrateCmd.Flags().BoolVar(&migrateDryRun, "dry-run", false, "preview every change without writing any output YAML files — writes migration-plan.md instead of migration-report.md")
	_ = migrateCmd.MarkFlagRequired("input-dir")
	_ = migrateCmd.MarkFlagRequired("output-dir")
	rootCmd.AddCommand(migrateCmd)
}

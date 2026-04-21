package cmd

import (
	"fmt"
	"os"

	"github.com/Kong/kuma-migrator/pkg/migrator"
	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Transform and write migrated policy files",
	Long: `Reads Kuma/Kong Mesh policy YAML files from --input-dir, transforms them
to the current API, writes the results to --output-dir, and writes a Markdown
report to --output-dir/migration-report.md.

Run 'kuma-migrator plan' first to preview all changes before applying.`,
	Example: `  kuma-migrator migrate --input-dir old-kuma-configs --output-dir new-kuma-configs`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := os.Stat(migrateInputDir); os.IsNotExist(err) {
			return fmt.Errorf("input directory does not exist: %s", migrateInputDir)
		}
		return migrator.Migrate(migrateInputDir, migrateOutputDir, migrateMesh)
	},
}

var migrateInputDir string
var migrateOutputDir string
var migrateMesh string

func init() {
	migrateCmd.Flags().StringVarP(&migrateInputDir, "input-dir", "i", "", "directory containing source policy YAML files (required)")
	migrateCmd.Flags().StringVarP(&migrateOutputDir, "output-dir", "o", "", "directory to write migrated YAML files and the migration-report.md (required)")
	migrateCmd.Flags().StringVar(&migrateMesh, "mesh", "", "restrict migration to the named Kuma mesh (default: all meshes)")
	_ = migrateCmd.MarkFlagRequired("input-dir")
	_ = migrateCmd.MarkFlagRequired("output-dir")
	rootCmd.AddCommand(migrateCmd)
}

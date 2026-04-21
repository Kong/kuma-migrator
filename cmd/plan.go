package cmd

import (
	"fmt"
	"os"

	"github.com/Kong/kuma-migrator/pkg/migrator"
	"github.com/spf13/cobra"
)

var planCmd = &cobra.Command{
	Use:   "plan",
	Short: "Preview all migration changes without writing output files",
	Long: `Reads Kuma/Kong Mesh policy YAML files from --input-dir, runs every
transformation in dry-run mode, and writes a Markdown plan report to
--output-dir/migration-plan.md.

No output YAML files are written. Use this command to review all changes,
warnings, and required manual actions before running 'kuma-migrator migrate'.`,
	Example: `  kuma-migrator plan --input-dir old-kuma-configs --output-dir plan`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := os.Stat(planInputDir); os.IsNotExist(err) {
			return fmt.Errorf("input directory does not exist: %s", planInputDir)
		}
		return migrator.Plan(planInputDir, planOutputDir, planMesh)
	},
}

var planInputDir string
var planOutputDir string
var planMesh string

func init() {
	planCmd.Flags().StringVarP(&planInputDir, "input-dir", "i", "", "directory containing source policy YAML files (required)")
	planCmd.Flags().StringVarP(&planOutputDir, "output-dir", "o", "", "directory to write the migration-plan.md report (required)")
	planCmd.Flags().StringVar(&planMesh, "mesh", "", "restrict planning to the named Kuma mesh (default: all meshes)")
	_ = planCmd.MarkFlagRequired("input-dir")
	_ = planCmd.MarkFlagRequired("output-dir")
	rootCmd.AddCommand(planCmd)
}

package cmd

import (
	"fmt"
	"os"

	"github.com/Kong/kuma-migrator/pkg/extractor"
	"github.com/spf13/cobra"
)

var extractCmd = &cobra.Command{
	Use:   "extract",
	Short: "Extract Kuma policy resources from a running control plane",
	Long: `Connects to a Kuma or Kong Mesh control plane and writes one YAML file per
resource to --output-dir. The output directory can then be used directly as
the --input-dir for 'kuma-migrator plan' or 'kuma-migrator migrate'.

Resources whose kind contains "Insight" (e.g. ZoneInsight, DataplaneInsight)
are automatically excluded — these are control-plane status objects, not policies.

Two connection modes are supported (mutually exclusive):

  --kube-context   Reads resources via kubectl from the given Kubernetes context.
                   Lists all kuma.io/v1alpha1 CRDs in the cluster and fetches
                   every instance, producing one file per resource.

  --kumactl-context  Uses the kumactl CLI and the kumactl config file
                   (~/.kumactl/config or $KUMACTL_CONFIG) to resolve the
                   context and its linked control plane, then runs:
                     kumactl export --profile no-dataplanes --format kubernetes
                   and splits the output into individual files.`,
	Example: `  # Extract via kubectl
  kuma-migrator extract --kube-context prod-global --output-dir ./raw-policies

  # Extract via kumactl (uses ~/.kumactl/config)
  kuma-migrator extract --kumactl-context my-cp --output-dir ./raw-policies

  # Then plan the migration
  kuma-migrator plan --input-dir ./raw-policies --output-dir ./plan`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if extractKubeContext == "" && extractKumactlContext == "" {
			return fmt.Errorf("one of --kube-context or --kumactl-context is required")
		}
		if extractKubeContext != "" && extractKumactlContext != "" {
			return fmt.Errorf("--kube-context and --kumactl-context are mutually exclusive")
		}
		if _, err := os.Stat(extractOutputDir); os.IsNotExist(err) {
			// Output dir will be created by the extractor — no pre-check needed.
		}
		if extractKubeContext != "" {
			return extractor.ExtractViaKubectl(extractKubeContext, extractOutputDir)
		}
		return extractor.ExtractViaKumactl(extractKumactlContext, extractOutputDir)
	},
}

var extractKubeContext string
var extractKumactlContext string
var extractOutputDir string

func init() {
	extractCmd.Flags().StringVar(&extractKubeContext, "kube-context", "", "Kubernetes context to use for resource extraction (kubectl)")
	extractCmd.Flags().StringVar(&extractKumactlContext, "kumactl-context", "", "kumactl context name to use for resource extraction (kumactl CLI)")
	extractCmd.Flags().StringVarP(&extractOutputDir, "output-dir", "o", "", "directory to write extracted YAML files (required)")
	_ = extractCmd.MarkFlagRequired("output-dir")
	rootCmd.AddCommand(extractCmd)
}

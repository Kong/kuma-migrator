package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/Kong/kuma-migrator/pkg/config"
	"github.com/Kong/kuma-migrator/pkg/extractor"
	"github.com/Kong/kuma-migrator/pkg/readiness"
	"github.com/Kong/kuma-migrator/pkg/ui"
	"github.com/spf13/cobra"
)

var extractCmd = &cobra.Command{
	Use:   "extract",
	Short: "Extract Kuma policy resources from a running control plane",
	Long: `Connects to a Kuma or Kong Mesh control plane and writes one YAML file per
resource to --output-dir. The output directory can then be used directly as
the --input-dir for 'kuma-migrator migrate' (add --dry-run to preview first).

Resources whose kind contains "Insight" (e.g. ZoneInsight, DataplaneInsight)
are automatically excluded — these are control-plane status objects, not policies.

Two connection modes are supported (mutually exclusive):

  --kube-context   Reads resources via kubectl from the given Kubernetes context.
                   Lists all kuma.io/v1alpha1 CRDs in the cluster and fetches
                   every instance, producing one file per resource.

  --kumactl-context  Uses the kumactl CLI and the kumactl config file
                   (~/.kumactl/config or $KUMACTL_CONFIG, which is also
                   passed to kumactl as --config-file) to resolve the
                   context and its linked control plane, calls its
                   /_resources endpoint to discover every writable resource
                   type, lists the meshes, then runs "kumactl get <type>
                   [--mesh <mesh>] -o yaml" for each type/mesh combination
                   (or an authenticated HTTP GET instead of the CLI, for a
                   Konnect-hosted control plane), writing one file per
                   resource.

Both modes detect the control plane mode and filter on the kuma.io/origin label:

  global                  zone-origin copies are skipped and listed (extract
                          them from their zone).
  zone, federated         copies synced from Global (kuma.io/origin: global)
                          are skipped. Resources labelled kuma.io/origin: zone
                          and resources with no origin label are kept: Kuma 2.x
                          does not label legacy policies, ExternalService,
                          ProxyTemplate, MeshGatewayRoute or the Kong Mesh RBAC
                          kinds, and KDS labels every copy it syncs.
  zone, not federated     no filter: everything is extracted.
  standalone / unknown    no filter: everything is extracted.

A zone is federated when it has a Global address and a zone name
(GET /config multizone.zone.globalAddress for kumactl,
KUMA_MULTIZONE_ZONE_GLOBAL_ADDRESS on the CP Deployment for kubectl).

In every mode, resources the control plane generates (kuma.io/managed-by:
k8s-controller, meshservice-generator, workload-generator, such as the
MeshServices created from Kubernetes Services) are skipped, and the fields
the server writes are dropped so the files can be re-applied: status,
metadata.resourceVersion/uid/generation/creationTimestamp/managedFields/
ownerReferences, the kubectl last-applied annotation, and Universal
creationTime/modificationTime/kri.

Each Mesh is written under its own mesh-<name>/ directory.

--preflight (kumactl mode only) then runs the Kong Mesh 3.0 readiness audit
against the same control plane (see 'kuma-migrator preflight --help') and writes
v3-preflight.json and v3-preflight.html into the context directory. 'migrate'
summarises them in its report. A failed audit is reported and does not fail the
extraction.`,
	Example: `  # Extract via kumactl (uses ~/.kumactl/config; self-hosted OR Konnect)
  kuma-migrator extract --kumactl-context konnect-global-prod --output-dir ./raw-policies

  # ...or via kubectl
  kuma-migrator extract --kube-context zone-eu-west1-prod --output-dir ./raw-policies

  # ...and audit the same control plane for 3.0 readiness
  kuma-migrator extract --kumactl-context prod-global --output-dir ./raw-policies --preflight

  # Then preview the migration
  kuma-migrator migrate --input-dir ./raw-policies --output-dir ./migrated --dry-run`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if extractKubeContext == "" && extractKumactlContext == "" {
			return fmt.Errorf("one of --kube-context or --kumactl-context is required")
		}
		if extractKubeContext != "" && extractKumactlContext != "" {
			return fmt.Errorf("--kube-context and --kumactl-context are mutually exclusive")
		}
		if extractPreflight && extractKubeContext != "" {
			return fmt.Errorf("--preflight needs the control plane REST API: use --kumactl-context, or port-forward the CP API (e.g. kubectl port-forward -n kong-mesh-system svc/kong-mesh-control-plane 5681) and run 'kuma-migrator preflight --address http://localhost:5681'")
		}
		if _, err := os.Stat(extractOutputDir); os.IsNotExist(err) {
			// Output dir will be created by the extractor — no pre-check needed.
		}
		// Config file can enable TLS skip verify; the -k flag also enables it.
		// Either source being true is sufficient.
		if extractOutputFormat != "kubernetes" && extractOutputFormat != "universal" {
			return fmt.Errorf("--output-format must be %q or %q, got %q", "kubernetes", "universal", extractOutputFormat)
		}
		cfg, _ := config.Load()
		extractor.TLSSkipVerify = extractTLSSkipVerify || (cfg != nil && cfg.AdminServer.TLSSkipVerify)
		if extractKubeContext != "" {
			return extractor.ExtractViaKubectl(extractKubeContext, extractOutputDir, extractMesh, extractOutputFormat)
		}
		res, err := extractor.ExtractViaKumactlResult(extractKumactlContext, extractOutputDir, extractMesh, extractOutputFormat)
		if err != nil || !extractPreflight {
			return err
		}
		if _, err := readiness.Run(readiness.RunOptions{
			Address:           res.Target.Address,
			Token:             res.Target.Token,
			Konnect:           res.Target.Konnect,
			Mesh:              extractMesh,
			LatestVersion:     extractLatestVersion,
			InspectDataplanes: extractInspectDataplanes,
			ResourceReadLimit: readiness.DefaultResourceReadLimit,
			Timeout:           extractPreflightTimeout,
			HTTPClient:        extractor.NewHTTPClient(extractPreflightTimeout),
			OutputDir:         res.ContextDir,
		}); err != nil {
			ui.Warn(fmt.Sprintf("Readiness audit: %v. The extracted files are complete; re-run the audit with 'kuma-migrator preflight'.", err))
		}
		return nil
	},
}

var extractKubeContext string
var extractKumactlContext string
var extractOutputDir string
var extractTLSSkipVerify bool
var extractMesh string
var extractOutputFormat string
var extractPreflight bool
var extractLatestVersion string
var extractInspectDataplanes int
var extractPreflightTimeout time.Duration

func init() {
	extractCmd.Flags().StringVar(&extractKubeContext, "kube-context", "", "Kubernetes context to use for resource extraction (kubectl)")
	extractCmd.Flags().StringVar(&extractKumactlContext, "kumactl-context", "", "kumactl context name to use for resource extraction (kumactl CLI)")
	extractCmd.Flags().StringVarP(&extractOutputDir, "output-dir", "o", "", "directory to write extracted YAML files (required)")
	extractCmd.Flags().BoolVarP(&extractTLSSkipVerify, "tls-skip-verify", "k", false, "disable TLS certificate verification for the control plane admin server (use for self-signed certs)")
	extractCmd.Flags().StringVar(&extractMesh, "mesh", "", "restrict extraction to the named Kuma mesh (default: all meshes)")
	extractCmd.Flags().StringVarP(&extractOutputFormat, "output-format", "f", "universal", `output format for extracted resources: "kubernetes" (apiVersion/kind/metadata) or "universal" (type/name/mesh)`)
	extractCmd.Flags().BoolVar(&extractPreflight, "preflight", false, "after extracting, run the Kong Mesh 3.0 readiness audit against the same control plane (--kumactl-context only)")
	addPreflightAuditFlags(extractCmd.Flags(), &extractLatestVersion, &extractInspectDataplanes, &extractPreflightTimeout, "preflight-")
	_ = extractCmd.MarkFlagRequired("output-dir")
	rootCmd.AddCommand(extractCmd)
}

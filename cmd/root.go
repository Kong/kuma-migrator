package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// SetVersion is called by main to inject the build-time version string.
func SetVersion(v string) {
	rootCmd.Version = v
}

var rootCmd = &cobra.Command{
	Use:   "kuma-migrator",
	Short: "Migrate Kuma/Kong Mesh policies to the current API",
	Long: `kuma-migrator transforms Kuma and Kong Mesh YAML policy manifests
across all supported migration paths:

  - Legacy sources/destinations → new targetRef/to/from policies
  - MeshSubset service-identity tags → Dataplane/MeshService references
  - Deprecated from[] on Mesh* policies → new rules[] API (Kuma 2.10+)
  - Mesh CRD observability sections → standalone MeshMetric/MeshTrace/MeshAccessLog
  - ExternalService → MeshExternalService
  - MeshGateway/MeshGatewayRoute/MeshHTTPRoute/MeshTCPRoute → Gateway API CRDs
  - Deprecated field detection (sidecar.regex, healthyPanicThreshold, spec.origin)
  - Workload env-var and annotation scanners

Use 'kuma-migrator plan' to preview changes before applying them.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

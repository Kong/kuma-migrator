package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// SetVersion is called by main to inject the build-time version string.
func SetVersion(v string) {
	rootCmd.Version = v
}

// toLatestFlagUsage is migrate's --to-latest flag usage text, pulled out to a
// constant so it stays consistent if referenced elsewhere. The 2.x and 3.0
// lines need different output: 3.0 removes fields that 2.14 still accepts,
// and some 3.0 replacements do not exist before 2.14.
const toLatestFlagUsage = `target major version for the migrated output: "v2" (latest 2.x — 2.14.x) or "v3" (3.0).
v2 keeps the output applicable to a 2.x control plane and reports 3.0 removals as
forward-looking advisories; v3 rewrites what it safely can and flags the rest`

var rootCmd = &cobra.Command{
	Use:   "kuma-migrator",
	Short: "Migrate Kuma/Kong Mesh policies to the current API",
	Long: `kuma-migrator extracts Kuma and Kong Mesh policy manifests from a running
control plane and transforms them across the supported migration paths.

Typical workflow:

  extract  →  migrate (--dry-run to preview first)  →  apply

Migration paths:

  - Legacy sources/destinations → new targetRef/to/from policies
  - MeshSubset service-identity tags → Dataplane/MeshService references
  - Deprecated from[] on Mesh* policies → new rules[] API (Kuma 2.10+)
  - Mesh CRD observability sections → standalone MeshMetric/MeshTrace/MeshAccessLog
  - ExternalService → MeshExternalService
  - MeshGateway/MeshGatewayRoute/MeshHTTPRoute/MeshTCPRoute → Gateway API CRDs
  - Kong Mesh OPAPolicy → MeshOPA

Alongside the transforms, every output document is scanned for deprecated and
removed fields across the 2.x line and 3.0. Some are repaired in place (sidecar
regex, spiffeID casing, hash-policy paths, MeshService port protocol); the rest
are reported, with the reason they are not rewritten automatically. The scan
also covers changes that reject on apply in 2.13/2.14 — MeshTrust spec.origin,
OpenTelemetry attribute keys, HostnameGenerator templates, MeshPassthrough
domain matches — and workload env-var and annotation usage.

Choosing a target with --to-latest v2|v3 matters: 3.0 removes fields that 2.14
still requires, and some 3.0 replacements do not exist before 2.14, so one
output cannot serve both. v2 (the default) keeps the result applicable to a 2.x
control plane and reports 3.0 removals as forward-looking advisories; v3
rewrites what it can do safely and flags the rest.

Use 'kuma-migrator migrate --dry-run' to preview changes before running it for real.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

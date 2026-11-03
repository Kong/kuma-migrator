package cmd

import (
	"fmt"
	"net/url"
	"time"

	"github.com/spf13/cobra"

	"github.com/Kong/kuma-migrator/pkg/config"
	"github.com/Kong/kuma-migrator/pkg/extractor"
	"github.com/Kong/kuma-migrator/pkg/readiness"
	"github.com/Kong/kuma-migrator/pkg/ui"
)

var preflightOpts struct {
	kumactlContext    string
	address           string
	token             string
	mesh              string
	outputDir         string
	latestVersion     string
	inspectDataplanes int
	maxResourceReads  int
	timeout           time.Duration
	tlsSkipVerify     bool
}

var preflightCmd = &cobra.Command{
	Use:   "preflight",
	Short: "Audit a running control plane for Kong Mesh 3.0 readiness",
	Long: `Runs the Kong Mesh 3.0 readiness audit (the engine of kong-mesh-v3-preflight,
github.com/Kong/kong-mesh-v3-readiness) against a running 2.x control plane over
its REST API, and writes the report as v3-preflight.json and v3-preflight.html
into --output-dir.

The audit covers what manifests do not show: control plane and zone versions,
control plane settings removed in 3.0, offline zones, resources stored on the
control plane, and, with --inspect-dataplanes, Envoy configuration of running
proxies. 'migrate' covers the manifest rewrite. Run both.

The control plane is taken from a kumactl context (address and token, Konnect
included), or from --address and --token. On Konnect the Global's own settings
and version are managed by Kong and are skipped; every zone is still audited.

The latest 2.x patch is read from the kumahq/kuma GitHub releases unless
--latest-version is set. Without it, the version check does not run and the
report is inconclusive.

Exit status is 0 whenever a report is produced, blockers included (read the
status in the report). It is non-zero when the audit could not run.

'extract --kumactl-context ... --preflight' runs the same audit after extracting,
and 'migrate' then summarises it in its report.`,
	Example: `  kuma-migrator preflight --kumactl-context prod-global -o ./audit
  kuma-migrator preflight --address http://localhost:5681 --latest-version 2.14.7
  kuma-migrator preflight --kumactl-context prod-zone-eu --inspect-dataplanes 20`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		o := preflightOpts
		if o.address != "" && cmd.Flags().Changed("kumactl-context") {
			return fmt.Errorf("--address and --kumactl-context are mutually exclusive")
		}
		cfg, _ := config.Load()
		extractor.TLSSkipVerify = o.tlsSkipVerify || (cfg != nil && cfg.AdminServer.TLSSkipVerify)

		ui.Header("preflight")
		var target extractor.KumactlTarget
		if o.address != "" {
			if err := validateAddress(o.address); err != nil {
				return err
			}
			target = extractor.TargetFromURL(o.address, o.token)
		} else {
			t, err := extractor.ResolveKumactlTarget(o.kumactlContext)
			if err != nil {
				return err
			}
			if o.token != "" {
				t.Token = o.token
			}
			target = t
			ui.KV("Context:", t.Context)
		}
		ui.KV("Control plane:", target.Address)
		if target.Konnect {
			ui.KV("Platform:", "Kong Konnect (hosted)")
		}
		_, err := readiness.Run(readiness.RunOptions{
			Address:           target.Address,
			Token:             target.Token,
			Konnect:           target.Konnect,
			Mesh:              o.mesh,
			LatestVersion:     o.latestVersion,
			InspectDataplanes: o.inspectDataplanes,
			ResourceReadLimit: o.maxResourceReads,
			Timeout:           o.timeout,
			HTTPClient:        extractor.NewHTTPClient(o.timeout),
			OutputDir:         o.outputDir,
		})
		return err
	},
}

func validateAddress(addr string) error {
	u, err := url.Parse(addr)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("invalid --address %q: want http(s)://host[:port], e.g. http://localhost:5681", addr)
	}
	return nil
}

func init() {
	f := preflightCmd.Flags()
	f.StringVar(&preflightOpts.kumactlContext, "kumactl-context", "", "kumactl context of the control plane (default: the current kumactl context)")
	f.StringVar(&preflightOpts.address, "address", "", "control plane REST API URL, instead of a kumactl context (e.g. http://localhost:5681)")
	f.StringVar(&preflightOpts.token, "token", "", "bearer token for the control plane API (overrides the kumactl context's token)")
	f.StringVar(&preflightOpts.mesh, "mesh", "", "audit only this mesh (default: all meshes)")
	f.StringVarP(&preflightOpts.outputDir, "output-dir", "o", ".", "directory for v3-preflight.json and v3-preflight.html")
	addPreflightAuditFlags(f, &preflightOpts.latestVersion, &preflightOpts.inspectDataplanes, &preflightOpts.timeout, "")
	f.IntVar(&preflightOpts.maxResourceReads, "max-resource-reads", readiness.DefaultResourceReadLimit, "cap on resources read across the audit (0 = unlimited)")
	f.BoolVarP(&preflightOpts.tlsSkipVerify, "tls-skip-verify", "k", false, "disable TLS certificate verification for the control plane")
	rootCmd.AddCommand(preflightCmd)
}

// addPreflightAuditFlags registers the flags preflight and extract --preflight
// share. prefix is "" for preflight and "preflight-" for extract, except
// --latest-version which has no prefix in either.
func addPreflightAuditFlags(f interface {
	StringVar(*string, string, string, string)
	IntVar(*int, string, int, string)
	DurationVar(*time.Duration, string, time.Duration, string)
}, latest *string, inspect *int, timeout *time.Duration, prefix string) {
	f.StringVar(latest, "latest-version", "", "latest 2.14 patch to check control plane and zone versions against, e.g. 2.14.7 (default: read from the kumahq/kuma GitHub releases)")
	f.IntVar(inspect, prefix+"inspect-dataplanes", 0, "fetch up to N proxies' Envoy config dumps to find features removed in 3.0 (0 = skip; one request per proxy)")
	f.DurationVar(timeout, prefix+"timeout", 5*time.Minute, "overall timeout for the readiness audit")
}

package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Kong/kuma-migrator/pkg/konnect"
)

var konnectCmd = &cobra.Command{
	Use:   "konnect",
	Short: "Helpers for Kong Konnect-hosted control planes",
}

var tokenScriptOpts struct {
	region        string
	scope         string
	controlPlanes []string
	accountName   string
	expiresIn     time.Duration
	output        string
}

var tokenScriptCmd = &cobra.Command{
	Use:   "token-script",
	Short: "Write the script a Konnect admin runs to give kuma-migrator read-only access",
	Long: `Writes a bash script for a Konnect organization admin. The script creates a
system account, grants it read-only access, and prints one short-lived access
token for 'kuma-migrator extract --kumactl-context'.

kuma-migrator does not call Konnect here and never sees the admin's token: the
admin reviews the script, runs it with KONNECT_ADMIN_PAT set, and passes on the
read-only token only.

Scopes:
  mesh  "Viewer" role on Mesh control planes (all in the region, or the ones
        named with --control-plane). Mesh only, nothing else in the org.
  org   membership of the predefined "Organization Admin (Read Only)" team:
        read access to every Konnect resource in the organization.`,
	Example: `  kuma-migrator konnect token-script --region eu > konnect-readonly-token.sh
  kuma-migrator konnect token-script --region us --control-plane prod-global --expires-in 72h -o token.sh`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if tokenScriptOpts.expiresIn < time.Hour || tokenScriptOpts.expiresIn > 365*24*time.Hour {
			return fmt.Errorf("--expires-in %s: must be between 1h and 8760h (one year)", tokenScriptOpts.expiresIn)
		}
		script, err := konnect.TokenScript(konnect.TokenScriptOptions{
			Region:        strings.ToLower(tokenScriptOpts.region),
			Scope:         konnect.Scope(tokenScriptOpts.scope),
			ControlPlanes: tokenScriptOpts.controlPlanes,
			AccountName:   tokenScriptOpts.accountName,
			ExpiresAt:     time.Now().Add(tokenScriptOpts.expiresIn).Truncate(time.Second),
			GeneratedBy:   rootCmd.Version,
		})
		if err != nil {
			return err
		}
		if tokenScriptOpts.output == "" || tokenScriptOpts.output == "-" {
			_, err = fmt.Fprint(cmd.OutOrStdout(), script)
			return err
		}
		if err := os.WriteFile(tokenScriptOpts.output, []byte(script), 0o755); err != nil {
			return err
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Wrote %s. Send it to a Konnect organization admin, who runs:\n  KONNECT_ADMIN_PAT=kpat_... bash %s\n",
			tokenScriptOpts.output, tokenScriptOpts.output)
		return nil
	},
}

func init() {
	f := tokenScriptCmd.Flags()
	f.StringVar(&tokenScriptOpts.region, "region", "", "Konnect region of the Mesh control planes: "+strings.Join(konnect.Regions, ", "))
	f.StringVar(&tokenScriptOpts.scope, "scope", string(konnect.ScopeMesh), `"mesh" (Viewer on Mesh control planes) or "org" (Organization Admin (Read Only) team)`)
	f.StringSliceVar(&tokenScriptOpts.controlPlanes, "control-plane", nil, "Mesh control plane name to grant (repeatable; default: all in the region; --scope mesh only)")
	f.StringVar(&tokenScriptOpts.accountName, "account-name", "kuma-migrator-readonly", "name of the system account the script creates or reuses")
	f.DurationVar(&tokenScriptOpts.expiresIn, "expires-in", 14*24*time.Hour, "token lifetime from now, e.g. 72h (max 8760h)")
	f.StringVarP(&tokenScriptOpts.output, "output", "o", "", "write the script to this file instead of stdout")
	_ = tokenScriptCmd.MarkFlagRequired("region")

	konnectCmd.AddCommand(tokenScriptCmd)
	rootCmd.AddCommand(konnectCmd)
}

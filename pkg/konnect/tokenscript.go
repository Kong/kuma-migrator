// Package konnect generates helper material for Kong Konnect: today, the shell
// script an organization admin runs to give kuma-migrator read-only access to
// the Konnect-hosted Mesh Global control planes.
//
// The script is generated, not executed: kuma-migrator never holds the admin's
// personal access token. The admin reads the script, runs it, and hands back a
// short-lived system account token.
package konnect

import (
	"bytes"
	_ "embed"
	"fmt"
	"regexp"
	"strings"
	"text/template"
	"time"
)

// Scope is how wide the read-only access is.
type Scope string

const (
	// ScopeMesh assigns the Mesh Control Planes "Viewer" role, per control plane.
	ScopeMesh Scope = "mesh"
	// ScopeOrg adds the system account to the predefined
	// "Organization Admin (Read Only)" team: read access to the whole org.
	ScopeOrg Scope = "org"
)

// Regions are the Konnect geographic regions (the <region>.api.konghq.com hosts).
var Regions = []string{"us", "eu", "au", "me", "in", "sg"}

// TokenScriptOptions configures the generated script.
type TokenScriptOptions struct {
	Region        string
	Scope         Scope
	ControlPlanes []string // Mesh control plane names; empty = every Mesh control plane in the region
	AccountName   string
	ExpiresAt     time.Time
	GeneratedBy   string // version string written in the header
}

// safeName is the character set accepted for names written into the script.
// The values end up inside double-quoted shell strings and JSON, so quotes,
// backslashes, `$` and backticks are refused rather than escaped.
var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// Validate reports the first invalid option.
func (o TokenScriptOptions) Validate() error {
	if !contains(Regions, o.Region) {
		return fmt.Errorf("--region %q: must be one of %s", o.Region, strings.Join(Regions, ", "))
	}
	switch o.Scope {
	case ScopeMesh, ScopeOrg:
	default:
		return fmt.Errorf("--scope %q: must be %q or %q", o.Scope, ScopeMesh, ScopeOrg)
	}
	if o.Scope == ScopeOrg && len(o.ControlPlanes) > 0 {
		return fmt.Errorf("--control-plane only applies to --scope %s; --scope %s already reads every control plane", ScopeMesh, ScopeOrg)
	}
	if !safeName.MatchString(o.AccountName) {
		return fmt.Errorf("--account-name %q: use letters, digits, '.', '_' or '-' (max 100)", o.AccountName)
	}
	for _, cp := range o.ControlPlanes {
		if !safeName.MatchString(cp) {
			return fmt.Errorf("--control-plane %q: use letters, digits, '.', '_' or '-' (max 100)", cp)
		}
	}
	if o.ExpiresAt.IsZero() {
		return fmt.Errorf("expiry is required")
	}
	return nil
}

//go:embed tokenscript.sh.tmpl
var tokenScriptTemplate string

var tokenScriptTmpl = template.Must(template.New("token-script").Parse(tokenScriptTemplate))

// TokenScript renders the script.
func TokenScript(o TokenScriptOptions) (string, error) {
	if err := o.Validate(); err != nil {
		return "", err
	}
	data := struct {
		TokenScriptOptions
		ExpiresAtRFC3339 string
		ScopeMesh        bool
		CPList           string
	}{
		TokenScriptOptions: o,
		ExpiresAtRFC3339:   o.ExpiresAt.UTC().Format(time.RFC3339),
		ScopeMesh:          o.Scope == ScopeMesh,
		CPList:             strings.Join(o.ControlPlanes, " "),
	}
	var buf bytes.Buffer
	if err := tokenScriptTmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

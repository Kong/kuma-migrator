package migrator

import (
	"fmt"
	"regexp"
)

// rfc1035Kinds are resource kinds that require RFC 1035 names (Kuma 2.10+):
// alphanumeric + hyphen, max 63 chars, must start with a letter, end alphanumeric.
var rfc1035Kinds = map[string]bool{
	"Mesh":                   true,
	"MeshService":            true,
	"MeshExternalService":    true,
	"MeshMultizoneService":   true,
}

// rfc1035Re matches a valid RFC 1035 label.
var rfc1035Re = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]{0,61}[a-zA-Z0-9]$|^[a-zA-Z]$`)

// rfc1123Re matches a valid RFC 1123 DNS label (relaxed: may start with digit).
var rfc1123Re = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{0,61}[a-zA-Z0-9]$|^[a-zA-Z0-9]$`)

// ValidateResourceName checks whether name is valid for kind under Kuma 2.10+ rules.
// Returns a non-empty warning string if the name violates the applicable standard.
func ValidateResourceName(name, kind string) string {
	if name == "" {
		return fmt.Sprintf("%s: empty resource name", kind)
	}
	if rfc1035Kinds[kind] {
		if len(name) > 63 {
			return fmt.Sprintf("%s %q: name exceeds 63 characters (RFC 1035, required for this kind in Kuma 2.10+)", kind, name)
		}
		if !rfc1035Re.MatchString(name) {
			return fmt.Sprintf("%s %q: name must start with a letter and contain only alphanumeric characters and hyphens (RFC 1035, required for this kind in Kuma 2.10+)", kind, name)
		}
	} else {
		if len(name) > 63 {
			return fmt.Sprintf("%s %q: name exceeds 63 characters (RFC 1123, required in Kuma 2.10+)", kind, name)
		}
		if !rfc1123Re.MatchString(name) {
			return fmt.Sprintf("%s %q: name must contain only alphanumeric characters and hyphens, and must not start or end with a hyphen (RFC 1123, required in Kuma 2.10+)", kind, name)
		}
	}
	return ""
}

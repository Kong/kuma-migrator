package migrator

import (
	"fmt"
	"regexp"
)

// rfc1035Kinds are resource kinds whose names must be lowercase RFC 1035 labels:
// lowercase alphanumeric + hyphen, max 63 chars, start with a letter, end
// alphanumeric. Deprecated with a warning since Kuma 2.10 and rejected on create or
// update in 3.0, which adds Zone to the list (UPGRADE.md "Names of Mesh, Zone,
// MeshService, MeshExternalService and MeshMultiZoneService must be RFC 1035 labels").
var rfc1035Kinds = map[string]bool{
	"Mesh":                 true,
	"Zone":                 true,
	"MeshService":          true,
	"MeshExternalService":  true,
	"MeshMultiZoneService": true,
}

// rfc1035Re matches a valid lowercase RFC 1035 label, the exact rule 3.0 enforces:
// "consist of lower case alphanumeric characters or '-', start with an alphabetic
// character and end with an alphanumeric character".
var rfc1035Re = regexp.MustCompile(`^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$`)

// rfc1123Re matches a valid RFC 1123 DNS label (relaxed: may start with digit).
var rfc1123Re = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{0,61}[a-zA-Z0-9]$|^[a-zA-Z0-9]$`)

// ValidateResourceName checks whether name is valid for kind under Kuma 2.10+ rules.
// Returns a non-empty warning string if the name violates the applicable standard.
func ValidateResourceName(name, kind string) string {
	if name == "" {
		return fmt.Sprintf("%s: empty resource name", kind)
	}
	if rfc1035Kinds[kind] {
		since := "deprecated for this kind since Kuma 2.10"
		if kind == "Zone" {
			since = "required for Zone from Kuma 3.0"
		}
		if len(name) > 63 {
			return fmt.Sprintf("%s %q: name exceeds 63 characters (lowercase RFC 1035 label, %s)", kind, name, since)
		}
		if !rfc1035Re.MatchString(name) {
			return fmt.Sprintf("%s %q: name must start with a lowercase letter, contain only lowercase alphanumeric characters and hyphens, and end with an alphanumeric character (lowercase RFC 1035 label, %s)", kind, name, since)
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

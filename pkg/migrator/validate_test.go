package migrator

import (
	"strings"
	"testing"
)

func TestValidateResourceName(t *testing.T) {
	tests := []struct {
		name    string
		kind    string
		wantErr bool
		desc    string
	}{
		// RFC 1035 kinds (Mesh, MeshService, MeshExternalService, MeshMultizoneService)
		{"my-mesh", "Mesh", false, "valid RFC1035 name"},
		{"a", "Mesh", false, "single letter valid"},
		{"ab", "Mesh", false, "two char alphanumeric"},
		{"1mesh", "Mesh", true, "RFC1035: must start with letter"},
		{"-mesh", "Mesh", true, "RFC1035: must start with letter not hyphen"},
		{"mesh-", "Mesh", true, "RFC1035: must end alphanumeric"},
		{"mesh_name", "Mesh", true, "RFC1035: underscore not allowed"},
		{strings.Repeat("a", 64), "Mesh", true, "RFC1035: exceeds 63 chars"},
		{strings.Repeat("a", 63), "Mesh", false, "RFC1035: exactly 63 chars OK"},

		// RFC 1123 kinds (everything else)
		{"my-policy", "MeshTimeout", false, "valid RFC1123 name"},
		{"1policy", "MeshTimeout", false, "RFC1123: may start with digit"},
		{"policy-1", "MeshTimeout", false, "RFC1123: may end with digit"},
		{"-policy", "MeshTimeout", true, "RFC1123: must not start with hyphen"},
		{"policy-", "MeshTimeout", true, "RFC1123: must not end with hyphen"},
		{"policy_name", "MeshTimeout", true, "RFC1123: underscore not allowed"},
		{strings.Repeat("a", 64), "MeshTimeout", true, "RFC1123: exceeds 63 chars"},
		{strings.Repeat("a", 63), "MeshTimeout", false, "RFC1123: exactly 63 chars OK"},

		// Edge cases
		{"", "MeshTimeout", true, "empty name"},
		{"valid-mesh-service", "MeshService", false, "valid MeshService name"},
		{"Valid", "MeshTimeout", false, "uppercase is allowed"},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			warn := ValidateResourceName(tc.name, tc.kind)
			if tc.wantErr && warn == "" {
				t.Errorf("expected a validation warning but got none for name=%q kind=%s", tc.name, tc.kind)
			}
			if !tc.wantErr && warn != "" {
				t.Errorf("expected no validation warning but got: %s", warn)
			}
		})
	}
}

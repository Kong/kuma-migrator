package migrator

import (
	"testing"
)

// ---- ParseKumaServiceTag ----

func TestParseKumaServiceTag(t *testing.T) {
	tests := []struct {
		input    string
		wantName string
		wantNS   string
	}{
		// Kubernetes encoded format
		{"backend_demo_svc_3001", "backend", "demo"},
		{"frontend_demo_svc_8080", "frontend", "demo"},
		{"redis_demo_svc_6379", "redis", "demo"},
		// Service name containing underscores
		{"my_service_with_underscores_myns_svc_3000", "my_service_with_underscores", "myns"},
		// Service name containing hyphens (still uses underscore-encoded namespace)
		{"my-service_prod_svc_80", "my-service", "prod"},
		// Universal mode: no _svc_ marker → raw value is the name
		{"my-service", "my-service", ""},
		{"backend", "backend", ""},
		// Wildcards
		{"*", "", ""},
		{"", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			gotName, gotNS := ParseKumaServiceTag(tt.input)
			if gotName != tt.wantName || gotNS != tt.wantNS {
				t.Errorf("ParseKumaServiceTag(%q) = (%q, %q), want (%q, %q)",
					tt.input, gotName, gotNS, tt.wantName, tt.wantNS)
			}
		})
	}
}

// ---- OldTypeToNew ----

func TestOldTypeToNew(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"Timeout", "MeshTimeout", false},
		{"CircuitBreaker", "MeshCircuitBreaker", false},
		{"Retry", "MeshRetry", false},
		{"TrafficPermission", "MeshTrafficPermission", false},
		{"FaultInjection", "MeshFaultInjection", false},
		{"RateLimit", "MeshRateLimit", false},
		{"HealthCheck", "MeshHealthCheck", false},
		{"TrafficLog", "MeshAccessLog", false},
		{"TrafficTrace", "MeshTrace", false},
		{"ProxyTemplate", "MeshProxyPatch", false},
		// Ambiguous — must error
		{"TrafficRoute", "", true},
		// Already migrated — pass through
		{"MeshTimeout", "MeshTimeout", false},
		{"MeshRetry", "MeshRetry", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := OldTypeToNew(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("OldTypeToNew(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("OldTypeToNew(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---- ConvertTargetRef ----

func TestConvertTargetRef(t *testing.T) {
	tests := []struct {
		name            string
		input           TargetRef
		policyNamespace string
		topLevel        bool
		want            TargetRef
		wantWarn        bool // true if a non-empty warning is expected
	}{
		// --- Unchanged kinds ---
		{
			name:  "Mesh kind unchanged",
			input: TargetRef{Kind: "Mesh"},
			want:  TargetRef{Kind: "Mesh"},
		},
		{
			name:  "MeshService kind unchanged (to[]/from[])",
			input: TargetRef{Kind: "MeshService", Name: strPtr("redis"), Namespace: strPtr("demo")},
			want:  TargetRef{Kind: "MeshService", Name: strPtr("redis"), Namespace: strPtr("demo")},
		},

		// --- Wildcard → Mesh (both positions) ---
		{
			name:     "MeshSubset wildcard → Mesh (not top-level)",
			input:    TargetRef{Kind: "MeshSubset", Tags: map[string]string{"kuma.io/service": "*"}},
			topLevel: false,
			want:     TargetRef{Kind: "Mesh"},
		},
		{
			name:     "MeshSubset wildcard → Mesh (top-level)",
			input:    TargetRef{Kind: "MeshSubset", Tags: map[string]string{"kuma.io/service": "*"}},
			topLevel: true,
			want:     TargetRef{Kind: "Mesh"},
		},

		// --- to[]/from[] (topLevel=false) → MeshService ---
		{
			name:     "MeshSubset kuma.io/service K8s format → MeshService with name+namespace",
			input:    TargetRef{Kind: "MeshSubset", Tags: map[string]string{"kuma.io/service": "backend_demo_svc_3001"}},
			topLevel: false,
			want:     TargetRef{Kind: "MeshService", Name: strPtr("backend"), Namespace: strPtr("demo")},
		},
		{
			name: "MeshSubset k8s.kuma.io/service-name → MeshService, namespace from tag",
			input: TargetRef{Kind: "MeshSubset", Tags: map[string]string{
				"k8s.kuma.io/service-name": "redis",
				"k8s.kuma.io/namespace":    "demo",
			}},
			topLevel: false,
			want:     TargetRef{Kind: "MeshService", Name: strPtr("redis"), Namespace: strPtr("demo")},
		},
		{
			name:            "MeshSubset k8s.kuma.io/service-name → namespace from policyNamespace fallback (same ns)",
			input:           TargetRef{Kind: "MeshSubset", Tags: map[string]string{"k8s.kuma.io/service-name": "redis"}},
			policyNamespace: "kong-mesh-system",
			topLevel:        false,
			want:            TargetRef{Kind: "MeshService", Name: strPtr("redis"), Namespace: strPtr("kong-mesh-system")},
		},
		{
			name: "MeshSubset with zone tag → MeshService with labels (to[])",
			input: TargetRef{Kind: "MeshSubset", Tags: map[string]string{
				"kuma.io/service": "backend_demo_svc_3001",
				"kuma.io/zone":    "east",
			}},
			topLevel: false,
			want: TargetRef{Kind: "MeshService", Labels: map[string]string{
				"kuma.io/display-name":  "backend",
				"k8s.kuma.io/namespace": "demo",
				"kuma.io/zone":          "east",
			}},
		},
		{
			name: "MeshSubset with extra refinement tags → MeshService with labels (to[])",
			input: TargetRef{Kind: "MeshSubset", Tags: map[string]string{
				"kuma.io/service": "backend_demo_svc_3001",
				"version":         "v2",
			}},
			topLevel: false,
			want: TargetRef{Kind: "MeshService", Labels: map[string]string{
				"kuma.io/display-name":  "backend",
				"k8s.kuma.io/namespace": "demo",
				"version":               "v2",
			}},
		},
		{
			name: "MeshSubset different namespace → MeshService with labels (to[])",
			input: TargetRef{Kind: "MeshSubset", Tags: map[string]string{
				"kuma.io/service": "redis_demo_svc_6379",
			}},
			policyNamespace: "kong-mesh-system",
			topLevel:        false,
			want: TargetRef{Kind: "MeshService", Labels: map[string]string{
				"kuma.io/display-name":  "redis",
				"k8s.kuma.io/namespace": "demo",
			}},
		},

		// --- spec.targetRef (topLevel=true) → Dataplane ---
		{
			name: "MeshSubset kuma.io/service K8s format → Dataplane with name+namespace (same ns)",
			input: TargetRef{Kind: "MeshSubset", Tags: map[string]string{
				"kuma.io/service": "backend_demo_svc_3001",
			}},
			policyNamespace: "demo",
			topLevel:        true,
			want:            TargetRef{Kind: "Dataplane", Name: strPtr("backend"), Namespace: strPtr("demo")},
		},
		{
			name: "MeshSubset kuma.io/service K8s format → Dataplane with labels (different ns, warns)",
			input: TargetRef{Kind: "MeshSubset", Tags: map[string]string{
				"kuma.io/service": "backend_demo_svc_3001",
			}},
			policyNamespace: "kong-mesh-system",
			topLevel:        true,
			want: TargetRef{Kind: "Dataplane", Labels: map[string]string{
				"app":                   "backend",
				"k8s.kuma.io/namespace": "demo",
			}},
			wantWarn: true,
		},
		{
			name: "MeshSubset with zone tag → Dataplane with labels (top-level, warns)",
			input: TargetRef{Kind: "MeshSubset", Tags: map[string]string{
				"kuma.io/service": "backend_demo_svc_3001",
				"kuma.io/zone":    "east",
			}},
			topLevel: true,
			want: TargetRef{Kind: "Dataplane", Labels: map[string]string{
				"app":                   "backend",
				"k8s.kuma.io/namespace": "demo",
				"kuma.io/zone":          "east",
			}},
			wantWarn: true,
		},

		// --- Workload-only selectors ---
		{
			name:     "MeshSubset with only non-service tags, top-level → Dataplane with labels",
			topLevel: true,
			input: TargetRef{Kind: "MeshSubset", Tags: map[string]string{
				"env":     "prod",
				"version": "v2",
			}},
			want: TargetRef{Kind: "Dataplane", Labels: map[string]string{
				"env":     "prod",
				"version": "v2",
			}},
		},
		{
			name: "MeshSubset with only non-service tags, from[]/to[] → unchanged (deprecation scanner will warn)",
			input: TargetRef{Kind: "MeshSubset", Tags: map[string]string{
				"env":     "prod",
				"version": "v2",
			}},
			want: TargetRef{Kind: "MeshSubset", Tags: map[string]string{
				"env":     "prod",
				"version": "v2",
			}},
		},

		// --- MeshService with old Kuma-internal name (partially migrated input) ---
		{
			name:            "MeshService with old internal name, same namespace → name+namespace",
			input:           TargetRef{Kind: "MeshService", Name: strPtr("echo_demo_svc_8000")},
			policyNamespace: "demo",
			topLevel:        false,
			want:            TargetRef{Kind: "MeshService", Name: strPtr("echo"), Namespace: strPtr("demo")},
		},
		{
			name:            "MeshService with old internal name, different namespace → labels",
			input:           TargetRef{Kind: "MeshService", Name: strPtr("redis_demo_svc_6379")},
			policyNamespace: "kong-mesh-system",
			topLevel:        false,
			want: TargetRef{Kind: "MeshService", Labels: map[string]string{
				"kuma.io/display-name":  "redis",
				"k8s.kuma.io/namespace": "demo",
			}},
		},
		{
			name:     "MeshService with display name (no _svc_) → unchanged",
			input:    TargetRef{Kind: "MeshService", Name: strPtr("echo"), Namespace: strPtr("demo")},
			topLevel: false,
			want:     TargetRef{Kind: "MeshService", Name: strPtr("echo"), Namespace: strPtr("demo")},
		},
		{
			name:            "MeshService with old internal name at top-level, same ns → Dataplane with name+namespace",
			input:           TargetRef{Kind: "MeshService", Name: strPtr("echo_demo_svc_8000")},
			policyNamespace: "demo",
			topLevel:        true,
			want:            TargetRef{Kind: "Dataplane", Name: strPtr("echo"), Namespace: strPtr("demo")},
		},
		{
			name:            "MeshService with old internal name at top-level, different ns → Dataplane with labels (warns)",
			input:           TargetRef{Kind: "MeshService", Name: strPtr("client-app_demo_svc_80")},
			policyNamespace: "kong-mesh-system",
			topLevel:        true,
			want: TargetRef{Kind: "Dataplane", Labels: map[string]string{
				"app":                   "client-app",
				"k8s.kuma.io/namespace": "demo",
			}},
			wantWarn: true,
		},

		// --- Universal mode ---
		{
			name:     "Universal mode kuma.io/service (no _svc_ marker) → MeshService, empty namespace (to[])",
			input:    TargetRef{Kind: "MeshSubset", Tags: map[string]string{"kuma.io/service": "my-backend"}},
			topLevel: false,
			want:     TargetRef{Kind: "MeshService", Name: strPtr("my-backend")},
		},
		{
			name:     "Universal mode kuma.io/service → Dataplane, empty namespace (top-level)",
			input:    TargetRef{Kind: "MeshSubset", Tags: map[string]string{"kuma.io/service": "my-backend"}},
			topLevel: true,
			want:     TargetRef{Kind: "Dataplane", Name: strPtr("my-backend")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, warn := ConvertTargetRef(tt.input, tt.policyNamespace, tt.topLevel)
			if !targetRefEqual(got, tt.want) {
				t.Errorf("ConvertTargetRef() =\n  %s\nwant\n  %s", formatRef(got), formatRef(tt.want))
			}
			if tt.wantWarn && warn == "" {
				t.Errorf("ConvertTargetRef() expected a warning but got none")
			}
			if !tt.wantWarn && warn != "" {
				t.Errorf("ConvertTargetRef() unexpected warning: %s", warn)
			}
		})
	}
}

// ---- helpers ----

func targetRefEqual(a, b TargetRef) bool {
	if a.Kind != b.Kind {
		return false
	}
	if !strPtrEqual(a.Name, b.Name) || !strPtrEqual(a.Namespace, b.Namespace) {
		return false
	}
	if !mapEqual(a.Tags, b.Tags) || !mapEqual(a.Labels, b.Labels) {
		return false
	}
	return true
}

func strPtrEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func mapEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func formatRef(r TargetRef) string {
	name := "<nil>"
	if r.Name != nil {
		name = *r.Name
	}
	ns := "<nil>"
	if r.Namespace != nil {
		ns = *r.Namespace
	}
	return "{Kind:" + r.Kind + " Name:" + name + " Namespace:" + ns + " Tags:" + formatMap(r.Tags) + " Labels:" + formatMap(r.Labels) + "}"
}

func formatMap(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	s := "{"
	for k, v := range m {
		s += k + ":" + v + " "
	}
	return s + "}"
}

package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// DefaultSkipKindsKubernetes is the built-in skip list for Kubernetes deployments.
// Dataplane, ZoneIngress, ZoneEgress, and Workload are CP-managed in Kubernetes
// and are never hand-authored by users.
var DefaultSkipKindsKubernetes = []string{
	"Dataplane",
	"AccessRole",
	"AccessRoleBinding",
	"ZoneEgress",
	"ZoneIngress",
	"Zone",
	"HostnameGenerator",
	"Workload",
	"Secret",
	"GlobalSecret",
}

// DefaultSkipKindsUniversal is the built-in skip list for Universal deployments.
// Dataplane, ZoneIngress, ZoneEgress, and Workload are hand-authored in Universal
// and may carry deprecated fields that the migrator should scan and fix.
var DefaultSkipKindsUniversal = []string{
	"AccessRole",
	"AccessRoleBinding",
	"Zone",
	"HostnameGenerator",
	"Secret",
	"GlobalSecret",
}

// DefaultSkipKinds is an alias for DefaultSkipKindsKubernetes, used when the
// deployment environment is unknown.
var DefaultSkipKinds = DefaultSkipKindsKubernetes

// SkipKindsConfig holds per-environment kind skip lists.
//
// The config file accepts two forms for the skip key:
//
//	New (per-environment):
//	  skip:
//	    kubernetes: [Dataplane, ZoneIngress, ...]
//	    universal:  [AccessRole, ...]
//
//	Legacy (flat list — applies to all environments):
//	  skip: [Dataplane, ZoneIngress, ...]
//
// The legacy form is kept for backward compatibility.
type SkipKindsConfig struct {
	Kubernetes []string
	Universal  []string
}

// UnmarshalJSON implements json.Unmarshaler so that sigs.k8s.io/yaml (which
// converts YAML → JSON before calling encoding/json) can decode both the new
// per-environment map form and the legacy flat-list form.
func (s *SkipKindsConfig) UnmarshalJSON(data []byte) error {
	// sigs.k8s.io/yaml renders a YAML sequence as a JSON array and a YAML
	// mapping as a JSON object, so the first byte unambiguously tells us
	// which form was used in the config file.
	if len(data) > 0 && data[0] == '[' {
		// Legacy: a single flat list that applies to all environments.
		var flat []string
		if err := json.Unmarshal(data, &flat); err != nil {
			return err
		}
		s.Kubernetes = flat
		s.Universal = flat
		return nil
	}
	// New: per-environment structured form.
	type alias struct {
		Kubernetes []string `json:"kubernetes"`
		Universal  []string `json:"universal"`
	}
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	s.Kubernetes = a.Kubernetes
	s.Universal = a.Universal
	return nil
}

// AdminServerConfig holds settings for connecting to the Kuma CP admin server.
type AdminServerConfig struct {
	// TLSSkipVerify disables TLS certificate verification when connecting to
	// the control plane admin server. Use only for self-signed certificates.
	TLSSkipVerify bool `yaml:"tlsSkipVerify"`
}

// Config holds kuma-migrator user configuration.
type Config struct {
	// AdminServer holds connection settings for the Kuma CP admin server.
	AdminServer AdminServerConfig `yaml:"adminServer"`
	// Skip holds per-environment kind skip lists.
	// See SkipKindsConfig for supported YAML forms.
	Skip SkipKindsConfig `yaml:"skip"`
}

// SkipSet returns a skip set for the Kubernetes environment (default when the
// deployment environment is not known, e.g. in the migrate pipeline).
func (c *Config) SkipSet() map[string]bool {
	return c.SkipSetForEnv("")
}

// SkipSetForEnv returns a skip set for the given deployment environment
// ("kubernetes", "universal", or "" for unknown). When the user has provided
// an explicit list for the requested environment in their config file it is
// used. When the list is empty (no config or omitted key), the appropriate
// built-in default is selected:
//
//	"universal"   → DefaultSkipKindsUniversal
//	anything else → DefaultSkipKindsKubernetes
func (c *Config) SkipSetForEnv(env string) map[string]bool {
	var skip []string
	switch env {
	case "universal":
		skip = c.Skip.Universal
		if len(skip) == 0 {
			skip = DefaultSkipKindsUniversal
		}
	default:
		skip = c.Skip.Kubernetes
		if len(skip) == 0 {
			skip = DefaultSkipKindsKubernetes
		}
	}
	s := make(map[string]bool, len(skip))
	for _, k := range skip {
		s[k] = true
	}
	return s
}

// Load reads the config file and returns a Config. The file path is resolved as:
//  1. $KUMA_MIGRATOR_CONFIG environment variable, if set
//  2. ~/.config/kuma-migrator.yaml
//
// If the file does not exist, Load returns an empty Config (built-in defaults
// are applied lazily by SkipSetForEnv).
// Parse errors are returned as an error.
func Load() (*Config, error) {
	path := configFilePath()

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	return &cfg, nil
}

func configFilePath() string {
	if p := os.Getenv("KUMA_MIGRATOR_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "kuma-migrator.yaml")
}

package config

import (
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
}

// DefaultSkipKindsUniversal is the built-in skip list for Universal deployments.
// Dataplane, ZoneIngress, ZoneEgress, and Workload are hand-authored in Universal
// and may carry deprecated fields that the migrator should scan and fix.
var DefaultSkipKindsUniversal = []string{
	"AccessRole",
	"AccessRoleBinding",
	"Zone",
	"HostnameGenerator",
}

// DefaultSkipKinds is an alias for DefaultSkipKindsKubernetes, used when the
// deployment environment is unknown.
var DefaultSkipKinds = DefaultSkipKindsKubernetes

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
	// Skip is the list of resource kinds to exclude from migration.
	// Documents whose kind matches an entry in this list are not transformed
	// and not written to the output directory.
	Skip []string `yaml:"skip"`
}

// SkipSet returns a set (map for O(1) lookup) built from c.Skip,
// using DefaultSkipKinds (Kubernetes) when no explicit list is configured.
func (c *Config) SkipSet() map[string]bool {
	return c.SkipSetForEnv("")
}

// SkipSetForEnv returns a skip set for the given deployment environment
// ("kubernetes", "universal", or "" for unknown). When the user has provided
// an explicit skip list in their config file it is always used. When the list
// is empty (defaults), the appropriate built-in list is selected:
//   - "universal"  → DefaultSkipKindsUniversal
//   - anything else → DefaultSkipKindsKubernetes
func (c *Config) SkipSetForEnv(env string) map[string]bool {
	skip := c.Skip
	if len(skip) == 0 {
		if env == "universal" {
			skip = DefaultSkipKindsUniversal
		} else {
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
// If the file does not exist, Load returns a Config with DefaultSkipKinds.
// Parse errors are returned as an error.
func Load() (*Config, error) {
	path := configFilePath()

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return defaults(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}

	// If the file exists but skip is empty, apply defaults.
	if len(cfg.Skip) == 0 {
		cfg.Skip = DefaultSkipKinds
	}
	return &cfg, nil
}

func defaults() *Config {
	return &Config{Skip: DefaultSkipKinds}
}

func configFilePath() string {
	if p := os.Getenv("KUMA_MIGRATOR_CONFIG"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "kuma-migrator.yaml")
}

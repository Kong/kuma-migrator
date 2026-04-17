package config

import (
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// DefaultSkipKinds is the built-in skip list used when no config file is present
// or when the file does not define a skip list. These are infrastructure/identity
// resources that do not need policy migration.
var DefaultSkipKinds = []string{
	"Dataplane",
	"AccessRole",
	"AccessRoleBinding",
	"ZoneEgress",
	"ZoneIngress",
	"Zone",
	"HostnameGenerator",
	"Workload",
}

// Config holds kuma-migrator user configuration.
type Config struct {
	// Skip is the list of resource kinds to exclude from migration.
	// Documents whose kind matches an entry in this list are not transformed
	// and not written to the output directory.
	Skip []string `yaml:"skip"`
}

// SkipSet returns a set (map for O(1) lookup) built from c.Skip.
func (c *Config) SkipSet() map[string]bool {
	s := make(map[string]bool, len(c.Skip))
	for _, k := range c.Skip {
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

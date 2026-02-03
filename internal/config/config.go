package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config represents the application configuration.
type Config struct {
	// ManifestDirs is a list of directories containing Kubernetes manifests.
	ManifestDirs []string `yaml:"manifest_dirs"`

	// CRISocketPath is the path to the CRI (Container Runtime Interface) socket.
	// e.g., /run/containerd/containerd.sock or /var/run/crio/crio.sock
	CRISocketPath string `yaml:"cri_socket_path"`

	// SyncInterval is the interval for the reconciliation loop (e.g., "10s", "1m").
	// Defaults to 10s if not set.
	SyncInterval string `yaml:"sync_interval"`
}

// Load reads the configuration from the specified file path.
func Load(path string) (*Config, error) {
	if path == "" {
		return nil, fmt.Errorf("config path is empty")
	}

	cleanPath := filepath.Clean(path)
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	// Set defaults
	if cfg.SyncInterval == "" {
		cfg.SyncInterval = "10s"
	}

	return cfg, nil
}

// validate checks required fields.
func (c *Config) validate() error {
	if len(c.ManifestDirs) == 0 {
		return fmt.Errorf("at least one manifest directory must be specified (manifest_dirs)")
	}
	if c.CRISocketPath == "" {
		return fmt.Errorf("cri_socket_path is required")
	}
	return nil
}

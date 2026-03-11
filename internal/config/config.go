package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// NetworkConfig holds CNI and network settings.
// kube-less does not configure networking itself – these values are used for
// startup validation and as reference for the install script.
type NetworkConfig struct {
	// NodeSubnet is the IP subnet allocated to this node (e.g. "10.88.0.0/24").
	// Each node in a multi-node setup must use a unique subnet.
	NodeSubnet string `yaml:"node_subnet"`

	// BridgeName is the Linux bridge interface name (default: kube-less0).
	BridgeName string `yaml:"bridge_name"`

	// CNIConfDir is the directory containing CNI conflist/conf files.
	// Default: /etc/cni/net.d
	CNIConfDir string `yaml:"cni_conf_dir"`

	// CNIBinDir is the directory containing CNI plugin binaries.
	// Default: /opt/cni/bin
	CNIBinDir string `yaml:"cni_bin_dir"`
}

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

	// DebugAPIPort is the port for the debug API server.
	// Defaults to 8080 if not set.
	DebugAPIPort int `yaml:"debug_api_port"`

	// DataDir is the root directory for kube-less runtime data (ConfigMap files, etc.).
	// Default: /var/lib/kube-less
	DataDir string `yaml:"data_dir"`

	// Network holds CNI / networking configuration.
	Network NetworkConfig `yaml:"network"`
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
	if cfg.DebugAPIPort == 0 {
		cfg.DebugAPIPort = 8080
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "/var/lib/kube-less"
	}
	if cfg.Network.BridgeName == "" {
		cfg.Network.BridgeName = "kube-less0"
	}
	if cfg.Network.CNIConfDir == "" {
		cfg.Network.CNIConfDir = "/etc/cni/net.d"
	}
	if cfg.Network.CNIBinDir == "" {
		cfg.Network.CNIBinDir = "/opt/cni/bin"
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

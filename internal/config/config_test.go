package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

const minimalConfig = `
manifest_dirs:
  - /tmp/manifests
cri_socket_path: "unix:///run/containerd/containerd.sock"
`

// ── Load: basic parsing ───────────────────────────────────────────────────────

func TestLoad_ValidMinimalConfig(t *testing.T) {
	path := writeConfig(t, minimalConfig)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CRISocketPath != "unix:///run/containerd/containerd.sock" {
		t.Errorf("unexpected CRISocketPath: %s", cfg.CRISocketPath)
	}
	if len(cfg.ManifestDirs) != 1 || cfg.ManifestDirs[0] != "/tmp/manifests" {
		t.Errorf("unexpected ManifestDirs: %v", cfg.ManifestDirs)
	}
}

func TestLoad_AllFields(t *testing.T) {
	path := writeConfig(t, `
manifest_dirs:
  - /tmp/a
  - /tmp/b
cri_socket_path: "unix:///run/crio/crio.sock"
sync_interval: "30s"
debug_api_port: 9090
data_dir: /data/kube-less
network:
  node_subnet: "10.99.0.0/24"
  bridge_name: "klbridge0"
  cni_conf_dir: "/etc/cni/custom"
  cni_bin_dir: "/usr/lib/cni"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.SyncInterval != "30s" {
		t.Errorf("expected 30s, got %s", cfg.SyncInterval)
	}
	if cfg.DebugAPIPort != 9090 {
		t.Errorf("expected 9090, got %d", cfg.DebugAPIPort)
	}
	if cfg.DataDir != "/data/kube-less" {
		t.Errorf("expected /data/kube-less, got %s", cfg.DataDir)
	}
	if cfg.Network.NodeSubnet != "10.99.0.0/24" {
		t.Errorf("expected 10.99.0.0/24, got %s", cfg.Network.NodeSubnet)
	}
	if cfg.Network.BridgeName != "klbridge0" {
		t.Errorf("expected klbridge0, got %s", cfg.Network.BridgeName)
	}
	if cfg.Network.CNIConfDir != "/etc/cni/custom" {
		t.Errorf("expected /etc/cni/custom, got %s", cfg.Network.CNIConfDir)
	}
	if cfg.Network.CNIBinDir != "/usr/lib/cni" {
		t.Errorf("expected /usr/lib/cni, got %s", cfg.Network.CNIBinDir)
	}
}

// ── Load: defaults ────────────────────────────────────────────────────────────

func TestLoad_Defaults_SyncInterval(t *testing.T) {
	cfg, _ := Load(writeConfig(t, minimalConfig))
	if cfg.SyncInterval != "10s" {
		t.Errorf("expected default 10s, got %s", cfg.SyncInterval)
	}
}

func TestLoad_Defaults_DebugAPIPort(t *testing.T) {
	cfg, _ := Load(writeConfig(t, minimalConfig))
	if cfg.DebugAPIPort != 8080 {
		t.Errorf("expected default 8080, got %d", cfg.DebugAPIPort)
	}
}

func TestLoad_Defaults_DataDir(t *testing.T) {
	cfg, _ := Load(writeConfig(t, minimalConfig))
	if cfg.DataDir != "/var/lib/kube-less" {
		t.Errorf("expected default /var/lib/kube-less, got %s", cfg.DataDir)
	}
}

func TestLoad_Defaults_NetworkConfig(t *testing.T) {
	cfg, _ := Load(writeConfig(t, minimalConfig))
	if cfg.Network.BridgeName != "kube-less0" {
		t.Errorf("expected kube-less0, got %s", cfg.Network.BridgeName)
	}
	if cfg.Network.CNIConfDir != "/etc/cni/net.d" {
		t.Errorf("expected /etc/cni/net.d, got %s", cfg.Network.CNIConfDir)
	}
	if cfg.Network.CNIBinDir != "/opt/cni/bin" {
		t.Errorf("expected /opt/cni/bin, got %s", cfg.Network.CNIBinDir)
	}
}

// ── Load: error paths ─────────────────────────────────────────────────────────

func TestLoad_EmptyPath_ReturnsError(t *testing.T) {
	if _, err := Load(""); err == nil {
		t.Error("expected error for empty path")
	}
}

func TestLoad_MissingFile_ReturnsError(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nonexistent.yaml")); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoad_InvalidYAML_ReturnsError(t *testing.T) {
	path := writeConfig(t, "not: valid: yaml: [}")
	if _, err := Load(path); err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoad_MissingManifestDirs_ReturnsError(t *testing.T) {
	path := writeConfig(t, `cri_socket_path: "unix:///run/containerd/containerd.sock"`)
	if _, err := Load(path); err == nil {
		t.Error("expected validation error for missing manifest_dirs")
	}
}

func TestLoad_MissingCRISocketPath_ReturnsError(t *testing.T) {
	path := writeConfig(t, `manifest_dirs: ["/tmp"]`)
	if _, err := Load(path); err == nil {
		t.Error("expected validation error for missing cri_socket_path")
	}
}

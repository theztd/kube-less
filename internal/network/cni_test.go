package network

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateCNI_Success(t *testing.T) {
	confDir := t.TempDir()
	binDir := t.TempDir()

	// Create a conflist file
	if err := os.WriteFile(filepath.Join(confDir, "10-kube-less.conflist"), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	// Create required binaries
	for _, bin := range requiredBins {
		if err := os.WriteFile(filepath.Join(binDir, bin), []byte(""), 0755); err != nil {
			t.Fatal(err)
		}
	}

	if err := ValidateCNI(confDir, binDir); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateCNI_ConfFile(t *testing.T) {
	confDir := t.TempDir()
	binDir := t.TempDir()

	// .conf (not .conflist) is also valid
	if err := os.WriteFile(filepath.Join(confDir, "10-bridge.conf"), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	for _, bin := range requiredBins {
		if err := os.WriteFile(filepath.Join(binDir, bin), []byte(""), 0755); err != nil {
			t.Fatal(err)
		}
	}

	if err := ValidateCNI(confDir, binDir); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateCNI_MissingConfDir(t *testing.T) {
	err := ValidateCNI("/nonexistent/cni/net.d", t.TempDir())
	if err == nil {
		t.Error("expected error for missing conf dir")
	}
}

func TestValidateCNI_NoConfigFiles(t *testing.T) {
	confDir := t.TempDir()
	binDir := t.TempDir()
	// confDir exists but is empty

	err := ValidateCNI(confDir, binDir)
	if err == nil {
		t.Error("expected error when no conflist/conf files present")
	}
}

func TestValidateCNI_MissingBinDir(t *testing.T) {
	confDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(confDir, "10-kube-less.conflist"), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}

	err := ValidateCNI(confDir, "/nonexistent/cni/bin")
	if err == nil {
		t.Error("expected error for missing bin dir")
	}
}

func TestValidateCNI_MissingBinary(t *testing.T) {
	confDir := t.TempDir()
	binDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(confDir, "10-kube-less.conflist"), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	// Only create "bridge" and "host-local", skip "portmap"
	for _, bin := range []string{"bridge", "host-local"} {
		if err := os.WriteFile(filepath.Join(binDir, bin), []byte(""), 0755); err != nil {
			t.Fatal(err)
		}
	}

	err := ValidateCNI(confDir, binDir)
	if err == nil {
		t.Error("expected error for missing portmap binary")
	}
}

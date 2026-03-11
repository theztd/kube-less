package network

import (
	"fmt"
	"os"
	"path/filepath"
)

// requiredBins is the minimum set of CNI plugin binaries kube-less needs.
var requiredBins = []string{"bridge", "host-local", "portmap"}

// ValidateCNI checks that:
//  1. cni_conf_dir contains at least one *.conflist or *.conf file.
//  2. cni_bin_dir contains the required CNI binaries.
//
// Returns a descriptive error on the first failure so the caller can log and exit.
func ValidateCNI(confDir, binDir string) error {
	if err := validateConfDir(confDir); err != nil {
		return err
	}
	if err := validateBinDir(binDir); err != nil {
		return err
	}
	return nil
}

func validateConfDir(confDir string) error {
	if _, err := os.Stat(confDir); err != nil {
		return fmt.Errorf("CNI: config directory %q not found: %w", confDir, err)
	}

	conflists, _ := filepath.Glob(filepath.Join(confDir, "*.conflist"))
	confs, _ := filepath.Glob(filepath.Join(confDir, "*.conf"))
	if len(conflists)+len(confs) == 0 {
		return fmt.Errorf("CNI: no *.conflist or *.conf found in %q – create a CNI config before starting kube-less", confDir)
	}
	return nil
}

func validateBinDir(binDir string) error {
	if _, err := os.Stat(binDir); err != nil {
		return fmt.Errorf("CNI: binary directory %q not found: %w", binDir, err)
	}

	for _, bin := range requiredBins {
		path := filepath.Join(binDir, bin)
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("CNI: required binary %q not found in %q – install containernetworking-plugins", bin, binDir)
		}
	}
	return nil
}

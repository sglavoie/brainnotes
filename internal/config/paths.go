package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// expandUser expands a leading "~" or "~/" in p to the user's home directory.
// A bare "~" expands to the home directory; "~/foo" expands to "<home>/foo".
// "~user" style paths are not supported and are returned unchanged.
// An empty path is returned unchanged.
func expandUser(p string) (string, error) {
	if p == "" {
		return p, nil
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~"+string(os.PathSeparator)) {
		home, err := os.UserHomeDir()
		if err != nil {
			return p, err
		}
		if p == "~" {
			return home, nil
		}
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}

// resolveVaultRoot applies the vault precedence chain and returns the absolute
// vault root plus the mechanism that produced it.
func resolveVaultRoot(opts LoadOptions) (string, VaultOrigin, error) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	if v := strings.TrimSpace(opts.VaultFlag); v != "" {
		root, err := explicitVaultRoot(v, "--vault")
		return root, VaultOriginFlag, err
	}
	if v := strings.TrimSpace(getenv(envVault)); v != "" {
		root, err := explicitVaultRoot(v, "$"+envVault)
		return root, VaultOriginEnv, err
	}

	start := strings.TrimSpace(opts.WorkingDir)
	if start == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", VaultOriginDiscovery, fmt.Errorf("config: resolve working directory: %w", err)
		}
		start = wd
	}
	root, err := discoverVaultRoot(start)
	return root, VaultOriginDiscovery, err
}

// explicitVaultRoot validates a vault path named directly by the user. The
// directory must exist and contain FileName — an explicit path that is not a
// vault root is a mistake worth reporting rather than searching past.
func explicitVaultRoot(raw, label string) (string, error) {
	expanded, err := expandUser(raw)
	if err != nil {
		return "", fmt.Errorf("config: %s %q: %w", label, raw, err)
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("config: %s %q: %w", label, raw, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("config: %s %q: %w", label, abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("config: %s %q is not a directory", label, abs)
	}

	path := filepath.Join(abs, FileName)
	markerInfo, err := os.Stat(path)
	switch {
	case os.IsNotExist(err):
		return "", fmt.Errorf("config: %s %q: no %s found (run `bn init` there)", label, abs, FileName)
	case err != nil:
		return "", fmt.Errorf("config: stat %s: %w", path, err)
	case markerInfo.IsDir():
		return "", fmt.Errorf("config: %s is a directory, expected a TOML file", path)
	}
	return abs, nil
}

// discoverVaultRoot walks upward from start until it finds a directory holding
// FileName.
func discoverVaultRoot(start string) (string, error) {
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("config: resolve %q: %w", start, err)
	}

	for dir := filepath.Clean(abs); ; {
		path := filepath.Join(dir, FileName)
		info, statErr := os.Stat(path)
		switch {
		case statErr == nil && !info.IsDir():
			return dir, nil
		case statErr != nil && !os.IsNotExist(statErr):
			return "", fmt.Errorf("config: stat %s: %w", path, statErr)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf(
				"config: no %s found in %s or any parent directory (run `bn init` in your vault root)",
				FileName, abs)
		}
		dir = parent
	}
}

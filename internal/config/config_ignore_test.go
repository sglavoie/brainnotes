package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_IgnoreMergesUserAndDefaults(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\nignore = [\"Drafts\", \".git\"]\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !contains(cfg.Ignore, "Drafts") {
		t.Errorf("user ignore missing: %v", cfg.Ignore)
	}
	count := 0
	for _, p := range cfg.Ignore {
		if p == ".git" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected .git to appear once after merge, got %d (%v)", count, cfg.Ignore)
	}
}

func TestLoad_IgnoreNormalizesRepeatedDotSlashPrefix(t *testing.T) {
	// "././Archive" should collapse to "Archive" and dedup with the default.
	// Before the fix, normalizeIgnore only stripped a single "./" prefix,
	// leaving "././Archive" as "./Archive" which did not dedup with "Archive".
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\nignore = [\"././Archive\", \"./././Drafts\"]\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	archiveCount := 0
	for _, p := range cfg.Ignore {
		if p == "Archive" {
			archiveCount++
		}
	}
	if archiveCount != 1 {
		t.Fatalf("expected Archive once after ././ normalization, got %d (%v)", archiveCount, cfg.Ignore)
	}
	if !contains(cfg.Ignore, "Drafts") {
		t.Fatalf("expected Drafts after ./././ normalization: %v", cfg.Ignore)
	}
}

func TestLoad_IgnoreNormalizesInteriorDotSegments(t *testing.T) {
	// "sub/./dir" should clean to "sub/dir" via path.Clean.
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\nignore = [\"sub/./dir\", \"sub/dir\"]\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	count := 0
	for _, p := range cfg.Ignore {
		if p == "sub/dir" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected sub/dir once after path.Clean dedup, got %d (%v)", count, cfg.Ignore)
	}
}

func TestLoad_IgnoreNormalizesBackslashes(t *testing.T) {
	// Backslashes in config should be treated as path separators, so a Windows
	// style ignore entry still matches slash-separated Walk paths on Unix.
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\nignore = ['.\\Archive', 'Drafts\\Old']\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	archiveCount := 0
	for _, p := range cfg.Ignore {
		if p == "Archive" {
			archiveCount++
		}
		if strings.Contains(p, "\\") {
			t.Errorf("ignore entry should not contain backslash after normalization: %q", p)
		}
	}
	if archiveCount != 1 {
		t.Fatalf("expected Archive once after backslash normalization, got %d (%v)",
			archiveCount, cfg.Ignore)
	}
	if !contains(cfg.Ignore, "Drafts/Old") {
		t.Fatalf("expected Drafts/Old after backslash normalization: %v", cfg.Ignore)
	}
}

func TestLoad_IgnoreDropsAbsoluteEntries(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	for _, raw := range []string{"/abs/Archive", `/rooted`, `C:\Vault\Archive`, "D:/Vault/Drafts"} {
		t.Run(raw, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config.toml")
			writeFile(t, cfgPath, "vault = \""+vault+"\"\nignore = ['"+raw+"', 'Drafts']\n")

			cfg, err := Load("", cfgPath)
			if err != nil {
				t.Fatalf("Load (%q): %v", raw, err)
			}
			for _, p := range cfg.Ignore {
				if isAbsolutePathLike(p) {
					t.Fatalf("absolute ignore entry should be dropped, got %v", cfg.Ignore)
				}
			}
			if !contains(cfg.Ignore, "Drafts") {
				t.Fatalf("valid ignore entry should remain after dropping absolute path: %v", cfg.Ignore)
			}
		})
	}
}

func TestLoad_IgnoreDropsParentTraversal(t *testing.T) {
	resetEnv(t)
	vault := t.TempDir()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	writeFile(t, cfgPath, "vault = \""+vault+"\"\nignore = ['../Archive', '..\\\\Drafts', 'Notes']\n")

	cfg, err := Load("", cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, p := range cfg.Ignore {
		if p == "../Archive" || p == "../Drafts" || p == "..\\Drafts" {
			t.Fatalf("parent-traversal ignore entry should be dropped, got %v", cfg.Ignore)
		}
	}
	if !contains(cfg.Ignore, "Notes") {
		t.Fatalf("valid ignore entry should remain after dropping traversal: %v", cfg.Ignore)
	}
}

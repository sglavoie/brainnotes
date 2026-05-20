// Package config loads the bnotes TOML configuration with flag/env/file precedence.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config mirrors the on-disk TOML schema. Zero-value fields are filled in with
// defaults by Load.
type Config struct {
	// Vault is the absolute path to the vault root.
	Vault string `toml:"vault"`

	// Ignore lists additional vault-relative paths to skip during Walk.
	// Hard-coded defaults are merged in by Load (see DefaultIgnore).
	Ignore []string `toml:"ignore"`

	Templates   TemplatesConfig   `toml:"templates"`
	Frontmatter FrontmatterConfig `toml:"frontmatter"`
	Fzf         FzfConfig         `toml:"fzf"`
	New         NewConfig         `toml:"new"`
}

// TemplatesConfig configures where templates live and how they are extended.
type TemplatesConfig struct {
	// Dir is vault-relative.
	Dir              string `toml:"dir"`
	DefaultExtension string `toml:"default_extension"`
}

// FrontmatterConfig configures property handling.
type FrontmatterConfig struct {
	WikilinkProperties []string `toml:"wikilink_properties"`
	ListProperties     []string `toml:"list_properties"`
}

// FzfConfig configures the fzf interactive shell-out.
type FzfConfig struct {
	Args []string `toml:"args"`
}

// NewConfig configures the `new` command.
type NewConfig struct {
	// Routes maps a template name to a vault-relative folder.
	Routes map[string]string `toml:"routes"`
}

// DefaultIgnore lists the vault-relative paths that are always skipped.
var DefaultIgnore = []string{".git", ".obsidian", "Attachments", "Archive"}

const (
	envVault   = "BNOTES_VAULT"
	envConfig  = "BNOTES_CONFIG"
	envXDG     = "XDG_CONFIG_HOME"
	configName = "bnotes/config.toml"
)

// Load resolves the configuration following the documented precedence.
//
// Vault path precedence: flagVaultPath > $BNOTES_VAULT > config-file `vault` key.
// Config-file path precedence: flagConfigPath > $BNOTES_CONFIG > $XDG_CONFIG_HOME/bnotes/config.toml > ~/.config/bnotes/config.toml.
//
// A missing config file is not an error provided the vault path can be resolved
// from a flag or environment variable. The returned Config has defaults applied
// for any unset fields, and the vault path is validated to exist and be a directory.
func Load(flagVaultPath, flagConfigPath string) (*Config, error) {
	cfg := &Config{}

	configPath, explicit, err := resolveConfigPath(flagConfigPath)
	if err != nil {
		return nil, err
	}

	if configPath != "" {
		if _, statErr := os.Stat(configPath); statErr == nil {
			if _, decErr := toml.DecodeFile(configPath, cfg); decErr != nil {
				return nil, fmt.Errorf("config: parse %s: %w", configPath, decErr)
			}
		} else if !os.IsNotExist(statErr) {
			return nil, fmt.Errorf("config: stat %s: %w", configPath, statErr)
		} else if explicit {
			return nil, fmt.Errorf("config: file %s does not exist", configPath)
		}
	}

	if flagVaultPath != "" {
		cfg.Vault = flagVaultPath
	} else if env := os.Getenv(envVault); env != "" {
		cfg.Vault = env
	}

	applyDefaults(cfg)

	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// ConfigSource identifies which mechanism contributed a candidate path.
type ConfigSource int

const (
	// SourceFlag is the --config CLI flag.
	SourceFlag ConfigSource = iota
	// SourceEnv is $BNOTES_CONFIG.
	SourceEnv
	// SourceXDG is $XDG_CONFIG_HOME/bnotes/config.toml.
	SourceXDG
	// SourceDefault is ~/.config/bnotes/config.toml.
	SourceDefault
)

// CandidatePath is one entry in the resolution chain.
type CandidatePath struct {
	Source ConfigSource
	Label  string // human-readable source label
	Path   string // empty when the source is not set
	Exists bool   // true only if Path != "" and the file exists
	Active bool   // true on the first entry with a non-empty Path
}

// Candidates returns the full precedence chain (4 entries, in source order).
// An error is only returned when no other source is set and os.UserHomeDir fails.
func Candidates(flagConfigPath string) ([]CandidatePath, error) {
	out := []CandidatePath{
		{Source: SourceFlag, Label: "--config flag", Path: flagConfigPath},
		{Source: SourceEnv, Label: "$BNOTES_CONFIG", Path: os.Getenv(envConfig)},
		{Source: SourceXDG, Label: "$XDG_CONFIG_HOME"},
		{Source: SourceDefault, Label: "default path"},
	}

	if xdg := os.Getenv(envXDG); xdg != "" {
		out[2].Path = filepath.Join(xdg, configName)
	}

	anySet := out[0].Path != "" || out[1].Path != "" || out[2].Path != ""
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		if !anySet {
			return nil, fmt.Errorf("config: resolve home dir: %w", homeErr)
		}
	} else {
		out[3].Path = filepath.Join(home, ".config", configName)
	}

	activeSet := false
	for i := range out {
		if out[i].Path == "" {
			continue
		}
		if !activeSet {
			out[i].Active = true
			activeSet = true
		}
		if info, err := os.Stat(out[i].Path); err == nil && !info.IsDir() {
			out[i].Exists = true
		}
	}

	return out, nil
}

// ResolvePath returns the active config path Load would consult. ok is false
// when no candidate produced a non-empty path. The returned path may not exist.
func ResolvePath(flagConfigPath string) (path string, ok bool, err error) {
	cands, err := Candidates(flagConfigPath)
	if err != nil {
		return "", false, err
	}
	for _, c := range cands {
		if c.Active {
			return c.Path, true, nil
		}
	}
	return "", false, nil
}

// resolveConfigPath returns the chosen config path and whether it was set explicitly
// (via flag or BNOTES_CONFIG). For explicit sources, a missing file is an error;
// for the default search paths, a missing file is fine.
func resolveConfigPath(flagConfigPath string) (path string, explicit bool, err error) {
	cands, err := Candidates(flagConfigPath)
	if err != nil {
		return "", false, err
	}
	for _, c := range cands {
		if !c.Active {
			continue
		}
		return c.Path, c.Source == SourceFlag || c.Source == SourceEnv, nil
	}
	return "", false, nil
}

// DefaultTemplate returns the commented TOML starter file body written when
// `bn config edit` is invoked with no existing config file.
func DefaultTemplate() string {
	return `# bnotes configuration file
# All keys are optional except ` + "`vault`" + `.

# Absolute path to your vault root. Required for most commands.
# vault = "/Users/you/vault"

# Extra vault-relative paths to skip when walking notes. These are MERGED
# with the built-in defaults: .git, .obsidian, Attachments, Archive.
# The configured [templates].dir (default "Templates") is also skipped
# automatically.
# ignore = ["Trash", "Drafts"]

[templates]
# Directory where note templates live (vault-relative).
# dir = "Templates"
# default_extension = ".md"

[frontmatter]
# Frontmatter property keys whose values are stored as [[wikilinks]].
# wikilink_properties = ["categories", "org", "parent", "area"]
# Property keys whose values are comma-separated lists.
# list_properties = ["categories", "tags", "topics", "org", "type"]

[fzf]
# Additional flags passed to fzf when --interactive is used.
# args = ["--height=40%", "--reverse"]

# Default folders for ` + "`bn new <template>`" + `. Keys are template names,
# values are vault-relative folders.
# [new.routes]
# book = "References/Books"
# person = "People"
`
}

func applyDefaults(cfg *Config) {
	if cfg.Templates.Dir == "" {
		cfg.Templates.Dir = "Templates"
	}
	if cfg.Templates.DefaultExtension == "" {
		cfg.Templates.DefaultExtension = ".md"
	}
	if !strings.HasPrefix(cfg.Templates.DefaultExtension, ".") {
		cfg.Templates.DefaultExtension = "." + cfg.Templates.DefaultExtension
	}
	if cfg.Frontmatter.WikilinkProperties == nil {
		cfg.Frontmatter.WikilinkProperties = []string{"categories", "org", "parent", "area"}
	}
	if cfg.Frontmatter.ListProperties == nil {
		cfg.Frontmatter.ListProperties = []string{"categories", "tags", "topics", "org", "type"}
	}
	if cfg.New.Routes == nil {
		cfg.New.Routes = map[string]string{}
	}

	cfg.Ignore = mergeIgnore(DefaultIgnore, cfg.Ignore)
}

func mergeIgnore(defaults, extra []string) []string {
	seen := make(map[string]struct{}, len(defaults)+len(extra))
	out := make([]string, 0, len(defaults)+len(extra))
	for _, p := range defaults {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	for _, p := range extra {
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func validate(cfg *Config) error {
	if cfg.Vault == "" {
		return errors.New("config: vault path is not set (use --vault, BNOTES_VAULT, or 'vault' in config.toml)")
	}
	info, err := os.Stat(cfg.Vault)
	if err != nil {
		return fmt.Errorf("config: vault path %q: %w", cfg.Vault, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("config: vault path %q is not a directory", cfg.Vault)
	}
	return nil
}

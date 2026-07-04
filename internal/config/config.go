// Package config loads the bnotes TOML configuration with flag/env/file precedence.
package config

import (
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
	Open        OpenConfig        `toml:"open"`
	Serve       ServeConfig       `toml:"serve"`
	New         NewConfig         `toml:"new"`
	Cockpit     CockpitConfig     `toml:"cockpit"`

	// SourcePath is the resolved path of the config file that was loaded.
	SourcePath string `toml:"-"`
}

// TemplatesConfig configures where templates live and how they are extended.
type TemplatesConfig struct {
	// Dir is vault-relative.
	Dir              string `toml:"dir"`
	DefaultExtension string `toml:"default_extension"`
}

// PropertySchema defines validation rules for a single frontmatter property.
type PropertySchema struct {
	Type     string   `toml:"type"`
	Required bool     `toml:"required"`
	Allowed  []string `toml:"allowed"`
}

// FrontmatterConfig configures property handling.
type FrontmatterConfig struct {
	WikilinkProperties []string                  `toml:"wikilink_properties"`
	ListProperties     []string                  `toml:"list_properties"`
	Schema             map[string]PropertySchema `toml:"schema"`
}

// FzfConfig configures the fzf interactive shell-out.
type FzfConfig struct {
	Args []string `toml:"args"`
}

// OpenConfig configures the `open` command.
type OpenConfig struct {
	// Editor is the editor binary (or absolute path) to invoke when opening a
	// note. When empty, bn falls back to $EDITOR.
	Editor string `toml:"editor"`
}

type ServeConfig struct {
	Addr        string `toml:"addr"`
	OpenBrowser *bool  `toml:"open_browser"`
}

func (s ServeConfig) BrowserEnabled() bool {
	return s.OpenBrowser == nil || *s.OpenBrowser
}

// NewConfig configures the `new` command.
type NewConfig struct {
	// Routes maps a template name to a vault-relative folder.
	Routes map[string]string `toml:"routes"`
}

// CockpitConfig tunes the Daily Fix Cockpit (the read-only hygiene queue served
// by `bn doctor` and the web cockpit). Rule identifiers in DisabledRules and
// Priority are the stable RuleKind strings owned by internal/cockpit; they are
// not validated for membership here so config stays decoupled from that
// package — unknown names are simply ignored by the cockpit at wiring time.
type CockpitConfig struct {
	// DisabledRules lists rule kinds to suppress from the queue entirely.
	DisabledRules []string `toml:"disabled_rules"`
	// Priority lists rule kinds in the order they should surface, overriding the
	// cockpit's built-in ordering for the named kinds.
	Priority []string `toml:"priority"`
	// LongTailLimit caps how many of the rarest values per frontmatter property
	// become long-tail tasks. Defaults to DefaultCockpitLongTailLimit.
	LongTailLimit int `toml:"long_tail_limit"`
}

// DefaultIgnore lists the vault-relative paths that are always skipped.
var DefaultIgnore = []string{".git", ".obsidian", "Attachments", "Archive", "Trash"}

// DefaultJournalDir is the vault-relative directory reserved for undo journal
// entries.
const DefaultJournalDir = ".bnotes/journal"

const DefaultServeAddr = "127.0.0.1:8729"

// DefaultCockpitLongTailLimit is the default cap on how many of the rarest
// values per frontmatter property become long-tail cockpit tasks.
const DefaultCockpitLongTailLimit = 5

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
//
// Leading `~` or `~/` in vault and config paths (from flag, environment, or the
// `vault` key in the config file) is expanded to the user's home directory.
func Load(flagVaultPath, flagConfigPath string) (*Config, error) {
	cfg := &Config{}

	configPath, explicit, err := resolveConfigPath(flagConfigPath)
	if err != nil {
		return nil, err
	}

	if configPath != "" {
		info, statErr := os.Stat(configPath)
		switch {
		case statErr == nil && info.IsDir():
			return nil, fmt.Errorf("config: %s is a directory, expected a TOML file", configPath)
		case statErr == nil:
			meta, decErr := toml.DecodeFile(configPath, cfg)
			if decErr != nil {
				return nil, fmt.Errorf("config: parse %s: %w", configPath, decErr)
			}
			if undecoded := meta.Undecoded(); len(undecoded) > 0 {
				keys := make([]string, len(undecoded))
				for i, k := range undecoded {
					keys[i] = k.String()
				}
				return nil, fmt.Errorf("config: %s: unknown keys: %s", configPath, strings.Join(keys, ", "))
			}
			cfg.SourcePath = configPath
		case !os.IsNotExist(statErr):
			return nil, fmt.Errorf("config: stat %s: %w", configPath, statErr)
		case explicit:
			return nil, fmt.Errorf("config: file %s does not exist", configPath)
		}
	}

	if err := validateTemplatesDir(cfg.Templates.Dir); err != nil {
		return nil, err
	}

	if v := strings.TrimSpace(flagVaultPath); v != "" {
		cfg.Vault = v
	} else if v := strings.TrimSpace(os.Getenv(envVault)); v != "" {
		cfg.Vault = v
	}

	cfg.Vault = strings.TrimSpace(cfg.Vault)

	if expanded, expErr := expandUser(cfg.Vault); expErr != nil {
		return nil, fmt.Errorf("config: vault path %q: %w", cfg.Vault, expErr)
	} else {
		cfg.Vault = expanded
	}

	if cfg.Vault != "" {
		abs, absErr := filepath.Abs(cfg.Vault)
		if absErr != nil {
			return nil, fmt.Errorf("config: resolve vault path %q: %w", cfg.Vault, absErr)
		}
		cfg.Vault = abs
	}

	applyDefaults(cfg)

	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

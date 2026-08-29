// Package config loads the vault-local brainnotes.toml configuration.
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
	// Vault is the absolute path to the vault root. It is resolved at load
	// time and never read from or written to the TOML file.
	Vault string `toml:"-"`

	// Ignore lists additional vault-relative paths to skip during Walk.
	// Hard-coded defaults are merged in by Load (see DefaultIgnore).
	Ignore []string `toml:"ignore"`

	Templates   TemplatesConfig   `toml:"templates"`
	Frontmatter FrontmatterConfig `toml:"frontmatter"`
	Fzf         FzfConfig         `toml:"fzf"`
	Open        OpenConfig        `toml:"open"`
	Serve       ServeConfig       `toml:"serve"`
	New         NewConfig         `toml:"new"`
	Related     RelatedConfig     `toml:"related"`
	Brief       BriefConfig       `toml:"brief"`
	Capture     CaptureConfig     `toml:"capture"`
	Agent       AgentConfig       `toml:"agent"`
	Cockpit     CockpitConfig     `toml:"cockpit"`
	Autosave    AutosaveConfig    `toml:"autosave"`

	// Views holds the saved queries declared by the [[views]] array of tables,
	// in file order.
	Views []SavedView `toml:"views"`

	// SourcePath is the absolute path of the brainnotes.toml that was loaded.
	SourcePath string `toml:"-"`

	// VaultOrigin records which mechanism resolved the vault root.
	VaultOrigin VaultOrigin `toml:"-"`
}

// legacyPropertyKey maps an undecoded TOML key back to the removed split
// frontmatter model so Load can point at the replacement instead of reporting
// an anonymous unknown key.
func legacyPropertyKey(key string) string {
	switch {
	case key == "frontmatter.list_properties", key == "frontmatter.wikilink_properties":
		return key
	case key == "frontmatter.schema" || strings.HasPrefix(key, "frontmatter.schema."):
		return "frontmatter.schema"
	}
	return ""
}

// TemplatesConfig configures where templates live and how they are extended.
type TemplatesConfig struct {
	// Dir is vault-relative.
	Dir              string `toml:"dir"`
	DefaultExtension string `toml:"default_extension"`
}

// PropertyKind names the value type of a frontmatter property.
type PropertyKind string

const (
	KindString   PropertyKind = "string"
	KindWikilink PropertyKind = "wikilink"
	KindDate     PropertyKind = "date"
	KindDatetime PropertyKind = "datetime"
	KindInt      PropertyKind = "int"
	KindFloat    PropertyKind = "float"
	KindBool     PropertyKind = "bool"
	KindList     PropertyKind = "list"
)

// PropertyDefinition is the single, unified definition of one frontmatter
// property. Items is only meaningful when Type is KindList, where it names the
// scalar kind every element must have.
type PropertyDefinition struct {
	Type     PropertyKind `toml:"type"`
	Items    PropertyKind `toml:"items"`
	Required bool         `toml:"required"`
	Allowed  []string     `toml:"allowed"`
}

// FrontmatterConfig configures property handling. Properties is the only
// source of list, wikilink, and validation semantics; there are no implicit
// runtime defaults.
type FrontmatterConfig struct {
	Properties map[string]PropertyDefinition `toml:"properties"`
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

// RelatedConfig configures the read-only contextual-recall engine.
type RelatedConfig struct {
	// MetadataProperties lists frontmatter properties whose values contribute
	// to metadata similarity. An explicitly empty list disables that signal.
	MetadataProperties []string `toml:"metadata_properties"`
}

// BriefConfig tunes Daily Brief candidate exclusions.
type BriefConfig struct {
	// ExcludeFolders lists vault-relative folder prefixes excluded from brief
	// recommendations.
	ExcludeFolders []string `toml:"exclude_folders"`
	// ExcludeTypes lists normalized frontmatter type values excluded from brief
	// recommendations.
	ExcludeTypes []string `toml:"exclude_types"`
}

// CaptureConfig configures `bn capture` and the web quick-capture box.
type CaptureConfig struct {
	// InboxNote is the vault-relative note captures are appended to.
	InboxNote string `toml:"inbox_note"`
	// Heading is the ATX heading captures are appended under. It is created at
	// the end of the note when absent.
	Heading string `toml:"heading"`
}

// AgentConfig configures explicit local agent execution. Command is an argv
// array: its first element is the executable and the rest are passed as literal
// arguments without shell interpretation.
type AgentConfig struct {
	Command        []string `toml:"command"`
	TimeoutSeconds int      `toml:"timeout_seconds"`
}

// CockpitConfig tunes the Daily Fix Cockpit (the read-only hygiene queue served
// by `bn doctor` and the web cockpit). Rule identifiers in DisabledRules and
// Priority are the stable RuleKind strings owned by internal/cockpit; they are
// not validated for membership here so config stays decoupled from that
// package — unknown names are simply ignored while the cockpit builds the queue.
type CockpitConfig struct {
	// DisabledRules lists rule kinds to suppress from the queue entirely.
	DisabledRules []string `toml:"disabled_rules"`
	// Priority lists rule kinds in the order they should surface, overriding the
	// cockpit's built-in ordering for the named kinds.
	Priority []string `toml:"priority"`
	// LongTailLimit caps how many of the rarest values per frontmatter property
	// become long-tail tasks. Defaults to DefaultCockpitLongTailLimit.
	LongTailLimit int `toml:"long_tail_limit"`
	// LongTailMaxCount is the highest per-value count that still counts as rare;
	// a value is only eligible when its count is at or below this threshold.
	// Defaults to DefaultCockpitLongTailMaxCount.
	LongTailMaxCount int `toml:"long_tail_max_count"`
	// LongTailHeadFactor requires a dominant sibling value before flagging: a
	// rare value surfaces only when the property's most common value is at least
	// this many times as frequent. Defaults to DefaultCockpitLongTailHeadFactor.
	LongTailHeadFactor int `toml:"long_tail_head_factor"`
	// LongTailIgnoreProperties lists frontmatter property names the long-tail rule
	// never flags — open-vocabulary / naturally-unique fields (created, tags,
	// ingredients, …) where a long tail is expected rather than a typo. Matched
	// case-sensitively against the frontmatter key.
	LongTailIgnoreProperties []string `toml:"long_tail_ignore_properties"`

	// DailyLimit caps how many tasks the daily cockpit view surfaces at once.
	// Defaults to DefaultCockpitDailyLimit.
	DailyLimit int `toml:"daily_limit"`
	// ReviewDateProperty names the frontmatter property that records when a note
	// was last reviewed; the missing-review-date rule checks this key. Defaults
	// to DefaultCockpitReviewDateProperty.
	ReviewDateProperty string `toml:"review_date_property"`
	// ReviewStatuses lists status values whose notes are expected to carry a
	// review date. Empty disables the missing-review-date rule.
	ReviewStatuses []string `toml:"review_statuses"`
	// WIPStatuses lists status values counted as work-in-progress for the WIP
	// limit rule. Empty disables the wip-limit rule.
	WIPStatuses []string `toml:"wip_statuses"`
	// MaxWIP is the highest number of work-in-progress notes allowed before the
	// wip-limit rule flags an overflow. Defaults to DefaultCockpitMaxWIP.
	MaxWIP int `toml:"max_wip"`
	// StaleStatusDays is the age in days after which a note's status is treated
	// as stale by the stale-status rule. Defaults to DefaultCockpitStaleStatusDays.
	StaleStatusDays int `toml:"stale_status_days"`
	// MaxCaptures is how many unprocessed captures may sit in the capture inbox
	// before the capture-backlog rule fires. Zero disables the rule.
	MaxCaptures int `toml:"max_captures"`
	// HubTypeValues lists frontmatter type values that identify a hub/MOC note
	// for the hub-needs-synthesis rule. Empty disables that rule.
	HubTypeValues []string `toml:"hub_type_values"`
	// HubEmptyHeadings lists heading titles that, when present but empty in a hub
	// note, mark it as needing synthesis. Empty disables the heading check.
	HubEmptyHeadings []string `toml:"hub_empty_headings"`
	// SensitiveScan enables the sensitive-content scanner. Off by default so the
	// cockpit never inspects note bodies for secrets unless explicitly asked.
	SensitiveScan bool `toml:"sensitive_scan"`
	// SensitiveIgnorePaths lists vault-relative path prefixes the sensitive-content
	// scanner skips entirely.
	SensitiveIgnorePaths []string `toml:"sensitive_ignore_paths"`
	// SensitiveIgnorePatterns lists regular-expression patterns whose matches the
	// sensitive-content scanner treats as allowed (never flagged).
	SensitiveIgnorePatterns []string `toml:"sensitive_ignore_patterns"`
	// AgentCommand is the local shell command invoked for the optional AI daily
	// review. Empty disables AI integration (the default).
	AgentCommand string `toml:"agent_command"`
	// AgentTimeoutSeconds bounds how long the AI agent command may run. Defaults
	// to DefaultCockpitAgentTimeoutSeconds.
	AgentTimeoutSeconds int `toml:"agent_timeout_seconds"`
}

// AutosaveConfig bounds how often the external file watcher may create its own
// safety commits. It never affects explicit `bn`, web, or sidecar mutations,
// which commit immediately whatever this says.
type AutosaveConfig struct {
	// MinInterval is the hard floor between two consecutive external autosave
	// commits: a Go duration between MinAutosaveInterval and MaxAutosaveInterval
	// inclusive, or AutosaveOff to stop the watcher committing on its own. It is
	// stored as the human-readable text the file carries (canonicalized by
	// applyDefaults, so "300s" is read back as "5m"); callers wanting the
	// resolved disabled/duration pair use Config.AutosavePolicy.
	MinInterval string `toml:"min_interval"`
}

// DefaultIgnore lists the vault-relative paths that are always skipped.
var DefaultIgnore = []string{".git", ".obsidian", "Attachments", "Archive", "Trash"}

// DefaultRelatedMetadataProperties are the frontmatter properties considered
// by contextual-recall metadata scoring when [related] is not configured.
var DefaultRelatedMetadataProperties = []string{
	"categories",
	"tags",
	"topics",
	"type",
	"parent",
	"area",
}

// DefaultJournalDir is the vault-relative directory reserved for undo journal
// entries.
const DefaultJournalDir = ".bnotes/journal"

const DefaultServeAddr = "127.0.0.1:8729"

// DefaultCockpitLongTailLimit is the default cap on how many of the rarest
// values per frontmatter property become long-tail cockpit tasks.
const DefaultCockpitLongTailLimit = 5

// DefaultCockpitLongTailMaxCount is the default highest per-value count that
// still counts as rare for the long-tail rule.
const DefaultCockpitLongTailMaxCount = 2

// DefaultCockpitLongTailHeadFactor is the default multiple by which a
// property's dominant value must exceed a rare value before it is flagged.
const DefaultCockpitLongTailHeadFactor = 3

// DefaultCockpitDailyLimit is the default cap on how many tasks the daily
// cockpit view surfaces at once.
const DefaultCockpitDailyLimit = 10

// DefaultCockpitReviewDateProperty is the default frontmatter property the
// missing-review-date rule inspects for a note's last-reviewed date.
const DefaultCockpitReviewDateProperty = "reviewed"

// DefaultCockpitMaxWIP is the default number of concurrent work-in-progress
// notes allowed before the wip-limit rule flags an overflow.
const DefaultCockpitMaxWIP = 3

// DefaultCockpitStaleStatusDays is the default age in days after which a note's
// status is treated as stale.
const DefaultCockpitStaleStatusDays = 30

// DefaultAgentTimeoutSeconds is the default timeout applied to shared agent
// commands.
const DefaultAgentTimeoutSeconds = 60

// DefaultCockpitAgentTimeoutSeconds preserves the default for the legacy
// cockpit-specific agent override.
const DefaultCockpitAgentTimeoutSeconds = DefaultAgentTimeoutSeconds

const envVault = "BNOTES_VAULT"

// FileName is the vault-root configuration file. Its presence is what marks a
// directory as a vault root.
const FileName = "brainnotes.toml"

// VaultOrigin identifies which mechanism resolved the vault root.
type VaultOrigin string

const (
	VaultOriginFlag      VaultOrigin = "--vault flag"
	VaultOriginEnv       VaultOrigin = "$BNOTES_VAULT"
	VaultOriginDiscovery VaultOrigin = "upward search"
)

// LoadOptions carries every input vault discovery depends on, so precedence is
// explicit rather than read from ambient process state.
type LoadOptions struct {
	// VaultFlag is the --vault value. Whitespace-only counts as unset.
	VaultFlag string
	// WorkingDir seeds the upward search for FileName. Empty means os.Getwd.
	WorkingDir string
	// Getenv reads environment variables. Nil means os.Getenv.
	Getenv func(string) string
}

// Load resolves the vault root, then loads <vault>/brainnotes.toml from it.
//
// Vault precedence: --vault > $BNOTES_VAULT > the nearest ancestor of the
// working directory that contains brainnotes.toml. An explicit --vault or
// $BNOTES_VAULT must name an existing directory that contains brainnotes.toml;
// `bn init` creates that file.
//
// Leading `~` or `~/` in an explicit vault path is expanded to the user's home
// directory. The returned Config has defaults applied for any unset fields.
func Load(opts LoadOptions) (*Config, error) {
	root, origin, err := resolveVaultRoot(opts)
	if err != nil {
		return nil, err
	}

	source, err := os.ReadFile(filepath.Join(root, FileName))
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", filepath.Join(root, FileName), err)
	}
	return Decode(root, origin, source)
}

// Decode parses one brainnotes.toml body for a known vault root, applying the
// same defaults and validation as Load. Reload paths use it so a config
// rebuilt from bytes is byte-for-byte equivalent to one read from disk.
func Decode(root string, origin VaultOrigin, source []byte) (*Config, error) {
	cfg := &Config{
		Vault:       root,
		SourcePath:  filepath.Join(root, FileName),
		VaultOrigin: origin,
	}

	meta, err := toml.Decode(string(source), cfg)
	if err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", cfg.SourcePath, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
			if keys[i] == "vault" {
				return nil, fmt.Errorf(
					"config: %s: the `vault` key was removed; the vault root is the directory holding %s",
					cfg.SourcePath, FileName)
			}
			if legacy := legacyPropertyKey(keys[i]); legacy != "" {
				return nil, fmt.Errorf(
					"config: %s: `%s` was replaced by unified [frontmatter.properties.<name>] definitions with type/items/required/allowed",
					cfg.SourcePath, legacy)
			}
		}
		return nil, fmt.Errorf("config: %s: unknown keys: %s", cfg.SourcePath, strings.Join(keys, ", "))
	}

	if err := validateTemplatesDir(cfg.Templates.Dir); err != nil {
		return nil, err
	}

	applyDefaults(cfg)

	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

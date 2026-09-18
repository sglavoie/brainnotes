package config

import (
	"path"
	"strings"
)

// DefaultCaptureInboxNote is the vault-relative note `bn capture` appends to
// when [capture].inbox_note is unset.
const DefaultCaptureInboxNote = "Inbox.md"

// DefaultCaptureHeading is the heading captures are appended under when
// [capture].heading is unset.
const DefaultCaptureHeading = "## Captures"

// DefaultTemplate returns the commented TOML starter file body written by
// `bn init`.
//
// [autosave] is deliberately last: it is the one section
// PatchAutosaveMinInterval may have to create in a file a user has already
// edited, and appending is the only insertion position that cannot split a
// section of theirs. Keeping the starter file in that order means a created
// section lands where this template would have put it.
func DefaultTemplate() string {
	return `# bnotes configuration file
# This file marks its own directory as the vault root. All keys are optional.

# Extra vault-relative paths to skip when walking notes. These are MERGED
# with the built-in defaults: .git, .obsidian, Attachments, Archive, Trash.
# The configured [templates].dir (default "Templates") is also skipped
# automatically.
# ignore = ["Drafts"]

[templates]
# Directory where note templates live (vault-relative).
# dir = "Templates"
# default_extension = ".md"

# Every frontmatter property is described by one definition. A definition names
# a type (string, wikilink, date, datetime, int, float, bool, or list), and a
# list additionally names the scalar kind of its items. Optional keys are
# 'required' and 'allowed'. Properties with no definition are left alone.
#
# [frontmatter.properties.status]
# type     = "string"
# required = true
# allowed  = ["WIP", "Done", "Archived"]

[frontmatter.properties.categories]
type  = "list"
items = "wikilink"

[frontmatter.properties.tags]
type  = "list"
items = "string"

[frontmatter.properties.topics]
type  = "list"
items = "string"

[frontmatter.properties.org]
type  = "list"
items = "wikilink"

[frontmatter.properties.type]
type  = "list"
items = "string"

[frontmatter.properties.parent]
type = "wikilink"

[frontmatter.properties.area]
type = "wikilink"

# Ordering key for pinned notes: lower ranks come first, and a drag in the web
# UI writes the midpoint between two neighbours. Declared here as documentation
# only — pinning writes this type explicitly, so it works without this block.
[frontmatter.properties.pinned]
type = "float"

[fzf]
# Additional flags passed to fzf when --interactive is used.
# args = ["--height=40%", "--reverse"]

[open]
# Editor binary to invoke when opening a note with 'bn open'.
# Falls back to $EDITOR when not set.
# editor = "code --reuse-window"

[serve]
# HTTP listen address for ` + "`bn serve`" + `.
# addr = "` + DefaultServeAddr + `"
# Open the browser automatically on startup. Defaults to true.
# open_browser = true

# Default folders for ` + "`bn new <template>`" + `. Keys are template names,
# values are vault-relative folders.
# [new.routes]
# book = "References/Books"
# person = "People"

[related]
# Frontmatter properties whose values contribute to related-note ranking.
# Set this to [] to disable metadata scoring.
# metadata_properties = ["categories", "tags", "topics", "type", "parent", "area"]

[brief]
# Vault-relative folder prefixes excluded from Daily Brief recommendations.
# exclude_folders = ["Archive", "Templates/Drafts"]
# Frontmatter type values excluded from Daily Brief recommendations.
# exclude_types = ["journal", "scratch"]

[capture]
# Note that ` + "`bn capture`" + ` and the web quick-capture box append to (vault-relative).
# inbox_note = "` + DefaultCaptureInboxNote + `"
# Heading captures are appended under. Appended to the end of the note if absent.
# heading = "` + DefaultCaptureHeading + `"

[agent]
# Local agent command as an argv array. The executable is launched directly;
# arguments are never interpreted by a shell. Empty disables agent execution.
# command = ["my-agent", "--stdin"]
# Timeout in seconds for local agent execution.
# timeout_seconds = 60

[cockpit]
# Tuning for the Daily Fix Cockpit (` + "`bn doctor`" + ` and the web cockpit).
# Rule kinds to hide from the queue entirely.
# disabled_rules = ["long_tail_value"]
# Rule kinds to surface first, overriding the built-in ordering.
# priority = ["broken_target", "schema_violation"]
# How many of the rarest values per property become long-tail tasks.
# long_tail_limit = 5
# Highest per-value count that still counts as rare for long-tail tasks.
# long_tail_max_count = 2
# A rare value is flagged only if the property's dominant value is at least this many times as frequent.
# long_tail_head_factor = 3
# Frontmatter properties the long-tail rule never flags (open-vocabulary / naturally-unique fields).
# long_tail_ignore_properties = ["created", "tags", "topics", "ingredients"]
# Maximum number of tasks the daily cockpit view surfaces at once.
# daily_limit = 10
# Frontmatter property that records when a note was last reviewed.
# review_date_property = "reviewed"
# Status values whose notes must carry a review date. Empty disables the rule.
# review_statuses = ["Evergreen", "Reference"]
# Status values counted as work-in-progress. Empty disables the WIP-limit rule.
# wip_statuses = ["WIP", "In progress"]
# Maximum concurrent work-in-progress notes before the WIP-limit rule flags overflow.
# max_wip = 3
# Age in days after which a note's status is treated as stale.
# stale_status_days = 30
# Unprocessed captures before the capture-backlog rule fires. 0 disables it.
# max_captures = 20
# Frontmatter type values identifying hub/MOC notes. Empty disables the synthesis rule.
# hub_type_values = ["moc", "hub"]
# Headings that, when present but empty in a hub note, mark it as needing synthesis.
# hub_empty_headings = ["## Synthesis", "## Notes"]
# Enable the sensitive-content scanner. Off by default; matched values are never exposed unredacted.
# sensitive_scan = false
# Vault-relative path prefixes the sensitive-content scanner skips.
# sensitive_ignore_paths = ["Templates", "Archive"]
# Regular-expression patterns whose matches the sensitive-content scanner treats as allowed.
# sensitive_ignore_patterns = ["EXAMPLE_[A-Z]+"]
# Legacy daily-review command override. When empty, [agent].command is used.
# agent_command = "my-review-agent"
# Timeout for the legacy override. When agent_command is empty, [agent].timeout_seconds is used.
# agent_timeout_seconds = 60

[autosave]
# Hard floor between two commits made automatically by the external file
# watcher. A Go duration between "30s" and "24h" inclusive, or "off" to stop the
# watcher committing on its own. Explicit ` + "`bn`" + ` changes always commit
# immediately either way. Defaults to "` + DefaultAutosaveMinInterval + `".
# min_interval = "` + DefaultAutosaveMinInterval + `"
`
}

// applyDefaults fills in every unset key and canonicalizes the ones a person can
// spell more than one way.
//
// It is one call per section. The sections are independent — no default here
// depends on another — so the only thing a single long body added was the need
// to read all of it to change any of it, and a configuration that grows a
// section should grow a function rather than this one.
func applyDefaults(cfg *Config) {
	applyTemplateDirDefault(cfg)
	applyTemplateExtensionDefault(cfg)
	applyCollectionDefaults(cfg)
	applyCaptureAndAgentDefaults(cfg)
	applyOpenAndServeDefaults(cfg)
	applyCockpitListDefaults(cfg)
	applyCockpitScalarDefaults(cfg)
	applyAutosaveDefaults(cfg)
	applyAttachmentDefaults(cfg)
	cfg.Ignore = mergeIgnore(DefaultIgnore, cfg.Ignore)
}

func applyTemplateDirDefault(cfg *Config) {
	// Trim surrounding whitespace, convert platform separators to slash, and
	// strip trailing slashes so the configured templates dir matches the
	// vault-relative paths Walk compares against. Without this,
	// `dir = "Templates/"` (or with stray spaces) would silently fail to be
	// pruned and template files would be walked.
	cfg.Templates.Dir = strings.TrimSpace(cfg.Templates.Dir)
	cfg.Templates.Dir = normalizeSlashSeparators(cfg.Templates.Dir)
	cfg.Templates.Dir = strings.TrimRight(cfg.Templates.Dir, "/")
	if cfg.Templates.Dir != "" {
		cfg.Templates.Dir = path.Clean(cfg.Templates.Dir)
	}
	// Reject absolute paths and parent-traversal — the templates dir must be
	// vault-relative for Walk's pruning to match. Silently falling back to
	// the default is safer than walking a value the user clearly intended
	// but that cannot work as written.
	if isAbsolutePathLike(cfg.Templates.Dir) ||
		cfg.Templates.Dir == ".." ||
		strings.HasPrefix(cfg.Templates.Dir, "../") {
		cfg.Templates.Dir = "Templates"
	}
	if cfg.Templates.Dir == "" || cfg.Templates.Dir == "." {
		cfg.Templates.Dir = "Templates"
	}
}

func applyTemplateExtensionDefault(cfg *Config) {
	// Normalize the configured extension: trim surrounding whitespace,
	// fall back to ".md" when empty or just ".", then ensure a single
	// leading dot. A bare "." would otherwise produce filenames ending in
	// a stray period (e.g. "Note.").
	cfg.Templates.DefaultExtension = strings.TrimSpace(cfg.Templates.DefaultExtension)
	if cfg.Templates.DefaultExtension == "" || cfg.Templates.DefaultExtension == "." {
		cfg.Templates.DefaultExtension = ".md"
	}
	// Reject extensions containing path separators or control characters — they
	// cannot appear in a sane filename suffix and would produce unusable output
	// paths or template lookup candidates when `bn new` / `template.Find`
	// concatenate title + extension.
	if strings.ContainsAny(cfg.Templates.DefaultExtension, "/\\") ||
		containsControlRune(cfg.Templates.DefaultExtension) {
		cfg.Templates.DefaultExtension = ".md"
	}
	if !strings.HasPrefix(cfg.Templates.DefaultExtension, ".") {
		cfg.Templates.DefaultExtension = "." + cfg.Templates.DefaultExtension
	}
}

// applyCollectionDefaults gives every configured map and list either a normalized
// version of what the user wrote or an empty, non-nil value.
//
// Non-nil matters beyond tidiness: an absent table and an empty one are the same
// thing to every reader above this package, and a nil map here would make one of
// them a panic.
func applyCollectionDefaults(cfg *Config) {
	if cfg.Frontmatter.Properties == nil {
		cfg.Frontmatter.Properties = map[string]PropertyDefinition{}
	} else {
		normalizeProperties(cfg.Frontmatter.Properties)
	}
	if cfg.New.Routes == nil {
		cfg.New.Routes = map[string]string{}
	} else {
		normalizeRoutes(cfg.New.Routes)
	}
	if cfg.Related.MetadataProperties == nil {
		cfg.Related.MetadataProperties = append([]string(nil), DefaultRelatedMetadataProperties...)
	} else {
		cfg.Related.MetadataProperties = normalizePropertyList(cfg.Related.MetadataProperties)
	}
	if cfg.Brief.ExcludeFolders == nil {
		cfg.Brief.ExcludeFolders = []string{}
	} else {
		cfg.Brief.ExcludeFolders = normalizeBriefFolders(cfg.Brief.ExcludeFolders)
	}
	if cfg.Brief.ExcludeTypes == nil {
		cfg.Brief.ExcludeTypes = []string{}
	} else {
		cfg.Brief.ExcludeTypes = normalizeBriefTypes(cfg.Brief.ExcludeTypes)
	}
}

func applyCaptureAndAgentDefaults(cfg *Config) {
	cfg.Capture.InboxNote = normalizeCaptureInbox(cfg.Capture.InboxNote)
	cfg.Capture.Heading = strings.TrimSpace(cfg.Capture.Heading)
	if cfg.Capture.Heading == "" {
		cfg.Capture.Heading = DefaultCaptureHeading
	}
	if cfg.Agent.Command == nil {
		cfg.Agent.Command = []string{}
	}
	cfg.Agent.TimeoutSeconds = intOrDefault(cfg.Agent.TimeoutSeconds, DefaultAgentTimeoutSeconds)
}

func applyOpenAndServeDefaults(cfg *Config) {
	// Trim surrounding whitespace from [open].editor. Without this, a value
	// like `editor = "  "` is non-empty and shadows the $EDITOR fallback in
	// `bn open`, then fails with an opaque exec error.
	cfg.Open.Editor = strings.TrimSpace(cfg.Open.Editor)

	cfg.Serve.Addr = strings.TrimSpace(cfg.Serve.Addr)
	if cfg.Serve.Addr == "" {
		cfg.Serve.Addr = DefaultServeAddr
	}

}

// listOrEmpty turns an absent list into an empty, non-nil one.
//
// Every cockpit rule list defaults this way: the owning rule stays off until the
// user opts in, and an empty slice round-trips through TOML where a nil one
// disappears. What the user did write is left exactly as written, so validate
// still sees it — membership against the known rule kinds is a wiring-time
// concern in internal/cockpit, not a config-load one.
func listOrEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// intOrDefault fills in an unset numeric knob.
//
// Zero means "unset" and takes the documented default. Anything else survives —
// including a negative — because the value a person wrote is the value validate
// has to be able to complain about.
func intOrDefault(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

func applyCockpitListDefaults(cfg *Config) {
	c := &cfg.Cockpit
	c.DisabledRules = listOrEmpty(c.DisabledRules)
	c.Priority = listOrEmpty(c.Priority)
	c.LongTailIgnoreProperties = listOrEmpty(c.LongTailIgnoreProperties)
	c.ReviewStatuses = listOrEmpty(c.ReviewStatuses)
	c.WIPStatuses = listOrEmpty(c.WIPStatuses)
	c.HubTypeValues = listOrEmpty(c.HubTypeValues)
	c.HubEmptyHeadings = listOrEmpty(c.HubEmptyHeadings)
	c.SensitiveIgnorePaths = listOrEmpty(c.SensitiveIgnorePaths)
	c.SensitiveIgnorePatterns = listOrEmpty(c.SensitiveIgnorePatterns)
}

func applyCockpitScalarDefaults(cfg *Config) {
	c := &cfg.Cockpit
	c.LongTailLimit = intOrDefault(c.LongTailLimit, DefaultCockpitLongTailLimit)
	c.LongTailMaxCount = intOrDefault(c.LongTailMaxCount, DefaultCockpitLongTailMaxCount)
	c.LongTailHeadFactor = intOrDefault(c.LongTailHeadFactor, DefaultCockpitLongTailHeadFactor)
	c.DailyLimit = intOrDefault(c.DailyLimit, DefaultCockpitDailyLimit)
	c.MaxWIP = intOrDefault(c.MaxWIP, DefaultCockpitMaxWIP)
	c.StaleStatusDays = intOrDefault(c.StaleStatusDays, DefaultCockpitStaleStatusDays)
	c.AgentTimeoutSeconds = intOrDefault(c.AgentTimeoutSeconds, DefaultCockpitAgentTimeoutSeconds)

	c.ReviewDateProperty = strings.TrimSpace(c.ReviewDateProperty)
	if c.ReviewDateProperty == "" {
		c.ReviewDateProperty = DefaultCockpitReviewDateProperty
	}
	c.AgentCommand = strings.TrimSpace(c.AgentCommand)
}

func applyAutosaveDefaults(cfg *Config) {
	// An absent autosave floor means DefaultAutosaveMinInterval, and a value the
	// user spelled some other way ("300s", "5m0s") is stored in its canonical
	// form so `bn config get`, the watcher's persisted policy, and a re-read of
	// this file all show one spelling. A value that does not parse is left
	// untouched for validate to reject with the text the user actually wrote.
	cfg.Autosave.MinInterval = strings.TrimSpace(cfg.Autosave.MinInterval)
	if cfg.Autosave.MinInterval == "" {
		cfg.Autosave.MinInterval = DefaultAutosaveMinInterval
	}
	if policy, err := ParseAutosaveMinInterval(cfg.Autosave.MinInterval); err == nil {
		cfg.Autosave.MinInterval = policy.Canonical
	}
}

func applyAttachmentDefaults(cfg *Config) {
	// The attachments directory is normalized exactly the way the templates
	// directory is, for the same reason: it is compared against vault-relative
	// slash paths, so `dir = "Media/"` or a stray space has to mean `Media`. An
	// unusable value falls back to the default rather than being left to fail at
	// the first upload, because the vault has to be able to say where a new
	// attachment goes before anyone picks one.
	cfg.Attachments.Dir = normalizeSlashSeparators(strings.TrimSpace(cfg.Attachments.Dir))
	cfg.Attachments.Dir = strings.TrimRight(cfg.Attachments.Dir, "/")
	if cfg.Attachments.Dir != "" {
		cfg.Attachments.Dir = path.Clean(cfg.Attachments.Dir)
	}
	if isAbsolutePathLike(cfg.Attachments.Dir) ||
		cfg.Attachments.Dir == "" ||
		cfg.Attachments.Dir == "." ||
		cfg.Attachments.Dir == ".." ||
		strings.HasPrefix(cfg.Attachments.Dir, "../") {
		cfg.Attachments.Dir = DefaultAttachmentsDir
	}
	// A zero limit means "unset" and takes the documented default; anything else
	// — including a negative or an over-ceiling number — survives for validate to
	// reject with the value the user actually wrote.
	if cfg.Attachments.MaxFileBytes == 0 {
		cfg.Attachments.MaxFileBytes = MaxAttachmentFileBytes
	}
}

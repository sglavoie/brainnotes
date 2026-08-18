package config

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
)

// DefaultCaptureInboxNote is the vault-relative note `bn capture` appends to
// when [capture].inbox_note is unset.
const DefaultCaptureInboxNote = "Inbox.md"

// DefaultCaptureHeading is the heading captures are appended under when
// [capture].heading is unset.
const DefaultCaptureHeading = "## Captures"

// DefaultTemplate returns the commented TOML starter file body written by
// `bn init`.
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
`
}

func applyDefaults(cfg *Config) {
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
	cfg.Capture.InboxNote = normalizeCaptureInbox(cfg.Capture.InboxNote)
	cfg.Capture.Heading = strings.TrimSpace(cfg.Capture.Heading)
	if cfg.Capture.Heading == "" {
		cfg.Capture.Heading = DefaultCaptureHeading
	}
	if cfg.Agent.Command == nil {
		cfg.Agent.Command = []string{}
	}
	if cfg.Agent.TimeoutSeconds == 0 {
		cfg.Agent.TimeoutSeconds = DefaultAgentTimeoutSeconds
	}

	// Trim surrounding whitespace from [open].editor. Without this, a value
	// like `editor = "  "` is non-empty and shadows the $EDITOR fallback in
	// `bn open`, then fails with an opaque exec error.
	cfg.Open.Editor = strings.TrimSpace(cfg.Open.Editor)

	cfg.Serve.Addr = strings.TrimSpace(cfg.Serve.Addr)
	if cfg.Serve.Addr == "" {
		cfg.Serve.Addr = DefaultServeAddr
	}

	// Cockpit rule lists default to empty (no rules disabled, no priority
	// override). They are left otherwise untouched so validate can still see
	// user-supplied entries; membership against known rule kinds is a wiring-time
	// concern in internal/cockpit, not a config-load concern.
	if cfg.Cockpit.DisabledRules == nil {
		cfg.Cockpit.DisabledRules = []string{}
	}
	if cfg.Cockpit.Priority == nil {
		cfg.Cockpit.Priority = []string{}
	}
	if cfg.Cockpit.LongTailIgnoreProperties == nil {
		cfg.Cockpit.LongTailIgnoreProperties = []string{}
	}
	// A zero long_tail_limit means "unset"; fall back to the documented default.
	// Negative values are left as-is so validate can reject them.
	if cfg.Cockpit.LongTailLimit == 0 {
		cfg.Cockpit.LongTailLimit = DefaultCockpitLongTailLimit
	}
	if cfg.Cockpit.LongTailMaxCount == 0 {
		cfg.Cockpit.LongTailMaxCount = DefaultCockpitLongTailMaxCount
	}
	if cfg.Cockpit.LongTailHeadFactor == 0 {
		cfg.Cockpit.LongTailHeadFactor = DefaultCockpitLongTailHeadFactor
	}

	// New daily-cockpit rule config. Each list defaults to an empty, non-nil
	// slice so the owning rule stays off until the user opts in, and TOML round
	// trips cleanly. Scalar review/agent fields are trimmed and defaulted; the
	// numeric knobs follow the long-tail precedent (zero means "unset" and falls
	// back to the documented default; negatives survive so validate can reject
	// them).
	if cfg.Cockpit.ReviewStatuses == nil {
		cfg.Cockpit.ReviewStatuses = []string{}
	}
	if cfg.Cockpit.WIPStatuses == nil {
		cfg.Cockpit.WIPStatuses = []string{}
	}
	if cfg.Cockpit.HubTypeValues == nil {
		cfg.Cockpit.HubTypeValues = []string{}
	}
	if cfg.Cockpit.HubEmptyHeadings == nil {
		cfg.Cockpit.HubEmptyHeadings = []string{}
	}
	if cfg.Cockpit.SensitiveIgnorePaths == nil {
		cfg.Cockpit.SensitiveIgnorePaths = []string{}
	}
	if cfg.Cockpit.SensitiveIgnorePatterns == nil {
		cfg.Cockpit.SensitiveIgnorePatterns = []string{}
	}
	cfg.Cockpit.ReviewDateProperty = strings.TrimSpace(cfg.Cockpit.ReviewDateProperty)
	if cfg.Cockpit.ReviewDateProperty == "" {
		cfg.Cockpit.ReviewDateProperty = DefaultCockpitReviewDateProperty
	}
	cfg.Cockpit.AgentCommand = strings.TrimSpace(cfg.Cockpit.AgentCommand)
	if cfg.Cockpit.DailyLimit == 0 {
		cfg.Cockpit.DailyLimit = DefaultCockpitDailyLimit
	}
	if cfg.Cockpit.MaxWIP == 0 {
		cfg.Cockpit.MaxWIP = DefaultCockpitMaxWIP
	}
	if cfg.Cockpit.StaleStatusDays == 0 {
		cfg.Cockpit.StaleStatusDays = DefaultCockpitStaleStatusDays
	}
	if cfg.Cockpit.AgentTimeoutSeconds == 0 {
		cfg.Cockpit.AgentTimeoutSeconds = DefaultCockpitAgentTimeoutSeconds
	}

	cfg.Ignore = mergeIgnore(DefaultIgnore, cfg.Ignore)
}

func normalizeProperties(props map[string]PropertyDefinition) {
	for key, def := range props {
		def.Type = normalizeKind(def.Type)
		def.Items = normalizeKind(def.Items)
		props[key] = def
	}
}

func normalizeKind(k PropertyKind) PropertyKind {
	return PropertyKind(strings.ToLower(strings.TrimSpace(string(k))))
}

func containsControlRune(s string) bool {
	for _, r := range s {
		if r == 0 || r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// normalizeCaptureInbox mirrors the templates.dir pipeline: the capture inbox
// must be a vault-relative note path, so an absolute or upward-escaping value
// silently falls back to the default rather than writing outside the vault.
func normalizeCaptureInbox(p string) string {
	p = strings.TrimSpace(p)
	p = normalizeSlashSeparators(p)
	p = strings.TrimRight(p, "/")
	if p != "" {
		p = path.Clean(p)
	}
	if isAbsolutePathLike(p) || p == ".." || strings.HasPrefix(p, "../") || p == "" || p == "." {
		return DefaultCaptureInboxNote
	}
	if !strings.EqualFold(path.Ext(p), ".md") {
		p += ".md"
	}
	return p
}

func normalizeRoutes(routes map[string]string) {
	for name, dest := range routes {
		p := normalizeRouteDest(dest)
		if p == "" {
			delete(routes, name)
			continue
		}
		routes[name] = p
	}
}

func normalizeRouteDest(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = normalizeSlashSeparators(p)
	if isAbsolutePathLike(p) {
		return path.Clean(p)
	}
	p = strings.TrimRight(p, "/")
	if p == "" {
		return ""
	}
	return path.Clean(p)
}

func normalizeSlashSeparators(p string) string {
	return strings.ReplaceAll(filepath.ToSlash(p), "\\", "/")
}

// normalizePropertyList trims surrounding whitespace, drops empty entries, and
// deduplicates so a user-provided property-name list matches the exact
// frontmatter key string used by downstream callers (which compare with string
// equality, not whitespace-insensitively).
func normalizePropertyList(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		k := strings.TrimSpace(raw)
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out
}

func normalizeBriefFolders(in []string) []string {
	return normalizeBriefList(in, func(value string) string {
		value = textnorm.NFC(value)
		value = normalizeSlashSeparators(value)
		if isAbsolutePathLike(value) {
			return value
		}
		value = strings.Trim(value, "/")
		if value == "" {
			return ""
		}
		cleaned := path.Clean(value)
		if cleaned == "." {
			return ""
		}
		return cleaned
	})
}

func normalizeBriefTypes(in []string) []string {
	return normalizeBriefList(in, func(value string) string {
		return strings.ToLower(textnorm.NFC(value))
	})
}

func normalizeBriefList(in []string, normalize func(string) string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, raw := range in {
		value := strings.TrimSpace(raw)
		if normalize != nil {
			value = normalize(value)
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func mergeIgnore(defaults, extra []string) []string {
	seen := make(map[string]struct{}, len(defaults)+len(extra))
	out := make([]string, 0, len(defaults)+len(extra))
	add := func(raw string) {
		p := normalizeIgnore(raw)
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	for _, p := range defaults {
		add(p)
	}
	for _, p := range extra {
		add(p)
	}
	return out
}

// normalizeIgnore trims surrounding whitespace, converts platform separators
// to forward slashes, and applies path.Clean so that variants like "Archive/",
// "./Archive", "././Archive", and " Archive " all collapse to "Archive" for
// dedup purposes. Entries that are empty, point at the vault root itself, are
// absolute, or escape upward via ".." are dropped because ignore values are
// documented to be vault-relative paths.
func normalizeIgnore(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	// Convert platform separators to forward slash before cleaning so that
	// entries like ".\Archive" are treated the same as "./Archive".
	p = normalizeSlashSeparators(p)
	// Trim trailing slashes so "Archive/" becomes "Archive".
	p = strings.TrimRight(p, "/")
	// path.Clean handles repeated "./", interior ".", and other artifacts,
	// matching the normalization the vault walker applies in ignoreSet.
	p = path.Clean(p)
	if p == "" || p == "." || isAbsolutePathLike(p) ||
		p == ".." || strings.HasPrefix(p, "../") {
		return ""
	}
	return p
}

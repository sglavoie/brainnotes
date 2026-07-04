package config

import (
	"path"
	"path/filepath"
	"strings"
)

// DefaultTemplate returns the commented TOML starter file body written when
// `bn config edit` is invoked with no existing config file.
func DefaultTemplate() string {
	return `# bnotes configuration file
# All keys are optional except ` + "`vault`" + `.

# Absolute path to your vault root. Required for most commands.
# vault = "/Users/you/vault"

# Extra vault-relative paths to skip when walking notes. These are MERGED
# with the built-in defaults: .git, .obsidian, Attachments, Archive, Trash.
# The configured [templates].dir (default "Templates") is also skipped
# automatically.
# ignore = ["Drafts"]

[templates]
# Directory where note templates live (vault-relative).
# dir = "Templates"
# default_extension = ".md"

[frontmatter]
# Frontmatter property keys whose values are stored as [[wikilinks]].
# wikilink_properties = ["categories", "org", "parent", "area"]
# Property keys whose values are comma-separated lists.
# list_properties = ["categories", "tags", "topics", "org", "type"]

# Property schema for validation with 'bn lint'.
# [frontmatter.schema.status]
# type     = "string"        # string, list, date, int, bool, float
# required = true
# allowed  = ["WIP", "Done", "Archived"]

[fzf]
# Additional flags passed to fzf when --interactive is used.
# args = ["--height=40%", "--reverse"]

[open]
# Editor binary to invoke when opening a note with 'bn open'.
# Falls back to $EDITOR when not set.
# editor = "nvim"

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

[cockpit]
# Tuning for the Daily Fix Cockpit (` + "`bn doctor`" + ` and the web cockpit).
# Rule kinds to hide from the queue entirely.
# disabled_rules = ["long_tail_value"]
# Rule kinds to surface first, overriding the built-in ordering.
# priority = ["broken_target", "schema_violation"]
# How many of the rarest values per property become long-tail tasks.
# long_tail_limit = 5
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
	if cfg.Frontmatter.WikilinkProperties == nil {
		cfg.Frontmatter.WikilinkProperties = []string{"categories", "org", "parent", "area"}
	} else {
		cfg.Frontmatter.WikilinkProperties = normalizePropertyList(cfg.Frontmatter.WikilinkProperties)
	}
	if cfg.Frontmatter.ListProperties == nil {
		cfg.Frontmatter.ListProperties = []string{"categories", "tags", "topics", "org", "type"}
	} else {
		cfg.Frontmatter.ListProperties = normalizePropertyList(cfg.Frontmatter.ListProperties)
	}
	if cfg.Frontmatter.Schema == nil {
		cfg.Frontmatter.Schema = map[string]PropertySchema{}
	} else {
		normalizeSchema(cfg.Frontmatter.Schema)
	}
	if cfg.New.Routes == nil {
		cfg.New.Routes = map[string]string{}
	} else {
		normalizeRoutes(cfg.New.Routes)
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
	// A zero long_tail_limit means "unset"; fall back to the documented default.
	// Negative values are left as-is so validate can reject them.
	if cfg.Cockpit.LongTailLimit == 0 {
		cfg.Cockpit.LongTailLimit = DefaultCockpitLongTailLimit
	}

	cfg.Ignore = mergeIgnore(DefaultIgnore, cfg.Ignore)
}

func normalizeSchema(schema map[string]PropertySchema) {
	for key, rule := range schema {
		rule.Type = strings.ToLower(strings.TrimSpace(rule.Type))
		schema[key] = rule
	}
}

func containsControlRune(s string) bool {
	for _, r := range s {
		if r == 0 || r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
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
// deduplicates so a user-provided list_properties / wikilink_properties value
// matches the exact frontmatter key string used by downstream callers (which
// compare with string equality, not whitespace-insensitively).
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

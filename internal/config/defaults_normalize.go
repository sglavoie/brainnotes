package config

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
)

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

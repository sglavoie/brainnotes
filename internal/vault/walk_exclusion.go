package vault

import (
	"strings"

	"github.com/sglavoie/brainnotes/internal/config"
)

// WalkExclusion reports whether relSlash names a location Walk would never
// visit — or, for a case variant of a configured exclusion, might not — and why.
// It exists so writers can refuse a destination before creating it: Walk
// silently prunes hidden directories, configured ignores, and the templates
// directory, so a note written there would exist on disk yet be invisible to
// every `bn` query.
//
// relSlash is a vault-relative path in either slash or OS separator form. The
// check is prefix-wise, so a path is excluded when any of its ancestors is.
//
// Both configured exclusions are compared case-insensitively, which makes this
// predicate **stricter than Walk** on a case-sensitive filesystem, where
// `archive/` and `Archive/` are two directories and Walk visits the first. That
// asymmetry is deliberate and it is what a writer needs: Walk prunes by the name
// it finds on disk, so on a case-insensitive filesystem a create of
// `archive/Old.md` against an ignored `Archive/` lands *inside* that real
// directory and is then pruned under its on-disk name — the note exists and no
// query can see it. The cost of refusing a writable location is a message; the
// cost of accepting an invisible one is a lost note.
//
// The whole price, stated rather than left to be discovered: because the source
// API resolves reads through the same predicate, a case-sensitive filesystem
// holding a real lowercase `archive/` beside an ignored `Archive` gets notes
// that are walked, indexed and listed but answered 403 when opened. Learning a
// destination's true on-disk casing means stat-ing it, which is more machinery
// than this case is worth, and refusing is the safe direction of the two.
func WalkExclusion(cfg *config.Config, relSlash string) (string, bool) {
	rel := cleanSlashRel(relSlash)
	if rel == "" || relEscapesRoot(rel) {
		return "outside the vault", true
	}

	ignore := ignoreSet(cfg)
	templatesDir := ""
	if cfg != nil {
		templatesDir = cleanSlashRel(cfg.Templates.Dir)
		if relEscapesRoot(templatesDir) {
			templatesDir = ""
		}
	}

	components := strings.Split(rel, "/")
	for i, name := range components {
		prefix := strings.Join(components[:i+1], "/")
		if strings.HasPrefix(name, ".") {
			return "hidden path " + prefix, true
		}
		// Templates is reported before the ignore set because ignoreSet already
		// contains the exactly-cased templates dir; checking it first would
		// mask the more specific reason with a generic "ignored path".
		if templatesDir != "" && strings.EqualFold(prefix, templatesDir) {
			return "templates directory " + prefix, true
		}
		if ignoredPrefix(ignore, prefix) {
			return "ignored path " + prefix, true
		}
	}
	return "", false
}

// ignoredPrefix reports whether prefix names an entry of the ignore set,
// compared the way the templates directory is. The exact lookup comes first so
// the ordinary case stays a map hit; the fold-wise scan is the case-variant a
// case-insensitive filesystem would resolve onto an ignored directory.
func ignoredPrefix(ignore map[string]struct{}, prefix string) bool {
	if _, skip := ignore[prefix]; skip {
		return true
	}
	for entry := range ignore {
		if strings.EqualFold(prefix, entry) {
			return true
		}
	}
	return false
}

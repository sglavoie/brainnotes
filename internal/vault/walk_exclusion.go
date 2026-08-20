package vault

import (
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// WalkExclusion reports whether relSlash names a location Walk would never
// visit, and why. It exists so writers can refuse a destination before creating
// it: Walk silently prunes hidden directories, configured ignores, and the
// templates directory, so a note written there would exist on disk yet be
// invisible to every `bn` query.
//
// relSlash is a vault-relative path in either slash or OS separator form. The
// check is prefix-wise, so a path is excluded when any of its ancestors is.
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
		if _, skip := ignore[prefix]; skip {
			return "ignored path " + prefix, true
		}
	}
	return "", false
}

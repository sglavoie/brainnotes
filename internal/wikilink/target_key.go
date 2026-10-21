package wikilink

import (
	"path"
	"strings"

	"github.com/sglavoie/brainnotes/internal/textnorm"
)

// TargetPathKey is the vault-path key a folder-qualified wikilink target, or a
// vault-relative note path, reduces to: backslashes read as `/`, the path
// cleaned (so `/A/Foo` and `A//Foo` are `a/foo`), one trailing `.md` dropped,
// then NFC-normalized and lowercased. It returns "" for a target that cleans
// to nothing. The link graph keys notes and resolves links with it, and
// RewriteMove matches with it, so a move rewrites exactly the links the graph
// resolved to the moved note.
func TargetPathKey(target string) string {
	target = strings.TrimSpace(strings.ReplaceAll(target, "\\", "/"))
	target = strings.TrimPrefix(path.Clean("/"+target), "/")
	if base, ext := splitMDExt(target); ext != "" && base != "" {
		target = base
	}
	if target == "" || target == "." {
		return ""
	}
	return strings.ToLower(textnorm.NFC(target))
}

// ResolveTargetPathKey is TargetPathKey for a link target the graph is about
// to resolve. A target with a `.` or `..` segment is refused rather than
// cleaned, so `[[../Beta]]` stays broken instead of collapsing onto a real
// note.
func ResolveTargetPathKey(target string) (string, bool) {
	target = strings.TrimSpace(strings.ReplaceAll(target, "\\", "/"))
	for _, part := range strings.Split(target, "/") {
		if part == "." || part == ".." {
			return "", false
		}
	}
	key := TargetPathKey(target)
	if key == "" {
		return "", false
	}
	return key, true
}

package attachments

import (
	"fmt"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// filenameMaxLen bounds the sanitized filename in bytes.
//
// It is well under the 255 every filesystem this vault can live on allows,
// which is the point: a destination candidate may add a `-<8 hex>` suffix, the
// attachments directory is prefixed onto it, and a synchronized replica may
// carry the whole thing through another layout again. Leaving that much room is
// cheaper than discovering the limit on somebody else's machine.
const filenameMaxLen = 128

// extMaxLen bounds how much of a name is treated as an extension when the name
// has to be shortened. Past this it is not an extension, it is a name with a dot
// in it, and truncating the stem to preserve it would be preserving the wrong
// half.
const extMaxLen = 16

// noteExtensions are the extensions an attachment may not take.
//
// A file in the vault with a note's extension *is* a note: vault.Walk loads it,
// the index caches it, and frontmatter.Parse is handed whatever bytes a browser
// uploaded. Renaming around it would be worse than refusing — the reference in
// the note would then point at a name the user never chose — so this is one of
// the two hard refusals in cleanFilename.
var noteExtensions = map[string]bool{".md": true, ".markdown": true}

// hostileRunes are characters that are legal on this machine and are not legal,
// or not literal, somewhere the vault may be synchronized to.
//
// They are replaced rather than refused. A user pasting a screenshot named
// `Screen Shot 2026-09-18 at 09:41:22.png` has done nothing wrong, and the colon
// is a path separator on one platform and an alternate-data-stream marker on
// another; a vault that syncs is a vault whose filenames have to survive both.
// The separators are not in this set — they are handled ahead of it by taking
// the base name, which is a containment decision and not a portability one.
const hostileRunes = `:*?"<>|` + "\x00"

// isControl reports whether r is a character that must not reach a filename, a
// log line, or a JSON field this package writes.
func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

// cleanFilename turns whatever a client called a file into a name this vault can
// carry, or refuses it.
//
// The order is load-bearing. Separators are folded and the base is taken first,
// so everything after it is working on one path component and no later rule has
// to think about traversal at all. Then the characters that cannot travel are
// replaced, then the name is shortened to fit, and only then is the result
// judged — because a name that is unusable *after* sanitizing is the one worth
// reporting, and a name that merely needed sanitizing is not a failure.
func cleanFilename(name string) (string, error) {
	if !utf8.ValidString(name) {
		return "", fmt.Errorf("%w: not valid UTF-8", ErrInvalidFilename)
	}
	base := path.Base(strings.TrimSpace(strings.ReplaceAll(name, `\`, "/")))
	base = capFilename(scrubHostileRunes(base))
	switch {
	case base == "" || base == "." || base == "..":
		return "", fmt.Errorf("%w: %q names no file", ErrInvalidFilename, name)
	case strings.ContainsAny(base, `/\`):
		// path.Base answers "/" for "/", which is the one input it does not reduce
		// to a plain component. Checked rather than reasoned about, because every
		// rule after this one is written as if a separator here were impossible.
		return "", fmt.Errorf("%w: %q names no file", ErrInvalidFilename, name)
	case strings.HasPrefix(base, "."):
		// A dotted leading component is how every private directory in this vault
		// is spelled, and a hidden attachment is one the walk, the history, and
		// the user's file browser all disagree about.
		return "", fmt.Errorf("%w: %q would be a hidden file", ErrInvalidFilename, name)
	case noteExtensions[strings.ToLower(path.Ext(base))]:
		return "", fmt.Errorf("%w: %q would be a note, not an attachment", ErrInvalidFilename, name)
	}
	return base, nil
}

// scrubHostileRunes replaces what cannot travel and collapses what that leaves.
//
// A control character is replaced here rather than refused, unlike in
// cleanOwner: a filename is picked by a user's operating system and not typed
// into this application, so refusing one is refusing a file the person can see,
// while an owner is a value this application's own client chose.
func scrubHostileRunes(name string) string {
	replaced := strings.Map(func(r rune) rune {
		if isControl(r) || strings.ContainsRune(hostileRunes, r) || unicode.IsSpace(r) && r != ' ' {
			return '-'
		}
		return r
	}, name)
	// A trailing dot or space is silently dropped by Windows, which would make
	// the committed name and the name in the note's link two different strings.
	return strings.TrimRight(replaced, ". ")
}

// capFilename shortens a name to filenameMaxLen bytes, keeping the extension.
//
// The extension is what tells a browser, an editor, and this server's own media
// handler what the bytes are, so it survives and the stem gives way. Truncation
// is on rune boundaries: half a rune is invalid UTF-8, which the caller already
// refused at the top of the file and must not reintroduce here.
func capFilename(name string) string {
	if len(name) <= filenameMaxLen {
		return name
	}
	ext := path.Ext(name)
	if len(ext) > extMaxLen {
		ext = ""
	}
	stem := strings.TrimSuffix(name, ext)
	for len(stem)+len(ext) > filenameMaxLen && stem != "" {
		_, size := utf8.DecodeLastRuneInString(stem)
		stem = stem[:len(stem)-size]
	}
	return strings.TrimRight(stem, ". ") + ext
}

// destinationCandidates lists, in order, the vault-relative destinations an
// upload of filename may be committed to.
//
// The first is the name the user chose. The rest exist because that name may
// already be taken — by a file in the vault, or by another upload in flight —
// and the alternative to disambiguating is either overwriting somebody's file or
// refusing an upload over a clash the user cannot see. `photo.png` becomes
// `photo-1.png`, and past a small handful of those the upload id's own prefix is
// appended, which is unique by construction and ends the search. The numbered
// tries come first only because they are the ones a person would have chosen.
func destinationCandidates(dir, filename, id string) []string {
	ext := path.Ext(filename)
	if len(ext) > extMaxLen {
		ext = ""
	}
	stem := strings.TrimSuffix(filename, ext)
	candidates := make([]string, 0, numberedCandidates+2)
	candidates = append(candidates, path.Join(dir, filename))
	for i := 1; i <= numberedCandidates; i++ {
		candidates = append(candidates, path.Join(dir, fmt.Sprintf("%s-%d%s", stem, i, ext)))
	}
	return append(candidates, path.Join(dir, stem+"-"+id[:8]+ext))
}

// numberedCandidates is how many `-1`, `-2`, … tries are made before falling
// back to the id-suffixed name. It is small because the fallback is not a
// degradation — it is a guaranteed-free name — and a long search would mean
// stat-ing the destination directory once per try for no benefit.
const numberedCandidates = 8

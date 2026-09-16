package vaultsync

import (
	"path"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
)

// Class is what the portable-content inclusion policy says about one path.
type Class string

const (
	// ClassPortable is vault content that describes the vault rather than a
	// machine: notes, attachments, templates, and the portable configuration.
	ClassPortable Class = "portable"
	// ClassPrivate is a real path inside the vault that must not travel.
	ClassPrivate Class = "private"
	// ClassMalformed is not a usable vault-relative path at all.
	ClassMalformed Class = "malformed"
)

// syncStateDir is the vault-relative directory holding this vault's
// synchronization state and its two staging channels. It is derived from
// config.EnrollmentMarker rather than restated, so the path the configuration
// loader probes and the path this package owns cannot drift apart.
var syncStateDir = path.Dir(config.EnrollmentMarker)

// stateRoot is the top-level directory BrainNotes keeps its own state under,
// `.bnotes`. Like syncStateDir it is derived from config.EnrollmentMarker rather
// than restated — and here the derivation is what makes the exclusion hold
// rather than tidiness. This is the rule that refuses to send the enrollment
// record, and the record's own path is derived. A literal beside a derived
// EnrollmentPath would let the two drift silently: move the marker out from
// under `.bnotes/` and EnrollmentPath follows it while Classify stays behind, at
// which point this machine's device identity becomes portable content.
var stateRoot = firstComponent(config.EnrollmentMarker)

// firstComponent is the leading slash-separated element of a vault-relative
// path, or the whole path when it has only one.
func firstComponent(p string) string {
	if cut := strings.Index(p, "/"); cut >= 0 {
		return p[:cut]
	}
	return p
}

// privateRoots are top-level names excluded wholesale from the portable scope.
//
// The three literals match internal/vaultgit's exclusions for the same reasons —
// regenerable caches and BrainNotes' own local backups. stateRoot is the one
// that makes this policy different, and it is the whole point of having a
// separate policy: `.bnotes/` is excluded *entirely*, with no allowlist and no
// journal exception. The content history includes `.bnotes/brief.json`,
// `.bnotes/cockpit.json`, `.bnotes/recall.json`, and every entry under
// `.bnotes/journal/`, and it is right to: they are how a bad day is undone on
// the machine that had it. None of them describe the vault. Sending one to a
// peer would hand that peer this machine's reading activity, its daily
// selections, and undo manifests naming operations it never performed — and,
// since the outgoing channel itself lives under `.bnotes/sync/`, would package
// the packages.
var privateRoots = foldedNames(stateRoot, ".codegraph", ".vault-cache", "backups")

// privateComponents are names excluded wherever they appear, at any depth.
//
// `.DS_Store` is a Finder sidecar that says nothing about the note beside it and
// differs on every machine that opened the folder. `.git` is here rather than in
// privateRoots because internal/vaultgit excludes it at any depth too, and this
// policy is required to stay *inside* that one: a `Projects/repo/.git/config`
// this package agreed to send would be written by the receiver into a working
// tree whose content history cannot cover it, which is an applied change nothing
// can undo — the one outcome receipts exist to prevent.
var privateComponents = foldedNames(".DS_Store", ".git")

// foldedNames builds a set keyed the way a path component is compared, so a
// name spelled `.GIT` or `.Bnotes` on the case-insensitive replica cannot walk
// past an exclusion written in its canonical spelling.
func foldedNames(names ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[CollisionKey(name)] = struct{}{}
	}
	return set
}

// scratchPrefixes are the sibling temp-file prefixes internal/vault creates next
// to a destination anywhere in the vault. They are in-flight state for a write
// that has not landed, so sending one would replicate a half-written note.
var scratchPrefixes = []string{".note-", ".txn-"}

// Classify judges one vault-relative slash-separated path against the
// portable-content inclusion policy.
//
// It is total and side-effect free: it stats nothing, reads no configuration,
// and consults no repository, so the same path gets the same answer on every
// device. That is a requirement rather than a convenience — two replicas that
// disagreed about whether a path is portable would each be certain the other
// was sending it garbage.
//
// The three answers are distinct because the remedies are. A malformed path is
// a package to refuse outright; a private path is a package that was built by a
// producer applying a different policy; a portable path is content.
func Classify(rel string) Class {
	rel = strings.TrimSpace(rel)
	if malformedRelPath(rel) {
		return ClassMalformed
	}
	parts := strings.Split(rel, "/")
	switch {
	case malformedComponent(parts):
		return ClassMalformed
	case privateComponent(parts):
		return ClassPrivate
	default:
		return ClassPortable
	}
}

// malformedComponent reports whether any component is one path.Clean would have
// normalized away. malformedRelPath's Clean comparison already covers every such
// path, so this is a second statement of the same refusal rather than the only
// one: a traversal component must never reach the private/portable question,
// whatever a future edit does to the check above it.
func malformedComponent(parts []string) bool {
	for _, part := range parts {
		switch part {
		case "", ".", "..":
			return true
		}
	}
	return false
}

// privateComponent reports whether a well-formed path is excluded from the
// portable scope, by a name anywhere in it, by its top-level directory, or by
// what its last component is.
//
// Every comparison here goes through CollisionKey rather than through raw
// bytes. An exclusion that matched only the canonical spelling would be an
// exclusion the case-insensitive replica does not have: `.Bnotes/sync/
// enrollment.json` created on the Pi is a different string and the same file on
// the Mac, so a byte-exact check would let this machine's identity, epoch and
// peer list travel and then land on the real record.
func privateComponent(parts []string) bool {
	for _, part := range parts {
		if _, private := privateComponents[CollisionKey(part)]; private {
			return true
		}
	}
	if _, private := privateRoots[CollisionKey(parts[0])]; private {
		return true
	}
	// The machine-local configuration overlay names this machine's editor,
	// listen address, and agent command. Only at the root, because
	// brainnotes.toml is the vault marker only at the root and a note filed
	// under that name deeper in the tree is ordinary content.
	if len(parts) == 1 && CollisionKey(parts[0]) == CollisionKey(config.LocalFileName) {
		return true
	}
	return isScratchName(parts[len(parts)-1])
}

// Portable reports whether rel may travel between enrolled devices.
func Portable(rel string) bool { return Classify(rel) == ClassPortable }

// classifyError turns a path verdict into the sentinel a package validator
// returns, or nil when the path is portable.
func classifyError(rel string) error {
	switch Classify(rel) {
	case ClassMalformed:
		return ErrPathTraversal
	case ClassPrivate:
		return ErrPrivatePath
	case ClassPortable:
		return nil
	default:
		return ErrPrivatePath
	}
}

// malformedRelPath reports whether rel is not a usable vault-relative path:
// empty, absolute, a Windows-style rooted or drive-qualified path, carrying a
// NUL, or carrying a component path.Clean would have normalized away.
//
// Backslash is judged rather than translated for interior use — it is a legal
// character in a POSIX filename — but a leading one is refused, because a path
// that is absolute on the device that will apply it is not vault-relative
// anywhere.
func malformedRelPath(rel string) bool {
	switch {
	case rel == "":
		return true
	case strings.ContainsRune(rel, 0):
		return true
	case strings.HasPrefix(rel, "/"), strings.HasPrefix(rel, `\`):
		return true
	case windowsDriveRooted(rel):
		return true
	case rel != path.Clean(rel):
		return true
	default:
		return false
	}
}

// windowsDriveRooted reports whether rel begins with a drive letter, as in
// `C:/notes`. Such a path is relative on this platform and absolute on the
// other one, which is the worst of the two.
func windowsDriveRooted(rel string) bool {
	if len(rel) < 2 || rel[1] != ':' {
		return false
	}
	c := rel[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isScratchName reports whether base is one of the transaction scratch or
// custody temp files. The prefixes are matched rather than the full shapes
// because the hex body varies and a future prefix variant should stay excluded
// by default, and they are matched against the folded name for the same reason
// every other exclusion here is.
func isScratchName(base string) bool {
	folded := CollisionKey(base)
	if !strings.HasSuffix(folded, ".tmp") {
		return false
	}
	for _, prefix := range scratchPrefixes {
		if strings.HasPrefix(folded, prefix) {
			return true
		}
	}
	return false
}

// CollisionKey is the identity a path has on a filesystem that is insensitive
// to case and to Unicode normalization form. Two portable paths sharing a key
// are the same file on such a filesystem even though they are different strings
// here.
//
// This is not a theoretical concern for this deployment: the Pi's ext4 stores
// `Café.md` and `café.md` as two notes and preserves whichever normalization
// form the writer used, while the Mac's default APFS volume stores one note and
// returns it under whichever form was written first. A package built on Linux
// that changes both therefore applies on macOS as two writes to one file, and
// the second silently wins.
//
// The order is normalize, fold, normalize again, and each step earns its place.
//
// The fold is a real Unicode case fold rather than a lowercase mapping, because
// the two disagree exactly where a filesystem does not: strings.ToLower maps
// U+1E9E (capital sharp s) to U+00DF rather than to `ss`, so it would give
// `Strasse.md` and the U+1E9E spelling of it two keys where a case-insensitive
// volume holds one file.
//
// The trailing normalization is there because folding does not preserve NFC, and
// it earns its place twice over.
//
// It keeps the key in NFC. Twenty-four code points that are already NFC fold to
// a sequence that is not — U+0390, whose fold is the decomposed U+03B9 U+0308
// U+0301, among them — so a key that stopped at the fold would be neither
// idempotent nor comparable against the NFC paths every other layer here holds.
// TestCollisionKeyIsIdempotent pins that, and it needs only one code point.
//
// It also decides verdicts, which needs two. A fold can move a base character to
// one that has a precomposed form with the mark already following it: U+017F
// (long s) folds to U+0073, so U+017F U+0301 folds to U+0073 U+0301 — still two
// runes — while U+015A folds to the single rune U+015B. Those are one file on a
// case-insensitive volume, and only the trailing step composes the first into
// U+015B so the two collide. Over BMP base characters crossed with the whole
// U+0300-U+036F block, six groups merge only here;
// TestCollisionKeyMergesAFoldThatComposesWithTheNextMark pins this one.
//
// The *leading* normalization is a different job and does not cover that: it
// makes the caller's choice of composed or decomposed spelling irrelevant, on
// both sides of a comparison, before the fold is reached. What it cannot undo is
// a composition the fold itself makes newly possible. A probe over single code
// points sees only the first of these two jobs, and concludes the trailing step
// decides nothing.
//
// This is also the comparison every private-path rule above uses, so an
// exclusion written once in its canonical spelling covers every spelling of it.
func CollisionKey(rel string) string {
	return textnorm.NFC(textnorm.Fold(textnorm.NFC(rel)))
}

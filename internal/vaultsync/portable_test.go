package vaultsync

import (
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/textnorm"
	"github.com/sglavoie/brainnotes/internal/vaultgit"
)

// The Unicode fixtures are written as \uXXXX escapes rather than as literal
// characters, deliberately: the point of every one of them is a distinction
// between byte sequences that render identically, and an editor or a review tool
// that normalized this file would rewrite a pair into the same bytes and turn
// the assertion into a tautology that still passes.
const (
	composedNote            = "Caf\u00e9.md"          // e-acute as one rune, what an editor writes
	decomposedNote          = "Cafe\u0301.md"         // e plus combining acute, what macOS stores
	sharpSNote              = "Stra\u1e9ee.md"        // capital sharp s, folds to "ss"
	smallSharpSNote         = "stra\u00dfe.md"        // small sharp s, folds to "ss" as well
	dottedINote             = "\u0130stanbul.md"      // capital I with dot above
	foldedINote             = "i\u0307stanbul.md"     // i plus combining dot above, its fold
	dialytikaNote           = "\u0390.md"             // composed; its fold is not composed
	dialytikaDecomposedNote = "\u03b9\u0308\u0301.md" // the same note, spelled as the fold
	longSAcuteNote          = "\u017f\u0301.md"       // long s + combining acute; folds to s + that acute
	capSAcuteNote           = "\u015a.md"             // the precomposed spelling; folds to one rune
)

// classifyCase is one row of the policy table. It is a named type with a
// package-level table rather than a literal inside TestClassify because the
// corpus is reused: TestPortableImpliesTracked quantifies over every path here,
// so a row added to describe a new exclusion is automatically also checked
// against internal/vaultgit's scope.
type classifyCase struct {
	name string
	path string
	want Class
}

var classifyCases = []classifyCase{
	{"a note", "Alpha.md", ClassPortable},
	{"a nested note", "Notes/Gamma.md", ClassPortable},
	{"an attachment", "attachments/diagram.png", ClassPortable},
	{"a template", "templates/daily.md", ClassPortable},
	{"the portable configuration", config.FileName, ClassPortable},
	{"a note named like the overlay deeper in the tree", "Notes/" + config.LocalFileName, ClassPortable},
	{"a dotfile that is not a known local name", ".gitignore", ClassPortable},

	{"the machine-local overlay at the root", config.LocalFileName, ClassPrivate},
	{"the enrollment record", config.EnrollmentMarker, ClassPrivate},
	{"an outgoing package", ".bnotes/sync/outgoing/pkg-x/manifest.json", ClassPrivate},
	{"the brief state the content history does track", ".bnotes/brief.json", ClassPrivate},
	{"the cockpit state the content history does track", ".bnotes/cockpit.json", ClassPrivate},
	{"the recall state the content history does track", ".bnotes/recall.json", ClassPrivate},
	{"a journal entry the content history does track", ".bnotes/journal/2026-01-01T00-00-00Z-abc/manifest.json", ClassPrivate},
	{"the generated index", ".bnotes/index/notes.json", ClassPrivate},
	{"the repository itself", ".git/config", ClassPrivate},
	{"a local backup", "backups/2026-01-01.tar.gz", ClassPrivate},
	{"a code index", ".codegraph/graph.db", ClassPrivate},
	{"the pre-migration cache", ".vault-cache/x", ClassPrivate},
	{"a Finder sidecar", "Notes/.DS_Store", ClassPrivate},
	{"a note transaction scratch file", "Notes/.note-abc123.tmp", ClassPrivate},
	{"a transaction scratch file", ".txn-abc123.tmp", ClassPrivate},

	// A case-insensitive replica answers to every spelling of a name, so an
	// exclusion that matched only the canonical one would be an exclusion the
	// Mac does not have. Each row below is a real file the Mac would open as
	// the excluded one.
	{"the enrollment record under a shouted root", ".BNOTES/sync/enrollment.json", ClassPrivate},
	{"the state directory in mixed case", ".BNotes/sync/outgoing/pkg-x/manifest.json", ClassPrivate},
	{"the repository in mixed case", ".Git/config", ClassPrivate},
	{"a local backup directory in mixed case", "Backups/2026-01-01.tar.gz", ClassPrivate},
	{"the overlay shouted at the root", "BRAINNOTES.LOCAL.TOML", ClassPrivate},
	{"a Finder sidecar in mixed case", "Notes/.ds_store", ClassPrivate},
	{"a scratch file in mixed case", "Notes/.NOTE-ABC123.TMP", ClassPrivate},

	// .git is excluded at any depth rather than only at the root, because
	// internal/vaultgit excludes it at any depth and this policy is required to
	// stay inside that one. A nested repository's config is the case: writing
	// it into a working tree whose content history cannot cover it is an
	// applied change nothing can undo.
	{"a nested repository", "Projects/repo/.git/config", ClassPrivate},
	{"a nested repository object", "Projects/repo/.git/objects/ab/cdef", ClassPrivate},
	{"a nested repository in mixed case", "Projects/repo/.GIT/config", ClassPrivate},
	{"a note inside a directory a repository lives beside", "Projects/repo/README.md", ClassPortable},

	{"empty", "", ClassMalformed},
	{"only whitespace", "   ", ClassMalformed},
	{"absolute", "/etc/passwd", ClassMalformed},
	{"parent traversal", "../outside.md", ClassMalformed},
	{"interior traversal", "Notes/../../outside.md", ClassMalformed},
	{"a trailing parent component", "Notes/..", ClassMalformed},
	{"a current-directory component", "Notes/./Gamma.md", ClassMalformed},
	{"an unnormalized double slash", "Notes//Gamma.md", ClassMalformed},
	{"a trailing slash", "Notes/", ClassMalformed},
	{"a windows rooted path", `\Notes\Gamma.md`, ClassMalformed},
	{"a drive-qualified path", "C:/Notes/Gamma.md", ClassMalformed},
	{"a path carrying NUL", "Notes/Gamma\x00.md", ClassMalformed},
}

func TestClassify(t *testing.T) {
	t.Parallel()

	for _, tc := range classifyCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Classify(tc.path); got != tc.want {
				t.Errorf("Classify(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// TestPortableScopeIsNarrowerThanTheGitScope is the pin on the one decision the
// expansion notes for this session flagged: the content history's tracked scope
// is not the transport's allowlist, and the paths where they differ are exactly
// the machine-local material that must not travel.
//
// If somebody later implements the transport allowlist by calling
// vaultgit.InScope, this fails and names the file that would have been sent.
func TestPortableScopeIsNarrowerThanTheGitScope(t *testing.T) {
	t.Parallel()

	trackedButNotPortable := []string{
		".bnotes/brief.json",
		".bnotes/cockpit.json",
		".bnotes/recall.json",
		".bnotes/journal/2026-01-01T00-00-00Z-abc/manifest.json",
		".bnotes/journal/2026-01-01T00-00-00Z-abc/snapshots/deadbeef.snap",
	}
	for _, path := range trackedButNotPortable {
		if !vaultgit.InScope(path) {
			t.Fatalf("%q is no longer tracked by the content history; this test's premise has changed", path)
		}
		if Portable(path) {
			t.Errorf("%q is portable, but it is machine-local activity that must not reach a peer", path)
		}
	}

	// The other direction has to hold too, or the narrowing would be hiding a
	// disagreement about ordinary content rather than expressing one about
	// machine-local state.
	for _, path := range []string{"Alpha.md", "Notes/Gamma.md", config.FileName, "attachments/x.png"} {
		if !vaultgit.InScope(path) || !Portable(path) {
			t.Errorf("%q should be both tracked and portable, got tracked=%v portable=%v", path, vaultgit.InScope(path), Portable(path))
		}
	}

	// And the overlay is excluded by both, for the same reason stated twice.
	if vaultgit.InScope(config.LocalFileName) || Portable(config.LocalFileName) {
		t.Errorf("%q must be excluded from both scopes", config.LocalFileName)
	}
}

// TestPortableImpliesTracked states the narrowing as an implication over a
// corpus rather than as a handful of named examples: every path this package
// agrees to send must be one the receiver's content history will track.
//
// The named examples in the test above show the difference is real. This shows
// there is no path on the *other* side of it — a file that travels and then
// lands somewhere no snapshot covers. That file would be an applied change with
// no undo, which is the one outcome the receipt protocol exists to prevent, and
// it would arrive as a policy gap rather than as an error anybody sees.
func TestPortableImpliesTracked(t *testing.T) {
	t.Parallel()

	corpus := []string{}
	for _, tc := range classifyCases {
		corpus = append(corpus, tc.path)
	}
	// Names the exclusion lists mention, in every position a path can put them,
	// so the implication is checked where the two policies are most likely to
	// have been written with different shapes — one at the root, one at depth.
	for _, name := range []string{".bnotes", ".git", ".DS_Store", ".codegraph", ".vault-cache", "backups", config.FileName, config.LocalFileName} {
		corpus = append(corpus,
			name,
			name+"/child.md",
			"Notes/"+name,
			"Notes/"+name+"/child.md",
			"a/b/c/"+name+"/child.md",
		)
	}
	corpus = append(corpus,
		"Alpha.md",
		"Notes/Gamma.md",
		"attachments/diagram.png",
		"templates/daily.md",
		"Projects/repo/README.md",
		composedNote,
		decomposedNote,
	)

	checked := 0
	for _, path := range corpus {
		if !Portable(path) {
			continue
		}
		checked++
		if !vaultgit.InScope(path) {
			t.Errorf("%q is portable but the content history does not track it: it would apply with no snapshot covering it", path)
		}
	}
	// Without this the test passes on a Portable that answers false to everything.
	if checked < 10 {
		t.Fatalf("only %d paths in the corpus were portable; the implication was nearly vacuous", checked)
	}
}

// TestSyncStateDirIsDerivedFromTheEnrollmentMarker pins the seam between this
// package and the configuration loader. internal/config probes a hardcoded path
// to decide whether a vault is enrolled; this package owns what lives there. A
// move that updated only one of them would leave the loader reporting every
// vault unenrolled with no error anywhere.
func TestSyncStateDirIsDerivedFromTheEnrollmentMarker(t *testing.T) {
	t.Parallel()

	if got, want := StateDir(), ".bnotes/sync"; got != want {
		t.Errorf("StateDir() = %q, want %q", got, want)
	}
	if got := EnrollmentPath("/vault"); got != "/vault/.bnotes/sync/enrollment.json" {
		t.Errorf("EnrollmentPath = %q", got)
	}
	if Classify(config.EnrollmentMarker) != ClassPrivate {
		t.Error("the enrollment record must never be portable content")
	}

	// And the exclusion must be by *root* rather than by the marker's own path.
	// Asking privateRoots whether it contains stateRoot would prove nothing — it
	// is built out of stateRoot — so ask instead about paths that are not the
	// marker. An implementation that special-cased the marker's exact path would
	// pass the assertion above and fail both of these. (That the root is
	// *derived* rather than hardcoded is the separate claim, and it is the
	// literal StateDir() check above that pins it.)
	for _, p := range []string{
		StateDir() + "/receipts/0000.json",
		stateRoot + "/journal/0001.json",
	} {
		if Classify(p) != ClassPrivate {
			t.Errorf(
				"%q lives under %q and must be private; the exclusion is matching a path literal rather than the state root",
				p, stateRoot,
			)
		}
	}
}

func TestCollisionKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		a, b      string
		wantEqual bool
	}{
		{"identical", "Alpha.md", "Alpha.md", true},
		{"case only", "Alpha.md", "alpha.md", true},
		{"case in a directory", "Notes/Alpha.md", "notes/Alpha.md", true},
		{"normalization only", composedNote, decomposedNote, true},
		{"normalization and case", composedNote, "caf\u00e9.md", true},
		{"decomposed and lowercased", composedNote, "cafe\u0301.md", true},

		// Folding rather than lowercasing is what makes these one key. The simple
		// lowercase mapping is rune-to-rune, so it would map U+1E9E to U+00DF and
		// U+0130 to U+0069 — two keys each, for one file on the Mac.
		{"capital sharp s against its expansion", sharpSNote, "Strasse.md", true},
		{"small sharp s against its expansion", smallSharpSNote, "strasse.md", true},
		{"dotted capital I against its expansion", dottedINote, foldedINote, true},

		// A polytonic Greek spelling that differs only by normalization, kept
		// because it is the one whose *fold* is also unnormalized — but the row
		// passes on the leading normalization like every other pair above it, not
		// on the trailing one.
		{"a fold that decomposes", dialytikaNote, dialytikaDecomposedNote, true},

		// This one does need the trailing normalization, and it is the only row
		// here that does. Both spellings are already NFC, so the leading step is
		// a no-op on them. They agree because folding U+017F to `s` puts a base
		// in front of the acute that has a precomposed form with it — a
		// composition the input could not make — which the third step then
		// builds, reaching the U+015B the precomposed spelling folds straight to.
		// TestCollisionKeyMergesAFoldThatComposesWithTheNextMark makes the
		// dependence explicit rather than incidental.
		{"a fold that produces a base the next mark composes with", longSAcuteNote, capSAcuteNote, true},

		{"different notes", "Alpha.md", "Beta.md", false},
		{"different directories", "Notes/Alpha.md", "Other/Alpha.md", false},

		// Folding is not transliteration: it collapses case, never diacritics. A
		// row differing at an ASCII letter would hold for the identity function
		// too and would say nothing, so the difference here is the accent itself.
		{"folding is not transliteration", composedNote, "Cafe.md", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := CollisionKey(tc.a) == CollisionKey(tc.b); got != tc.wantEqual {
				t.Errorf("CollisionKey(%q)==CollisionKey(%q) = %v, want %v", tc.a, tc.b, got, tc.wantEqual)
			}
		})
	}
}

// TestCollisionKeyIsIdempotent is one of the two things the third step of the
// key buys: the key is a normal form in its own right, so feeding one back
// through changes nothing and it can be compared against the NFC paths every
// other layer in this repository holds. Folding does not preserve NFC — the fold
// of U+0390 is the decomposed U+03B9 U+0308 U+0301, which NFC composes straight
// back — so a fold-and-stop key would be neither.
//
// The other thing it buys is verdicts, which is
// TestCollisionKeyMergesAFoldThatComposesWithTheNextMark below. Neither test
// subsumes the other: this one is about the shape of a single key, that one is
// about two paths reaching the same key.
func TestCollisionKeyIsIdempotent(t *testing.T) {
	t.Parallel()

	corpus := []string{
		"Alpha.md",
		composedNote,
		decomposedNote,
		dottedINote,
		foldedINote,
		sharpSNote,
		smallSharpSNote,
		dialytikaNote,
		dialytikaDecomposedNote,
	}
	for _, path := range corpus {
		once := CollisionKey(path)
		if twice := CollisionKey(once); twice != once {
			t.Errorf("CollisionKey(%q) is not idempotent: %q then %q", path, once, twice)
		}
	}

	// Idempotence alone would still hold for a key that folded and stopped on
	// most inputs, so state the premise directly: on this one the trailing
	// normalization changes the bytes, which is what there is to demonstrate.
	stopped := textnorm.Fold(textnorm.NFC(dialytikaNote))
	if stopped == CollisionKey(dialytikaNote) {
		t.Errorf(
			"folding %q left NFC intact (%q); the trailing normalization is no longer load-bearing and this test proves nothing",
			dialytikaNote, stopped,
		)
	}
}

// TestCollisionKeyMergesAFoldThatComposesWithTheNextMark is the case that makes
// the trailing normalization load-bearing for the answer and not only for the
// shape of the key.
//
// The tempting argument is that normalizing both paths on the way in has already
// made every spelling agree, leaving the third step with nothing to decide. It
// takes two code points to see that this is false — and the mechanism is not
// that the fold decomposes anything. U+017F (long s) folds to `s`, one rune for
// one rune. It is that `s` has a precomposed form with the acute already
// following it and U+017F does not, so folding makes available a composition the
// input could not make: U+017F plus a combining acute folds to two runes, while
// the precomposed U+015A folds straight to the single rune U+015B. A
// case-insensitive volume holds those two names as one file. Only the trailing
// normalization makes this package say so.
//
// Deliberately asserted in both directions, because the interesting half is the
// negative one: a fold-and-stop key would report these as two paths and let a
// producer publish a package that silently overwrites on the Mac.
func TestCollisionKeyMergesAFoldThatComposesWithTheNextMark(t *testing.T) {
	t.Parallel()

	// The premise: the leading normalization cannot be what merges them, because
	// both spellings are already NFC before it runs.
	for _, in := range []string{longSAcuteNote, capSAcuteNote} {
		if got := textnorm.NFC(in); got != in {
			t.Fatalf("fixture is stale: %q is not already NFC (NFC gives %q)", in, got)
		}
	}

	if CollisionKey(longSAcuteNote) != CollisionKey(capSAcuteNote) {
		t.Errorf(
			"CollisionKey(%q) = %q and CollisionKey(%q) = %q; these are one file on a case-insensitive volume",
			longSAcuteNote, CollisionKey(longSAcuteNote), capSAcuteNote, CollisionKey(capSAcuteNote),
		)
	}
	if textnorm.Fold(textnorm.NFC(longSAcuteNote)) == textnorm.Fold(textnorm.NFC(capSAcuteNote)) {
		t.Error("folding alone already merges the pair; this case no longer shows the trailing normalization deciding anything")
	}
}

func TestValidPayloadNameRefusesAPath(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "../escape", "/abs", "sub/dir.bin", ".DS_Store"} {
		if err := validPayloadName(name); err == nil {
			t.Errorf("validPayloadName(%q) accepted a name that is not a single component", name)
		}
	}
	if err := validPayloadName("deadbeef.bin"); err != nil {
		t.Errorf("validPayloadName rejected an ordinary payload name: %v", err)
	}
}

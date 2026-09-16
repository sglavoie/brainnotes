package vaultsync

import (
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
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

		// The fold of U+0390 is the decomposed U+03B9 U+0308 U+0301, so these two
		// spellings reach one key only because the key normalizes *after* folding
		// and not only before.
		{"a fold that decomposes", dialytikaNote, dialytikaDecomposedNote, true},

		{"different notes", "Alpha.md", "Beta.md", false},
		{"different directories", "Notes/Alpha.md", "Other/Alpha.md", false},
		{"folding is not transliteration", "Stra\u00dfe.md", "Strasze.md", false},
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

// TestCollisionKeyIsIdempotent is the reason the key normalizes after folding
// rather than before. Folding does not preserve NFC: the fold of U+0390 is the
// decomposed U+03B9 U+0308 U+0301, which NFC composes straight back. A key that
// stopped at the fold would therefore depend on which form the caller happened
// to hold, so feeding a key back through must be a no-op or the key is not one.
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

	// Idempotence alone would still hold for a key that folded and stopped, so
	// state the premise directly: on this input the trailing normalization
	// changes the bytes, which is the whole reason the third step exists.
	stopped := textnorm.Fold(textnorm.NFC(dialytikaNote))
	if stopped == CollisionKey(dialytikaNote) {
		t.Errorf(
			"folding %q left NFC intact (%q); the trailing normalization is no longer load-bearing and this test proves nothing",
			dialytikaNote, stopped,
		)
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

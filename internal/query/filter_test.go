package query

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// newVault writes files into a temp dir and returns an opened *vault.Vault
// plus a map from vault-relative path → loaded note for direct assertion.
func newVault(t *testing.T, files map[string]string) (*vault.Vault, map[string]*vault.Note) {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir parent of %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	v, err := vault.Open(&config.Config{Vault: dir})
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	notes, err := v.Notes()
	if err != nil {
		t.Fatalf("notes: %v", err)
	}
	out := make(map[string]*vault.Note, len(notes))
	for _, n := range notes {
		rel, err := filepath.Rel(v.Root, n.Path)
		if err != nil {
			t.Fatalf("rel: %v", err)
		}
		out[filepath.ToSlash(rel)] = n
	}
	return v, out
}

func TestTagPredicate(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: [foo, bar]\n---\nbody\n",
		"b.md": "---\ntags: [bar]\n---\nbody\n",
		"c.md": "---\ntitle: c\n---\nbody\n",
		"d.md": "---\ntags: Foo\n---\nbody (scalar tag)\n",
	})
	p := Tag("foo")
	cases := map[string]bool{"a.md": true, "b.md": false, "c.md": false, "d.md": true}
	for path, want := range cases {
		if got := p(notes[path]); got != want {
			t.Errorf("Tag(foo)(%s) = %v, want %v", path, got, want)
		}
	}
}

// TestTagPredicateMatchesNonStringListItems asserts that tag list items whose
// YAML parsed type is not string (e.g. an int from `tags: [2026]` after
// `bn set tags+=2026` auto-coerces the literal) are still matched by the Tag
// predicate. Before the fix, non-string list items were silently skipped, so a
// freshly set numeric tag could never be queried back via `bn find --tag`.
func TestTagPredicateMatchesNonStringListItems(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"int.md":   "---\ntags:\n  - 2026\n---\nbody\n",
		"bool.md":  "---\ntags:\n  - true\n---\nbody\n",
		"date.md":  "---\ntags:\n  - 2026-05-19\n---\nbody\n",
		"mixed.md": "---\ntags:\n  - foo\n  - 7\n---\nbody\n",
	})

	if !Tag("2026")(notes["int.md"]) {
		t.Errorf("Tag(2026) should match a note whose tags list holds the int 2026")
	}
	if !Tag("true")(notes["bool.md"]) {
		t.Errorf("Tag(true) should match a note whose tags list holds the bool true")
	}
	if !Tag("2026-05-19")(notes["date.md"]) {
		t.Errorf("Tag(2026-05-19) should match a note whose tags list holds a YAML date scalar")
	}
	if !Tag("7")(notes["mixed.md"]) {
		t.Errorf("Tag(7) should match a tags list mixing strings and ints")
	}
	if !Tag("foo")(notes["mixed.md"]) {
		t.Errorf("Tag(foo) should still match the string entry of a mixed list")
	}
	if Tag("8")(notes["mixed.md"]) {
		t.Errorf("Tag(8) should not match a list that contains only 7")
	}
}

// TestTagPredicateScalarNonString asserts the same behaviour for a scalar
// (non-list) tags value: `tags: 2026` (a YAML int scalar) should still match
// `--tag 2026`. The GetString fast path returned false for non-strings before
// the fix, so any auto-coerced scalar tag was unreachable.
func TestTagPredicateScalarNonString(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"int.md":  "---\ntags: 2026\n---\nbody\n",
		"bool.md": "---\ntags: false\n---\nbody\n",
	})
	if !Tag("2026")(notes["int.md"]) {
		t.Errorf("Tag(2026) should match a scalar int tag value")
	}
	if !Tag("false")(notes["bool.md"]) {
		t.Errorf("Tag(false) should match a scalar bool tag value")
	}
}

func TestCategoryPredicate(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ncategories:\n  - \"[[Books]]\"\n---\nbody\n",
		"b.md": "---\ncategories:\n  - \"[[People]]\"\n  - \"[[Books]]\"\n---\nbody\n",
		"c.md": "---\ncategories:\n  - \"[[People]]\"\n---\nbody\n",
		"d.md": "---\ntitle: d\n---\nbody\n",
	})
	p := Category("books")
	cases := map[string]bool{"a.md": true, "b.md": true, "c.md": false, "d.md": false}
	for path, want := range cases {
		if got := p(notes[path]); got != want {
			t.Errorf("Category(books)(%s) = %v, want %v", path, got, want)
		}
	}
}

func TestStatusAndType(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\nstatus: Reading\ntype: Goal\n---\nbody\n",
		"b.md": "---\nstatus: Completed\nkind: Project\n---\nbody\n",
		"c.md": "---\ntitle: c\n---\nbody\n",
	})

	if !Status("reading")(notes["a.md"]) {
		t.Errorf("Status(reading)(a) = false, want true")
	}
	if Status("reading")(notes["b.md"]) {
		t.Errorf("Status(reading)(b) = true, want false")
	}
	if !Type("goal")(notes["a.md"]) {
		t.Errorf("Type(goal)(a) = false, want true")
	}
	if !Type("project")(notes["b.md"]) {
		t.Errorf("Type(project)(b) = false, want true (kind fallback)")
	}
	if Type("goal")(notes["c.md"]) {
		t.Errorf("Type(goal)(c) = true, want false")
	}
}

func TestHasMissing(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\nstatus: Reading\n---\nbody\n",
		"b.md": "---\ntitle: b\n---\nbody\n",
	})
	if !Has("status")(notes["a.md"]) {
		t.Errorf("Has(status)(a) = false")
	}
	if Has("status")(notes["b.md"]) {
		t.Errorf("Has(status)(b) = true")
	}
	if !Missing("status")(notes["b.md"]) {
		t.Errorf("Missing(status)(b) = false")
	}
	if Missing("status")(notes["a.md"]) {
		t.Errorf("Missing(status)(a) = true")
	}
}

func TestFolderPredicate(t *testing.T) {
	v, notes := newVault(t, map[string]string{
		"root.md":            "# r\n",
		"People/a.md":        "# a\n",
		"People/Nested/b.md": "# b\n",
		"Other/c.md":         "# c\n",
	})
	p := Folder("People", v.Root)
	cases := map[string]bool{
		"root.md":            false,
		"People/a.md":        true,
		"People/Nested/b.md": true,
		"Other/c.md":         false,
	}
	for path, want := range cases {
		if got := p(notes[path]); got != want {
			t.Errorf("Folder(People)(%s) = %v, want %v", path, got, want)
		}
	}

	// empty folder matches every note
	if !Folder("", v.Root)(notes["root.md"]) {
		t.Errorf("Folder(\"\") should match root note")
	}
}

// TestFolderPredicateNormalizesDotSlash asserts that `--folder ./References`
// and `--folder .` are normalized. Before the fix, `./References` was compared
// verbatim against note folders like `References` and matched nothing.
func TestFolderPredicateNormalizesDotSlash(t *testing.T) {
	v, notes := newVault(t, map[string]string{
		"root.md":            "# r\n",
		"People/a.md":        "# a\n",
		"People/Nested/b.md": "# b\n",
	})
	for _, raw := range []string{"./People", "People/.", "./People/"} {
		p := Folder(raw, v.Root)
		if !p(notes["People/a.md"]) {
			t.Errorf("Folder(%q) should match People/a.md", raw)
		}
		if !p(notes["People/Nested/b.md"]) {
			t.Errorf("Folder(%q) should match People/Nested/b.md", raw)
		}
	}
	// A bare "." (or "./") should match every note, like the empty folder.
	for _, raw := range []string{".", "./"} {
		p := Folder(raw, v.Root)
		if !p(notes["root.md"]) || !p(notes["People/a.md"]) {
			t.Errorf("Folder(%q) should match every note", raw)
		}
	}
}

// TestFolderPredicateNormalizesSlashes asserts that leading slashes, trailing
// slashes, and backslashes in the user-supplied folder arg are all normalized
// to a plain forward-slash path. Before the fix, "/People" did not match
// "People/a.md" because the leading slash was retained verbatim.
func TestFolderPredicateNormalizesSlashes(t *testing.T) {
	v, notes := newVault(t, map[string]string{
		"People/a.md":        "# a\n",
		"People/Nested/b.md": "# b\n",
		"Other/c.md":         "# c\n",
	})
	for _, raw := range []string{"People", "/People", "People/", "/People/", "People\\", "\\People\\"} {
		p := Folder(raw, v.Root)
		if !p(notes["People/a.md"]) {
			t.Errorf("Folder(%q) should match People/a.md", raw)
		}
		if !p(notes["People/Nested/b.md"]) {
			t.Errorf("Folder(%q) should match People/Nested/b.md", raw)
		}
		if p(notes["Other/c.md"]) {
			t.Errorf("Folder(%q) should not match Other/c.md", raw)
		}
	}
}

func TestAgePredicate(t *testing.T) {
	now := time.Now()
	old := now.Add(-40 * 24 * time.Hour).Format("2006-01-02")
	recent := now.Add(-2 * 24 * time.Hour).Format("2006-01-02")
	today := now.Format("2006-01-02")
	_, notes := newVault(t, map[string]string{
		"old.md":    "---\ncreated: " + old + "\n---\n",
		"recent.md": "---\ncreated: " + recent + "\n---\n",
		"today.md":  "---\ncreated: " + today + "\n---\n",
		"nofm.md":   "no fm\n",
	})

	gt30 := Age(">", 30*24*time.Hour, "created")
	if !gt30(notes["old.md"]) {
		t.Errorf(">30d should match old")
	}
	if gt30(notes["recent.md"]) {
		t.Errorf(">30d should not match recent")
	}
	if gt30(notes["nofm.md"]) {
		t.Errorf(">30d should not match note without timestamp")
	}

	lt7 := Age("<", 7*24*time.Hour, "created")
	if !lt7(notes["recent.md"]) {
		t.Errorf("<7d should match recent")
	}
	if lt7(notes["old.md"]) {
		t.Errorf("<7d should not match old")
	}

	eq0 := Age("=", 0, "created")
	if !eq0(notes["today.md"]) {
		t.Errorf("=0d should match today")
	}
	if eq0(notes["recent.md"]) {
		t.Errorf("=0d should not match recent")
	}

	eq3 := Age("=", 3*24*time.Hour, "created")
	threeDays := now.AddDate(0, 0, -3).Format("2006-01-02")
	twoDays := now.AddDate(0, 0, -2).Format("2006-01-02")
	_, eqNotes := newVault(t, map[string]string{
		"three.md": "---\ncreated: " + threeDays + "\n---\n",
		"two.md":   "---\ncreated: " + twoDays + "\n---\n",
	})
	if !eq3(eqNotes["three.md"]) {
		t.Errorf("=3d should match a date 3 days ago")
	}
	if eq3(eqNotes["two.md"]) {
		t.Errorf("=3d should not match a date 2 days ago")
	}
}

// TestAgeEqualDayLocalZone verifies that bare YAML date scalars (which parse
// as midnight UTC) are reinterpreted in the local zone for `=` comparisons.
// Without this, a user east of UTC would see `=0d` miss a note created today
// (because midnight UTC today already lies more than a day in the past in their
// local zone), and a user west of UTC would see `=0d` match yesterday's note.
func TestAgeEqualDayLocalZone(t *testing.T) {
	now := time.Now()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")

	_, notes := newVault(t, map[string]string{
		"today.md":     "---\ncreated: " + today + "\n---\n",
		"yesterday.md": "---\ncreated: " + yesterday + "\n---\n",
	})

	if !Age("=", 0, "created")(notes["today.md"]) {
		t.Errorf("=0d should match today's note regardless of local zone")
	}
	if Age("=", 0, "created")(notes["yesterday.md"]) {
		t.Errorf("=0d should not match yesterday's note")
	}
	if !Age("=", 24*time.Hour, "created")(notes["yesterday.md"]) {
		t.Errorf("=1d should match yesterday's note regardless of local zone")
	}
	if Age("=", 24*time.Hour, "created")(notes["today.md"]) {
		t.Errorf("=1d should not match today's note")
	}
}

// TestAgeEqualUTCDateConvention pins down the YAML-date convention: a bare
// `YYYY-MM-DD` parses as midnight UTC, and the predicate should treat that
// instant as the same calendar day in the local zone (not the UTC day).
func TestAgeEqualUTCDateConvention(t *testing.T) {
	// Build a frontmatter time exactly as the YAML parser would: midnight UTC.
	now := time.Now()
	utcMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if !equalDayAge(utcMidnight, 0) {
		t.Errorf("equalDayAge(midnight-UTC, 0) = false; want true (today)")
	}

	// And a UTC midnight from three days ago should match =3d.
	threeBack := utcMidnight.AddDate(0, 0, -3)
	if !equalDayAge(threeBack, 3*24*time.Hour) {
		t.Errorf("equalDayAge(midnight-UTC-3d, 3d) = false; want true")
	}
	if equalDayAge(threeBack, 0) {
		t.Errorf("equalDayAge(midnight-UTC-3d, 0) = true; want false")
	}
}

func TestParseAge(t *testing.T) {
	cases := []struct {
		in     string
		op     string
		dur    time.Duration
		hasErr bool
	}{
		{">30d", ">", 30 * 24 * time.Hour, false},
		{"<7d", "<", 7 * 24 * time.Hour, false},
		{"=0d", "=", 0, false},
		{">12h", ">", 12 * time.Hour, false},
		{"<15m", "<", 15 * time.Minute, false},
		{"=45s", "=", 45 * time.Second, false},
		{"", "", 0, true},
		{"30d", "", 0, true},
		{">30x", "", 0, true},
		{">d", "", 0, true},
		{">-1d", "", 0, true},
	}
	for _, c := range cases {
		op, dur, err := ParseAge(c.in)
		if c.hasErr {
			if err == nil {
				t.Errorf("ParseAge(%q) = (%q,%v,nil); expected error", c.in, op, dur)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseAge(%q) err: %v", c.in, err)
			continue
		}
		if op != c.op || dur != c.dur {
			t.Errorf("ParseAge(%q) = (%q,%v); want (%q,%v)", c.in, op, dur, c.op, c.dur)
		}
	}
}

func TestBuildGraphBacklinks(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Apple.md":  "Body links to [[Banana]] and [[Cherry|Cherries]].\n",
		"Banana.md": "---\ncategories:\n  - \"[[Apple]]\"\n---\nBody.\n",
		"Cherry.md": "---\ntitle: c\n---\nNo links here.\n",
		"Lonely.md": "---\ntitle: l\n---\nNothing references me.\n",
	})

	all := []*vault.Note{notes["Apple.md"], notes["Banana.md"], notes["Cherry.md"], notes["Lonely.md"]}
	g := BuildGraph(all)

	if len(g.BackLinks("banana")) != 1 {
		t.Errorf("expected one referrer for Banana, got %v", g.BackLinks("banana"))
	}
	if len(g.BackLinks("cherry")) != 1 {
		t.Errorf("expected one referrer for Cherry, got %v", g.BackLinks("cherry"))
	}
	if len(g.BackLinks("apple")) != 1 {
		t.Errorf("expected one referrer for Apple (from Banana frontmatter), got %v", g.BackLinks("apple"))
	}
	if len(g.BackLinks("lonely")) != 0 {
		t.Errorf("Lonely should have no backlinks, got %v", g.BackLinks("lonely"))
	}

	orphan := Orphan(g)
	if orphan(notes["Apple.md"]) {
		t.Errorf("Apple has an incoming link; not orphan")
	}
	if !orphan(notes["Lonely.md"]) {
		t.Errorf("Lonely should be orphan")
	}
}

// TestOrphanIgnoresSelfReferences verifies that a note whose only "inbound"
// wikilink is to itself still counts as an orphan. Before the fix, Orphan
// matched any non-empty entry in idx[title], so a self-link silently kept the
// note out of the orphan set. `bn backlinks` already filters self-references;
// `--orphan` should behave consistently.
func TestOrphanIgnoresSelfReferences(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"SelfOnly.md":   "---\n---\nI link to [[SelfOnly]] only.\n",
		"Referenced.md": "---\n---\nBody.\n",
		"Referrer.md":   "---\n---\nLinks to [[Referenced]].\n",
	})
	all := []*vault.Note{notes["SelfOnly.md"], notes["Referenced.md"], notes["Referrer.md"]}
	g := BuildGraph(all)
	o := Orphan(g)

	if !o(notes["SelfOnly.md"]) {
		t.Errorf("SelfOnly should be an orphan: its only inbound link is from itself")
	}
	if o(notes["Referenced.md"]) {
		t.Errorf("Referenced has an external inbound link; not orphan")
	}
	if !o(notes["Referrer.md"]) {
		t.Errorf("Referrer has no inbound link; orphan")
	}
}

func TestAndOr(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: [foo, bar]\nstatus: Reading\n---\n",
		"b.md": "---\ntags: [foo]\nstatus: Completed\n---\n",
	})
	combined := And(Tag("foo"), Status("Reading"))
	if !combined(notes["a.md"]) {
		t.Errorf("And: a should match foo+Reading")
	}
	if combined(notes["b.md"]) {
		t.Errorf("And: b should not match foo+Reading")
	}

	either := Or(Tag("bar"), Status("Completed"))
	if !either(notes["a.md"]) || !either(notes["b.md"]) {
		t.Errorf("Or should match both notes")
	}

	// And with no terms = match everything
	all := And()
	for _, n := range notes {
		if !all(n) {
			t.Errorf("And() should match every note")
		}
	}
	if all(nil) {
		t.Errorf("And() should reject nil note")
	}
}

func TestFromFlags(t *testing.T) {
	v, notes := newVault(t, map[string]string{
		"References/a.md": "---\ntags: [foo]\ncategories:\n  - \"[[Books]]\"\nstatus: Reading\n---\nLinks to [[b]].\n",
		"b.md":            "---\ntags: [foo, bar]\n---\nbody\n",
		"c.md":            "---\ntitle: c\n---\nbody\n",
	})

	all := []*vault.Note{notes["References/a.md"], notes["b.md"], notes["c.md"]}

	t.Run("multi-term AND", func(t *testing.T) {
		pred, err := FromFlags(Flags{Tags: []string{"foo"}, Status: "Reading", MinBacklinks: -1, MaxBacklinks: -1, MinLinks: -1, MaxLinks: -1})
		if err != nil {
			t.Fatalf("FromFlags: %v", err)
		}
		if !pred(notes["References/a.md"]) {
			t.Errorf("a should match")
		}
		if pred(notes["b.md"]) {
			t.Errorf("b should not match (no status)")
		}
	})

	t.Run("folder requires root", func(t *testing.T) {
		pred, err := FromFlags(Flags{Folder: "References", Root: v.Root, MinBacklinks: -1, MaxBacklinks: -1, MinLinks: -1, MaxLinks: -1})
		if err != nil {
			t.Fatalf("FromFlags: %v", err)
		}
		if !pred(notes["References/a.md"]) {
			t.Errorf("a should match folder")
		}
		if pred(notes["b.md"]) {
			t.Errorf("b should not match folder")
		}
	})

	t.Run("orphan needs graph", func(t *testing.T) {
		if _, err := FromFlags(Flags{Orphan: true, MinBacklinks: -1, MaxBacklinks: -1, MinLinks: -1, MaxLinks: -1}); err == nil {
			t.Errorf("expected error: orphan without graph")
		}
		g := BuildGraph(all)
		pred, err := FromFlags(Flags{Orphan: true, Graph: g, MinBacklinks: -1, MaxBacklinks: -1, MinLinks: -1, MaxLinks: -1})
		if err != nil {
			t.Fatalf("FromFlags: %v", err)
		}
		if pred(notes["b.md"]) {
			t.Errorf("b is linked from a, not orphan")
		}
		if !pred(notes["c.md"]) {
			t.Errorf("c should be orphan")
		}
	})

	t.Run("age error", func(t *testing.T) {
		if _, err := FromFlags(Flags{Age: "bogus", MinBacklinks: -1, MaxBacklinks: -1, MinLinks: -1, MaxLinks: -1}); err == nil {
			t.Errorf("expected error from bogus age")
		}
	})

	t.Run("folder without root errors", func(t *testing.T) {
		_, err := FromFlags(Flags{Folder: "People", MinBacklinks: -1, MaxBacklinks: -1, MinLinks: -1, MaxLinks: -1})
		if err == nil {
			t.Errorf("expected error: folder without root")
		}
	})

	t.Run("empty flags matches all", func(t *testing.T) {
		pred, err := FromFlags(Flags{MinBacklinks: -1, MaxBacklinks: -1, MinLinks: -1, MaxLinks: -1})
		if err != nil {
			t.Fatalf("FromFlags: %v", err)
		}
		for path, n := range notes {
			if !pred(n) {
				t.Errorf("empty flags should match %s", path)
			}
		}
	})

	t.Run("min-backlinks zero is no-op without graph", func(t *testing.T) {
		pred, err := FromFlags(Flags{MinBacklinks: 0, MinLinks: 0, MaxBacklinks: -1, MaxLinks: -1})
		if err != nil {
			t.Fatalf("FromFlags with MinBacklinks=0 should not require a graph: %v", err)
		}
		for path, n := range notes {
			if !pred(n) {
				t.Errorf("MinBacklinks=0 is trivially true, should match %s", path)
			}
		}
	})
}

// TestFolderPredicateNoFalsePrefix verifies that the Folder predicate does not
// match notes in sibling folders whose name shares a prefix with the requested
// folder. Without the trailing "/" in the prefix check, `--folder Refs` would
// erroneously match `Refs2/a.md`.
func TestFolderPredicateNoFalsePrefix(t *testing.T) {
	v, notes := newVault(t, map[string]string{
		"Refs/a.md":  "# a\n",
		"Refs2/b.md": "# b\n",
		"Refsy/c.md": "# c\n",
	})
	p := Folder("Refs", v.Root)
	if !p(notes["Refs/a.md"]) {
		t.Errorf("Folder(Refs) should match Refs/a.md")
	}
	if p(notes["Refs2/b.md"]) {
		t.Errorf("Folder(Refs) should not match Refs2/b.md (sibling with shared prefix)")
	}
	if p(notes["Refsy/c.md"]) {
		t.Errorf("Folder(Refs) should not match Refsy/c.md (sibling with shared prefix)")
	}
}

// TestOrEmpty pins down the documented behaviour of Or with no terms: it is the
// identity element for logical OR (false), and it must still reject a nil note
// for consistency with And.
func TestOrEmpty(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: [foo]\n---\n",
	})
	none := Or()
	if none(notes["a.md"]) {
		t.Errorf("Or() with no terms should match nothing (identity is false)")
	}
	if none(nil) {
		t.Errorf("Or() should reject nil note")
	}
}

// TestPredicatesRejectErrorFront verifies that predicates which read frontmatter
// degrade gracefully when Front() returns an error or the requested key is
// absent. We use notes that have neither frontmatter nor the target key.
func TestPredicatesAbsentKey(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"empty.md": "no frontmatter at all\n",
		"other.md": "---\ntitle: o\n---\nbody\n",
	})
	for _, name := range []string{"empty.md", "other.md"} {
		n := notes[name]
		if Tag("foo")(n) {
			t.Errorf("Tag(foo) should not match %s (no tags)", name)
		}
		if Category("books")(n) {
			t.Errorf("Category(books) should not match %s (no categories)", name)
		}
		if Status("reading")(n) {
			t.Errorf("Status(reading) should not match %s (no status)", name)
		}
		if Type("goal")(n) {
			t.Errorf("Type(goal) should not match %s (no type/kind)", name)
		}
		if Has("status")(n) {
			t.Errorf("Has(status) should not match %s", name)
		}
		if !Missing("status")(n) {
			t.Errorf("Missing(status) should match %s", name)
		}
		if Age(">", 24*time.Hour, "created")(n) {
			t.Errorf("Age should not match %s (no date)", name)
		}
	}
}

// Smoke check that the path of a referrer is recorded correctly.
func TestBacklinkPathsRecorded(t *testing.T) {
	v, notes := newVault(t, map[string]string{
		"Source.md": "Reference to [[Target]].\n",
		"Target.md": "# t\n",
	})
	g := BuildGraph([]*vault.Note{notes["Source.md"], notes["Target.md"]})
	refs := g.BackLinks("target")
	if len(refs) != 1 {
		t.Fatalf("want 1 ref, got %d", len(refs))
	}
	rel, _ := filepath.Rel(v.Root, refs[0])
	if filepath.ToSlash(rel) != "Source.md" {
		t.Errorf("ref path = %s, want Source.md", rel)
	}
	_ = strings.TrimSpace // keep strings import used if tests trimmed later
}

// TestOrphanRejectsNilNote pins down the defensive nil-note guard. Other
// predicates rely on the And/Or wrapper to reject nil before they ever run,
// but Orphan does not call Front() and previously dereferenced n.Path through
// n.Title() — which panics on a nil *Note. The guard makes Orphan consistent
// with And/Or (false on nil) so callers can compose it safely.
func TestOrphanRejectsNilNote(t *testing.T) {
	g := BuildGraph(nil)
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Orphan should not panic on nil note, got panic: %v", r)
		}
	}()
	if Orphan(g)(nil) {
		t.Errorf("Orphan(g)(nil) = true, want false")
	}
}

// TestOrphanLinkedOnlyByAlias verifies that a link of the form
// `[[Target|some alias]]` correctly marks Target as non-orphan. The alias is a
// display label only — Title() must extract Target, not Alias. A bug in
// wikilink.Parse that mixed those up would cause Orphan to misclassify aliased
// notes as orphans.
func TestOrphanLinkedOnlyByAlias(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Target.md":   "---\n---\nbody\n",
		"Referrer.md": "---\n---\nSee [[Target|the target]] for details.\n",
	})
	all := []*vault.Note{notes["Target.md"], notes["Referrer.md"]}
	g := BuildGraph(all)
	if Orphan(g)(notes["Target.md"]) {
		t.Errorf("Target should not be an orphan: Referrer links it via an alias")
	}
	if len(g.BackLinks("the target")) != 0 {
		t.Errorf("alias text should not produce a backlink entry")
	}
}

// TestOrphanLinkedByFolderPrefixedWikilink verifies that a link of the form
// `[[Folder/Target]]` correctly marks Target as non-orphan. Orphan looks up by
// the note's lowercased title (filename stem), and Link.Title() must strip the
// folder prefix, otherwise a folder-prefixed link would never resolve.
func TestOrphanLinkedByFolderPrefixedWikilink(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Books/Target.md": "---\n---\nbody\n",
		"Referrer.md":     "---\n---\nSee [[Books/Target]].\n",
	})
	all := []*vault.Note{notes["Books/Target.md"], notes["Referrer.md"]}
	g := BuildGraph(all)
	if Orphan(g)(notes["Books/Target.md"]) {
		t.Errorf("Books/Target should not be an orphan: Referrer links it with a folder prefix")
	}
}

// TestFolderTrimsWhitespace verifies that a folder argument padded with spaces
// (e.g. from a shell that quoted the value with stray padding) is normalized.
// Before the fix, `--folder "  References  "` retained the surrounding spaces
// after path.Clean (which only collapses path syntax, not whitespace), so the
// predicate matched a literal folder named "  References  " and never matched
// the real "References" folder.
func TestFolderTrimsWhitespace(t *testing.T) {
	v, notes := newVault(t, map[string]string{
		"References/a.md": "# a\n",
		"Other/b.md":      "# b\n",
	})
	for _, raw := range []string{"  References  ", "\tReferences\t", " References/"} {
		p := Folder(raw, v.Root)
		if !p(notes["References/a.md"]) {
			t.Errorf("Folder(%q) should match References/a.md", raw)
		}
		if p(notes["Other/b.md"]) {
			t.Errorf("Folder(%q) should not match Other/b.md", raw)
		}
	}
	// All-whitespace folder normalizes to empty -> match everything.
	for _, raw := range []string{"   ", "\t", " / "} {
		p := Folder(raw, v.Root)
		if !p(notes["References/a.md"]) || !p(notes["Other/b.md"]) {
			t.Errorf("Folder(%q) should match every note (normalizes to empty)", raw)
		}
	}
}

// TestCategoryScalarWikilink verifies that a scalar (non-list) categories
// value like `categories: "[[Books]]"` matches Category("books"). The
// frontmatter helper supports this, but a regression in either layer would
// break the convenient single-category notation.
func TestCategoryScalarWikilink(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ncategories: \"[[Books]]\"\n---\nbody\n",
		"b.md": "---\ncategories: \"[[People]]\"\n---\nbody\n",
	})
	if !Category("books")(notes["a.md"]) {
		t.Errorf("Category(books) should match a scalar `categories: [[Books]]`")
	}
	if Category("books")(notes["b.md"]) {
		t.Errorf("Category(books) should not match a scalar `categories: [[People]]`")
	}
}

// TestAgeFutureDate documents how the predicate handles a date in the future
// (e.g. a note dated tomorrow). `time.Since` returns a negative duration for
// future timestamps, so `>30d` rejects it (a future note is not "older than
// 30 days") while `<30d` accepts it (it is trivially "younger than 30 days").
// `=0d` rejects unless today.
func TestAgeFutureDate(t *testing.T) {
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	_, notes := newVault(t, map[string]string{
		"future.md": "---\ncreated: " + tomorrow + "\n---\n",
	})
	if Age(">", 30*24*time.Hour, "created")(notes["future.md"]) {
		t.Errorf(">30d should not match a future note")
	}
	if !Age("<", 30*24*time.Hour, "created")(notes["future.md"]) {
		t.Errorf("<30d should match a future note (negative since)")
	}
	if Age("=", 0, "created")(notes["future.md"]) {
		t.Errorf("=0d should not match tomorrow's note")
	}
}

// TestAgeBogusOp pins down the safety net for an unknown comparison operator
// passed directly to Age (bypassing ParseAge). The predicate returns false
// rather than panicking on a typo or programmer error.
func TestAgeBogusOp(t *testing.T) {
	today := time.Now().Format("2006-01-02")
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ncreated: " + today + "\n---\n",
	})
	if Age("!?", 0, "created")(notes["a.md"]) {
		t.Errorf("Age with unknown op should return false")
	}
}

// TestHasAllowsNullValue documents that Has reports presence even when the
// value is YAML null. This matches the broader frontmatter model: nil-valued
// keys are present but empty, and `bn unset` is the only operation that
// removes a key entirely. Has and Missing must therefore be exact inverses on
// every note that loads.
func TestHasAllowsNullValue(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: ~\n---\nbody\n",
		"b.md": "---\ntags: []\n---\nbody\n",
	})
	for _, name := range []string{"a.md", "b.md"} {
		n := notes[name]
		if !Has("tags")(n) {
			t.Errorf("Has(tags) should be true for %s (key is present, value is null/empty)", name)
		}
		if Missing("tags")(n) {
			t.Errorf("Missing(tags) should be false for %s", name)
		}
	}
}

// TestAndShortCircuits verifies that And stops calling predicates after the
// first failure. Without short-circuiting, a query with an expensive late
// predicate would always pay its cost even when an early cheap predicate has
// already excluded the note.
func TestAndShortCircuits(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: [foo]\n---\n",
	})
	calls := 0
	count := Predicate(func(n *vault.Note) bool {
		calls++
		return true
	})
	fail := Predicate(func(n *vault.Note) bool { return false })

	if And(fail, count)(notes["a.md"]) {
		t.Errorf("And(fail, count) should be false")
	}
	if calls != 0 {
		t.Errorf("And should short-circuit on first false; count called %d times, want 0", calls)
	}
}

// TestOrShortCircuits verifies that Or stops calling predicates after the
// first success.
func TestOrShortCircuits(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: [foo]\n---\n",
	})
	calls := 0
	count := Predicate(func(n *vault.Note) bool {
		calls++
		return true
	})
	pass := Predicate(func(n *vault.Note) bool { return true })

	if !Or(pass, count)(notes["a.md"]) {
		t.Errorf("Or(pass, count) should be true")
	}
	if calls != 0 {
		t.Errorf("Or should short-circuit on first true; count called %d times, want 0", calls)
	}
}

// TestAndOrSkipNilPredicates verifies the documented behaviour that nil
// entries in the predicate list are silently skipped rather than panicking.
// This matters because FromFlags may compose lists where a builder returned
// nil to mean "no predicate for this flag".
func TestAndOrSkipNilPredicates(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: [foo]\n---\n",
	})
	if !And(nil, Tag("foo"), nil)(notes["a.md"]) {
		t.Errorf("And should skip nil predicates and still match")
	}
	if Or(nil, nil)(notes["a.md"]) {
		t.Errorf("Or with only nil predicates should match nothing")
	}
	if !Or(nil, Tag("foo"))(notes["a.md"]) {
		t.Errorf("Or with a nil mixed in should still consider the non-nil predicate")
	}
}

// TestStatusAndTypeMatchNonStringValues asserts that `bn set status=5` (which
// auto-coerces to int) and similar numeric/bool-valued status/type fields stay
// query-able. Without this, the same `--status 5` round-trip that works for
// tags would silently miss notes whose status was auto-coerced to a non-string.
func TestStatusAndTypeMatchNonStringValues(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"int.md":   "---\nstatus: 5\ntype: 7\n---\nbody\n",
		"bool.md":  "---\nstatus: true\nkind: false\n---\nbody\n",
		"plain.md": "---\nstatus: Reading\n---\nbody\n",
	})
	if !Status("5")(notes["int.md"]) {
		t.Errorf("Status(5) should match an int-valued status")
	}
	if !Type("7")(notes["int.md"]) {
		t.Errorf("Type(7) should match an int-valued type")
	}
	if !Status("true")(notes["bool.md"]) {
		t.Errorf("Status(true) should match a bool-valued status")
	}
	if !Type("false")(notes["bool.md"]) {
		t.Errorf("Type(false) should match a bool-valued kind fallback")
	}
	if !Status("reading")(notes["plain.md"]) {
		t.Errorf("Status(reading) should still match a plain string status")
	}
}

func TestMinBacklinks(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Hub.md":    "---\n---\nbody\n",
		"A.md":      "---\n---\n[[Hub]] link.\n",
		"B.md":      "---\n---\n[[Hub]] link.\n",
		"C.md":      "---\n---\n[[Hub]] link.\n",
		"Lonely.md": "---\n---\nNo links.\n",
	})
	all := []*vault.Note{notes["Hub.md"], notes["A.md"], notes["B.md"], notes["C.md"], notes["Lonely.md"]}
	g := BuildGraph(all)

	if !MinBacklinks(g, 2)(notes["Hub.md"]) {
		t.Errorf("Hub has 3 inbound; MinBacklinks(2) should match")
	}
	if MinBacklinks(g, 4)(notes["Hub.md"]) {
		t.Errorf("Hub has 3 inbound; MinBacklinks(4) should not match")
	}
	if MinBacklinks(g, 1)(notes["Lonely.md"]) {
		t.Errorf("Lonely has 0 inbound; MinBacklinks(1) should not match")
	}
}

func TestMaxBacklinks(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Target.md":   "---\n---\nbody\n",
		"Referrer.md": "---\n---\n[[Target]] link.\n",
		"Lonely.md":   "---\n---\nNo links.\n",
	})
	all := []*vault.Note{notes["Target.md"], notes["Referrer.md"], notes["Lonely.md"]}
	g := BuildGraph(all)

	if !MaxBacklinks(g, 2)(notes["Target.md"]) {
		t.Errorf("Target has 1 inbound; MaxBacklinks(2) should match")
	}
	if MaxBacklinks(g, 0)(notes["Target.md"]) {
		t.Errorf("Target has 1 inbound; MaxBacklinks(0) should not match")
	}
	if !MaxBacklinks(g, 0)(notes["Lonely.md"]) {
		t.Errorf("Lonely has 0 inbound; MaxBacklinks(0) should match")
	}
}

func TestMinLinks(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Hub.md": "---\n---\n[[A]] [[B]] [[C]] [[D]] [[E]]\n",
		"A.md":   "---\n---\nbody\n",
		"B.md":   "---\n---\nbody\n",
		"C.md":   "---\n---\nbody\n",
		"D.md":   "---\n---\nbody\n",
		"E.md":   "---\n---\nbody\n",
	})
	all := []*vault.Note{notes["Hub.md"], notes["A.md"], notes["B.md"], notes["C.md"], notes["D.md"], notes["E.md"]}
	g := BuildGraph(all)

	if !MinLinks(g, 3)(notes["Hub.md"]) {
		t.Errorf("Hub has 5 outgoing; MinLinks(3) should match")
	}
	if MinLinks(g, 1)(notes["A.md"]) {
		t.Errorf("A has 0 outgoing; MinLinks(1) should not match")
	}
}

func TestMaxLinks(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Hub.md":  "---\n---\n[[A]] [[B]]\n",
		"A.md":    "---\n---\nbody\n",
		"B.md":    "---\n---\nbody\n",
		"Dead.md": "---\n---\nNo outgoing links.\n",
	})
	all := []*vault.Note{notes["Hub.md"], notes["A.md"], notes["B.md"], notes["Dead.md"]}
	g := BuildGraph(all)

	if !MaxLinks(g, 0)(notes["Dead.md"]) {
		t.Errorf("Dead has 0 outgoing; MaxLinks(0) should match")
	}
	if MaxLinks(g, 0)(notes["Hub.md"]) {
		t.Errorf("Hub has 2 outgoing; MaxLinks(0) should not match")
	}
}

func TestDeadEnd(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Linker.md": "---\n---\n[[Target]]\n",
		"Target.md": "---\n---\nbody\n",
		"Island.md": "---\n---\nNo links at all.\n",
	})
	all := []*vault.Note{notes["Linker.md"], notes["Target.md"], notes["Island.md"]}
	g := BuildGraph(all)

	if DeadEnd(g)(notes["Linker.md"]) {
		t.Errorf("Linker has outgoing links; should not be dead end")
	}
	if !DeadEnd(g)(notes["Target.md"]) {
		t.Errorf("Target has no outgoing links; should be dead end")
	}
	if !DeadEnd(g)(notes["Island.md"]) {
		t.Errorf("Island has no outgoing links; should be dead end")
	}
}

func TestDeadEnd_EquivalentToMaxLinksZero(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Linker.md": "---\n---\n[[A]] [[B]]\n",
		"A.md":      "---\n---\nbody\n",
		"B.md":      "---\n---\n[[A]]\n",
		"Island.md": "---\n---\nNo links.\n",
	})
	all := []*vault.Note{notes["Linker.md"], notes["A.md"], notes["B.md"], notes["Island.md"]}
	g := BuildGraph(all)

	deadEnd := DeadEnd(g)
	maxZero := MaxLinks(g, 0)
	for name, n := range notes {
		if deadEnd(n) != maxZero(n) {
			t.Errorf("%s: DeadEnd=%v MaxLinks(0)=%v — should be identical", name, deadEnd(n), maxZero(n))
		}
	}
}

func TestLinkPredicatesSelfLinkExcluded(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Self.md":  "---\n---\n[[Self]] only links to itself.\n",
		"Other.md": "---\n---\n[[Self]]\n",
	})
	all := []*vault.Note{notes["Self.md"], notes["Other.md"]}
	g := BuildGraph(all)

	if g.InDegree("self") != 1 {
		t.Errorf("Self should have 1 inbound (from Other), got %d", g.InDegree("self"))
	}
	if g.OutDegree("self") != 0 {
		t.Errorf("Self should have 0 outgoing (self-link excluded), got %d", g.OutDegree("self"))
	}
	if !DeadEnd(g)(notes["Self.md"]) {
		t.Errorf("Self-link only: should be a dead end")
	}
}

func TestLinkPredicatesRejectNil(t *testing.T) {
	g := BuildGraph(nil)
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("link predicates should not panic on nil note, got: %v", r)
		}
	}()
	if MinBacklinks(g, 0)(nil) {
		t.Errorf("MinBacklinks should reject nil")
	}
	if MaxBacklinks(g, 0)(nil) {
		t.Errorf("MaxBacklinks should reject nil")
	}
	if MinLinks(g, 0)(nil) {
		t.Errorf("MinLinks should reject nil")
	}
	if MaxLinks(g, 0)(nil) {
		t.Errorf("MaxLinks should reject nil")
	}
	if DeadEnd(g)(nil) {
		t.Errorf("DeadEnd should reject nil")
	}
}

// TestFolderPredicateRejectsNilNote verifies that the Folder predicate returns
// false for nil notes, including when the folder is empty (matches everything
// for non-nil notes). Before the fix, `Folder("", root)(nil)` returned true
// because the early-return for the empty-folder case did not guard against nil.
func TestFolderPredicateRejectsNilNote(t *testing.T) {
	v, _ := newVault(t, map[string]string{
		"a.md": "# a\n",
	})
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Folder predicate should not panic on nil note, got: %v", r)
		}
	}()
	// Non-empty folder with nil note.
	if Folder("People", v.Root)(nil) {
		t.Errorf("Folder(People)(nil) = true, want false")
	}
	// Empty folder (match-all) with nil note — this was the bug.
	if Folder("", v.Root)(nil) {
		t.Errorf("Folder(\"\")(nil) = true, want false")
	}
	// Whitespace-only folder normalizes to empty — same bug path.
	if Folder("   ", v.Root)(nil) {
		t.Errorf("Folder(\"   \")(nil) = true, want false")
	}
	// Dot folder normalizes to empty.
	if Folder(".", v.Root)(nil) {
		t.Errorf("Folder(\".\")(nil) = true, want false")
	}
}

// TestFromFlagsZeroValueIntFieldsRequireGraph documents the sentinel convention:
// the Go zero value (0) for MaxBacklinks/MaxLinks activates the filter, so
// callers must set them to -1 to mean "not set". This test pins down the
// expected error when a zero-valued Flags struct is used without a graph.
func TestFromFlagsZeroValueIntFieldsRequireGraph(t *testing.T) {
	// Zero-valued Flags has MaxBacklinks=0 and MaxLinks=0, both of which
	// activate filters that require a graph.
	_, err := FromFlags(Flags{})
	if err == nil {
		t.Errorf("FromFlags(Flags{}) should error because MaxBacklinks=0 and MaxLinks=0 require a graph")
	}

	// With sentinel values of -1, no graph is needed and all notes match.
	pred, err := FromFlags(Flags{MinBacklinks: -1, MaxBacklinks: -1, MinLinks: -1, MaxLinks: -1})
	if err != nil {
		t.Fatalf("FromFlags with sentinel values: %v", err)
	}
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntitle: a\n---\n",
	})
	if !pred(notes["a.md"]) {
		t.Errorf("all-sentinel Flags should match every note")
	}
}

// TestTagPredicateRejectsNilNote verifies that the Tag predicate returns false
// for a nil note rather than panicking. The frontmatter-reading predicates rely
// on Note.Load() to return an error for a nil note; this asserts that contract.
func TestTagPredicateRejectsNilNote(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Tag should not panic on nil note, got: %v", r)
		}
	}()
	if Tag("foo")(nil) {
		t.Errorf("Tag(foo)(nil) = true, want false")
	}
	if Category("books")(nil) {
		t.Errorf("Category(books)(nil) = true, want false")
	}
	if Status("reading")(nil) {
		t.Errorf("Status(reading)(nil) = true, want false")
	}
	if Type("goal")(nil) {
		t.Errorf("Type(goal)(nil) = true, want false")
	}
	if Has("status")(nil) {
		t.Errorf("Has(status)(nil) = true, want false")
	}
	if Missing("status")(nil) {
		t.Errorf("Missing(status)(nil) = true, want false")
	}
	if Age(">", 24*time.Hour, "created")(nil) {
		t.Errorf("Age(nil) = true, want false")
	}
}

// TestTagPredicateEmptyList asserts that an empty tags list (`tags: []`) never
// matches any tag query. Before the fix this was already correct; the test
// pins down the behaviour against regressions where an empty list might be
// confused for "matches anything".
func TestTagPredicateEmptyList(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: []\n---\nbody\n",
	})
	if Tag("foo")(notes["a.md"]) {
		t.Errorf("Tag(foo) should not match a note with empty tags list")
	}
	if Tag("")(notes["a.md"]) {
		t.Errorf("Tag(empty) should not match a note with empty tags list")
	}
}

// TestTagPredicateNullValue asserts that `tags: null` (a YAML null value) does
// not match any tag query, including the empty string. The key is present but
// has no value to match against.
func TestTagPredicateNullValue(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: ~\n---\nbody\n",
	})
	if Tag("foo")(notes["a.md"]) {
		t.Errorf("Tag(foo) should not match `tags: null`")
	}
}

// TestCategoryPredicateEmptyList asserts that empty `categories: []` matches no
// category query, mirroring TestTagPredicateEmptyList.
func TestCategoryPredicateEmptyList(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ncategories: []\n---\nbody\n",
	})
	if Category("books")(notes["a.md"]) {
		t.Errorf("Category(books) should not match an empty categories list")
	}
}

// TestCategoryPredicateNullValue asserts that `categories: null` is handled
// gracefully and does not match any category. GetWikilinkList returns an empty
// slice (not nil) for a null value, so the empty-loop returns false correctly.
func TestCategoryPredicateNullValue(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ncategories: ~\n---\nbody\n",
	})
	if Category("books")(notes["a.md"]) {
		t.Errorf("Category(books) should not match `categories: null`")
	}
}

// TestTypeMatchesEitherTypeOrKind verifies that when a note has both `type`
// and `kind` set, a query for either value matches. The doc comment explicitly
// states "accepts either so queries don't need to know the difference" — this
// pins that OR semantics down.
func TestTypeMatchesEitherTypeOrKind(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"both.md": "---\ntype: Article\nkind: Project\n---\nbody\n",
	})
	if !Type("article")(notes["both.md"]) {
		t.Errorf("Type(article) should match when `type: Article` is set")
	}
	if !Type("project")(notes["both.md"]) {
		t.Errorf("Type(project) should match when `kind: Project` is set (either-or semantics)")
	}
	if Type("other")(notes["both.md"]) {
		t.Errorf("Type(other) should not match")
	}
}

// TestFolderNestedDescendantMatch verifies the descendant-match contract: a
// folder filter matches notes in any subfolder, however deeply nested.
func TestFolderNestedDescendantMatch(t *testing.T) {
	v, notes := newVault(t, map[string]string{
		"References/a.md":                 "# a\n",
		"References/Books/b.md":           "# b\n",
		"References/Books/SciFi/c.md":     "# c\n",
		"References/Books/SciFi/d/e.md":   "# e\n",
		"References/Books/SciFi/d/f/g.md": "# g\n",
	})
	p := Folder("References", v.Root)
	for _, path := range []string{
		"References/a.md",
		"References/Books/b.md",
		"References/Books/SciFi/c.md",
		"References/Books/SciFi/d/e.md",
		"References/Books/SciFi/d/f/g.md",
	} {
		if !p(notes[path]) {
			t.Errorf("Folder(References) should match deeply-nested %s", path)
		}
	}
}

// TestOrphanPredicateGraphLookupCaseInsensitive verifies that Orphan correctly
// finds a backlink even when the note title's case differs from the link text's
// case. The lookup must be case-folded both directions.
func TestOrphanPredicateGraphLookupCaseInsensitive(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Target.md":   "---\n---\nbody\n",
		"Referrer.md": "---\n---\nSee [[target]] (lowercase link text).\n",
	})
	all := []*vault.Note{notes["Target.md"], notes["Referrer.md"]}
	g := BuildGraph(all)
	if Orphan(g)(notes["Target.md"]) {
		t.Errorf("Target should not be an orphan: Referrer links it (case-insensitive)")
	}
}

// TestFolderPredicateRejectsParentEscape verifies that `..` segments in a
// folder query are normalized but do not allow escaping the vault root: a query
// that path.Cleans to a `..` prefix matches nothing real.
func TestFolderPredicateRejectsParentEscape(t *testing.T) {
	v, notes := newVault(t, map[string]string{
		"People/a.md": "# a\n",
		"Other/b.md":  "# b\n",
	})
	// "People/.." -> "."  -> "" -> match all.
	if !Folder("People/..", v.Root)(notes["People/a.md"]) {
		t.Errorf("Folder(People/..) normalizes to empty and should match all")
	}
	// "../Other" stays as "../Other" after Clean — should match nothing.
	if Folder("../Other", v.Root)(notes["Other/b.md"]) {
		t.Errorf("Folder(../Other) should not match a real folder (parent escape)")
	}
}

// TestAndOrCompositionNested verifies that combinators compose recursively:
// And(Or(A, B), C) should work as expected.
func TestAndOrCompositionNested(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: [foo]\nstatus: Reading\n---\n",
		"b.md": "---\ntags: [bar]\nstatus: Reading\n---\n",
		"c.md": "---\ntags: [baz]\nstatus: Reading\n---\n",
		"d.md": "---\ntags: [foo]\nstatus: Done\n---\n",
	})
	// (tag=foo OR tag=bar) AND status=Reading
	p := And(Or(Tag("foo"), Tag("bar")), Status("Reading"))
	cases := map[string]bool{
		"a.md": true,  // foo + Reading
		"b.md": true,  // bar + Reading
		"c.md": false, // baz + Reading: missing tag
		"d.md": false, // foo + Done: wrong status
	}
	for path, want := range cases {
		if got := p(notes[path]); got != want {
			t.Errorf("(foo OR bar) AND Reading on %s = %v, want %v", path, got, want)
		}
	}
}

// TestFromFlagsSkipsEmptyTagsAndCategories verifies that empty strings in the
// Tags/Categories slices are silently dropped rather than producing a
// match-nothing predicate. This matters when callers build slices from raw user
// input where an empty entry signals "no value given".
func TestFromFlagsSkipsEmptyTagsAndCategories(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"a.md": "---\ntags: [foo]\ncategories:\n  - \"[[Books]]\"\n---\n",
	})
	pred, err := FromFlags(Flags{
		Tags:         []string{"", "foo", ""},
		Categories:   []string{"", "books"},
		MinBacklinks: -1, MaxBacklinks: -1, MinLinks: -1, MaxLinks: -1,
	})
	if err != nil {
		t.Fatalf("FromFlags: %v", err)
	}
	if !pred(notes["a.md"]) {
		t.Errorf("empty entries in Tags/Categories should be skipped, leaving the real terms to match")
	}
}

// TestMissingIsStrictComplementOfHasOnParseError verifies that Missing returns
// true when the note's frontmatter cannot be parsed, so that Has and Missing
// partition every loadable note exactly. Before the fix both returned false on
// a YAML parse error, leaving the note in neither set.
func TestMissingIsStrictComplementOfHasOnParseError(t *testing.T) {
	// Write a file whose frontmatter is a YAML sequence at the document root,
	// which the frontmatter parser rejects ("top-level must be a mapping").
	// Use a bare *vault.Note so we can bypass Vault.Notes (which fails fast on
	// parse errors) and exercise the predicate's own error path.
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.md")
	if err := os.WriteFile(path, []byte("---\n  - x\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	n := &vault.Note{Path: path}
	if Has("anything")(n) {
		t.Errorf("Has(anything) should be false when frontmatter fails to parse")
	}
	if !Missing("anything")(n) {
		t.Errorf("Missing(anything) should be true when frontmatter fails to parse (strict complement of Has)")
	}
}

// TestMissingRejectsNilNote verifies that Missing rejects a nil note rather
// than treating it as "frontmatter failed to load, so the key is absent". A
// nil note isn't a real note and should match no predicate.
func TestMissingRejectsNilNote(t *testing.T) {
	if Missing("x")(nil) {
		t.Errorf("Missing(x)(nil) = true, want false")
	}
	if Has("x")(nil) {
		t.Errorf("Has(x)(nil) = true, want false")
	}
}

// TestParseAgeRoundTripsAllUnits verifies every documented unit (s, m, h, d)
// is accepted with a non-zero value.
func TestParseAgeRoundTripsAllUnits(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{">10s", 10 * time.Second},
		{"<3m", 3 * time.Minute},
		{"=4h", 4 * time.Hour},
		{">2d", 2 * 24 * time.Hour},
	}
	for _, c := range cases {
		_, dur, err := ParseAge(c.in)
		if err != nil {
			t.Errorf("ParseAge(%q) unexpected err: %v", c.in, err)
		}
		if dur != c.want {
			t.Errorf("ParseAge(%q) dur=%v, want %v", c.in, dur, c.want)
		}
	}
}

// TestStatusPredicateFloatStringification asserts that a YAML-typed float value
// in the frontmatter status field is queryable using the same textual form the
// user would type. Previously stringifyTag used "%v" for float64, which drops
// the trailing ".0" on whole-number floats — so `bn find --status 3.0` against
// a note whose frontmatter held `status: 3.0` (a YAML float scalar) silently
// returned no results. The fix renders floats with their decimal marker
// preserved so `3.0` round-trips through query stringification.
func TestStatusPredicateFloatStringification(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"whole.md":  "---\nstatus: 3.0\n---\nbody\n",
		"frac.md":   "---\nstatus: 1.5\n---\nbody\n",
		"strnum.md": "---\nstatus: \"3.0\"\n---\nbody\n",
	})
	if !Status("3.0")(notes["whole.md"]) {
		t.Errorf("Status(3.0) should match a note whose frontmatter holds the float 3.0")
	}
	if !Status("1.5")(notes["frac.md"]) {
		t.Errorf("Status(1.5) should match a note whose frontmatter holds the float 1.5")
	}
	if !Status("3.0")(notes["strnum.md"]) {
		t.Errorf("Status(3.0) should still match a string-typed \"3.0\" status (backcompat)")
	}
}

// TestTagPredicateFloatStringification mirrors the float-stringification fix
// for the tag predicate so that a tags list containing a YAML float scalar
// (e.g. `tags: [3.0]`) is queryable as `--tag 3.0`. The previous "%v" path
// would format the float as "3" and the textual `3.0` query would silently
// miss it.
func TestTagPredicateFloatStringification(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"list.md":   "---\ntags:\n  - 3.0\n---\nbody\n",
		"scalar.md": "---\ntags: 3.0\n---\nbody\n",
	})
	if !Tag("3.0")(notes["list.md"]) {
		t.Errorf("Tag(3.0) should match a tags list holding the float 3.0")
	}
	if !Tag("3.0")(notes["scalar.md"]) {
		t.Errorf("Tag(3.0) should match a scalar float tag value 3.0")
	}
}

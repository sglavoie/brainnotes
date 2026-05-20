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

func TestBuildBacklinkIndex(t *testing.T) {
	_, notes := newVault(t, map[string]string{
		"Apple.md":  "Body links to [[Banana]] and [[Cherry|Cherries]].\n",
		"Banana.md": "---\ncategories:\n  - \"[[Apple]]\"\n---\nBody.\n",
		"Cherry.md": "---\ntitle: c\n---\nNo links here.\n",
		"Lonely.md": "---\ntitle: l\n---\nNothing references me.\n",
	})

	all := []*vault.Note{notes["Apple.md"], notes["Banana.md"], notes["Cherry.md"], notes["Lonely.md"]}
	idx := BuildBacklinkIndex(all)

	if len(idx["banana"]) != 1 {
		t.Errorf("expected one referrer for Banana, got %v", idx["banana"])
	}
	if len(idx["cherry"]) != 1 {
		t.Errorf("expected one referrer for Cherry, got %v", idx["cherry"])
	}
	if len(idx["apple"]) != 1 {
		t.Errorf("expected one referrer for Apple (from Banana frontmatter), got %v", idx["apple"])
	}
	if _, ok := idx["lonely"]; ok {
		t.Errorf("Lonely should have no index entry, got %v", idx["lonely"])
	}

	orphan := Orphan(idx)
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
	idx := BuildBacklinkIndex(all)
	o := Orphan(idx)

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
		pred, err := FromFlags(Flags{Tags: []string{"foo"}, Status: "Reading"})
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
		pred, err := FromFlags(Flags{Folder: "References", Root: v.Root})
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

	t.Run("orphan needs index", func(t *testing.T) {
		if _, err := FromFlags(Flags{Orphan: true}); err == nil {
			t.Errorf("expected error: orphan without index")
		}
		idx := BuildBacklinkIndex(all)
		pred, err := FromFlags(Flags{Orphan: true, BacklinkIndex: idx})
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
		if _, err := FromFlags(Flags{Age: "bogus"}); err == nil {
			t.Errorf("expected error from bogus age")
		}
	})

	t.Run("empty flags matches all", func(t *testing.T) {
		pred, err := FromFlags(Flags{})
		if err != nil {
			t.Fatalf("FromFlags: %v", err)
		}
		for path, n := range notes {
			if !pred(n) {
				t.Errorf("empty flags should match %s", path)
			}
		}
	})
}

// Smoke check that the path of a referrer is recorded correctly.
func TestBacklinkPathsRecorded(t *testing.T) {
	v, notes := newVault(t, map[string]string{
		"Source.md": "Reference to [[Target]].\n",
		"Target.md": "# t\n",
	})
	idx := BuildBacklinkIndex([]*vault.Note{notes["Source.md"], notes["Target.md"]})
	refs := idx["target"]
	if len(refs) != 1 {
		t.Fatalf("want 1 ref, got %d", len(refs))
	}
	rel, _ := filepath.Rel(v.Root, refs[0])
	if filepath.ToSlash(rel) != "Source.md" {
		t.Errorf("ref path = %s, want Source.md", rel)
	}
	_ = strings.TrimSpace // keep strings import used if tests trimmed later
}

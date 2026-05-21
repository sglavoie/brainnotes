package query

import (
	"os"
	"path/filepath"
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

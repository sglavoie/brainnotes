package template

import "testing"

func TestSlug(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"Hello World", "hello-world"},
		{"  Foo  Bar  ", "foo-bar"},
		{"snake_case.value", "snake-case-value"},
		{"Trailing!!!", "trailing"},
		{"Numbers 123 mix", "numbers-123-mix"},
		{"", ""},
		{"---", ""},
		// Leading punctuation must not produce a leading dash.
		{"-foo", "foo"},
		{"!!!foo", "foo"},
		// Unicode letters are lowercased and preserved (not stripped).
		{"Café 2", "café-2"},
		// Only whitespace/punctuation collapses to empty after trim.
		{"   \t\n  ", ""},
	}
	for _, c := range cases {
		if got := Slug(c.in); got != c.want {
			t.Errorf("Slug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTitle(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"hello world", "Hello World"},
		{"already Capital", "Already Capital"},
		{"  spaced  out", "  Spaced  Out"},
		{"", ""},
	}
	for _, c := range cases {
		if got := Title(c.in); got != c.want {
			t.Errorf("Title(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTitleUnicode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"café latte", "Café Latte"},
		{"über cool", "Über Cool"},
		// Digit at word start stays as-is (ToUpper is identity for digits).
		{"123 abc", "123 Abc"},
		// A leading combining mark must not consume the word start; the first
		// real letter still gets capitalized.
		{"́test", "́Test"},
	}
	for _, c := range cases {
		if got := Title(c.in); got != c.want {
			t.Errorf("Title(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSlugCombiningMarks(t *testing.T) {
	t.Parallel()
	// NFD-normalized "Café" is C + a + f + e + U+0301 (COMBINING ACUTE ACCENT).
	// The combining mark must stay attached to the base letter, producing
	// "café" not "caf-e".
	cases := []struct {
		in, want string
	}{
		// NFD "Café 2": e + combining acute accent must produce café, not caf-e.
		{"Café 2", "café-2"},
		// NFD "über": u + combining diaeresis.
		{"über", "über"},
		// Multiple combining marks on one base letter (e.g. Vietnamese).
		{"ạ́b", "ạ́b"},
		// Orphan combining mark at start (pathological but should not leak
		// into the slug).
		{"́test", "test"},
	}
	for _, c := range cases {
		if got := Slug(c.in); got != c.want {
			t.Errorf("Slug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSlugTrailingOrphanMark(t *testing.T) {
	t.Parallel()
	// A combining mark that ends up at the end of the output after a
	// separator-induced dash is an orphan (no preceding base letter). It
	// must be trimmed along with the dash, instead of producing artifacts
	// like "foo-́".
	cases := []struct {
		in, want string
	}{
		{"foo!́", "foo"},
		{"abc-́", "abc"},
		{"abc-́́́", "abc"}, // multiple orphan marks
		{"abc-́def", "abc-def"},
		// Mark attached to a real base letter must be preserved.
		{"abć", "abć"},
		// Leading orphan mark is skipped.
		{"́test", "test"},
	}
	for _, c := range cases {
		if got := Slug(c.in); got != c.want {
			t.Errorf("Slug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSlugSingleChar(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"A", "a"},
		{"1", "1"},
		{"-", ""},
		{".", ""},
	}
	for _, c := range cases {
		if got := Slug(c.in); got != c.want {
			t.Errorf("Slug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

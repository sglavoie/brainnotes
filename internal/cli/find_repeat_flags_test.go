package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func buildHasMissingVault(t *testing.T) string {
	t.Helper()
	isolateBnEnv(t)
	return buildVaultFromFiles(t, map[string]string{
		"both.md":     "---\ntitle: Both\nrating: 4\nsource: book\n---\nBody.\n",
		"rating.md":   "---\ntitle: Rating\nrating: 3\n---\nBody.\n",
		"source.md":   "---\ntitle: Source\nsource: web\n---\nBody.\n",
		"neither.md":  "---\ntitle: Neither\n---\nBody.\n",
		"status.md":   "---\ntitle: Status\nstatus: active\n---\nBody.\n",
		"inactive.md": "---\ntitle: Inactive\nstatus: done\n---\nBody.\n",
	})
}

func findBasenames(t *testing.T, root string, args ...string) string {
	t.Helper()
	out := runCLIOutcome(t, root, append([]string{"find"}, args...)...)
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			names = append(names, filepath.Base(line))
		}
	}
	return strings.Join(names, " ")
}

// TestFindHasMissingRepeatAND pins --has and --missing as repeatable and ANDed,
// like --tag: a second value narrows the result instead of replacing the first.
func TestFindHasMissingRepeatAND(t *testing.T) {
	root := buildHasMissingVault(t)

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--has", "rating", "--has", "source"}, "both.md"},
		{[]string{"--has", "rating,source"}, "both.md"},
		{[]string{"--missing", "rating", "--missing", "source"}, "inactive.md neither.md status.md"},
		{[]string{"--has", "rating", "--missing", "source"}, "rating.md"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			if got := findBasenames(t, root, tc.args...); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFindSingleValueFlagRepeatRefused pins that a single-value filter flag
// given twice is a usage error naming the flag, not a silent last-one-wins.
func TestFindSingleValueFlagRepeatRefused(t *testing.T) {
	root := buildHasMissingVault(t)

	for _, flag := range []string{"status", "type", "age", "created", "folder", "query", "view"} {
		t.Run(flag, func(t *testing.T) {
			got := runCLIOutcome(t, root, "find", "--"+flag, "a", "--"+flag, "b")
			want := "bn: find: --" + flag + " given more than once; see 'bn find --help'\n"
			if got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}

	got := runCLIOutcome(t, root, "find", "-q", "a", "-q", "b")
	if want := "bn: find: --query given more than once; see 'bn find --help'\n"; got != want {
		t.Fatalf("-q twice: got %q, want %q", got, want)
	}
	got = runCLIOutcome(t, root, "search", "x", "--status", "a", "--status", "b")
	if want := "bn: search: --status given more than once; see 'bn search --help'\n"; got != want {
		t.Fatalf("search: got %q, want %q", got, want)
	}
	if got := findBasenames(t, root, "--status", "active"); got != "status.md" {
		t.Fatalf("single --status: got %q", got)
	}
}

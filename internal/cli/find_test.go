package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// buildFixtureVault writes a small vault under t.TempDir() and returns its root.
// Dates are computed from time.Now so age-based queries stay deterministic at
// the day boundary level.
func buildFixtureVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	now := time.Now()
	old := now.Add(-40 * 24 * time.Hour).Format("2006-01-02")
	recent := now.Add(-2 * 24 * time.Hour).Format("2006-01-02")

	files := map[string]string{
		"References/Apple.md":  "---\ntags:\n  - foo\n  - fruit\ncreated: " + old + "\n---\nApple body links to [[Banana]].\n",
		"References/Banana.md": "---\ntags:\n  - fruit\ncreated: " + recent + "\n---\nBanana body.\n",
		"People/Bob.md":        "---\ntags:\n  - foo\ncreated: " + recent + "\n---\nBob body.\n",
		"Orphan.md":            "---\ntitle: orphan\n---\nUnreferenced.\n",
	}
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	markVaultRoot(t, root, "")
	return root
}

// runFind executes the root command with the given args against vaultRoot and
// returns the produced lines, converted to vault-relative slash paths and
// sorted lexically.
func runFind(t *testing.T, vaultRoot string, args ...string) []string {
	t.Helper()
	isolateBnEnv(t)

	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs(append([]string{"--vault", vaultRoot}, args...))

	if err := root.Execute(); err != nil {
		t.Fatalf("execute %v: %v\noutput: %s", args, err, out.String())
	}

	var rels []string
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		rel, err := filepath.Rel(vaultRoot, line)
		if err != nil {
			t.Fatalf("rel %q: %v", line, err)
		}
		rels = append(rels, filepath.ToSlash(rel))
	}
	sort.Strings(rels)
	return rels
}

func readGolden(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	var lines []string
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimRight(l, "\r")
		if l == "" {
			continue
		}
		lines = append(lines, l)
	}
	sort.Strings(lines)
	return lines
}

func TestFindByTag(t *testing.T) {
	root := buildFixtureVault(t)
	got := runFind(t, root, "find", "--tag", "foo")
	want := readGolden(t, "find-tag-foo.golden")
	if !slices.Equal(got, want) {
		t.Fatalf("find --tag foo:\n got=%v\nwant=%v", got, want)
	}
}

func TestFindByTagAndAge(t *testing.T) {
	root := buildFixtureVault(t)
	got := runFind(t, root, "find", "--tag", "foo", "--created", ">30d")
	want := readGolden(t, "find-tag-foo-age-gt30d.golden")
	if !slices.Equal(got, want) {
		t.Fatalf("find --tag foo --created >30d:\n got=%v\nwant=%v", got, want)
	}
}

func TestFindOrphan(t *testing.T) {
	root := buildFixtureVault(t)
	got := runFind(t, root, "find", "--orphan")
	// Only Banana has an inbound link (from Apple's body); everything else is orphan.
	expected := []string{"Orphan.md", "People/Bob.md", "References/Apple.md"}
	sort.Strings(expected)
	if !slices.Equal(got, expected) {
		t.Fatalf("find --orphan:\n got=%v\nwant=%v", got, expected)
	}
}

func TestFindNullSeparator(t *testing.T) {
	root := buildFixtureVault(t)

	isolateBnEnv(t)

	cmd := NewRootCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--vault", root, "find", "--tag", "foo", "-0"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "\x00") {
		t.Fatalf("expected NUL separator in output, got %q", got)
	}
	if strings.Contains(got, "\n") {
		t.Fatalf("expected no newline separator with -0, got %q", got)
	}
}

func TestFindEmptyResult(t *testing.T) {
	root := buildFixtureVault(t)
	got := runFind(t, root, "find", "--tag", "nonexistent")
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func buildBrokenLinkFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		// Resolved target — exists in the vault.
		"References/Dune.md": "---\ntitle: Dune\n---\nA book about sand.\n",

		// Resolved body link only.
		"Notes/Resolved.md": "---\ntitle: Resolved\n---\nSee [[Dune]] for details.\n",

		// Unresolved body link.
		"Notes/Missing Body.md": "---\ntitle: Missing Body\n---\nReading [[Foundation]] soon.\n",

		// Resolved alias + unresolved heading-anchored link.
		"Notes/Mixed.md": "---\ntitle: Mixed\n---\nSee [[Dune|the book]] and [[Atreides#House]].\n",

		// Frontmatter with one broken, one resolved category.
		"Notes/FM Broken.md": "---\ncategories:\n  - \"[[Books]]\"\n  - \"[[Dune]]\"\ntitle: FM Broken\n---\nBody.\n",

		// Code-fenced link — must NOT be flagged.
		"Notes/Code Fence.md": "---\ntitle: Code Fence\n---\nSample:\n```\n[[Foundation]]\n```\n",

		// Media embed — must NOT be flagged.
		"Notes/Embed.md": "---\ntitle: Embed\n---\n![[diagram.png]]\n",
	}
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	markVaultRoot(t, root, "")
	return root
}

func TestFind_BrokenLinks(t *testing.T) {
	root := buildBrokenLinkFixture(t)
	got := runFind(t, root, "find", "--broken-links")
	want := readGolden(t, "find-broken-links.golden")
	if !slices.Equal(got, want) {
		t.Fatalf("find --broken-links:\n got=%v\nwant=%v", got, want)
	}
}

func runFindRaw(t *testing.T, vaultRoot string, args ...string) string {
	t.Helper()
	isolateBnEnv(t)

	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs(append([]string{"--vault", vaultRoot}, args...))

	if err := root.Execute(); err != nil {
		t.Fatalf("execute %v: %v\noutput: %s", args, err, out.String())
	}
	return out.String()
}

func TestFind_BrokenLinks_Show(t *testing.T) {
	root := buildBrokenLinkFixture(t)
	got := runFindRaw(t, root, "find", "--broken-links", "--show-broken")
	assertGolden(t, "find-broken-links-show.golden", got)
}

func TestFind_BrokenLinks_Targets(t *testing.T) {
	root := buildBrokenLinkFixture(t)
	got := runFindRaw(t, root, "find", "--broken-links", "--list-broken-targets")
	assertGolden(t, "find-broken-links-targets.golden", got)
}

func TestFind_BrokenLinks_ErrorCombinations(t *testing.T) {
	root := buildBrokenLinkFixture(t)

	isolateBnEnv(t)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "show-broken without broken-links",
			args: []string{"--vault", root, "find", "--show-broken"},
			want: "--show-broken requires --broken-links",
		},
		{
			name: "list-broken-targets without broken-links",
			args: []string{"--vault", root, "find", "--list-broken-targets"},
			want: "--list-broken-targets requires --broken-links",
		},
		{
			name: "show-broken and list-broken-targets",
			args: []string{"--vault", root, "find", "--broken-links", "--show-broken", "--list-broken-targets"},
			want: "--show-broken cannot be combined with --list-broken-targets",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewRootCmd()
			out := &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(out)
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %q", tt.want, err.Error())
			}
		})
	}
}

func buildLinkCountFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"Hub.md":    "---\ntags:\n  - important\n---\n[[A]] and [[B]] and [[C]].\n",
		"A.md":      "---\ntags:\n  - important\n---\n[[Hub]] back.\n",
		"B.md":      "---\n---\n[[Hub]] back.\n",
		"C.md":      "---\n---\n[[Hub]] back.\n",
		"Island.md": "---\n---\nNo links at all.\n",
		"Exempt.md": "---\ndead_end: false\n---\nNo links at all.\n",
	}
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	markVaultRoot(t, root, "")
	return root
}

func TestFind_MinBacklinks(t *testing.T) {
	root := buildLinkCountFixture(t)
	got := runFind(t, root, "find", "--min-backlinks", "3")
	want := []string{"Hub.md"}
	if !slices.Equal(got, want) {
		t.Fatalf("find --min-backlinks 3:\n got=%v\nwant=%v", got, want)
	}
}

func TestFind_DeadEnd(t *testing.T) {
	root := buildLinkCountFixture(t)
	got := runFind(t, root, "find", "--dead-end")
	want := []string{"Island.md"}
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Fatalf("find --dead-end:\n got=%v\nwant=%v", got, want)
	}
}

func TestFind_MaxLinksZeroIncludesDeadEndExemption(t *testing.T) {
	root := buildLinkCountFixture(t)
	got := runFind(t, root, "find", "--max-links", "0")
	want := []string{"Exempt.md", "Island.md"}
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Fatalf("find --max-links 0:\n got=%v\nwant=%v", got, want)
	}
}

func TestFind_ComposedMetrics(t *testing.T) {
	root := buildLinkCountFixture(t)
	got := runFind(t, root, "find", "--min-backlinks", "1", "--dead-end")
	if len(got) != 0 {
		t.Fatalf("find --min-backlinks 1 --dead-end: expected no matches (no note is both linked-to and has zero outgoing), got %v", got)
	}
}

func TestFind_MinBacklinksWithTag(t *testing.T) {
	root := buildLinkCountFixture(t)
	got := runFind(t, root, "find", "--min-backlinks", "2", "--tag", "important")
	want := []string{"Hub.md"}
	if !slices.Equal(got, want) {
		t.Fatalf("find --min-backlinks 2 --tag important:\n got=%v\nwant=%v", got, want)
	}
}

// TestFindSetNullPipeline runs the documented `bn find ... -0 | bn set ...`
// composition end to end, against an isolated temporary vault.
//
// TestFindNullSeparator pins the producer and TestSetNullRequiresStdinNoteInput
// pins the consumer's input requirement, but neither one runs the join, so a
// separator that changed on only one side would pass both. This feeds find's
// real NUL-separated bytes into `set --null -` and then reads the notes back off
// disk, which is also what makes it notice a path that survived the pipe but
// resolved to the wrong note.
func TestFindSetNullPipeline(t *testing.T) {
	isolateBnEnv(t)
	root := t.TempDir()

	notes := map[string]string{
		"Alpha.md": "---\ntags:\n  - pipeline\ntitle: Alpha\n---\nAlpha body.\n",
		"Beta.md":  "---\ntags:\n  - pipeline\ntitle: Beta\n---\nBeta body.\n",
		// Control: matched by neither filter, so it proves the pipe carried the
		// selection rather than the whole vault.
		"Gamma.md": "---\ntags:\n  - other\ntitle: Gamma\n---\nGamma body.\n",
	}
	for rel, body := range notes {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	markVaultRoot(t, root, testPropertyDefinitions)

	piped, err := runCLI(t, "", "--vault", root, "find", "--tag", "pipeline", "-0")
	if err != nil {
		t.Fatalf("find -0: %v\noutput: %q", err, piped)
	}
	wantPiped := filepath.Join(root, "Alpha.md") + "\x00" + filepath.Join(root, "Beta.md") + "\x00"
	if piped != wantPiped {
		t.Fatalf("find -0 output:\n got=%q\nwant=%q", piped, wantPiped)
	}

	out, err := runCLI(t, piped, "--vault", root, "set", "--null", "-", "status=done")
	if err != nil {
		t.Fatalf("set --null -: %v\noutput: %q", err, out)
	}

	want := map[string]string{
		"Alpha.md": "---\nstatus: done\ntags:\n  - pipeline\ntitle: Alpha\n---\nAlpha body.\n",
		"Beta.md":  "---\nstatus: done\ntags:\n  - pipeline\ntitle: Beta\n---\nBeta body.\n",
		"Gamma.md": notes["Gamma.md"],
	}
	for rel, wantBytes := range want {
		if got := readNote(t, filepath.Join(root, rel)); got != wantBytes {
			t.Fatalf("%s after the pipeline:\n got=%q\nwant=%q", rel, got, wantBytes)
		}
	}
}

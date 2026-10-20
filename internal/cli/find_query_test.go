package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// queryFixtureProperties declares the typed properties the -q goldens compare
// against. rating is an int so `rating>=4` exercises the typed comparison path
// rather than the stringified fallback.
const queryFixtureProperties = `[frontmatter.properties.rating]
type = "int"

[frontmatter.properties.status]
type = "string"

[frontmatter.properties.tags]
type  = "list"
items = "string"
`

// buildQueryFixtureVault writes a vault with declared typed properties and a
// three-note link chain (Hub -> Spoke -> Leaf) plus one unlinked note, which is
// what lets one fixture cover boolean, typed-property, and graph queries.
func buildQueryFixtureVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"Notes/Hub.md":   "---\nrating: 5\nstatus: wip\ntags:\n  - core\n---\nHub links to [[Spoke]].\n",
		"Notes/Spoke.md": "---\nrating: 2\nstatus: done\n---\nSpoke links to [[Leaf]].\n",
		"Notes/Leaf.md":  "---\nrating: 4\nstatus: wip\n---\nLeaf body.\n",
		"Notes/Away.md":  "---\nrating: 1\nstatus: done\n---\nAway body.\n",
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
	markVaultRoot(t, root, queryFixtureProperties)
	return root
}

// runFindError executes args against vaultRoot expecting a failure, and returns
// the error rendered exactly as `bn` prints it. Going through RenderError is the
// point: it is what pins the caret block rather than the one-line Error form.
func runFindError(t *testing.T, vaultRoot string, args ...string) string {
	t.Helper()
	isolateBnEnv(t)

	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs(append([]string{"--vault", vaultRoot}, args...))

	err := root.Execute()
	if err == nil {
		t.Fatalf("execute %v: expected an error, got none\noutput: %s", args, out.String())
	}
	var rendered bytes.Buffer
	RenderError(&rendered, err)
	return rendered.String()
}

func TestFindQuery_Boolean(t *testing.T) {
	root := buildQueryFixtureVault(t)
	got := runFind(t, root, "find", "-q", "status:wip and not rating>4")
	want := readGolden(t, "find-query-boolean.golden")
	if !slices.Equal(got, want) {
		t.Fatalf("find -q 'status:wip and not rating>4':\n got=%v\nwant=%v", got, want)
	}
}

func TestFindQuery_TypedProperty(t *testing.T) {
	root := buildQueryFixtureVault(t)
	got := runFind(t, root, "find", "-q", "rating>=4")
	want := readGolden(t, "find-query-typed-property.golden")
	if !slices.Equal(got, want) {
		t.Fatalf("find -q 'rating>=4':\n got=%v\nwant=%v", got, want)
	}
}

func TestFindQuery_Near(t *testing.T) {
	root := buildQueryFixtureVault(t)
	got := runFind(t, root, "find", "-q", "near:[[Leaf]]<=1")
	want := readGolden(t, "find-query-near.golden")
	if !slices.Equal(got, want) {
		t.Fatalf("find -q 'near:[[Leaf]]<=1':\n got=%v\nwant=%v", got, want)
	}
}

func TestFindQuery_ParseError(t *testing.T) {
	root := buildQueryFixtureVault(t)
	got := runFindError(t, root, "find", "-q", "status:wip and (rating>3")
	assertGolden(t, "find-query-parse-error.golden", got)
}

func TestFindQuery_UnknownField(t *testing.T) {
	root := buildQueryFixtureVault(t)
	got := runFindError(t, root, "find", "-q", "ratign>3")
	assertGolden(t, "find-query-unknown-field.golden", got)
}

// TestFindQuery_AndsWithFlags pins that compile really ANDs the two halves
// rather than letting either one win.
func TestFindQuery_AndsWithFlags(t *testing.T) {
	root := buildQueryFixtureVault(t)
	got := runFind(t, root, "find", "-q", "rating>=4", "--status", "wip")
	want := []string{"Notes/Hub.md", "Notes/Leaf.md"}
	if !slices.Equal(got, want) {
		t.Fatalf("find -q 'rating>=4' --status wip:\n got=%v\nwant=%v", got, want)
	}

	got = runFind(t, root, "find", "-q", "rating>=4", "--status", "done")
	if len(got) != 0 {
		t.Fatalf("find -q 'rating>=4' --status done: expected no matches, got %v", got)
	}
}

// queryFixtureViews saves two views over the same fixture the -q tests use, so
// a --view result can be compared against the -q result it must equal.
const queryFixtureViews = `
[[views]]
name = "Active"
query = "status:wip"
description = "still in flight"

[[views]]
name = "Top rated"
query = "rating>=4"
`

func buildViewFixtureVault(t *testing.T) string {
	t.Helper()
	root := buildQueryFixtureVault(t)
	markVaultRoot(t, root, queryFixtureProperties+queryFixtureViews)
	return root
}

// TestFindQuery_View pins that --view resolves a saved view's query out of
// brainnotes.toml and feeds it through the same compile path as -q.
func TestFindQuery_View(t *testing.T) {
	root := buildViewFixtureVault(t)

	got := runFind(t, root, "find", "--view", "Active")
	want := runFind(t, root, "find", "-q", "status:wip")
	if !slices.Equal(got, want) {
		t.Fatalf("find --view Active:\n got=%v\nwant=%v", got, want)
	}

	// Names are matched case-insensitively, matching Config.View and the
	// uniqueness rule the config layer enforces.
	if got := runFind(t, root, "find", "--view", "aCTIVE"); !slices.Equal(got, want) {
		t.Fatalf("find --view aCTIVE:\n got=%v\nwant=%v", got, want)
	}
}

// TestFindQuery_ViewAndsWithEverythingElse pins that a view is one more operand
// of the same AND rather than a filter that replaces the others.
func TestFindQuery_ViewAndsWithEverythingElse(t *testing.T) {
	root := buildViewFixtureVault(t)

	got := runFind(t, root, "find", "--view", "Active", "-q", "rating>=4")
	want := []string{"Notes/Hub.md", "Notes/Leaf.md"}
	if !slices.Equal(got, want) {
		t.Fatalf("find --view Active -q 'rating>=4':\n got=%v\nwant=%v", got, want)
	}

	got = runFind(t, root, "find", "--view", "Top rated", "--status", "done")
	if len(got) != 0 {
		t.Fatalf("find --view 'Top rated' --status done: expected no matches, got %v", got)
	}
}

// TestFindQuery_UnknownView pins both misses: a vault with no views at all says
// so, and a vault that has some lists them rather than only reporting the miss.
func TestFindQuery_UnknownView(t *testing.T) {
	root := buildQueryFixtureVault(t)
	got := runFindError(t, root, "find", "--view", "recent")
	if !strings.Contains(got, `unknown view "recent"`) || !strings.Contains(got, "no views are defined") {
		t.Fatalf("find --view recent with no views: got %q", got)
	}

	root = buildViewFixtureVault(t)
	got = runFindError(t, root, "find", "--view", "recent")
	if !strings.Contains(got, `unknown view "recent"`) || !strings.Contains(got, "Active, Top rated") {
		t.Fatalf("find --view recent with views defined: got %q", got)
	}
}

// TestFindQuery_ViewWithARottedQuery pins the other end of the session-11
// contract at the CLI: config cannot validate a saved expression, so a view
// whose query stopped parsing loads fine and fails only when it is *used* —
// with the caret block that says which part rotted.
func TestFindQuery_ViewWithARottedQuery(t *testing.T) {
	root := buildQueryFixtureVault(t)
	markVaultRoot(t, root, queryFixtureProperties+"\n[[views]]\nname = \"Rotted\"\nquery = \"ratign>3\"\n")

	// Loading the vault must still work — every other command keeps running.
	if got := runFind(t, root, "find", "--status", "wip"); len(got) == 0 {
		t.Fatal("a rotted saved view must not break an unrelated query")
	}

	got := runFindError(t, root, "find", "--view", "Rotted")
	if !strings.Contains(got, "unknown field") || !strings.Contains(got, "^") {
		t.Fatalf("find --view Rotted should render the parse error: got %q", got)
	}
}

// TestQueryFlagsRejectPositionalNotes pins the session's "don't miss": --query
// and --view are find-filter flags, so combining either with positional notes
// must trip the same mutual-exclusion check every other find flag trips.
func TestQueryFlagsRejectPositionalNotes(t *testing.T) {
	commands := []struct {
		name string
		args []string
	}{
		{name: "replace", args: []string{"replace", "status", "wip", "done", "Notes/Hub.md"}},
		{name: "repair", args: []string{"repair", "Notes/Hub.md"}},
		{name: "relink", args: []string{"relink", "Notes/Hub.md"}},
		{name: "rename-key", args: []string{"rename-key", "status", "state", "Notes/Hub.md"}},
		{name: "search", args: []string{"search", "Hub", "Notes/Hub.md"}},
	}
	for _, flag := range [][]string{{"-q", "tag:core"}, {"--view", "recent"}} {
		for _, tc := range commands {
			t.Run(tc.name+strings.Join(flag, ""), func(t *testing.T) {
				root := buildQueryFixtureVault(t)
				isolateBnEnv(t)
				cmd := NewRootCmd()
				out := &bytes.Buffer{}
				cmd.SetOut(out)
				cmd.SetErr(out)
				args := append([]string{"--vault", root}, tc.args...)
				cmd.SetArgs(append(args, flag...))
				err := cmd.Execute()
				if err == nil {
					t.Fatalf("%s %v: expected mutual-exclusion error, got none\noutput: %s", tc.name, flag, out.String())
				}
				if !strings.Contains(err.Error(), "find filter flags") &&
					!strings.Contains(err.Error(), "cannot be combined with find flags") {
					t.Fatalf("%s %v: unexpected error %v", tc.name, flag, err)
				}
			})
		}
	}
}

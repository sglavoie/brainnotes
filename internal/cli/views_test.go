package cli

import (
	"bytes"
	"regexp"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// viewsFixtureConfig declares one typed property plus three saved views: one
// that compiles, one whose query names a field that does not exist, and one
// whose ordering is unusable. The last two are what `bn views check` reports,
// and they differ on purpose — a query failure renders carets, a sort failure
// has no span to point at (see queryexpr.ResolveView).
const viewsFixtureConfig = `[frontmatter.properties.status]
type = "string"

[[views]]
name        = "Active work"
query       = "status:wip and not folder:Archive"
sort        = "-modified"
limit       = 20
description = "What I am actually working on"

[[views]]
name        = "Stale query"
query       = "statuz:wip"
description = "Names a field that no longer exists"

[[views]]
name        = "Bad ordering"
query       = "status:done"
sort        = "relevance"
description = "Relevance with no anchor note"
`

// buildViewsFixtureVault marks an empty temp dir as a vault root carrying the
// saved views above. No notes are written: `bn views` reads the config only.
func buildViewsFixtureVault(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	markVaultRoot(t, root, body)
	return root
}

// runViews executes args against vaultRoot and returns everything printed on
// both streams, with a failure rendered exactly as `bn` prints it. Both halves
// matter to the goldens: `views check` writes its per-view report to stdout and
// signals failure through the exit error.
func runViews(t *testing.T, vaultRoot string, wantErr bool, args ...string) string {
	t.Helper()
	isolateBnEnv(t)

	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs(append([]string{"--vault", vaultRoot}, args...))

	err := root.Execute()
	switch {
	case err != nil && !wantErr:
		t.Fatalf("execute %v: %v\noutput: %s", args, err, out.String())
	case err == nil && wantErr:
		t.Fatalf("execute %v: expected an error, got none\noutput: %s", args, out.String())
	}
	if err != nil {
		RenderError(out, err)
	}
	return out.String()
}

// configPathPattern matches the absolute config path `views check` prints. The
// vault lives in a temp dir, so the path has to be normalized before it can be
// compared against a golden.
var configPathPattern = regexp.MustCompile(`(?m)^.*` + regexp.QuoteMeta(config.FileName))

func normalizeConfigPath(s string) string {
	return configPathPattern.ReplaceAllString(s, "<vault>/"+config.FileName)
}

func TestViews_List(t *testing.T) {
	root := buildViewsFixtureVault(t, viewsFixtureConfig)
	got := runViews(t, root, false, "views")
	assertGolden(t, "views-list.golden", got)
}

func TestViews_Show(t *testing.T) {
	root := buildViewsFixtureVault(t, viewsFixtureConfig)
	got := runViews(t, root, false, "views", "show", "active WORK")
	assertGolden(t, "views-show.golden", got)
}

func TestViews_CheckInvalid(t *testing.T) {
	root := buildViewsFixtureVault(t, viewsFixtureConfig)
	got := runViews(t, root, true, "views", "check")
	assertGolden(t, "views-check-invalid.golden", normalizeConfigPath(got))
}

// TestViews_CheckAllValid pins the clean path: every view compiles, nothing is
// reported, and the command succeeds.
func TestViews_CheckAllValid(t *testing.T) {
	root := buildViewsFixtureVault(t, `[[views]]
name  = "Recent"
query = "age:modified<7d"
`)
	got := runViews(t, root, false, "views", "check")
	want := "views check: 1 saved view compiles\n"
	if got != want {
		t.Fatalf("views check:\n got=%q\nwant=%q", got, want)
	}
}

// TestViews_ShowCanonicalizes pins that show re-renders through Expr.String()
// rather than echoing the stored text: `-tag:foo` comes back as `not tag:foo`
// and the redundant parentheses are gone.
func TestViews_ShowCanonicalizes(t *testing.T) {
	root := buildViewsFixtureVault(t, `[[views]]
name  = "Canon"
query = "((status:wip)) -folder:Archive"
`)
	got := runViews(t, root, false, "views", "show", "Canon")
	if !bytes.Contains([]byte(got), []byte("status:wip and not folder:Archive\n")) {
		t.Fatalf("views show did not canonicalize the query:\n%s", got)
	}
	if !bytes.Contains([]byte(got), []byte("((status:wip)) -folder:Archive\n")) {
		t.Fatalf("views show did not report the stored spelling:\n%s", got)
	}
}

// TestViews_ShowUnknownName pins that a miss lists what is available, the same
// way --view does.
func TestViews_ShowUnknownName(t *testing.T) {
	root := buildViewsFixtureVault(t, viewsFixtureConfig)
	got := runViews(t, root, true, "views", "show", "nope")
	want := "bn: unknown view \"nope\": defined views are Active work, Stale query, Bad ordering\n"
	if got != want {
		t.Fatalf("views show nope:\n got=%q\nwant=%q", got, want)
	}
}

// TestViews_ShowDisabledRendersItsError pins that a rotted view fails through
// the same caret rendering `bn find -q` uses, rather than printing a view whose
// query cannot run.
func TestViews_ShowDisabledRendersItsError(t *testing.T) {
	root := buildViewsFixtureVault(t, viewsFixtureConfig)
	got := runViews(t, root, true, "views", "show", "Stale query")
	if !bytes.Contains([]byte(got), []byte("statuz")) || !bytes.Contains([]byte(got), []byte("^")) {
		t.Fatalf("views show of a disabled view did not render its parse error:\n%s", got)
	}
}

// TestViews_ListEmptyHints pins that a vault with no saved views gets the
// example block rather than an empty table.
func TestViews_ListEmptyHints(t *testing.T) {
	root := buildViewsFixtureVault(t, "")
	got := runViews(t, root, false, "views")
	if !bytes.Contains([]byte(got), []byte("no saved views defined in config")) ||
		!bytes.Contains([]byte(got), []byte("[[views]]")) {
		t.Fatalf("views on an empty config did not hint:\n%s", got)
	}
}

// TestViewsCheckHelpNamesTheConfigLimitation pins the help-text contract: the
// command has to say why it exists, because nothing in internal/config can
// validate a saved expression.
func TestViewsCheckHelpNamesTheConfigLimitation(t *testing.T) {
	root := buildViewsFixtureVault(t, viewsFixtureConfig)
	got := runViews(t, root, false, "views", "check", "--help")
	for _, want := range []string{"internal/config", "import cycle", "shape-only"} {
		if !bytes.Contains([]byte(got), []byte(want)) {
			t.Fatalf("views check --help does not mention %q:\n%s", want, got)
		}
	}
}

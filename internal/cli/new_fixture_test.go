package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	tmpl "github.com/sglavoie/sglavoie_brainnotes/internal/template"
)

func buildNewFixture(t *testing.T) string {
	return buildNewFixtureWithRouteKey(t, "book")
}

func buildNewFixtureWithRouteKey(t *testing.T, routeKey string) string {
	return buildNewFixtureWithRoutes(t, routeKey+` = "References/Books"`+"\n")
}

func buildNewFixtureWithRoutes(t *testing.T, routeLines string) string {
	t.Helper()
	root := t.TempDir()
	tmplDir := filepath.Join(root, "Templates")
	if err := os.MkdirAll(tmplDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	bookTmpl := "---\ntitle: {{ .Title }}\ncreated: {{today}}\nauthor: {{ .Vars.author }}\n---\nslug: {{ .Slug }}\n"
	if err := os.WriteFile(filepath.Join(tmplDir, "Book Template.md"), []byte(bookTmpl), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	useVaultRoot(t, root, "[new.routes]\n"+routeLines)
	return root
}

func newTestFilesystemCaseInsensitive(t *testing.T, root string) bool {
	t.Helper()
	probe := filepath.Join(root, "CaseProbe")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	info, err := os.Stat(probe)
	if err != nil {
		t.Fatalf("stat probe: %v", err)
	}
	altInfo, err := os.Stat(filepath.Join(root, "caseprobe"))
	switch {
	case err == nil:
		return os.SameFile(info, altInfo)
	case os.IsNotExist(err):
		return false
	default:
		t.Fatalf("stat alternate probe: %v", err)
		return false
	}
}

func runNew(t *testing.T, args ...string) (string, error) {
	t.Helper()
	old := clockForNew
	clockForNew = tmpl.FixedClock{T: time.Date(2026, 5, 18, 10, 0, 0, 0, time.UTC)}
	t.Cleanup(func() { clockForNew = old })

	cmd := NewRootCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

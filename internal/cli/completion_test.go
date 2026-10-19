package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// completeArgs runs cobra's hidden __complete command and returns the
// candidates, dropping the trailing ":<directive>" line.
func completeArgs(t *testing.T, root string, args ...string) []string {
	t.Helper()
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(append([]string{"__complete", "--vault", root}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("__complete %v: %v", args, err)
	}
	var candidates []string
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line != "" && !strings.HasPrefix(line, ":") {
			candidates = append(candidates, line)
		}
	}
	return candidates
}

func TestCompletion_VaultBackedAndEnumValues(t *testing.T) {
	isolateBnEnv(t)
	root := t.TempDir()
	markVaultRoot(t, root, "[[views]]\nname = \"reading\"\nquery = \"tag:alpha\"\n")
	note := filepath.Join(root, "Projects", "Deep", "Idea.md")
	if err := os.MkdirAll(filepath.Dir(note), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(note, []byte("---\ntags: [alpha, beta]\n---\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"open", ""}, []string{"Idea"}},
		{[]string{"backlinks", ""}, []string{"Idea"}},
		{[]string{"find", "--tag", ""}, []string{"alpha", "beta"}},
		{[]string{"find", "--view", ""}, []string{"reading"}},
		{[]string{"search", "--folder", ""}, []string{"Projects", "Projects/Deep"}},
		{[]string{"values", ""}, []string{"tags"}},
		{[]string{"related", "--mode", ""}, []string{"balanced", "rediscover", "connect"}},
		{[]string{"context", "--format", ""}, []string{"markdown", "json"}},
		{[]string{"graph", "--format", ""}, []string{"dot", "mermaid", "code"}},
	} {
		got := completeArgs(t, root, tc.args...)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("complete %v = %v, want %v", tc.args, got, tc.want)
		}
	}
}

// A vault that does not load completes nothing rather than failing the shell.
func TestCompletion_UnloadableVaultCompletesNothing(t *testing.T) {
	isolateBnEnv(t)
	root := t.TempDir()
	markVaultRoot(t, root, "[open\n")
	if got := completeArgs(t, root, "open", ""); len(got) != 0 {
		t.Fatalf("candidates = %v, want none", got)
	}
}

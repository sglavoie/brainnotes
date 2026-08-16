package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func buildSearchVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"Notes/Alpha.md": "---\ntitle: Alpha\n---\nthe keyword is here\n",
		"Notes/Beta.md":  "---\ntitle: Beta\n---\nanother keyword usage\n",
		"Notes/Gamma.md": "---\ntitle: Gamma\n---\nnothing relevant here\n",
		"Notes/Upper.md": "---\ntitle: Upper\n---\nUPPERCASE CONTENT only\n",
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

func runSearch(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errBuf)
	cmd.SetArgs(append([]string{"--vault", root, "search"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

// searchRels converts absolute-path stdout lines to sorted vault-relative slash paths.
func searchRels(t *testing.T, root, stdout string) []string {
	t.Helper()
	var rels []string
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		rel, err := filepath.Rel(root, line)
		if err != nil {
			t.Fatalf("rel %q: %v", line, err)
		}
		rels = append(rels, filepath.ToSlash(rel))
	}
	sort.Strings(rels)
	return rels
}

func runSearchStdin(t *testing.T, root string, stdin io.Reader, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(stdin)
	cmd.SetArgs(append([]string{"--vault", root, "search"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

func buildTaggedVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"Notes/Physics.md":   "---\ntitle: Physics\ntags:\n  - physics\n---\nactivation energy is key\n",
		"Notes/Chemistry.md": "---\ntitle: Chemistry\ntags:\n  - chemistry\n---\nactivation energy transfer here\n",
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

func buildFolderVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"References/Paper1.md": "---\ntitle: Paper1\n---\nfull-text unique reference marker\n",
		"Notes/Misc.md":        "---\ntitle: Misc\n---\nfull-text misc content here\n",
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

func buildOrphanVault(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"Notes/Linker.md": "---\ntitle: Linker\n---\nSee [[Linked]] for details.\n",
		"Notes/Linked.md": "---\ntitle: Linked\n---\norphan-test-term appears here too\n",
		"Notes/Orphan.md": "---\ntitle: Orphan\n---\norphan-test-term appears here\n",
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

// buildVaultFromFiles creates a temp vault with the given relative-path → content map.
func buildVaultFromFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
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

// contextLines splits stdout by newline, dropping only empty lines (preserving "--" separators).
func contextLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

func fzfArgValue(args []string, flag string) (string, bool) {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1], true
		}
		if strings.HasPrefix(arg, flag+"=") {
			return strings.TrimPrefix(arg, flag+"="), true
		}
	}
	return "", false
}

func shellQuoteForTest(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// runCLICanceled runs the root command under a context that is already
// cancelled, which is what a Ctrl-C before the write began leaves behind.
func runCLICanceled(t *testing.T, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd := NewRootCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(ctx)
	return out.String(), err
}

// Every journaled CLI write runs under the command context, so an interrupt
// stops it before it changes the vault rather than being ignored until the
// write is done.
func TestInterruptedWritesChangeNothing(t *testing.T) {
	t.Run("remove", func(t *testing.T) {
		root := buildRemoveFixture(t)
		_, err := runCLICanceled(t, "remove", "--yes", "Remove me")
		requireCanceled(t, err)
		requireFile(t, filepath.Join(root, "Projects", "Nested", "Remove me.md"))
	})
	t.Run("pin", func(t *testing.T) {
		root := buildPinFixture(t)
		before := readNote(t, filepath.Join(root, "Alpha.md"))
		_, err := runCLICanceled(t, "pin", "Alpha")
		requireCanceled(t, err)
		if got := readNote(t, filepath.Join(root, "Alpha.md")); got != before {
			t.Fatalf("pin wrote under a cancelled context:\n%s", got)
		}
	})
	t.Run("unpin", func(t *testing.T) {
		root := buildPinFixture(t)
		before := readNote(t, filepath.Join(root, "Charlie.md"))
		_, err := runCLICanceled(t, "unpin", "Charlie")
		requireCanceled(t, err)
		if got := readNote(t, filepath.Join(root, "Charlie.md")); got != before {
			t.Fatalf("unpin wrote under a cancelled context:\n%s", got)
		}
	})
	t.Run("capture", func(t *testing.T) {
		root := buildCaptureFixture(t, map[string]string{"Inbox.md": "---\ntitle: Inbox\n---\n## Captures\n"})
		before := readNote(t, filepath.Join(root, "Inbox.md"))
		_, err := runCLICanceled(t, "capture", "a thought")
		requireCanceled(t, err)
		if got := readNote(t, filepath.Join(root, "Inbox.md")); got != before {
			t.Fatalf("capture wrote under a cancelled context:\n%s", got)
		}
	})
	t.Run("undo", func(t *testing.T) {
		root := buildRemoveFixture(t)
		if out, err := runCLI(t, "", "remove", "--yes", "Remove me"); err != nil {
			t.Fatalf("remove: %v\n%s", err, out)
		}
		_, err := runCLICanceled(t, "undo")
		requireCanceled(t, err)
		requireFile(t, filepath.Join(root, "Trash", "Projects", "Nested", "Remove me.md"))
	})
	t.Run("backup", func(t *testing.T) {
		root := buildBackupFixture(t, map[string]string{"Daily.md": "# Daily\n"})
		_, err := runCLICanceled(t, "backup")
		requireCanceled(t, err)
		entries, _ := os.ReadDir(filepath.Join(root, "backups"))
		if len(entries) != 0 {
			t.Fatalf("a cancelled backup left %d file(s) in backups/", len(entries))
		}
	})
	t.Run("restore", func(t *testing.T) {
		root := buildBackupFixture(t, map[string]string{"Daily.md": "# Daily\n"})
		if out, err := runBackupCLI(t, "backup"); err != nil {
			t.Fatalf("backup: %v\n%s", err, out)
		}
		if err := os.WriteFile(filepath.Join(root, "Daily.md"), []byte("changed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := runCLICanceled(t, "restore", "backups/backup-20260528T232425.123456789Z.tar.gz")
		requireCanceled(t, err)
		if got := readNote(t, filepath.Join(root, "Daily.md")); got != "changed\n" {
			t.Fatalf("restore wrote under a cancelled context: %q", got)
		}
	})
}

func requireCanceled(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func requireFile(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s should still exist: %v", path, err)
	}
}

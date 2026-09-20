package vault

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// requirePathContinuity runs during while a destination is being replaced and
// fails the test if the name ever stops resolving, even for the width of one
// syscall.
//
// Final bytes are not the property under test. An evacuate-then-place sequence
// ends with exactly the right content too; what it also does is leave the path
// absent for a moment, and a synchronizing file provider that samples the vault
// in that moment reads the absence as a deletion and later propagates it. Only a
// continuous observation can tell the two apart, which is why every overwrite
// path below is watched rather than merely checked afterwards.
func requirePathContinuity(t *testing.T, paths []string, during func()) {
	t.Helper()
	watch := watchPresence(paths)
	during()
	if vanished := watch.stop(); len(vanished) > 0 {
		t.Fatalf("these paths stopped existing during the operation: %v", vanished)
	}
}

// noteTempFiles lists the temp files Note.Save stages, which carry their own
// prefix so they are distinguishable from a transaction's.
func noteTempFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	var leftovers []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".note-") {
			leftovers = append(leftovers, entry.Name())
		}
	}
	return leftovers
}

func TestFileTxnOverwriteNeverVacatesTheDestination(t *testing.T) {
	t.Run("a committed overwrite", func(t *testing.T) {
		root := txnTestRoot(t, map[string]string{"a.md": "old-a\n", "b.md": "old-b\n"})
		txn, err := NewFileTxn(root)
		if err != nil {
			t.Fatalf("new txn: %v", err)
		}
		for _, name := range []string{"a.md", "b.md"} {
			if err := txn.Write(filepath.Join(root, name), []byte("new-"+name+"\n"), 0, ExpectAnyContent()); err != nil {
				t.Fatalf("plan %s: %v", name, err)
			}
		}

		watched := []string{filepath.Join(root, "a.md"), filepath.Join(root, "b.md")}
		requirePathContinuity(t, watched, func() {
			if err := txn.Commit(&captureRecorder{}); err != nil {
				t.Errorf("commit: %v", err)
			}
		})
		if got := readFileString(t, filepath.Join(root, "a.md")); got != "new-a.md\n" {
			t.Fatalf("a.md = %q", got)
		}
	})

	t.Run("a stale writer being rejected", func(t *testing.T) {
		root := txnTestRoot(t, map[string]string{"a.md": "old-a\n"})
		txn, err := NewFileTxn(root)
		if err != nil {
			t.Fatalf("new txn: %v", err)
		}
		if err := txn.Write(filepath.Join(root, "a.md"), []byte("new-a\n"), 0, ExpectAnyContent()); err != nil {
			t.Fatalf("plan a: %v", err)
		}
		// The editor saves after the destination check passed, so the conflict is
		// only discovered from the bytes the exchange displaces — and the
		// put-back that resolves it must keep the path occupied too.
		txn.hooks.beforeCommit = func(path string) error {
			return os.WriteFile(path, []byte("editor-saved\n"), 0o644)
		}

		var commitErr error
		requirePathContinuity(t, []string{filepath.Join(root, "a.md")}, func() {
			commitErr = txn.Commit(&captureRecorder{})
		})
		if !IsTxnConflict(commitErr) {
			t.Fatalf("commit should conflict, got %v", commitErr)
		}
		if got := readFileString(t, filepath.Join(root, "a.md")); got != "editor-saved\n" {
			t.Fatalf("the other writer's version must survive, got %q", got)
		}
		if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
			t.Fatalf("staged temp files left behind: %v", leftovers)
		}
	})

	t.Run("a rollback after an injected failure", func(t *testing.T) {
		root := txnTestRoot(t, map[string]string{
			"a.md": "old-a\n",
			"b.md": "old-b\n",
			"c.md": "old-c\n",
		})
		txn, err := NewFileTxn(root)
		if err != nil {
			t.Fatalf("new txn: %v", err)
		}
		for _, name := range []string{"a.md", "b.md", "c.md"} {
			if err := txn.Write(filepath.Join(root, name), []byte("new-"+name+"\n"), 0, ExpectAnyContent()); err != nil {
				t.Fatalf("plan %s: %v", name, err)
			}
		}
		commitErr := errors.New("commit exploded")
		txn.hooks.beforeCommit = func(path string) error {
			if strings.HasSuffix(path, "c.md") {
				return commitErr
			}
			return nil
		}

		watched := []string{
			filepath.Join(root, "a.md"),
			filepath.Join(root, "b.md"),
			filepath.Join(root, "c.md"),
		}
		var got error
		requirePathContinuity(t, watched, func() {
			got = txn.Commit(&captureRecorder{})
		})
		if !errors.Is(got, commitErr) {
			t.Fatalf("commit error = %v, want the injected failure", got)
		}
		for _, name := range []string{"a.md", "b.md", "c.md"} {
			if content := readFileString(t, filepath.Join(root, name)); content != "old-"+strings.TrimSuffix(name, ".md")+"\n" {
				t.Fatalf("%s = %q, want the pre-transaction content", name, content)
			}
		}
		if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
			t.Fatalf("staged temp files left behind: %v", leftovers)
		}
	})
}

func TestNoteSaveNeverVacatesTheDestination(t *testing.T) {
	root := txnTestRoot(t, map[string]string{"a.md": "---\ntitle: a\n---\n\nbody\n"})
	path := filepath.Join(root, "a.md")
	note := NewNote(root, path)
	if err := note.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := note.SetBody("rewritten\n"); err != nil {
		t.Fatalf("set body: %v", err)
	}

	requirePathContinuity(t, []string{path}, func() {
		if err := note.Save(nil); err != nil {
			t.Errorf("save: %v", err)
		}
	})
	if got := readFileString(t, path); !strings.Contains(got, "rewritten") {
		t.Fatalf("a.md = %q", got)
	}
	if leftovers := noteTempFiles(t, root); len(leftovers) > 0 {
		t.Fatalf("temp files left behind: %v", leftovers)
	}
}

// Save's source-hash check and its replacement have to be one decision. The
// check is a plain read, so an editor saving between it and the replacement is
// invisible to it; the replacement re-judges the same revision against the bytes
// it actually displaces, which is the only place that writer can be seen.
func TestNoteSaveRefusesToOverwriteAnEditItNeverSaw(t *testing.T) {
	root := txnTestRoot(t, map[string]string{"a.md": "---\ntitle: a\n---\n\nbody\n"})
	path := filepath.Join(root, "a.md")
	note := NewNote(root, path)
	if err := note.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := note.SetBody("rewritten\n"); err != nil {
		t.Fatalf("set body: %v", err)
	}

	// The editor saves after Load and before Save.
	if err := os.WriteFile(path, []byte("---\ntitle: a\n---\n\neditor-saved\n"), 0o644); err != nil {
		t.Fatalf("editor write: %v", err)
	}

	err := note.Save(nil)
	if !IsTxnConflict(err) {
		t.Fatalf("save should conflict, got %v", err)
	}
	if got := readFileString(t, path); !strings.Contains(got, "editor-saved") {
		t.Fatalf("the concurrent edit must survive, got %q", got)
	}
	if leftovers := noteTempFiles(t, root); len(leftovers) > 0 {
		t.Fatalf("temp files left behind: %v", leftovers)
	}
}

func TestNoteSaveRefusesANonRegularDestination(t *testing.T) {
	root := txnTestRoot(t, map[string]string{"a.md": "---\ntitle: a\n---\n\nbody\n"})
	note := NewNote(root, filepath.Join(root, "a.md"))
	if err := note.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	directory := filepath.Join(root, "notes")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	note.Path = directory

	// An exchange against a directory would otherwise succeed and swap the note
	// with it, where the old plain rename simply failed.
	if err := note.Save(nil); !IsTxnConflict(err) {
		t.Fatalf("saving over a directory must conflict, got %v", err)
	}
	if info, err := os.Stat(directory); err != nil || !info.IsDir() {
		t.Fatalf("the directory must be untouched, stat = %v, %v", info, err)
	}
}

// A commit that fails leaves the vault exactly as it found it, which is what
// makes the operation safe to simply run again. The retry is planned from the
// current disk state, as a caller would after seeing the failure.
func TestFileTxnRetryAfterAnInterruptedCommitConverges(t *testing.T) {
	root := txnTestRoot(t, map[string]string{
		"a.md": "old-a\n",
		"b.md": "old-b\n",
		"c.md": "old-c\n",
	})
	plan := func(t *testing.T) *FileTxn {
		t.Helper()
		txn, err := NewFileTxn(root)
		if err != nil {
			t.Fatalf("new txn: %v", err)
		}
		for _, name := range []string{"a.md", "b.md", "c.md"} {
			if err := txn.Write(filepath.Join(root, name), []byte("new-"+name+"\n"), 0, ExpectAnyContent()); err != nil {
				t.Fatalf("plan %s: %v", name, err)
			}
		}
		return txn
	}

	interrupted := plan(t)
	replaceErr := errors.New("interrupted after the second exchange")
	interrupted.hooks.replaceExisting = func(source, path string) (string, error) {
		if strings.HasSuffix(path, "c.md") {
			return "", replaceErr
		}
		return replaceExisting(source, path)
	}
	if err := interrupted.Commit(&captureRecorder{}); !errors.Is(err, replaceErr) {
		t.Fatalf("first commit = %v, want the injected failure", err)
	}
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		want := "old-" + strings.TrimSuffix(name, ".md") + "\n"
		if got := readFileString(t, filepath.Join(root, name)); got != want {
			t.Fatalf("%s = %q, want %q after the interrupted commit", name, got, want)
		}
	}

	if err := plan(t).Commit(&captureRecorder{}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		if got := readFileString(t, filepath.Join(root, name)); got != "new-"+name+"\n" {
			t.Fatalf("%s = %q after the retry", name, got)
		}
	}
	if leftovers := stagedTempFiles(t, root); len(leftovers) > 0 {
		t.Fatalf("staged temp files left behind: %v", leftovers)
	}
}

// A temp file that cannot be unlinked after a mutation that landed is
// housekeeping, not a failed write. Rolling the mutation back over it would be
// the destructive answer to the harmless problem, so it is reported on its own
// and the artifact it names is left where it is.
func TestFileTxnReportsCleanupFailureWithoutUndoingTheWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not block unlinking on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	root := txnTestRoot(t, map[string]string{"a.md": "old-a\n"})
	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "a.md"), []byte("new-a\n"), 0, ExpectAnyContent()); err != nil {
		t.Fatalf("plan a: %v", err)
	}
	// The directory becomes read-only once the exchange has landed, so the
	// displaced copy can still be read and judged but no longer unlinked.
	forceTxnCleanupFailure(t, txn, root)

	err = txn.Commit(&captureRecorder{})
	assertTxnCleanupFailure(t, err, root)
}

func assertTxnCleanupFailure(t *testing.T, err error, root string) {
	t.Helper()
	if !IsTxnCleanupFailure(err) {
		t.Fatalf("commit = %v, want a cleanup failure", err)
	}
	if IsTxnConflict(err) || IsTxnRollbackFailure(err) || IsTxnRecoveryNeeded(err) {
		t.Fatalf("a leftover temp file must not be reported as a failed mutation: %v", err)
	}
	if got := readFileString(t, filepath.Join(root, "a.md")); got != "new-a\n" {
		t.Fatalf("the write landed, so it must stand: a.md = %q", got)
	}
	var cleanup *TxnCleanupError
	if !errors.As(err, &cleanup) {
		t.Fatalf("errors.As: %v", err)
	}
	if got := readFileString(t, cleanup.LeftoverPath); got != "old-a\n" {
		t.Fatalf("the named artifact must still hold the displaced content, got %q", got)
	}
}

func forceTxnCleanupFailure(t *testing.T, txn *FileTxn, root string) {
	t.Helper()
	var stagedDir string
	t.Cleanup(func() {
		_ = os.Chmod(root, 0o755)
		if stagedDir != "" {
			_ = os.Chmod(stagedDir, 0o700)
		}
	})
	txn.hooks.afterExchange = func(_ string, displaced string) error {
		stagedDir = filepath.Dir(displaced)
		return os.Chmod(stagedDir, 0o500)
	}
}

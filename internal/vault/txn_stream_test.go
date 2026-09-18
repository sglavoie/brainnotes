package vault

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// streamedSource writes size bytes of deterministic filler outside the vault and
// returns the path, its digest and its size — the three things WriteFrom takes.
//
// The filler is not zeroes: a sparse file would let a broken copy look correct
// on a filesystem that fills holes with the same byte.
func streamedSource(t *testing.T, size int) (path, digest string, length int64) {
	t.Helper()
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i%251 + 1)
	}
	path = filepath.Join(t.TempDir(), "upload.bin")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	digest, length, err := HashFile(path)
	if err != nil {
		t.Fatalf("hash source: %v", err)
	}
	return path, digest, length
}

// assertFileDigest requires a path to hold exactly the bytes a caller measured,
// asked by digest rather than by comparison so the assertion itself does not
// read the file it is checking the residency of.
func assertFileDigest(t *testing.T, path, digest string, size int64) {
	t.Helper()
	gotDigest, gotSize, err := HashFile(path)
	if err != nil {
		t.Fatalf("hash %s: %v", path, err)
	}
	if gotDigest != digest || gotSize != size {
		t.Fatalf("%s = sha256:%s size %d, want sha256:%s size %d", path, gotDigest, gotSize, digest, size)
	}
}

func TestWriteFromCommitsTheSourceBytesAndLeavesTheSourceAlone(t *testing.T) {
	root := txnTestRoot(t, map[string]string{"note.md": "old\n"})
	source, digest, size := streamedSource(t, 4096)
	dest := filepath.Join(root, "attachments", "photo.png")

	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := txn.Write(filepath.Join(root, "note.md"), []byte("new\n"), 0, ExpectContent([]byte("old\n"))); err != nil {
		t.Fatalf("plan note: %v", err)
	}
	if err := txn.WriteFrom(dest, source, size, digest, 0o644); err != nil {
		t.Fatalf("plan attachment: %v", err)
	}
	recorder := &captureRecorder{}
	if err := txn.Commit(recorder); err != nil {
		t.Fatalf("commit: %v", err)
	}

	assertFileDigest(t, dest, digest, size)
	if got := readFileString(t, filepath.Join(root, "note.md")); got != "new\n" {
		t.Fatalf("note.md = %q, the note and the attachment are one transaction", got)
	}
	// The caller owns the staging file: sweeping or retrying is its business, and
	// a transaction that consumed it would take that away.
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source should survive the commit: %v", err)
	}
	if len(recorder.paths) != 2 {
		t.Fatalf("journal should capture both paths, got %v", recorder.paths)
	}
	if leftovers := stagedTempFiles(t, filepath.Dir(dest)); len(leftovers) > 0 {
		t.Fatalf("staged temp files left behind: %v", leftovers)
	}
}

func TestWriteFromRefusesASourceThatIsNotWhatTheCallerMeasured(t *testing.T) {
	root := txnTestRoot(t, nil)
	source, digest, size := streamedSource(t, 2048)
	dest := filepath.Join(root, "attachments", "photo.png")

	// The staging file is replaced after it was measured, which is what a
	// half-swept or truncated upload looks like from here.
	if err := os.WriteFile(source, []byte("something else"), 0o600); err != nil {
		t.Fatalf("rewrite source: %v", err)
	}

	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := txn.WriteFrom(dest, source, size, digest, 0o644); err != nil {
		t.Fatalf("plan: %v", err)
	}
	err = txn.Commit(nil)
	if !IsTxnConflict(err) {
		t.Fatalf("commit = %v, want a conflict naming the source", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("nothing may be written when the bytes are not the measured bytes, stat = %v", statErr)
	}
}

func TestWriteFromRefusesADestinationThatAppearedAfterStaging(t *testing.T) {
	root := txnTestRoot(t, nil)
	source, digest, size := streamedSource(t, 1024)
	dest := filepath.Join(root, "attachments", "photo.png")

	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	if err := txn.WriteFrom(dest, source, size, digest, 0o644); err != nil {
		t.Fatalf("plan: %v", err)
	}
	// beforeCommit is the widest part of the window between staging and the
	// rename, and a streamed create must judge an occupant it never read. The
	// hook is matched by base name because a transaction resolves its root's
	// symlinks and the temp directory on darwin is one.
	txn.hooks.beforeCommit = func(path string) error {
		if filepath.Base(path) == "photo.png" {
			if err := os.WriteFile(dest, []byte("somebody else got here first"), 0o644); err != nil {
				t.Fatalf("interloper write: %v", err)
			}
		}
		return nil
	}
	err = txn.Commit(nil)
	if !IsTxnConflict(err) {
		t.Fatalf("commit = %v, want a conflict over the occupied destination", err)
	}
	if got := readFileString(t, dest); got != "somebody else got here first" {
		t.Fatalf("the other writer's file must survive, got %q", got)
	}
}

func TestARolledBackStreamedCreateTakesItsFileBackOut(t *testing.T) {
	root := txnTestRoot(t, nil)
	source, digest, size := streamedSource(t, 4096)
	attachment := filepath.Join(root, "attachments", "photo.png")
	note := filepath.Join(root, "zz-note.md")

	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatalf("new txn: %v", err)
	}
	// Commit order is path order, so the attachment lands and the note is the
	// change that fails — which is the ordering that exercises restoreCreated
	// against a change holding no bytes of its own.
	if err := txn.WriteFrom(attachment, source, size, digest, 0o644); err != nil {
		t.Fatalf("plan attachment: %v", err)
	}
	if err := txn.Write(note, []byte("body\n"), 0, ExpectMissing()); err != nil {
		t.Fatalf("plan note: %v", err)
	}
	boom := errors.New("placement refused")
	txn.hooks.placeNew = func(source, path string) error {
		if filepath.Base(path) == "zz-note.md" {
			return boom
		}
		return placeNew(nil, source, path)
	}
	if err := txn.Commit(nil); !errors.Is(err, boom) {
		t.Fatalf("commit = %v, want the injected failure", err)
	}
	if _, err := os.Stat(attachment); !os.IsNotExist(err) {
		t.Fatalf("the compensated attachment should be gone, stat = %v", err)
	}
	if _, err := os.Stat(note); !os.IsNotExist(err) {
		t.Fatalf("the failed note should not exist, stat = %v", err)
	}
}

func TestRecoveringAStreamedCreateFinishesItFromTheStagedCopy(t *testing.T) {
	root := txnTestRoot(t, nil)
	source, digest, size := streamedSource(t, 4096)
	dest := filepath.Join(root, "attachments", "photo.png")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	staged, err := stageTempFileFrom(dest, source, size, digest, 0o644)
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	change := txnIntentChange{
		Path:      dest,
		Staged:    staged,
		Mode:      0o644,
		NewSHA256: digest,
		NewSize:   size,
	}

	if err := recoverTxnIntentChange(change); err != nil {
		t.Fatalf("recover: %v", err)
	}
	assertFileDigest(t, dest, digest, size)

	// Recovery runs again on every later Open and mutation, so the second pass
	// has to be a no-op rather than a second placement.
	if err := recoverTxnIntentChange(change); err != nil {
		t.Fatalf("second recover: %v", err)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("a reached destination should leave no staged copy, stat = %v", err)
	}
}

func TestRecoveryLeavesADestinationAnotherWriterOwns(t *testing.T) {
	root := txnTestRoot(t, nil)
	source, digest, size := streamedSource(t, 1024)
	dest := filepath.Join(root, "attachments", "photo.png")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	staged, err := stageTempFileFrom(dest, source, size, digest, 0o644)
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if err := os.WriteFile(dest, []byte("not ours"), 0o644); err != nil {
		t.Fatalf("interloper write: %v", err)
	}
	change := txnIntentChange{Path: dest, Staged: staged, Mode: 0o644, NewSHA256: digest, NewSize: size}

	if err := recoverTxnIntentChange(change); err != nil {
		t.Fatalf("recover should decide, not fail: %v", err)
	}
	if got := readFileString(t, dest); got != "not ours" {
		t.Fatalf("destination = %q, the other writer's file must survive", got)
	}
	if _, err := os.Stat(staged); err != nil {
		t.Fatalf("the staged bytes exist nowhere else and must be kept: %v", err)
	}
}

func TestWriteFromRefusesAPlanItCannotVerify(t *testing.T) {
	root := txnTestRoot(t, nil)
	source, digest, size := streamedSource(t, 32)
	dest := filepath.Join(root, "attachments", "photo.png")
	cases := []struct {
		name   string
		source string
		size   int64
		digest string
	}{
		{"no source", "   ", size, digest},
		{"relative source", "upload.bin", size, digest},
		{"no digest", source, size, ""},
		{"short digest", source, size, "abc123"},
		{"negative size", source, -1, digest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			txn, err := NewFileTxn(root)
			if err != nil {
				t.Fatalf("new txn: %v", err)
			}
			if err := txn.WriteFrom(dest, tc.source, tc.size, tc.digest, 0o644); err == nil {
				t.Fatal("plan should be refused before anything is staged")
			}
		})
	}
}

// streamedBoundBytes is large enough that holding one copy of it is
// unmistakable in an allocation total and small enough to stay a cheap test.
const streamedBoundBytes = 8 << 20

// allocatedDuring reports the bytes allocated by fn. TotalAlloc is cumulative
// and never decreases, so it measures what was allocated rather than what
// happened to be live when the collector last ran — which is the question here:
// a whole-file read is a single big allocation that a GC between the two reads
// would hide from a live-heap measurement.
func allocatedDuring(t *testing.T, fn func()) uint64 {
	t.Helper()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// TestTheStreamedPathNeverHoldsTheFileItIsMoving is the assertion that makes the
// bound a guarantee rather than a comment. Every step of a streamed create —
// staging, the recovery intent, the rename, the rollback check, and the crash
// recovery that finishes it — has a whole-file counterpart it could regress to,
// and each one would still pass every other test in this package.
func TestTheStreamedPathNeverHoldsTheFileItIsMoving(t *testing.T) {
	root := txnTestRoot(t, map[string]string{"note.md": "old\n"})
	source, digest, size := streamedSource(t, streamedBoundBytes)
	dest := filepath.Join(root, "attachments", "photo.png")
	// One copy of the file is the regression being watched for; the ceiling is
	// well under that and far above the 32 KiB io.Copy buffer plus the ordinary
	// churn of a commit, so it fails on residency and not on noise.
	ceiling := uint64(streamedBoundBytes / 2)

	commit := allocatedDuring(t, func() {
		txn, err := NewFileTxn(root)
		if err != nil {
			t.Fatalf("new txn: %v", err)
		}
		if err := txn.WriteFrom(dest, source, size, digest, 0o644); err != nil {
			t.Fatalf("plan: %v", err)
		}
		if err := txn.Commit(nil); err != nil {
			t.Fatalf("commit: %v", err)
		}
	})
	if commit > ceiling {
		t.Fatalf("committing a %d byte attachment allocated %d bytes, want under %d", size, commit, ceiling)
	}

	// Recovery judges the destination and the staged copy by digest, so it is
	// bounded too — and it is the path that runs on every Open, including the
	// read-only commands.
	staged, err := stageTempFileFrom(filepath.Join(root, "attachments", "second.png"), source, size, digest, 0o644)
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	change := txnIntentChange{
		Path:      filepath.Join(root, "attachments", "second.png"),
		Staged:    staged,
		Mode:      0o644,
		NewSHA256: digest,
		NewSize:   size,
	}
	recover := allocatedDuring(t, func() {
		if err := recoverTxnIntentChange(change); err != nil {
			t.Fatalf("recover: %v", err)
		}
	})
	if recover > ceiling {
		t.Fatalf("recovering a %d byte attachment allocated %d bytes, want under %d", size, recover, ceiling)
	}
}

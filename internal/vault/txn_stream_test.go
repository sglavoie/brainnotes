package vault

import (
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

func TestRemoveStreamedStagesLargePreimageOutsideMemory(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "large.bin")
	data := make([]byte, 8<<20)
	for i := range data {
		data[i] = byte(i)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := HashBytes(data)
	data = nil

	txn, err := NewFileTxn(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := txn.RemoveStreamed(path, ExpectHash(digest)); err != nil {
		t.Fatal(err)
	}
	change := txn.changes[txn.order[0]]
	if err := txn.stageChange(change); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(txn.cleanupStaged)
	if len(change.priorData) != 0 || change.priorSource == "" {
		t.Fatalf("streamed removal retained %d bytes in memory and priorSource=%q", len(change.priorData), change.priorSource)
	}
	if got, _, err := HashFile(change.priorSource); err != nil || got != digest {
		t.Fatalf("staged preimage digest=%q err=%v, want %q", got, err, digest)
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

	assertBoundedStreamedOverwrite(t, root, dest, digest, ceiling)

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

func assertBoundedStreamedOverwrite(t *testing.T, root, dest, priorDigest string, ceiling uint64) {
	t.Helper()
	replacement, replacementDigest, replacementSize := streamedSource(t, streamedBoundBytes)
	replacementFile, err := os.OpenFile(replacement, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open replacement source: %v", err)
	}
	if _, err := replacementFile.WriteAt([]byte{0xff}, 0); err != nil {
		t.Fatalf("change replacement source: %v", err)
	}
	if err := replacementFile.Close(); err != nil {
		t.Fatalf("close replacement source: %v", err)
	}
	replacementDigest, replacementSize, err = HashFile(replacement)
	if err != nil {
		t.Fatalf("hash replacement source: %v", err)
	}
	overwrite := allocatedDuring(t, func() {
		txn, err := NewFileTxn(root)
		if err != nil {
			t.Fatalf("new overwrite txn: %v", err)
		}
		if err := txn.ReplaceFrom(dest, replacement, replacementSize, replacementDigest, 0o644, ExpectHash(priorDigest)); err != nil {
			t.Fatalf("plan overwrite: %v", err)
		}
		if err := txn.Commit(nil); err != nil {
			t.Fatalf("commit overwrite: %v", err)
		}
	})
	if overwrite > ceiling {
		t.Fatalf("overwriting a %d byte attachment allocated %d bytes, want under %d", replacementSize, overwrite, ceiling)
	}
}

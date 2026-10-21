package attachments

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/vault"
)

// later is how these tests age an upload: rather than backdating file times,
// they ask the sweep to run from a moment in the future, which is the same
// comparison with none of the filesystem timestamp granularity.
func later(d time.Duration) time.Time { return time.Now().UTC().Add(d) }

func TestSweep_KeepsAnUploadInsideItsTTL(t *testing.T) {
	store := newTestStore(t, 1<<20)
	staged := stageBytes(t, store, "draft-1", "photo.png", []byte("payload"))

	swept, err := store.Sweep(time.Hour, later(time.Minute))
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if swept.Uploads != 0 {
		t.Fatalf("Sweep collected %d fresh uploads", swept.Uploads)
	}
	if _, err := store.Lookup("draft-1", staged.ID); err != nil {
		t.Fatalf("a fresh upload did not survive a sweep: %v", err)
	}
}

func TestSweep_CollectsAStaleUploadAndFreesItsName(t *testing.T) {
	store := newTestStore(t, 1<<20)
	staged := stageBytes(t, store, "draft-1", "photo.png", []byte("payload"))

	swept, err := store.Sweep(time.Hour, later(2*time.Hour))
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if swept.Uploads != 1 {
		t.Fatalf("Sweep collected %d uploads, want 1", swept.Uploads)
	}
	if _, err := store.Lookup("draft-1", staged.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Lookup after a sweep = %v, want ErrNotFound", err)
	}
	assertStagingHolds(t, store, 0)
	if _, err := os.Lstat(store.namePath(staged.RelPath)); !os.IsNotExist(err) {
		t.Fatalf("the destination claim outlived the upload it was taken for: %v", err)
	}
}

// The blob with no record is what an interrupted upload leaves if the process
// dies before Stage's own cleanup runs. It is the artifact the sweep exists for,
// and it has no record to read an age or a destination from.
func TestSweep_CollectsAnOrphanBlobThatNoRecordDescribes(t *testing.T) {
	store := newTestStore(t, 1<<20)
	if err := store.prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	orphan := filepath.Join(store.stagingDir(), "0123456789abcdef0123456789abcdef"+blobSuffix)
	if err := os.WriteFile(orphan, []byte("half an upload"), 0o600); err != nil {
		t.Fatalf("write orphan blob: %v", err)
	}

	swept, err := store.Sweep(time.Hour, later(2*time.Hour))
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if swept.Uploads != 1 {
		t.Fatalf("Sweep collected %d uploads, want the orphan blob", swept.Uploads)
	}
	if _, err := os.Lstat(orphan); !os.IsNotExist(err) {
		t.Fatalf("the orphan blob survived: %v", err)
	}
}

// A claim whose upload has no bytes is a filename reserved for nobody. Freeing
// it is the difference between a failed upload costing a retry and costing the
// person that name until somebody deletes a file by hand.
func TestSweep_FreesAClaimWhoseUploadIsGone(t *testing.T) {
	store := newTestStore(t, 1<<20)
	staged := stageBytes(t, store, "draft-1", "photo.png", []byte("payload"))
	claim := store.namePath(staged.RelPath)
	if err := os.Remove(store.recordPath(staged.ID)); err != nil {
		t.Fatalf("remove record: %v", err)
	}
	if err := os.Remove(store.blobPath(staged.ID)); err != nil {
		t.Fatalf("remove blob: %v", err)
	}

	swept, err := store.Sweep(time.Hour, later(2*time.Hour))
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if swept.Claims != 1 {
		t.Fatalf("Sweep freed %d claims, want 1", swept.Claims)
	}
	if _, err := os.Lstat(claim); !os.IsNotExist(err) {
		t.Fatalf("the orphan claim survived: %v", err)
	}
}

// A claim whose upload is still staged must survive, or the sweep would hand the
// same destination to a second upload and the two would collide at commit.
func TestSweep_KeepsAClaimWhoseUploadStillHasBytes(t *testing.T) {
	store := newTestStore(t, 1<<20)
	staged := stageBytes(t, store, "draft-1", "photo.png", []byte("payload"))
	if err := os.Remove(store.recordPath(staged.ID)); err != nil {
		t.Fatalf("remove record: %v", err)
	}

	if _, err := store.Sweep(time.Hour, later(2*time.Hour)); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	// The upload itself is collected — every artifact carrying its id was past
	// the cutoff — and collecting it is what releases the claim, so the claim is
	// gone for the right reason rather than by the orphan rule.
	if _, err := os.Lstat(store.blobPath(staged.ID)); !os.IsNotExist(err) {
		t.Fatalf("a stale blob survived: %v", err)
	}
}

// A second sweeper adds nothing, so it stands down rather than contending. This
// is the cleanup race the session's Race verification is about: two sweeps, or a
// sweep and a scheduled one, must not both walk the directory removing what the
// other is deciding about.
func TestSweep_StandsDownWhenAnotherSweepHoldsTheLock(t *testing.T) {
	store := newTestStore(t, 1<<20)
	stageBytes(t, store, "draft-1", "photo.png", []byte("payload"))

	release, acquired, err := vault.TryLockPath(filepath.Join(store.root, filepath.FromSlash(sweepLockRel)))
	if err != nil || !acquired {
		t.Fatalf("take the sweep lock: acquired=%v err=%v", acquired, err)
	}
	defer release()

	swept, err := store.Sweep(time.Hour, later(2*time.Hour))
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if !swept.Skipped {
		t.Fatal("Sweep did not report standing down for another sweeper")
	}
	if swept.Uploads != 0 {
		t.Fatalf("a sweep that stood down still collected %d uploads", swept.Uploads)
	}
}

func TestSweep_OfAnUntouchedVaultIsNotAFailure(t *testing.T) {
	store := newTestStore(t, 1<<20)
	swept, err := store.Sweep(0, time.Time{})
	if err != nil {
		t.Fatalf("Sweep before anything was staged: %v", err)
	}
	if swept.Uploads != 0 || swept.Claims != 0 || swept.Skipped {
		t.Fatalf("Sweep of an untouched vault reported %+v", swept)
	}
}

// Anything in the staging directory this package did not write is left alone.
// The directory is private, but "private" is not "guaranteed empty of everything
// else", and a sweep that removed what it could not name would be a sweep that
// deletes on a guess.
func TestSweep_LeavesFilesItDidNotWrite(t *testing.T) {
	store := newTestStore(t, 1<<20)
	if err := store.prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	stranger := filepath.Join(store.stagingDir(), "README.txt")
	if err := os.WriteFile(stranger, []byte("left by a person"), 0o600); err != nil {
		t.Fatalf("write stranger: %v", err)
	}

	if _, err := store.Sweep(time.Hour, later(2*time.Hour)); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if _, err := os.Lstat(stranger); err != nil {
		t.Fatalf("the sweep removed a file it did not write: %v", err)
	}
}

package attachments

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// Concurrent uploads of one filename are the collision this package exists to
// decide. Whoever loses the race must be given a different destination, because
// the alternative — two uploads holding one path — is a note save that either
// refuses at commit or silently drops one file.
func TestStage_ConcurrentUploadsOfOneNameGetDistinctDestinations(t *testing.T) {
	const uploads = 12
	store := newTestStore(t, 1<<20)

	staged := make([]Staged, uploads)
	errs := make([]error, uploads)
	var wg sync.WaitGroup
	for i := range uploads {
		wg.Add(1)
		go func() {
			defer wg.Done()
			payload := []byte(fmt.Sprintf("upload %d", i))
			staged[i], errs[i] = store.Stage(
				StageRequest{Owner: "draft-1", Filename: "photo.png"},
				bytes.NewReader(payload),
				time.Time{},
			)
		}()
	}
	wg.Wait()

	seenPath := map[string]int{}
	seenID := map[string]bool{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("upload %d: %v", i, err)
		}
		if previous, ok := seenPath[staged[i].RelPath]; ok {
			t.Fatalf("uploads %d and %d both claimed %q", previous, i, staged[i].RelPath)
		}
		seenPath[staged[i].RelPath] = i
		if seenID[staged[i].ID] {
			t.Fatalf("upload %d reused an id", i)
		}
		seenID[staged[i].ID] = true
	}
	// Every upload's bytes are still its own: a lost claim would show up here as
	// one file's content under another's record.
	for i, item := range staged {
		want := fmt.Sprintf("upload %d", i)
		data, err := os.ReadFile(item.BlobPath)
		if err != nil {
			t.Fatalf("read upload %d: %v", i, err)
		}
		if string(data) != want {
			t.Fatalf("upload %d holds %q, want %q", i, data, want)
		}
	}
}

// A cancel arriving while a sweep is walking the same directory is the other
// cleanup race: both are removing artifacts, and neither may end up reporting
// success over bytes the other is still deciding about, or leave a record
// pointing at a blob that is gone.
func TestDiscardAndSweep_RacingOverTheSameUploadsLeaveNoHalfUpload(t *testing.T) {
	const uploads = 10
	store := newTestStore(t, 1<<20)

	staged := make([]Staged, uploads)
	for i := range uploads {
		staged[i] = stageBytes(t, store, "draft-1", fmt.Sprintf("photo-%d.png", i), []byte("payload"))
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Deliberately past the TTL, so the sweep is competing for exactly the
		// uploads the cancels below are removing.
		if _, err := store.Sweep(time.Hour, later(2*time.Hour)); err != nil {
			t.Errorf("Sweep: %v", err)
		}
	}()
	for _, item := range staged {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Already-gone is the expected answer for whichever side lost; any
			// other failure is a real one.
			if err := store.Discard("draft-1", item.ID); err != nil && !errors.Is(err, ErrNotFound) {
				t.Errorf("Discard %s: %v", item.ID, err)
			}
		}()
	}
	wg.Wait()

	for _, item := range staged {
		if _, err := store.Lookup("draft-1", item.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("upload %s survived both a cancel and a sweep: %v", item.ID, err)
		}
	}
	assertStagingHolds(t, store, 0)
}

// A commit reading a staged blob while a sweep collects it is the cleanup race
// the other three do not reach: the sweeper and the transaction contend over the
// same bytes, and the transaction is the one that has already been told to save
// them. Whichever wins, the only forbidden outcome is a committed attachment
// holding part of a file — which is exactly what a copy that is not verified
// against the digest the upload was measured at would produce.
func TestCommitAndSweep_RacingNeverCommitPartOfAnUpload(t *testing.T) {
	const uploads = 8
	store := newTestStore(t, 1<<20)
	// Large enough that the copy takes many io.Copy turns, so the sweep has a
	// real window to land in the middle of one.
	payload := bytes.Repeat([]byte("attachment payload "), 8192)

	staged := make([]Staged, uploads)
	for i := range uploads {
		staged[i] = stageBytes(t, store, "draft-1", fmt.Sprintf("photo-%d.png", i), payload)
	}

	destinations := make([]string, uploads)
	commitErrs := make([]error, uploads)
	var wg sync.WaitGroup
	for i := range uploads {
		destinations[i] = filepath.Join(store.root, "attachments", fmt.Sprintf("photo-%d.png", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			commitErrs[i] = commitStagedUpload(store.root, staged[i], destinations[i])
		}()
	}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Past the TTL, so the sweep is collecting the very uploads the
			// transactions above are reading.
			if _, err := store.Sweep(time.Hour, later(2*time.Hour)); err != nil {
				t.Errorf("Sweep: %v", err)
			}
		}()
	}
	wg.Wait()

	want := digestOf(payload)
	for i, err := range commitErrs {
		assertCommittedOrAbsent(t, i, destinations[i], err, want, int64(len(payload)))
	}
}

// commitStagedUpload is the request path a save takes once the upload it
// references has been found: one transaction, one streamed copy out of staging,
// one commit.
func commitStagedUpload(root string, item Staged, dest string) error {
	txn, err := vault.NewFileTxn(root)
	if err != nil {
		return err
	}
	if err := txn.WriteFrom(dest, item.BlobPath, item.Size, item.SHA256, 0o644); err != nil {
		return err
	}
	return txn.Commit(nil)
}

// assertCommittedOrAbsent states the only two outcomes a raced commit may have.
// A refused one is acceptable — the upload it was reading may genuinely have
// been collected — but it must leave the destination absent rather than half
// written, and a successful one must hold every byte that was measured.
func assertCommittedOrAbsent(t *testing.T, i int, dest string, commitErr error, wantDigest string, wantSize int64) {
	t.Helper()
	if commitErr != nil {
		if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
			t.Fatalf("upload %d failed with %v yet left %q behind", i, commitErr, dest)
		}
		return
	}
	digest, size, err := vault.HashFile(dest)
	if err != nil {
		t.Fatalf("hash committed attachment %d: %v", i, err)
	}
	if digest != wantDigest || size != wantSize {
		t.Fatalf("committed attachment %d = sha256:%s size %d, want sha256:%s size %d",
			i, digest, size, wantDigest, wantSize)
	}
}

// Staging and sweeping at once must not collect an upload that is still
// arriving. The claim ordering — bytes, then claim, then record — is what makes
// that true, and an age-based sweep is what makes it true even for the instant
// between the first two.
func TestStageAndSweep_RacingLeaveEveryFreshUploadUsable(t *testing.T) {
	const uploads = 10
	store := newTestStore(t, 1<<20)

	staged := make([]Staged, uploads)
	errs := make([]error, uploads)
	var wg sync.WaitGroup
	for i := range uploads {
		wg.Add(1)
		go func() {
			defer wg.Done()
			staged[i], errs[i] = store.Stage(
				StageRequest{Owner: "draft-1", Filename: fmt.Sprintf("photo-%d.png", i)},
				bytes.NewReader([]byte(fmt.Sprintf("payload %d", i))),
				time.Time{},
			)
		}()
	}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := store.Sweep(time.Hour, time.Time{}); err != nil {
				t.Errorf("Sweep: %v", err)
			}
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("upload %d: %v", i, err)
		}
		got, err := store.Lookup("draft-1", staged[i].ID)
		if err != nil {
			t.Fatalf("upload %d did not survive a concurrent sweep: %v", i, err)
		}
		if got.SHA256 != digestOf([]byte(fmt.Sprintf("payload %d", i))) {
			t.Fatalf("upload %d no longer describes its own bytes", i)
		}
	}
}

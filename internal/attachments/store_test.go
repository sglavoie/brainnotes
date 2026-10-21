package attachments

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/checkpoint"
	"github.com/sglavoie/brainnotes/internal/vaultfixture"
)

func TestStageWaitsForCheckpointBeforePublishingAnyUploadState(t *testing.T) {
	store := newTestStore(t, 1<<20)
	held, err := checkpoint.Hold(store.root, checkpoint.Options{Reason: "upload consistency", Drain: time.Second})
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, stageErr := store.Stage(StageRequest{Owner: "draft", Filename: "photo.png"}, bytes.NewReader([]byte("payload")), time.Time{})
		done <- stageErr
	}()
	select {
	case err := <-done:
		t.Fatalf("Stage completed during checkpoint: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	assertStagingHolds(t, store, 0)
	if err := held.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Stage after checkpoint: %v", err)
	}
	assertStagingHolds(t, store, 1)
}

// newTestStore builds a store over a disposable vault. Every test here writes,
// so the vault is a real fixture rather than a t.TempDir: the isolation refusals
// are the ones that keep a mutating test off somebody's actual notes.
func newTestStore(t *testing.T, maxBytes int64) *Store {
	t.Helper()
	v := vaultfixture.New(t, vaultfixture.Options{})
	store, err := NewStore(v.Root, "attachments", maxBytes)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func stageBytes(t *testing.T, store *Store, owner, filename string, payload []byte) Staged {
	t.Helper()
	staged, err := store.Stage(StageRequest{Owner: owner, Filename: filename}, bytes.NewReader(payload), time.Time{})
	if err != nil {
		t.Fatalf("Stage(%q): %v", filename, err)
	}
	return staged
}

func digestOf(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func TestStage_DescribesTheBytesItReceived(t *testing.T) {
	store := newTestStore(t, 1<<20)
	payload := bytes.Repeat([]byte("brainnotes"), 4096)

	staged := stageBytes(t, store, "draft-1", "photo.png", payload)

	switch {
	case staged.Size != int64(len(payload)):
		t.Errorf("Size = %d, want %d", staged.Size, len(payload))
	case staged.SHA256 != digestOf(payload):
		t.Errorf("SHA256 = %q, want %q", staged.SHA256, digestOf(payload))
	case staged.RelPath != "attachments/photo.png":
		t.Errorf("RelPath = %q, want attachments/photo.png", staged.RelPath)
	case staged.Owner != "draft-1":
		t.Errorf("Owner = %q, want draft-1", staged.Owner)
	case staged.CreatedAt.IsZero():
		t.Error("CreatedAt is zero, so a sweep could not age this upload")
	}
	onDisk, err := os.ReadFile(staged.BlobPath)
	if err != nil {
		t.Fatalf("read staged bytes: %v", err)
	}
	if !bytes.Equal(onDisk, payload) {
		t.Fatalf("staged bytes differ from what was uploaded (%d vs %d bytes)", len(onDisk), len(payload))
	}
}

// The staging area must be somewhere the rest of the vault already refuses to
// look. This asserts the location rather than trusting the constant, because
// every other guarantee in the package doc — out of the content history, out of
// the walk, unservable — is a consequence of it.
func TestStage_KeepsBytesUnderTheDottedStagingDirectory(t *testing.T) {
	store := newTestStore(t, 1<<20)
	staged := stageBytes(t, store, "draft-1", "photo.png", []byte("bytes"))

	rel, err := filepath.Rel(store.root, staged.BlobPath)
	if err != nil {
		t.Fatalf("relativize staged path: %v", err)
	}
	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, stagingRel+"/") {
		t.Fatalf("staged bytes live at %q, outside %q", rel, stagingRel)
	}
	if _, err := os.Lstat(filepath.Join(store.root, "attachments", "photo.png")); !os.IsNotExist(err) {
		t.Fatalf("staging put a file in the vault's own attachments directory: %v", err)
	}
}

func TestStage_RefusesAnOversizedUploadAndKeepsNothing(t *testing.T) {
	store := newTestStore(t, 16)

	_, err := store.Stage(
		StageRequest{Owner: "draft-1", Filename: "big.bin"},
		bytes.NewReader(bytes.Repeat([]byte("x"), 4096)),
		time.Time{},
	)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Stage of an oversized body = %v, want ErrTooLarge", err)
	}
	assertStagingHolds(t, store, 0)
}

// A body of exactly the limit is accepted, which is what makes the limit a limit
// rather than a suggestion one byte away from the documented number.
func TestStage_AcceptsABodyOfExactlyTheLimit(t *testing.T) {
	store := newTestStore(t, 16)
	staged := stageBytes(t, store, "draft-1", "exact.bin", bytes.Repeat([]byte("x"), 16))
	if staged.Size != 16 {
		t.Fatalf("Size = %d, want 16", staged.Size)
	}
}

// failingReader stops part-way, which is what a browser tab closed mid-upload
// looks like from this side of the connection.
type failingReader struct {
	data []byte
	at   int
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.at >= len(r.data) {
		return 0, errors.New("connection reset")
	}
	n := copy(p, r.data[r.at:min(r.at+64, len(r.data))])
	r.at += n
	return n, nil
}

func TestStage_InterruptedUploadIsInvisibleAndLeavesNoBytes(t *testing.T) {
	store := newTestStore(t, 1<<20)

	_, err := store.Stage(
		StageRequest{Owner: "draft-1", Filename: "photo.png"},
		&failingReader{data: bytes.Repeat([]byte("x"), 4096)},
		time.Time{},
	)
	if err == nil {
		t.Fatal("Stage of an interrupted body succeeded")
	}
	assertStagingHolds(t, store, 0)
}

// A partial upload must not leave a destination reserved either: the name the
// person chose has to still be available to the retry they are about to make.
func TestStage_InterruptedUploadFreesTheNameItWouldHaveTaken(t *testing.T) {
	store := newTestStore(t, 1<<20)
	_, _ = store.Stage(
		StageRequest{Owner: "draft-1", Filename: "photo.png"},
		&failingReader{data: bytes.Repeat([]byte("x"), 4096)},
		time.Time{},
	)

	retry := stageBytes(t, store, "draft-1", "photo.png", []byte("second time"))
	if retry.RelPath != "attachments/photo.png" {
		t.Fatalf("retry landed at %q; the interrupted upload never released the name", retry.RelPath)
	}
}

func TestStage_DivertsAroundAFileTheVaultAlreadyHolds(t *testing.T) {
	store := newTestStore(t, 1<<20)
	writeVaultFile(t, store, "attachments/photo.png", "already here")

	staged := stageBytes(t, store, "draft-1", "photo.png", []byte("new upload"))

	if staged.RelPath != "attachments/photo-1.png" {
		t.Fatalf("RelPath = %q, want attachments/photo-1.png", staged.RelPath)
	}
	if got := readVaultFile(t, store, "attachments/photo.png"); got != "already here" {
		t.Fatalf("existing file now holds %q", got)
	}
}

// Two names that fold to one are one file on the machine this vault syncs to, so
// the claim has to fold too. Without this the second upload would be granted a
// destination the first already owns and one of them would be lost at commit.
func TestStage_DivertsAroundACaseFoldedClaim(t *testing.T) {
	store := newTestStore(t, 1<<20)
	first := stageBytes(t, store, "draft-1", "Photo.PNG", []byte("first"))
	second := stageBytes(t, store, "draft-1", "photo.png", []byte("second"))

	if strings.EqualFold(first.RelPath, second.RelPath) {
		t.Fatalf("both uploads claimed %q / %q, which fold to one name", first.RelPath, second.RelPath)
	}
}

func TestStage_RefusesAnUnusableOwner(t *testing.T) {
	store := newTestStore(t, 1<<20)
	for name, owner := range map[string]string{
		"empty":   "  ",
		"control": "draft\n1",
		"long":    strings.Repeat("d", ownerMaxLen+1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := store.Stage(StageRequest{Owner: owner, Filename: "a.png"}, bytes.NewReader(nil), time.Time{})
			if !errors.Is(err, ErrInvalidOwner) {
				t.Fatalf("Stage with owner %q = %v, want ErrInvalidOwner", owner, err)
			}
		})
	}
	assertStagingHolds(t, store, 0)
}

func TestLookup_AnswersOnlyTheDraftThatStagedIt(t *testing.T) {
	store := newTestStore(t, 1<<20)
	staged := stageBytes(t, store, "draft-1", "photo.png", []byte("payload"))

	if _, err := store.Lookup("draft-1", staged.ID); err != nil {
		t.Fatalf("Lookup by the owning draft: %v", err)
	}
	if _, err := store.Lookup("draft-2", staged.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Lookup by another draft = %v, want ErrNotFound", err)
	}
}

func TestLookup_RefusesIDsThisPackageCouldNotHaveMinted(t *testing.T) {
	store := newTestStore(t, 1<<20)
	for name, id := range map[string]string{
		"empty":     "",
		"short":     "abcd",
		"uppercase": strings.Repeat("A", 32),
		"traversal": "../../../etc/passwd",
		"dotted":    strings.Repeat("a", 30) + "..",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := store.Lookup("draft-1", id); !errors.Is(err, ErrInvalidID) {
				t.Fatalf("Lookup(%q) = %v, want ErrInvalidID", id, err)
			}
		})
	}
}

// An upload whose record is there but whose bytes are not is not usable, and
// saying so here is what keeps a commit from opening a file that is gone.
func TestLookup_RefusesAnUploadWhoseBytesAreGone(t *testing.T) {
	store := newTestStore(t, 1<<20)
	staged := stageBytes(t, store, "draft-1", "photo.png", []byte("payload"))

	if err := os.Remove(staged.BlobPath); err != nil {
		t.Fatalf("remove staged bytes: %v", err)
	}
	if _, err := store.Lookup("draft-1", staged.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Lookup of an upload with no bytes = %v, want ErrNotFound", err)
	}
}

func TestDiscard_RemovesEveryArtifactAndFreesTheName(t *testing.T) {
	store := newTestStore(t, 1<<20)
	staged := stageBytes(t, store, "draft-1", "photo.png", []byte("payload"))

	if err := store.Discard("draft-1", staged.ID); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	assertStagingHolds(t, store, 0)
	if _, err := os.Lstat(store.namePath(staged.RelPath)); !os.IsNotExist(err) {
		t.Fatalf("the destination claim survived the discard: %v", err)
	}
	next := stageBytes(t, store, "draft-1", "photo.png", []byte("replacement"))
	if next.RelPath != "attachments/photo.png" {
		t.Fatalf("after a discard the name is still taken: %q", next.RelPath)
	}
}

func TestDiscard_RefusesAnotherDraftsUpload(t *testing.T) {
	store := newTestStore(t, 1<<20)
	staged := stageBytes(t, store, "draft-1", "photo.png", []byte("payload"))

	if err := store.Discard("draft-2", staged.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Discard by another draft = %v, want ErrNotFound", err)
	}
	if _, err := store.Lookup("draft-1", staged.ID); err != nil {
		t.Fatalf("the owning draft lost its upload to somebody else's cancel: %v", err)
	}
}

// Cancelling twice is what a retrying client does, and the second call must not
// be an error a person is shown.
func TestDiscard_OfAnAlreadyDiscardedUploadReportsItIsGone(t *testing.T) {
	store := newTestStore(t, 1<<20)
	staged := stageBytes(t, store, "draft-1", "photo.png", []byte("payload"))
	if err := store.Discard("draft-1", staged.ID); err != nil {
		t.Fatalf("first Discard: %v", err)
	}
	if err := store.Discard("draft-1", staged.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Discard = %v, want ErrNotFound", err)
	}
}

func TestNewStore_RefusesADestinationOutsideTheVaultsContent(t *testing.T) {
	v := vaultfixture.New(t, vaultfixture.Options{})
	for name, dir := range map[string]string{
		"empty":    "  ",
		"dot":      ".",
		"absolute": "/tmp/attachments",
		"parent":   "../elsewhere",
		"hidden":   ".bnotes/uploads",
		"nested":   "notes/.private/files",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewStore(v.Root, dir, 1<<20); err == nil {
				t.Fatalf("NewStore accepted destination %q", dir)
			}
		})
	}
}

func TestNewStore_RefusesANonPositiveLimit(t *testing.T) {
	v := vaultfixture.New(t, vaultfixture.Options{})
	for _, limit := range []int64{0, -1} {
		if _, err := NewStore(v.Root, "attachments", limit); err == nil {
			t.Fatalf("NewStore accepted a per-file limit of %d", limit)
		}
	}
}

// assertStagingHolds counts completed uploads by their records, which is the one
// artifact that means an upload exists, and additionally refuses any leftover
// blob — the thing a failed Stage is most likely to abandon.
func assertStagingHolds(t *testing.T, store *Store, want int) {
	t.Helper()
	entries, err := os.ReadDir(store.stagingDir())
	if err != nil {
		if os.IsNotExist(err) && want == 0 {
			return
		}
		t.Fatalf("read staging directory: %v", err)
	}
	records, blobs := 0, 0
	for _, entry := range entries {
		switch {
		case strings.HasSuffix(entry.Name(), recordSuffix):
			records++
		case strings.HasSuffix(entry.Name(), blobSuffix):
			blobs++
		}
	}
	if records != want || blobs != want {
		t.Fatalf("staging holds %d records and %d blobs, want %d of each", records, blobs, want)
	}
}

func writeVaultFile(t *testing.T, store *Store, rel, body string) {
	t.Helper()
	abs := filepath.Join(store.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir for %q: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatalf("write %q: %v", rel, err)
	}
}

func readVaultFile(t *testing.T, store *Store, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(store.root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %q: %v", rel, err)
	}
	return string(data)
}

var _ io.Reader = (*failingReader)(nil)

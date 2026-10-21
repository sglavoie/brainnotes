package attachments

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/brainnotes/internal/checkpoint"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// DefaultTTL is how long an upload nothing has committed is kept.
//
// It is long because the cost of being wrong is asymmetric. Sweeping too early
// takes away a file somebody is still composing a note around; sweeping too late
// costs disk in a directory that is already bounded by the per-file limit times
// however many files one person can plausibly drag into an editor. A day covers
// a draft left open overnight, which is the case this number exists for.
const DefaultTTL = 24 * time.Hour

// Swept is what one sweep did.
//
// Skipped is not a failure: another process held the sweep lock, which means a
// sweep is happening, which is the outcome the caller wanted. It is reported
// rather than hidden so a caller that schedules sweeps can tell "nothing was
// old" from "somebody else is doing it".
type Swept struct {
	Uploads int
	Claims  int
	Skipped bool
}

// Sweep collects staged uploads and destination claims older than ttl.
//
// What it is cleaning up is every way an upload can stop mattering without
// anybody saying so: a draft the user closed, a browser tab that died mid-body
// and left a blob with no record, a claim taken by an upload whose record write
// then failed, and a commit whose post-commit [Store.Forget] could not finish.
// All of them look the same from here — artifacts in the staging area older than
// ttl — which is why this is one age-based pass rather than four special cases.
//
// It takes [vault.TryLockPath] rather than a blocking lock, and never
// vault.LockPaths: this is optional work, a second sweeper has nothing to add,
// and LockPaths escalates to the same vault guard a transaction escalates to
// from the inside. Every failure below the lock is *collected* rather than
// returned immediately, because a single unreadable entry must not stop the pass
// that would have collected the other hundred.
func (s *Store) Sweep(ttl time.Duration, now time.Time) (Swept, error) {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	// A vault with no staging directory has nothing to collect — the claims live
	// inside it too — so the pass stops before the lock rather than after it.
	// Taking the lock would create a file, and a vault nobody has ever uploaded
	// to should not acquire one because a server started over it.
	if _, err := os.Stat(s.stagingDir()); os.IsNotExist(err) {
		return Swept{}, nil
	}
	// Before the sweep lock, and refused rather than skipped: a sweep that could
	// not join is a sweep that would have deleted staging files out from under a
	// checkpoint's copy. The caller already treats this pass as optional.
	barrier, err := checkpoint.Join(s.root)
	if err != nil {
		return Swept{Skipped: true}, nil
	}
	defer barrier()
	release, acquired, err := vault.TryLockPath(filepath.Join(s.root, filepath.FromSlash(sweepLockRel)))
	if err != nil {
		return Swept{}, fmt.Errorf("attachments: take sweep lock: %w", err)
	}
	if !acquired {
		return Swept{Skipped: true}, nil
	}
	defer release()

	cutoff := stamp(now).Add(-ttl)
	swept, uploadErr := s.sweepUploads(cutoff)
	claims, claimErr := s.sweepClaims(cutoff)
	swept.Claims = claims
	if uploadErr != nil {
		return swept, uploadErr
	}
	return swept, claimErr
}

// sweepUploads removes every blob and record whose upload has gone stale.
//
// Age is taken from the file's own modification time rather than from the
// record's CreatedAt, and deliberately so: the blob with no record — an upload
// that was interrupted — is the artifact most in need of collecting and the one
// with no CreatedAt to read. One rule that covers both is worth more than a
// slightly more accurate rule that covers one.
//
// A stale entry's claim is released through the record when there is one, so the
// destination becomes available again as soon as the upload that reserved it is
// gone.
func (s *Store) sweepUploads(cutoff time.Time) (Swept, error) {
	var swept Swept
	entries, err := os.ReadDir(s.stagingDir())
	if err != nil {
		if os.IsNotExist(err) {
			return swept, nil
		}
		return swept, fmt.Errorf("attachments: read staging directory: %w", err)
	}
	var failures []error
	for _, id := range staleUploadIDs(entries, cutoff) {
		rel := ""
		if record, recordErr := s.readRecord(id); recordErr == nil {
			rel = record.RelPath
		}
		if err := s.remove(id, rel); err != nil {
			failures = append(failures, err)
			continue
		}
		swept.Uploads++
	}
	return swept, firstFailure("sweep staged uploads", failures)
}

// staleUploadIDs picks the ids in one directory listing that are past cutoff.
//
// An id is stale when *every* artifact carrying it is, which is what keeps a
// record written a moment ago from being collected because its blob was opened
// first and the two straddled the cutoff. Anything that is not one of this
// package's own files — the names directory, a lock, something a person put
// there — is ignored by name rather than acted on.
func staleUploadIDs(entries []os.DirEntry, cutoff time.Time) []string {
	fresh := map[string]bool{}
	seen := map[string]bool{}
	for _, entry := range entries {
		id, ok := uploadIDOf(entry)
		if !ok {
			continue
		}
		seen[id] = true
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			// An entry that cannot be stat-ed is treated as fresh. The next pass
			// asks again, and the alternative — removing what cannot be described
			// — is the one mistake here that costs somebody their file.
			fresh[id] = true
		}
	}
	stale := make([]string, 0, len(seen))
	for id := range seen {
		if !fresh[id] {
			stale = append(stale, id)
		}
	}
	return stale
}

// uploadIDOf reports the upload a staging entry belongs to, if it belongs to one.
func uploadIDOf(entry os.DirEntry) (string, bool) {
	if entry.IsDir() {
		return "", false
	}
	name := entry.Name()
	for _, suffix := range []string{blobSuffix, recordSuffix} {
		id, ok := strings.CutSuffix(name, suffix)
		if !ok {
			continue
		}
		if validID(id) != nil {
			return "", false
		}
		return id, true
	}
	return "", false
}

// sweepClaims removes destination claims no upload is behind any more.
//
// A claim outlives its upload in two ways: the upload was swept, or its record
// write failed after the claim was taken and the cleanup did not finish. Both
// leave a name reserved for nobody, and a reservation nobody holds is a filename
// a person can no longer use.
//
// The blob is the liveness test — it is created *before* the claim and removed
// *after* it — so a claim whose upload still has bytes on disk is in use, even
// if the record has not landed yet. The age check is kept on top of that as the
// guard for the one gap the ordering leaves: the instant between a blob being
// created and its claim being taken, where a claim genuinely in flight has a
// blob the reader might not see yet.
//
//bn:direct-write removes one claim file under .bnotes/uploads/names, named by a digest this package wrote, whose upload id no longer has staged bytes
func (s *Store) sweepClaims(cutoff time.Time) (int, error) {
	entries, err := os.ReadDir(s.namesDir())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("attachments: read claim directory: %w", err)
	}
	removed := 0
	var failures []error
	for _, entry := range entries {
		claim := filepath.Join(s.namesDir(), entry.Name())
		stale, checkErr := s.claimIsStale(entry, claim, cutoff)
		if checkErr != nil {
			failures = append(failures, checkErr)
			continue
		}
		if !stale {
			continue
		}
		if err := os.Remove(claim); err != nil && !os.IsNotExist(err) {
			failures = append(failures, err)
			continue
		}
		removed++
	}
	return removed, firstFailure("sweep destination claims", failures)
}

// claimIsStale reports whether one claim file has outlived the upload that took
// it.
func (s *Store) claimIsStale(entry os.DirEntry, claim string, cutoff time.Time) (bool, error) {
	if entry.IsDir() || !strings.HasSuffix(entry.Name(), nameSuffix) {
		return false, nil
	}
	info, err := entry.Info()
	if err != nil || !info.ModTime().Before(cutoff) {
		return false, nil
	}
	held, err := os.ReadFile(claim)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	id := strings.TrimSpace(string(held))
	if validID(id) != nil {
		// A claim naming something this package could not have written is not one
		// of ours to reason about, and removing it would be freeing a name on a
		// guess.
		return false, nil
	}
	_, err = os.Lstat(s.blobPath(id))
	switch {
	case os.IsNotExist(err):
		return true, nil
	case err != nil:
		return false, err
	}
	return false, nil
}

// firstFailure folds a pass's collected failures into one error, keeping the
// count visible so a caller is not told about one unreadable entry when there
// were twelve.
func firstFailure(what string, failures []error) error {
	if len(failures) == 0 {
		return nil
	}
	if len(failures) == 1 {
		return fmt.Errorf("attachments: %s: %w", what, failures[0])
	}
	return fmt.Errorf("attachments: %s: %w (and %d more)", what, failures[0], len(failures)-1)
}

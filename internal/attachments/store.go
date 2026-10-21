package attachments

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/brainnotes/internal/checkpoint"
	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// Layout of the staging area, relative to the vault root. Every path this
// package writes is derived from these three and from a validated upload id, so
// there is no point where a client-supplied string becomes a path component.
const (
	stagingRel   = ".bnotes/uploads"
	namesRel     = "names"
	sweepLockRel = ".bnotes/locks/attachments-sweep.lock"
)

// blobSuffix and recordSuffix keep the bytes and the record in one directory
// while staying trivially separable by name, which is what lets [Store.Sweep]
// pair them up from a single directory read.
const (
	blobSuffix   = ".bin"
	recordSuffix = ".json"
	nameSuffix   = ".name"
)

// Store is one vault's staging area.
//
// It is deliberately given the destination directory and the size limit rather
// than a *config.Config: the two values it needs are decided differently — the
// directory comes from the vault's portable configuration, the limit from that
// configuration clamped by the transport bound the serving mode allows — and a
// store that took the config would be the place that had to know about the
// second one.
type Store struct {
	root     string
	dir      string
	maxBytes int64
}

// NewStore resolves a staging area over vaultRoot.
//
// destDir is re-validated here even though config.validateAttachments already
// refused the same shapes, because this store is reachable from a test and from
// any future caller that builds a config in memory, and the cost of asking twice
// is one function nobody has to trace to trust.
func NewStore(vaultRoot, destDir string, maxFileBytes int64) (*Store, error) {
	root, err := filepath.Abs(strings.TrimSpace(vaultRoot))
	if err != nil || strings.TrimSpace(vaultRoot) == "" {
		return nil, fmt.Errorf("attachments: unusable vault root %q", vaultRoot)
	}
	dir, err := cleanDestinationDir(destDir)
	if err != nil {
		return nil, err
	}
	if maxFileBytes <= 0 {
		return nil, fmt.Errorf("attachments: per-file limit must be positive, got %d", maxFileBytes)
	}
	return &Store{root: root, dir: dir, maxBytes: maxFileBytes}, nil
}

// cleanDestinationDir refuses a destination that would put an attachment
// somewhere the vault does not treat as its own content.
func cleanDestinationDir(destDir string) (string, error) {
	dir := path.Clean(strings.TrimSpace(strings.ReplaceAll(destDir, `\`, "/")))
	switch {
	case dir == "" || dir == "." || dir == "/" || path.IsAbs(dir):
		return "", fmt.Errorf("attachments: unusable destination directory %q", destDir)
	case dir == ".." || strings.HasPrefix(dir, "../"):
		return "", fmt.Errorf("attachments: destination directory %q leaves the vault", destDir)
	}
	for _, part := range strings.Split(dir, "/") {
		if strings.HasPrefix(part, ".") {
			return "", fmt.Errorf("attachments: destination directory %q is hidden", destDir)
		}
	}
	return dir, nil
}

func (s *Store) stagingDir() string { return filepath.Join(s.root, filepath.FromSlash(stagingRel)) }
func (s *Store) namesDir() string   { return filepath.Join(s.stagingDir(), namesRel) }
func (s *Store) blobPath(id string) string {
	return filepath.Join(s.stagingDir(), id+blobSuffix)
}
func (s *Store) recordPath(id string) string {
	return filepath.Join(s.stagingDir(), id+recordSuffix)
}

// namePath is where the claim on a vault-relative destination lives.
//
// The key is [vaultsync.CollisionKey], which is this repository's one statement
// of when two paths are one name — NFC, case-folded, NFC again. It is borrowed
// rather than restated because the question here is exactly the question there:
// `Photo.png` and `photo.png` are two claims on a case-sensitive filesystem and
// one file on the machine the vault is synchronized to, and a claim that did not
// fold would let both be granted and one of them be lost at commit.
//
// The key is hashed rather than used directly, so the claim's own filename is
// fixed-length hex and carries none of the user's name into a directory listing.
func (s *Store) namePath(rel string) string {
	sum := sha256.Sum256([]byte(vaultsync.CollisionKey(rel)))
	return filepath.Join(s.namesDir(), hex.EncodeToString(sum[:])+nameSuffix)
}

// StageRequest is what a client says about an upload before its bytes arrive.
type StageRequest struct {
	// Owner is the draft the upload belongs to: an opaque client-minted string
	// that scopes a cancel and lets an editor tab clean up after itself. It never
	// becomes a path component.
	Owner string
	// Filename is what the browser called the file. It is data until
	// cleanFilename has had it.
	Filename string
}

// Staged is a completed upload together with where its bytes are.
//
// BlobPath is absolute and inside the staging area. It is handed to
// vault.FileTxn.WriteFrom at commit, which re-hashes the bytes as it copies
// them, so this struct never has to be trusted about content.
type Staged struct {
	Record
	BlobPath string
}

// Stage receives one upload and makes it referenceable.
//
// The body is streamed and never held: the bytes go through a hasher into an
// exclusive file, bounded by the store's per-file limit, and the digest and size
// that come out of that one pass are the only description of the content that
// exists afterwards. An upload the client abandoned mid-body fails here, leaves
// no record, and is therefore invisible to everything above.
func (s *Store) Stage(req StageRequest, src io.Reader, now time.Time) (Staged, error) {
	// Staging is three durable steps — blob, destination-name claim, record — and
	// only all three together describe an upload. A copy taken between them holds
	// a blob nothing points at or a claim on a name nothing owns, which is why the
	// whole of Stage is inside one join rather than each write being its own.
	barrier, err := checkpoint.Join(s.root)
	if err != nil {
		return Staged{}, fmt.Errorf("attachments: %w", err)
	}
	defer barrier()
	owner, err := cleanOwner(req.Owner)
	if err != nil {
		return Staged{}, err
	}
	filename, err := cleanFilename(req.Filename)
	if err != nil {
		return Staged{}, err
	}
	id, err := newUploadID()
	if err != nil {
		return Staged{}, err
	}
	if err := s.prepare(); err != nil {
		return Staged{}, err
	}
	digest, size, err := s.receive(s.blobPath(id), src)
	if err != nil {
		return Staged{}, err
	}
	record := Record{
		Version: recordVersion, ID: id, Owner: owner, Filename: filename,
		Size: size, SHA256: digest, CreatedAt: stamp(now),
	}
	if record.RelPath, err = s.reserve(filename, id); err != nil {
		s.drop(id, "")
		return Staged{}, err
	}
	if err := s.writeRecord(record); err != nil {
		s.drop(id, record.RelPath)
		return Staged{}, err
	}
	return Staged{Record: record, BlobPath: s.blobPath(id)}, nil
}

// stamp normalizes the caller's clock. A zero time means "now"; every stored
// time is UTC, because the record is read back by a sweep comparing ages.
func stamp(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now.UTC()
}

// prepare creates the staging directories, once the location policy has allowed
// them.
//
// The guard is asked about the staging root and nothing else. Every other path
// this package writes is inside it, so one refusal covers them all, and asking
// here rather than per-write is what keeps a refused vault from ever having an
// empty `.bnotes/uploads` created in it.
//
//bn:direct-write creates the two staging directories under .bnotes/uploads after vault.GuardMutation has allowed the location; they hold no note content, are outside vaultgit.InScope, and are collected by Sweep
func (s *Store) prepare() error {
	if err := vault.GuardMutation(s.root, s.stagingDir()); err != nil {
		return err
	}
	if err := os.MkdirAll(s.namesDir(), 0o700); err != nil {
		return fmt.Errorf("attachments: create staging directory: %w", err)
	}
	return nil
}

// receive streams src into a fresh exclusive file, refusing anything over the
// per-file limit.
//
// The limit is applied to the reader rather than to a Content-Length, because a
// declared length is a claim and the body is the fact; one byte past the limit
// is read on purpose, since a copy that stopped exactly at the limit could not
// tell a file of precisely that size from one that was cut short. Nothing is
// buffered, so a limit of 25 MiB costs 32 KiB of memory and not 25 MiB.
//
//bn:direct-write creates and, on any failure, removes exactly one file it named itself from a validated upload id, inside the staging directory vault.GuardMutation already allowed
func (s *Store) receive(blob string, src io.Reader) (string, int64, error) {
	file, err := os.OpenFile(blob, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", 0, fmt.Errorf("attachments: create staging file: %w", err)
	}
	hasher := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(file, hasher), io.LimitReader(src, s.maxBytes+1))
	switch {
	case copyErr != nil:
		err = fmt.Errorf("attachments: receive upload: %w", copyErr)
	case size > s.maxBytes:
		err = fmt.Errorf("%w of %d bytes", ErrTooLarge, s.maxBytes)
	}
	if err != nil {
		_ = file.Close()
	} else {
		err = syncAndClose(file, blob)
	}
	if err != nil {
		_ = os.Remove(blob)
		return "", 0, err
	}
	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}

// syncAndClose makes a staged file durable before anything is allowed to depend
// on it.
//
// Both fsyncs matter and for different reasons: the file's, because the record
// written after it is a promise that these exact bytes are on disk, and the
// directory's, because a name that is not durable is a blob a sweep cannot find
// and a commit cannot open.
func syncAndClose(file *os.File, path string) error {
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("attachments: fsync %q: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("attachments: close %q: %w", path, err)
	}
	if err := vault.SyncDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("attachments: fsync staging directory: %w", err)
	}
	return nil
}

// reserve claims a vault-relative destination for filename and returns it.
//
// This is the whole of the collision story, and it happens here rather than at
// commit for one reason: a transaction can only refuse a destination that is
// taken, and refusing is the wrong answer to two people uploading a file called
// `photo.png`. Claiming while the uploads are still private turns that into two
// destinations, decided before either note is saved, with nothing left for the
// commit to negotiate.
func (s *Store) reserve(filename, id string) (string, error) {
	for _, candidate := range destinationCandidates(s.dir, filename, id) {
		claimed, err := s.claim(candidate, id)
		if err != nil {
			return "", err
		}
		if claimed {
			return candidate, nil
		}
	}
	// Unreachable while the last candidate carries the upload id, which no other
	// upload can have. Reported rather than assumed, because the day it is wrong
	// is the day an upload would otherwise be committed over somebody's file.
	return "", fmt.Errorf("attachments: no free destination for %q", filename)
}

// claim takes the O_EXCL claim on one candidate destination, reporting whether
// it got it.
//
// Two things can make a candidate unavailable and they are asked in this order
// on purpose. A file already in the vault is checked first because it is the
// case a person can see and the one that is true regardless of what any other
// upload is doing; the claim file is checked second, by trying to create it,
// which is the only way to ask that question without a gap between the asking
// and the taking.
//
//bn:direct-write creates one claim file under .bnotes/uploads/names whose name is a digest this package computed, inside the staging directory vault.GuardMutation already allowed; it records an upload id and no content, and Sweep collects it
func (s *Store) claim(rel, id string) (bool, error) {
	abs := filepath.Join(s.root, filepath.FromSlash(rel))
	switch _, err := os.Lstat(abs); {
	case err == nil:
		return false, nil
	case !os.IsNotExist(err):
		return false, fmt.Errorf("attachments: inspect destination %q: %w", rel, err)
	}
	claim := s.namePath(rel)
	file, err := os.OpenFile(claim, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	switch {
	case os.IsExist(err):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("attachments: claim destination %q: %w", rel, err)
	}
	if _, err := file.WriteString(id); err != nil {
		_ = file.Close()
		_ = os.Remove(claim)
		return false, fmt.Errorf("attachments: claim destination %q: %w", rel, err)
	}
	if err := syncAndClose(file, claim); err != nil {
		_ = os.Remove(claim)
		return false, err
	}
	return true, nil
}

// writeRecord makes the upload visible.
//
// It is the last of the three artifacts and it is what [Store.Lookup] reads, so
// until it is fsynced and its directory entry is durable the upload does not
// exist as far as anything above this package is concerned. O_EXCL rather than a
// replacement: the id is fresh, so a record already there is a bug worth hearing
// about rather than something to overwrite.
//
//bn:direct-write creates one record file named by a validated upload id inside the staging directory vault.GuardMutation already allowed; it holds a digest, a size and a sanitized filename rather than any note content
func (s *Store) writeRecord(record Record) error {
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("attachments: render upload record: %w", err)
	}
	path := s.recordPath(record.ID)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("attachments: create upload record: %w", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("attachments: write upload record: %w", err)
	}
	if err := syncAndClose(file, path); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

// Lookup resolves one of owner's uploads.
//
// It answers from the record, then confirms the bytes are still there and still
// the size the record claims. It deliberately does not re-hash them: the commit
// streams the file through a hasher and compares against the digest anyway, so
// hashing here would read a 25 MiB attachment twice to move a failure a few
// milliseconds earlier.
func (s *Store) Lookup(owner, id string) (Staged, error) {
	cleaned, err := cleanOwner(owner)
	if err != nil {
		return Staged{}, err
	}
	if err := validID(id); err != nil {
		return Staged{}, err
	}
	record, err := s.readRecord(id)
	if err != nil {
		return Staged{}, err
	}
	if record.Owner != cleaned {
		return Staged{}, ErrNotFound
	}
	blob := s.blobPath(id)
	info, err := os.Lstat(blob)
	switch {
	case os.IsNotExist(err):
		return Staged{}, ErrNotFound
	case err != nil:
		return Staged{}, fmt.Errorf("attachments: inspect staged upload: %w", err)
	case !info.Mode().IsRegular() || info.Size() != record.Size:
		return Staged{}, fmt.Errorf("attachments: staged upload %s no longer matches its record", id)
	}
	return Staged{Record: record, BlobPath: blob}, nil
}

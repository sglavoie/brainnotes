package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// A streamed change is the same transaction member every other write is — it
// locks, stages, journals, and rolls back identically — with one difference: its
// bytes are named by a file rather than carried in a []byte.
//
// That difference exists because FileTxn.Write holds its content for the whole
// commit. The slice is staged, hashed into the recovery intent, compared against
// the destination before a rollback, and compared again by the rollback itself,
// so a caller committing a 25 MiB attachment beside a note would hold the
// attachment in memory from plan to commit, and a request handler that accepted
// several would hold all of them. A file the caller already wrote somewhere is
// the one thing that is durable without being resident, so WriteFrom takes that
// path plus the digest and size the caller measured while writing it, and every
// later step that would have hashed the slice pins the digest instead.
//
// It is deliberately create-only. An overwrite has to capture the bytes it
// displaces — that capture is what makes the compare-and-swap and the
// compensating rollback possible — and capturing them means reading them, which
// would put back exactly the residency this exists to remove. A destination that
// must be absent has nothing to capture, so the whole path stays bounded: stage
// copies source to a sibling temp file through a hasher, apply places that temp
// file exclusively, and a rollback removes what it placed.
//
// The digest is not decoration. Staging verifies the copy against it, so a
// staging file another writer truncated, replaced, or left half-written is
// refused before anything is renamed, and the bytes the transaction commits are
// provably the bytes the caller measured.

// WriteFrom plans creating path from the bytes at sourcePath, which must hold
// exactly size bytes hashing to sha256Hex. A zero mode derives a new file's mode
// from the process umask.
//
// sourcePath is read, never moved or removed: the caller owns it and is free to
// retry, discard, or sweep it. It does not have to live inside the vault, but it
// does have to be on the same volume as path, because staging copies it into a
// sibling temp file of the destination and that temp file is what gets installed.
//
// The destination must be absent. See this file's leading comment for why an
// overwrite cannot be streamed.
func (t *FileTxn) WriteFrom(path, sourcePath string, size int64, sha256Hex string, mode os.FileMode) error {
	if t == nil {
		return errors.New("vault: nil transaction")
	}
	source := strings.TrimSpace(sourcePath)
	if source == "" {
		return errors.New("vault: streamed change needs a source file")
	}
	if !filepath.IsAbs(source) {
		return fmt.Errorf("vault: streamed source %q must be an absolute path", sourcePath)
	}
	digest := strings.ToLower(strings.TrimSpace(sha256Hex))
	if !validHexDigest(digest) {
		return fmt.Errorf("vault: streamed source %q needs a sha256 content digest", sourcePath)
	}
	if size < 0 {
		return fmt.Errorf("vault: streamed source %q cannot have a negative size", sourcePath)
	}
	return t.plan(&txnChange{
		path:   path,
		source: source,
		digest: digest,
		size:   size,
		mode:   mode,
		expect: ExpectMissing(),
	})
}

// validHexDigest reports whether token has the shape HashBytes produces. A
// malformed digest would otherwise never match anything and surface as a
// conflict, sending somebody looking for a concurrent writer that never existed.
func validHexDigest(token string) bool {
	if len(token) != sha256.Size*2 {
		return false
	}
	for i := 0; i < len(token); i++ {
		c := token[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// HashFile returns the lowercase hex SHA-256 of a file's bytes and its size
// without holding the file in memory. It is the streamed counterpart of
// HashBytes, for content a caller measures on its way to disk rather than in a
// slice it already has.
func HashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = file.Close() }()
	hasher := sha256.New()
	size, err := io.Copy(hasher, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}

// stageStreamedChange stages a create whose replacement bytes live in a file.
//
// Nothing is read back from the destination, which is the point: the
// precondition is absence, and absence is a stat. The ordinary staging path
// calls currentFileState, which reads whatever is there into memory before
// judging it — harmless for a note, but for a streamed create it would mean a
// file another writer dropped at the destination gets read in full only to be
// refused.
func (t *FileTxn) stageStreamedChange(change *txnChange) error {
	if err := verifyAbsent(change.path); err != nil {
		return err
	}
	wantMode, err := stagedMode(change.path, change.mode, 0, false)
	if err != nil {
		return err
	}
	staged, err := stageTempFileFrom(change.path, change.source, change.size, change.digest, wantMode)
	if err != nil {
		return err
	}
	change.staged = staged
	change.mode = wantMode
	return nil
}

// stageTempFileFrom copies source into a sibling temp file of dest with the
// final mode already applied, and refuses the copy unless it is exactly the size
// and digest the caller pinned.
//
// The verification is not belt and braces. source is a file some other part of
// the process wrote and may still be sweeping, and the digest is what the caller
// measured as it wrote; comparing the copy against both is what turns a
// truncated, replaced, or half-swept staging file into a refusal before any
// destination is touched, rather than into a committed attachment whose bytes
// nobody ever verified.
func stageTempFileFrom(dest, source string, size int64, digest string, mode os.FileMode) (string, error) {
	in, err := os.Open(source)
	if err != nil {
		return "", fmt.Errorf("vault: open streamed source %q: %w", source, err)
	}
	defer func() { _ = in.Close() }()

	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("vault: mkdir %q: %w", dir, err)
	}
	tmp, tmpPath, err := openExclusiveTemp(dir, ".txn-", ".tmp", 0o600)
	if err != nil {
		return "", fmt.Errorf("vault: tempfile in %q: %w", dir, err)
	}
	hasher := sha256.New()
	copied, copyErr := io.Copy(io.MultiWriter(tmp, hasher), in)
	if copyErr != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("vault: copy %q -> %q: %w", source, tmpPath, copyErr)
	}
	if actual := hex.EncodeToString(hasher.Sum(nil)); copied != size || actual != digest {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", &TxnConflictError{
			Path:     source,
			Expected: ExpectHash(digest).describe(),
			Actual:   "file(sha256=" + actual + ")",
		}
	}
	return finishTempFile(tmp, tmpPath, mode)
}

// verifyAbsent refuses a destination that must not exist, judged by stat alone
// so a create never reads the bytes it is refusing to replace.
func verifyAbsent(path string) error {
	info, err := os.Lstat(path)
	switch {
	case err == nil:
	case os.IsNotExist(err):
		return nil
	default:
		return fmt.Errorf("vault: stat %q: %w", path, err)
	}
	actual := "file"
	if !info.Mode().IsRegular() {
		actual = nonRegularLabel(info)
	}
	return &TxnConflictError{Path: path, Expected: "missing", Actual: actual}
}

// verifyFileDigest checks a path against expect by streaming its bytes through a
// hasher, so content that was never in memory is not read into it to be
// verified. It reports the same *TxnConflictError verifyExpectation does.
func verifyFileDigest(path string, expect FileExpectation) error {
	info, err := os.Lstat(path)
	switch {
	case err == nil:
	case os.IsNotExist(err):
		return &TxnConflictError{Path: path, Expected: expect.describe(), Actual: "missing"}
	default:
		return fmt.Errorf("vault: stat %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return &TxnConflictError{Path: path, Expected: expect.describe(), Actual: nonRegularLabel(info)}
	}
	digest, _, err := HashFile(path)
	if err != nil {
		return classifyStateReadError(path, err)
	}
	if !expect.Exists || (expect.SHA256 != "" && expect.SHA256 != digest) {
		return &TxnConflictError{Path: path, Expected: expect.describe(), Actual: "file(sha256=" + digest + ")"}
	}
	return nil
}

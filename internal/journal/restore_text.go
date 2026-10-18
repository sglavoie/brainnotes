package journal

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"unicode/utf8"
)

// RestoreText is a bounded, verified reading of the two versions in a restore.
// CurrentDigest is empty for an absent path, otherwise sha256:<hex>, so callers
// can bind the comparison to their existing preview receipt.
type RestoreText struct {
	Current       string
	Restored      string
	CurrentDigest string
}

// ReadRestoreText reads only the named recorded path and its validated snapshot.
// It never writes or locks. Callers decide which classes of file to expose.
func ReadRestoreText(vaultRoot, journalRoot, entryID, path string, maxBytes int64) (*RestoreText, error) {
	entry, err := LoadEntry(journalRoot, entryID)
	if err != nil {
		return nil, err
	}
	for _, record := range entry.Files {
		if record.Path == path {
			return readRestoreTextRecord(vaultRoot, journalRoot, entryID, record, maxBytes)
		}
	}
	return nil, fmt.Errorf("path is not part of this recorded change")
}

func readRestoreTextRecord(vaultRoot, journalRoot, entryID string, record FileRecord, maxBytes int64) (*RestoreText, error) {
	abs, _, err := normalizeWithinRoot(vaultRoot, record.Path)
	if err != nil {
		return nil, err
	}
	current, exists, err := readComparisonFile(abs, maxBytes)
	if err != nil {
		return nil, err
	}
	restored, err := readRestoreSnapshot(journalRoot, entryID, record.Before, maxBytes)
	if err != nil {
		return nil, err
	}
	result := &RestoreText{Current: string(current), Restored: string(restored)}
	if exists {
		result.CurrentDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(current))
	}
	return result, nil
}

func readRestoreSnapshot(root, id string, state FileState, maxBytes int64) ([]byte, error) {
	if state.Kind == KindMissing {
		return nil, nil
	}
	if state.Kind != KindFile {
		return nil, fmt.Errorf("this path is not a text file")
	}
	dir, err := entryDir(root, id)
	if err != nil {
		return nil, err
	}
	abs, err := resolveSnapshotPath(dir, state.Snapshot)
	if err != nil {
		return nil, err
	}
	data, exists, err := readComparisonFile(abs, maxBytes)
	if err != nil {
		return nil, err
	}
	if !exists || int64(len(data)) != state.Size || fmt.Sprintf("%x", sha256.Sum256(data)) != state.SHA256 {
		return nil, fmt.Errorf("the recorded snapshot could not be verified")
	}
	return data, nil
}

func readComparisonFile(abs string, maxBytes int64) ([]byte, bool, error) {
	info, err := os.Lstat(abs)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("this path is not a regular text file")
	}
	if maxBytes <= 0 || info.Size() > maxBytes {
		return nil, false, fmt.Errorf("this file exceeds the text comparison limit")
	}
	file, err := os.Open(abs)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, false, fmt.Errorf("the file changed while reading it; preview again")
	}
	data, err := readComparisonBytes(file, maxBytes)
	return data, err == nil, err
}

func readComparisonBytes(file *os.File, maxBytes int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("this file exceeds the text comparison limit")
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, fmt.Errorf("binary content has no text comparison")
	}
	return data, nil
}

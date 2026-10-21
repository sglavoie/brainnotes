package vault

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func recoverTempOwners(root string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	dir := filepath.Join(realRoot, txnIntentRelDir, "owners")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), txnOwnerSuffix) {
			continue
		}
		if err := recoverTempOwner(realRoot, filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func transferTempOwnersForIntent(root string, changes []txnIntentChange) error {
	staged := intentStagedPaths(changes)
	if len(staged) == 0 {
		return nil
	}
	dir := filepath.Join(root, txnIntentRelDir, "owners")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), txnOwnerSuffix) {
			continue
		}
		if err := transferTempOwnerIfMatching(filepath.Join(dir, entry.Name()), staged); err != nil {
			return err
		}
	}
	return nil
}

func intentStagedPaths(changes []txnIntentChange) map[string]struct{} {
	staged := make(map[string]struct{})
	for _, change := range changes {
		if change.Staged != "" {
			staged[change.Staged] = struct{}{}
		}
		if change.PriorStaged != "" {
			staged[change.PriorStaged] = struct{}{}
		}
	}
	return staged
}

func transferTempOwnerIfMatching(recordPath string, staged map[string]struct{}) error {
	_, active, release, err := openTempOwnerForRecovery(recordPath)
	if err != nil || !active {
		return err
	}
	defer release()
	record, found, err := readHeldTempOwnerRecord(recordPath)
	if err != nil || !found {
		return err
	}
	if record.Transferred || !ownerHasStagedPath(record.Entries, staged) {
		return nil
	}
	record.Transferred = true
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return replaceTempOwnerRecord(recordPath, data)
}

func ownerHasStagedPath(entries []txnTempOwnerEntry, staged map[string]struct{}) bool {
	for _, entry := range entries {
		if _, ok := staged[entry.Temp]; ok {
			return true
		}
	}
	return false
}

func recoverTempOwner(root, recordPath string) error {
	_, active, release, err := openTempOwnerForRecovery(recordPath)
	if err != nil || !active {
		return err
	}
	defer release()
	record, found, err := readHeldTempOwnerRecord(recordPath)
	if err != nil || !found {
		return err
	}
	if !record.Transferred {
		if err := removeTempOwnerEntries(root, record.Entries); err != nil {
			return err
		}
	}
	return removeTempOwnerMetadata(recordPath)
}

func openTempOwnerForRecovery(recordPath string) (*os.File, bool, func(), error) {
	lockPath := strings.TrimSuffix(recordPath, txnOwnerSuffix) + txnOwnerLockSuffix
	file, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, false, func() {}, err
	}
	release := func() {
		_ = unlockFile(file)
		_ = file.Close()
	}
	held, err := tryLock(file, true)
	if err != nil {
		_ = file.Close()
		return nil, false, func() {}, err
	}
	return file, held && sameOpenFile(file, lockPath), release, nil
}

// readHeldTempOwnerRecord reads a record whose lock the caller holds. A record
// listed by a directory scan can vanish before its lock is taken: its owner
// removes the record, unlocks, and only then removes the lock. Holding the lock
// with no record therefore means the owner already finished, so the leftover
// lock is housekeeping, not an error.
func readHeldTempOwnerRecord(recordPath string) (txnTempOwnerRecord, bool, error) {
	record, err := readTempOwnerRecord(recordPath)
	if errors.Is(err, fs.ErrNotExist) {
		return txnTempOwnerRecord{}, false, removeTempOwnerMetadata(recordPath)
	}
	return record, err == nil, err
}

func readTempOwnerRecord(recordPath string) (txnTempOwnerRecord, error) {
	data, err := os.ReadFile(recordPath)
	if err != nil {
		return txnTempOwnerRecord{}, err
	}
	var record txnTempOwnerRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return txnTempOwnerRecord{}, err
	}
	return record, nil
}

func removeTempOwnerMetadata(recordPath string) error {
	lockPath := strings.TrimSuffix(recordPath, txnOwnerSuffix) + txnOwnerLockSuffix
	if err := os.Remove(recordPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Remove(lockPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return SyncDir(filepath.Dir(recordPath))
}

func removeTempOwnerEntries(root string, entries []txnTempOwnerEntry) error {
	for _, entry := range entries {
		if !containedIn(root, entry.Temp) {
			return fmt.Errorf("vault: temp owner escapes root")
		}
		if !entry.Preserve {
			_ = os.Remove(entry.Temp)
		}
	}
	return nil
}

package vault

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	txnOwnerSuffix     = ".owner"
	txnOwnerLockSuffix = ".lock"
)

type txnTempOwner struct {
	root        string
	record      string
	lock        string
	file        *os.File
	entries     []txnTempOwnerEntry
	transferred bool
}

type txnTempOwnerEntry struct {
	Temp     string `json:"temp"`
	Preserve bool   `json:"preserve,omitempty"`
}

type txnTempOwnerRecord struct {
	Entries     []txnTempOwnerEntry `json:"entries"`
	Transferred bool                `json:"transferred,omitempty"`
}

func (t *FileTxn) openOwnedTemp(_ string) (*os.File, string, error) {
	return t.openTemp(false)
}

func (t *FileTxn) openPreservedTemp() (*os.File, string, error) {
	return t.openTemp(true)
}

func (t *FileTxn) openTemp(preserve bool) (*os.File, string, error) {
	owner, err := t.ensureTempOwner()
	if err != nil {
		return nil, "", err
	}
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, "", err
	}
	temp := filepath.Join(filepath.Dir(owner.record), ".txn-"+hex.EncodeToString(raw[:])+".tmp")
	if err := owner.add(temp, preserve); err != nil {
		return nil, "", err
	}
	file, err := os.OpenFile(temp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, "", err
	}
	return file, temp, nil
}

func (t *FileTxn) ensureTempOwner() (*txnTempOwner, error) {
	if t.tempOwner != nil {
		return t.tempOwner, nil
	}
	owner, err := newTempOwner(t.root)
	if err != nil {
		return nil, err
	}
	t.tempOwner = owner
	return owner, nil
}

func newTempOwner(root string) (*txnTempOwner, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	ownerDir := filepath.Join(realRoot, txnIntentRelDir, "owners")
	if err := EnsureDirDurable(ownerDir, 0o700); err != nil {
		return nil, err
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, err
	}
	stem := hex.EncodeToString(raw[:])
	lockPath := filepath.Join(ownerDir, stem+txnOwnerLockSuffix)
	file, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(lockPath)
	}
	if err := lockTempOwner(file); err != nil {
		cleanup()
		return nil, err
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return nil, err
	}
	if err := SyncDir(ownerDir); err != nil {
		cleanup()
		return nil, err
	}
	return &txnTempOwner{
		root:   realRoot,
		record: filepath.Join(ownerDir, stem+txnOwnerSuffix),
		lock:   lockPath,
		file:   file,
	}, nil
}

func lockTempOwner(file *os.File) error {
	if !lockingSupported {
		return nil
	}
	held, err := tryLock(file, true)
	if err != nil || !held {
		return errors.Join(err, fmt.Errorf("vault: lock temp owner"))
	}
	return nil
}

func (o *txnTempOwner) add(temp string, preserve bool) error {
	entries := append([]txnTempOwnerEntry(nil), o.entries...)
	entries = append(entries, txnTempOwnerEntry{Temp: temp, Preserve: preserve})
	data, err := json.Marshal(txnTempOwnerRecord{Entries: entries, Transferred: o.transferred})
	if err != nil {
		return err
	}
	if err := replaceTempOwnerRecord(o.record, data); err != nil {
		return err
	}
	o.entries = entries
	return nil
}

func (o *txnTempOwner) transfer() error {
	data, err := json.Marshal(txnTempOwnerRecord{Entries: o.entries, Transferred: true})
	if err != nil {
		return err
	}
	if err := replaceTempOwnerRecord(o.record, data); err != nil {
		return err
	}
	o.transferred = true
	return nil
}

func replaceTempOwnerRecord(name string, data []byte) error {
	dir := filepath.Dir(name)
	file, tmpName, err := openExclusiveTemp(dir, ".owner-update-", ".tmp", 0o600)
	if err != nil {
		return err
	}
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := file.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, name); err != nil { //bn:direct-write atomic metadata publication
		_ = os.Remove(tmpName)
		return err
	}
	return SyncDir(dir)
}

func (o *txnTempOwner) clear() error {
	if o == nil {
		return nil
	}
	dir := filepath.Dir(o.record)
	if err := os.Remove(o.record); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := SyncDir(dir); err != nil {
		return err
	}
	o.release()
	removeErr := os.Remove(o.lock)
	if errors.Is(removeErr, fs.ErrNotExist) {
		removeErr = nil
	}
	return errors.Join(removeErr, SyncDir(dir))
}

func (o *txnTempOwner) release() {
	if o.file != nil {
		_ = unlockFile(o.file)
		_ = o.file.Close()
		o.file = nil
	}
}

func (t *FileTxn) clearTempOwners() error {
	if err := t.tempOwner.clear(); err != nil {
		return err
	}
	t.tempOwner = nil
	return nil
}

func (t *FileTxn) abandonTempOwners() {
	if t.tempOwner == nil {
		return
	}
	_ = removeTempOwnerEntries(t.tempOwner.root, t.tempOwner.entries)
	_ = t.clearTempOwners()
}

func (t *FileTxn) handoffTempOwner() error {
	if t.tempOwner == nil {
		return nil
	}
	if err := t.tempOwner.transfer(); err != nil {
		return err
	}
	// Once the durable record says transferred, recovery will never delete its
	// entries: the main transaction intent owns them. Metadata removal is now
	// housekeeping, so release cannot create a destructive recovery race.
	o := t.tempOwner
	t.tempOwner = nil
	if err := o.clear(); err != nil {
		o.release()
	}
	return nil
}

func (t *FileTxn) retainTempOwner() {
	if t.tempOwner == nil {
		return
	}
	t.tempOwner.release()
	t.tempOwner = nil
}

func (t *FileTxn) syncTempOwnerDir() error {
	if t.tempOwner == nil {
		return nil
	}
	return SyncDir(filepath.Dir(t.tempOwner.record))
}

func (t *FileTxn) stageOwnedTempFile(dest string, data []byte, mode os.FileMode) (string, error) {
	tmp, name, err := t.openOwnedTemp(dest)
	if err != nil {
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return "", err
	}
	return finishTempFile(tmp, name, mode)
}

func (t *FileTxn) stagePreservedTempFile(data []byte, mode os.FileMode) (string, error) {
	tmp, name, err := t.openPreservedTemp()
	if err != nil {
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return "", err
	}
	finished, err := finishTempFile(tmp, name, mode)
	if err != nil {
		return "", err
	}
	if err := SyncDir(filepath.Dir(finished)); err != nil {
		return finished, fmt.Errorf("vault: sync preserved recovery copy: %w", err)
	}
	return finished, nil
}

func (t *FileTxn) stageOwnedTempFileFrom(dest, source string, size int64, digest string, mode os.FileMode) (string, error) {
	return t.stageTempFileFrom(dest, source, size, digest, mode, false)
}

func (t *FileTxn) stagePreservedTempFileFrom(source string, size int64, digest string, mode os.FileMode) (string, error) {
	staged, err := t.stageTempFileFrom("", source, size, digest, mode, true)
	if err != nil {
		return staged, err
	}
	if err := SyncDir(filepath.Dir(staged)); err != nil {
		return staged, fmt.Errorf("vault: sync preserved recovery copy: %w", err)
	}
	return staged, nil
}

func (t *FileTxn) stageTempFileFrom(_ string, source string, size int64, digest string, mode os.FileMode, preserve bool) (string, error) {
	in, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer in.Close()
	tmp, name, err := t.openTemp(preserve)
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	copied, err := io.Copy(io.MultiWriter(tmp, hasher), in)
	if err != nil {
		tmp.Close()
		os.Remove(name)
		return "", err
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if copied != size || actual != digest {
		tmp.Close()
		os.Remove(name)
		return "", &TxnConflictError{Path: source, Expected: ExpectHash(digest).describe(), Actual: "file(sha256=" + actual + ")"}
	}
	return finishTempFile(tmp, name, mode)
}

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
	record, err := readTempOwnerRecord(recordPath)
	if err != nil {
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
	record, err := readTempOwnerRecord(recordPath)
	if err != nil {
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

package vault

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// finishedOwnerLock leaves an owner exactly where its clear() pauses between
// unlocking and removing the lock: the record gone, the lock file still there
// and free. A concurrent scan that listed the record before it went lands here.
func finishedOwnerLock(t *testing.T, root string) (record, lock string) {
	t.Helper()
	owner, err := newTempOwner(root)
	if err != nil {
		t.Fatalf("newTempOwner: %v", err)
	}
	if err := owner.add(filepath.Join(owner.root, ".txn-0000000000000000.tmp"), false); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := os.Remove(owner.record); err != nil {
		t.Fatalf("remove record: %v", err)
	}
	owner.release()
	return owner.record, owner.lock
}

func TestTempOwnerScansSkipAnOwnerThatFinishedMidScan(t *testing.T) {
	scans := map[string]func(root, record string) error{
		"recover": recoverTempOwner,
		"transfer": func(_, record string) error {
			return transferTempOwnerIfMatching(record, map[string]struct{}{"staged": {}})
		},
	}
	for name, scan := range scans {
		t.Run(name, func(t *testing.T) {
			root := txnTestRoot(t, nil)
			record, lock := finishedOwnerLock(t, root)
			if err := scan(root, record); err != nil {
				t.Fatalf("scan of a finished owner: %v", err)
			}
			if _, err := os.Stat(lock); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("leftover lock should be removed, stat err = %v", err)
			}
		})
	}
}

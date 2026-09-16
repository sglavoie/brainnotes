//go:build unix

package vaultsync

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestAcceptRefusesAFifoInPlaceOfAPayload is the half of statPayloadFile's
// regular-file requirement that the symlink case cannot stand in for.
//
// A symlink is still refused once IsRegular is gone, because Lstat reports the
// link's own length and a link whose target is the declared size is work to
// arrange. A FIFO is the case the guard's comment actually names, and it is the
// one with teeth: it reports size 0, so against a package carrying an empty
// payload it passes the size check, and opening it blocks until a writer appears
// that never will. Without the requirement Accept does not fail here — it stops,
// inside hashFile, with no timeout and no error, on a path chosen by whoever
// produced the package.
//
// Which is also how this test reports that regression: as the suite hanging
// rather than as a failed assertion. That is stated rather than hidden, because
// a reader who finds this test wedged should know it has found the defect and
// not the harness.
//
// Unix-only, and in its own file for it: there is no portable mkfifo, and the
// two replicas this protocol exists for are APFS and ext4.
func TestAcceptRefusesAFifoInPlaceOfAPayload(t *testing.T) {
	r := enroll(t)
	manifest, dir := r.publish(t, r.channel(t), func(b *Builder) {
		b.Write("Empty.md", nil, Absent())
	})

	// The premise the case rests on: a FIFO's zero size has to match the
	// inventory, or the size check refuses it first and the regular-file
	// requirement is never reached.
	if got := manifest.Payloads[0].Size; got != 0 {
		t.Fatalf("the payload is %d bytes; this case needs an empty one to reach the guard it tests", got)
	}

	path := filepath.Join(dir, PayloadsName, manifest.Payloads[0].Name)
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove payload: %v", err)
	}
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	_, err := Accept(r.local, dir)
	requireIs(t, err, ErrIncompletePackage, "a FIFO in place of a payload")
}

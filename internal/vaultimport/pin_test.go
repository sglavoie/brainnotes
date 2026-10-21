package vaultimport

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// TestApplyRefusesWhenTheLocalOverlayAppearsMidCommit is the configuration pin
// under the only conditions that can exercise it: the overlay has to change
// between the moment the transaction was planned and the moment it commits, so
// the test drives the applier's own steps rather than Apply.
//
// Absence is pinned as a state, not as a missing pin. An overlay created while a
// peer's notes were landing would name this machine's editor and listen address
// — but it can also carry ownership decisions the loader enforces, so the import
// would finish under declarations that did not exist when it was planned.
func TestApplyRefusesWhenTheLocalOverlayAppearsMidCommit(t *testing.T) {
	r := enroll(t)
	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), r.believes(t, "Alpha.md"))
	})
	before := r.pair.Local.Read(t, "Alpha.md")

	session, txn := planned(t, r, dir)
	writeOverlay(t, r.root(), "editor = \"vi\"\n")
	err := session.commit(txn, begun(t, r, session))
	requireIs(t, err, ErrConflict, "committing against a configuration that changed after planning")

	if got := r.pair.Local.Read(t, "Alpha.md"); got != before {
		t.Fatalf("Alpha.md holds %q, want the pre-import body", got)
	}
	if entries := r.entries(t); len(entries) != 0 {
		t.Fatalf("a configuration conflict left %d recovery entries behind", len(entries))
	}
}

// TestApplyRefusesWhenThePortableConfigChangesMidCommit is the same pin on the
// other layer: the declarations that say what a property means are what decide
// what the applied notes are.
func TestApplyRefusesWhenThePortableConfigChangesMidCommit(t *testing.T) {
	r := enroll(t)
	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), r.believes(t, "Alpha.md"))
	})

	session, txn := planned(t, r, dir)
	portable := r.pair.Local.Read(t, config.FileName)
	r.pair.Local.Write(t, config.FileName, portable+"\n# edited mid-import\n")
	err := session.commit(txn, begun(t, r, session))
	requireIs(t, err, ErrConflict, "committing against a portable config that changed after planning")
}

// TestApplyAppliesAPackageThatCarriesThePortableConfig pins the one exemption.
// brainnotes.toml describes the vault, so it travels; a transaction cannot both
// write a path and require it unchanged, so the pin steps aside exactly when the
// package is the thing changing it — and the overlay's pin does not.
func TestApplyAppliesAPackageThatCarriesThePortableConfig(t *testing.T) {
	r := enroll(t)
	updated := r.pair.Peer.Read(t, config.FileName) + "\n# added on the peer\n"
	r.pair.Peer.Write(t, config.FileName, updated)

	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write(config.FileName, []byte(updated), vaultsync.Existing(vaultsync.HashBytes([]byte(r.pair.Local.Read(t, config.FileName)))))
	})

	result := r.mustApply(t, dir)

	if got := r.pair.Local.Read(t, config.FileName); got != updated {
		t.Fatalf("%s holds %q, want the packaged body", config.FileName, got)
	}
	changed := contentPaths(commitPaths(t, r.pair.Local, "HEAD"))
	requireEqual(t, changed, []string{config.FileName}, "the applying commit carries")
	if result.Receipt.Applied[0].SHA256 != vaultsync.HashBytes([]byte(updated)) {
		t.Fatal("the receipt does not acknowledge the configuration this vault now holds")
	}
}

// TestApplyPinsAnOverlayThatIsAlreadyThere covers the present-and-unchanged
// branch: an overlay the import never touches must survive it untouched, and
// must not be swept into the commit — it is the one root file internal/vaultgit
// deliberately keeps out of the history.
func TestApplyPinsAnOverlayThatIsAlreadyThere(t *testing.T) {
	r := enroll(t)
	const overlay = "editor = \"vi\"\n"
	writeOverlay(t, r.root(), overlay)

	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(alphaBody), r.believes(t, "Alpha.md"))
	})
	r.mustApply(t, dir)

	if got := r.pair.Local.Read(t, config.LocalFileName); got != overlay {
		t.Fatalf("the overlay holds %q, want it untouched", got)
	}
	changed := contentPaths(commitPaths(t, r.pair.Local, "HEAD"))
	requireEqual(t, changed, []string{"Alpha.md"}, "the applying commit carries")
	if dirty := dirtyInScope(t, r.pair.Local); len(dirty) != 0 {
		t.Fatalf("the vault is dirty after an import: %v", dirty)
	}
}

// writeOverlay creates the machine-local configuration layer directly, which is
// how a test stands in for the machine that owns it.
func writeOverlay(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, config.LocalFileName), []byte(body), 0o644); err != nil {
		t.Fatalf("write overlay: %v", err)
	}
}

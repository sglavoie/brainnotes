package vaultsync

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/sglavoie/brainnotes/internal/config"
)

// The on-disk layout under <vault>/.bnotes/sync/:
//
//	enrollment.json      this replica's identity, epoch, and accepted peers
//	outgoing/            this device's channel; it publishes here, the peer reads
//	outgoing/receipts/   acknowledgments this device owes the peer
//	outgoing/retirement.json  bounded high-water evidence for retired packages
//	incoming/            the peer's channel, delivered by the transport
//	incoming/receipts/   acknowledgments the peer owes this device
//
// Each device owns exactly one channel and only ever writes into it, which is
// what makes a file transfer between them a one-way copy with no merge. The
// transport is told to deliver `outgoing/` on one device into `incoming/` on the
// other; nothing here depends on how, and nothing here writes into `incoming/`.
const (
	outgoingName = "outgoing"
	incomingName = "incoming"
	receiptsName = "receipts"

	// ManifestName is the file inside a published package that describes it.
	ManifestName = "manifest.json"
	// PayloadsName is the directory inside a published package holding the
	// committed bytes its inventory lists.
	PayloadsName = "payloads"
	// CompanionLockRelPath serializes a companion pass with restore review and
	// restore operations that must begin while no pass is already in flight.
	CompanionLockRelPath = ".bnotes/locks/vault-sync.lock"
)

// StateDir returns the vault-relative synchronization directory, derived from
// the marker path internal/config probes so the two cannot drift.
func StateDir() string { return syncStateDir }

// EnrollmentPath returns the absolute path of the enrollment record for the
// vault at root.
func EnrollmentPath(root string) string {
	return filepath.Join(root, filepath.FromSlash(config.EnrollmentMarker))
}

func CompanionLockPath(root string) string {
	return filepath.Join(root, filepath.FromSlash(CompanionLockRelPath))
}

// OutgoingDir returns the absolute path of the channel this device publishes
// into.
func OutgoingDir(root string) string { return syncSubdir(root, outgoingName) }

// IncomingDir returns the absolute path of the channel the peer publishes into
// and the transport delivers here. Nothing in this package writes to it.
func IncomingDir(root string) string { return syncSubdir(root, incomingName) }

// ReceiptsDir returns the receipts sub-channel of a channel directory.
func ReceiptsDir(channelDir string) string { return filepath.Join(channelDir, receiptsName) }

func syncSubdir(root, name string) string {
	return filepath.Join(root, filepath.FromSlash(path.Join(syncStateDir, name)))
}

// The whole-operation guards beside the state they serialize. ImportLockName
// excludes a second import and ExportLockName a second export; neither is a
// note-path lock, which is why they live here rather than under `.bnotes/locks/`.
const (
	ImportLockName = "import.lock"
	ExportLockName = "export.lock"
)

// StateLockPath returns the absolute path of one of the guards above.
func StateLockPath(root, name string) string { return syncSubdir(root, name) }

// IsStateLock reports whether a vault-relative slash path names one of the
// guards above. A backup must neither archive nor restore one: the file carries
// no state, and replacing its inode under a holder would let a second import or
// export enter the section the first still believes it owns.
func IsStateLock(rel string) bool {
	for _, name := range []string{ImportLockName, ExportLockName} {
		if strings.EqualFold(rel, path.Join(syncStateDir, name)) {
			return true
		}
	}
	return false
}

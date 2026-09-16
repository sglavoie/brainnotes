package vaultsync

import (
	"path"
	"path/filepath"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// The on-disk layout under <vault>/.bnotes/sync/:
//
//	enrollment.json      this replica's identity, epoch, and accepted peers
//	outgoing/            this device's channel; it publishes here, the peer reads
//	outgoing/receipts/   acknowledgments this device owes the peer
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
)

// StateDir returns the vault-relative synchronization directory, derived from
// the marker path internal/config probes so the two cannot drift.
func StateDir() string { return syncStateDir }

// EnrollmentPath returns the absolute path of the enrollment record for the
// vault at root.
func EnrollmentPath(root string) string {
	return filepath.Join(root, filepath.FromSlash(config.EnrollmentMarker))
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

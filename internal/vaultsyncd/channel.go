package vaultsyncd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// pkgEntry is one package directory in a channel: the identity it is stored
// under and where it sits.
type pkgEntry struct {
	name string
	dir  string
}

// listPackages returns the package directories in a channel, sorted by identity.
//
// A channel that does not exist is not an error and returns nothing. That is
// what a vault which has never published — or never received — looks like, and
// it is also what one looks like before the first pass creates the directory.
//
// Only entries whose names are package identities are returned. A channel also
// holds `receipts/`, and a transport is free to leave its own bookkeeping
// alongside; naming the filter after the identity rather than after the things
// to skip means an unfamiliar entry is ignored rather than mistaken for a
// package.
func listPackages(channelDir string) ([]pkgEntry, error) {
	entries, err := os.ReadDir(channelDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("vaultsyncd: list channel %q: %w", channelDir, err)
	}
	packages := make([]pkgEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !vaultsync.PackageName(entry.Name()) {
			continue
		}
		packages = append(packages, pkgEntry{name: entry.Name(), dir: filepath.Join(channelDir, entry.Name())})
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].name < packages[j].name })
	return packages, nil
}

// receiptEntry is one published receipt: the package it acknowledges and the
// file it is stored in.
type receiptEntry struct {
	packageID string
	path      string
}

// listReceipts returns the receipts a channel holds, sorted by the identity they
// acknowledge.
//
// The identity comes from the file name, which is the opposite of what
// vaultsync.ReadReceipt does — deliberately, and only because of what the caller
// does with it. The one caller asks which of *this device's own* receipts may be
// released, which is a question about files this device wrote and named; it
// never treats the name as a statement about what the receipt says. Anything
// acting on a receipt's content reads it through ReadReceipt and gets that
// comparison.
//
// The derived identity is then asked of vaultsync.ReceiptPath and must name this
// exact file. That keeps the layout vaultsync's business rather than restating
// its extension here, and it means an entry this package cannot account for is
// skipped rather than deleted.
func listReceipts(channelDir string) ([]receiptEntry, error) {
	receiptsDir := vaultsync.ReceiptsDir(channelDir)
	entries, err := os.ReadDir(receiptsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("vaultsyncd: list receipts %q: %w", receiptsDir, err)
	}
	receipts := make([]receiptEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		packageID := strings.TrimSuffix(name, filepath.Ext(name))
		if !vaultsync.PackageName(packageID) {
			continue
		}
		path, err := vaultsync.ReceiptPath(channelDir, packageID)
		if err != nil || path != filepath.Join(receiptsDir, name) {
			continue
		}
		receipts = append(receipts, receiptEntry{packageID: packageID, path: path})
	}
	sort.Slice(receipts, func(i, j int) bool { return receipts[i].packageID < receipts[j].packageID })
	return receipts, nil
}

// verifyReceipt asks whether the peer has durably applied one of this device's
// packages.
//
// The receipt is read from this vault's *incoming* channel, which is where the
// peer's acknowledgments arrive: a device publishes receipts into its own
// outgoing channel, so the peer's outgoing channel is this device's incoming
// one. Getting that backwards would make every package look unacknowledged
// forever.
//
// Three answers, and the middle one is the whole reason this is not a boolean:
// the receipt has not arrived (false, nil — wait), the receipt arrived and does
// not verify (false, err — a person must look, because waiting will not fix it),
// or it verifies (true, nil — the peer has the change, durably, against a named
// local recovery entry of its own).
func verifyReceipt(root string, manifest *vaultsync.Manifest, local *vaultsync.Enrollment) (bool, error) {
	receipt, err := vaultsync.ReadReceipt(vaultsync.IncomingDir(root), manifest.PackageID)
	if err != nil {
		if errors.Is(err, vaultsync.ErrIncompletePackage) {
			return false, nil
		}
		return false, err
	}
	if err := receipt.VerifyAgainst(manifest, local); err != nil {
		return false, err
	}
	return true, nil
}

// newestDelivery reports the most recent modification time anywhere one level
// inside a channel, or the zero time when the channel holds nothing.
//
// It is a liveness hint and nothing more, which is why it is a filesystem
// timestamp rather than anything from the protocol. Every date inside a package
// or a receipt is the *producing* device's clock, and two machines' clocks
// disagree; the mtime of a file the transport wrote here is this machine's own
// record of when something last arrived. One level deep covers both a package
// directory appearing and a receipt landing in `receipts/`, which is every way a
// peer's work becomes visible.
func newestDelivery(channelDir string) time.Time {
	newest := stamp(channelDir)
	if seen := stamp(vaultsync.ReceiptsDir(channelDir)); seen.After(newest) {
		newest = seen
	}
	return newest
}

// stamp is the newest modification time among one directory's entries, and the
// zero time for a directory that cannot be read. A liveness hint that refused to
// answer would be worse than one that says "I have seen nothing", which is what
// an unreadable channel means here anyway.
func stamp(dir string) time.Time {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}
	}
	var newest time.Time
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if at := info.ModTime().UTC(); at.After(newest) {
			newest = at
		}
	}
	return newest
}

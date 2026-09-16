package vaultsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// publishPerm is the mode of a published file, and publishDirPerm of a
// published directory. A channel is read by the transport running as the same
// user; nothing here widens that.
const (
	publishPerm    fs.FileMode = 0o644
	publishDirPerm fs.FileMode = 0o755
)

// tempSuffix ends the name of a record being staged. It matches the scratch
// shape internal/vault uses for the same reason — a name a reader can recognize
// as in-flight rather than as content — and every such file is removed before
// the call that created it returns.
const tempSuffix = ".tmp"

// Publish writes a package into channelDir durably and immutably, and returns
// the directory it was published as.
//
// Durability ordering, which is the whole of this function:
//
//  1. The manifest is validated, the payload bytes are checked against its
//     inventory, and the manifest record is encoded. All three can refuse, and
//     all three happen before any directory exists, so a package that was never
//     publishable leaves nothing behind for a reader to find.
//  2. The package directory is created with an exclusive mkdir. That is the
//     immutability guarantee and it is atomic: a second publication under the
//     same identity fails here rather than replacing bytes a peer may already
//     have read.
//  3. Every payload is written and fsynced, then the payload directory holding
//     their names, then the package directory holding *its* name.
//  4. The manifest is written last, under a temporary name, fsynced, and linked
//     into place.
//  5. The package directory is fsynced again — this time for the manifest's own
//     entry — and then the channel directory that names the package.
//
// Step 4 is the commit point, for the same reason internal/vault writes its
// transaction intent only after staging: every byte the manifest refers to is
// already durable when the manifest naming them appears. A reader that finds a
// package directory with no manifest has found a publication in progress or one
// that was interrupted, and ReadManifest reports exactly that.
//
// Which is why the discard of a partial package stops at step 4. Up to there a
// failure has produced something no reader has accepted and this call is the
// only thing that knows it exists, so removing it is free. After there the
// manifest is on disk, an Accept may already have succeeded against it, and the
// transport may already be copying it to the peer — so a step 5 failure returns
// the directory *and* the error, leaving the package in place. Retracting a
// publication somebody may have acted on is the one repair worse than an
// un-fsynced directory entry.
//
// The manifest arrives by link rather than by being written in place, and that
// is load-bearing rather than tidy. A reader that caught a manifest half-written
// would parse a truncated document, and unparseable bytes are a *decision* —
// ErrMalformedPackage, this is not a package, stop waiting — while an absent
// manifest is ErrIncompletePackage, come back later. A partial payload needs no
// such care: it fails its hash and hashing failures are already the
// come-back-later answer.
//
// What this does *not* establish is delivery. The link that makes a publication
// visible here is a local filesystem event; the peer's copy arrives file by file
// over a transport that knows nothing about package boundaries, and a payload
// that has landed on the peer's disk is not a payload the peer has applied. Only
// a receipt says that, and only the peer can mint one.
func Publish(channelDir string, m *Manifest, payloads map[string][]byte) (string, error) {
	if err := m.Validate(); err != nil {
		return "", err
	}
	if err := checkInventory(m, payloads); err != nil {
		return "", err
	}
	// Encoded before anything exists on disk, so a record this build cannot
	// serialize is a plain refusal rather than a directory to clean up.
	record, err := encodeRecord(m)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(channelDir, m.PackageID)
	if err := vault.GuardMutation("", dir); err != nil {
		return "", err
	}
	if err := ensureChannel(channelDir); err != nil {
		return "", err
	}
	//bn:direct-write exclusive mkdir is the immutability guarantee itself: an identity already published fails here rather than being replaced
	if err := os.Mkdir(dir, publishDirPerm); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("%w: %s", ErrPackageExists, dir)
		}
		return "", fmt.Errorf("vaultsync: create package %q: %w", dir, err)
	}
	if err := writePayloads(dir, m, payloads); err != nil {
		discardPartial(dir)
		return "", err
	}
	if err := publishOnce(filepath.Join(dir, ManifestName), record); err != nil {
		discardPartial(dir)
		return "", err
	}
	// Past the commit point: the directory is returned alongside any error.
	return dir, syncPackage(channelDir, dir)
}

// writePayloads writes every inventory entry and makes the payload directory's
// entries durable, before the manifest that names them exists.
func writePayloads(dir string, m *Manifest, payloads map[string][]byte) error {
	if len(m.Payloads) == 0 {
		return nil
	}
	payloadDir := filepath.Join(dir, PayloadsName)
	//bn:direct-write inside the package directory this call exclusively created a moment ago, under the location guard Publish already ran
	if err := os.Mkdir(payloadDir, publishDirPerm); err != nil {
		return fmt.Errorf("vaultsync: create payload directory %q: %w", payloadDir, err)
	}
	for _, payload := range m.Payloads {
		if err := writeDurable(filepath.Join(payloadDir, payload.Name), payloads[payload.Name]); err != nil {
			return err
		}
	}
	if err := vault.SyncDir(payloadDir); err != nil {
		return fmt.Errorf("vaultsync: sync payload directory %q: %w", payloadDir, err)
	}
	// And then the package directory, which is the entry naming payloadDir
	// itself. Fsyncing only payloadDir commits the payload *names inside it* and
	// leaves the directory they live in uncommitted.
	//
	// `payloads` and `manifest.json` are entries in this same directory, so this
	// is an *ordering* fsync rather than the only one: step 5 fsyncs `dir` again
	// and would commit both. What it buys is that the payload directory's entry
	// cannot reach the disk after the manifest's does. Without it the window
	// between linking the manifest and step 5 completing can leave a durable
	// manifest beside no payloads directory at all — and a crash inside that
	// window is unrecoverable rather than merely untidy. The producer never
	// returns, so discardPartial never runs; the package is immutable, so nothing
	// may finish it in place; and a retry mints a new package identity rather
	// than completing this one. The orphan stays, and every reader polling it
	// gets ErrIncompletePackage — wait, the transfer is still running — forever.
	//
	// This is what makes step 4's guarantee true of the directory entries and not
	// only of the payload bytes.
	if err := vault.SyncDir(dir); err != nil {
		return fmt.Errorf("vaultsync: sync package %q: %w", dir, err)
	}
	return nil
}

// syncPackage makes the manifest's directory entry durable, then the channel
// entry that names the package directory holding it.
//
// The second fsync is the one that is easy to leave out. A name becomes durable
// only once the directory that holds it has been fsynced, so fsyncing the
// package directory alone would leave a machine that lost power with a manifest
// and its payloads on the platters and no entry in the channel leading to any of
// them — a package that is perfectly durable and unreachable.
func syncPackage(channelDir, dir string) error {
	if err := vault.SyncDir(dir); err != nil {
		return fmt.Errorf("vaultsync: sync package %q: %w", dir, err)
	}
	if err := vault.SyncDir(channelDir); err != nil {
		return fmt.Errorf("vaultsync: sync channel %q: %w", channelDir, err)
	}
	return nil
}

// checkInventory refuses a publication whose bytes and inventory disagree in
// any direction. Publishing a package that its own producer could not verify
// would put the detection of a mismatch on the far side of a transport, where
// the only available remedy is to wait for bytes that are never coming.
func checkInventory(m *Manifest, payloads map[string][]byte) error {
	if len(payloads) != len(m.Payloads) {
		return fmt.Errorf("%w: inventory lists %d payloads, %d were supplied", ErrIncompletePackage, len(m.Payloads), len(payloads))
	}
	for _, payload := range m.Payloads {
		data, supplied := payloads[payload.Name]
		if !supplied {
			return fmt.Errorf("%w: payload %q was not supplied", ErrIncompletePackage, payload.Name)
		}
		if int64(len(data)) != payload.Size {
			return fmt.Errorf("%w: payload %q is %d bytes, inventory says %d", ErrIncompletePackage, payload.Name, len(data), payload.Size)
		}
		if HashBytes(data) != payload.SHA256 {
			return fmt.Errorf("%w: payload %q does not hash to %s", ErrIncompletePackage, payload.Name, payload.SHA256)
		}
	}
	return nil
}

// ensureChannel creates a channel directory and its receipts sub-channel, and
// commits the entry naming each directory it had to create.
//
// os.MkdirAll on its own is not enough, for exactly the reason syncPackage
// fsyncs two levels rather than one: a directory's name is durable only once the
// directory holding it has been fsynced. Without the loop below, the first
// publication into a fresh channel writes its payloads, links its manifest,
// fsyncs the package directory and fsyncs the channel — and still leaves the
// entry *naming the channel* uncommitted. A machine that lost power there would
// come back with the channel, the package, the manifest and the payloads all
// gone together, while the producer has already recorded that sequence as
// published and will never send it again.
func ensureChannel(channelDir string) error {
	receipts := ReceiptsDir(channelDir)
	missing, err := missingAncestors(receipts)
	if err != nil {
		return err
	}
	//bn:direct-write the channel is a staging directory under .bnotes/sync, out of the content history's scope and out of the portable scope, and Publish ran the location guard on its destination first
	if err := os.MkdirAll(receipts, publishDirPerm); err != nil {
		return fmt.Errorf("vaultsync: create channel %q: %w", channelDir, err)
	}
	// Shallowest first, since each fsync commits the name of the directory
	// created one level below it. The deepest directory created needs none of
	// its own: it is empty, and the entry naming it is committed by the fsync of
	// its parent.
	for i := len(missing) - 1; i >= 0; i-- {
		parent := filepath.Dir(missing[i])
		if err := vault.SyncDir(parent); err != nil {
			return fmt.Errorf("vaultsync: sync %q: %w", parent, err)
		}
	}
	return nil
}

// missingAncestors returns dir and each ancestor above it that does not exist
// yet, deepest first, stopping at the first one that does.
//
// It runs before the MkdirAll rather than inferring afterwards, because
// afterwards every one of them exists and there is nothing left to tell apart.
// A directory another process creates in between is harmless: the cost is one
// redundant fsync of a directory that was already committed.
func missingAncestors(dir string) ([]string, error) {
	var missing []string
	for {
		if _, err := os.Stat(dir); err == nil {
			return missing, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("vaultsync: inspect %q: %w", dir, err)
		}
		missing = append(missing, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			return missing, nil
		}
		dir = parent
	}
}

// writeDurable creates one file inside a package directory and makes its bytes
// durable before returning.
//
// O_EXCL rather than a truncating create: every caller writes into a directory
// it has just exclusively created, so a name that already exists means
// something is writing into a package that is still being published, and
// overwriting would make the manifest describe bytes nobody chose.
//
// This is not internal/vault's path-continuous replacement, deliberately.
// That primitive exists to keep a *destination* from ever being vacant, which
// is what matters for a note somebody may be reading. Here the destination is
// brand new inside a directory no reader may touch until the manifest lands, so
// the guarantee needed is ordering — bytes before the record naming them —
// rather than continuity.
func writeDurable(path string, data []byte) error {
	//bn:direct-write O_EXCL create inside a package directory Publish exclusively created, followed by an fsync; the destination is new so there is nothing to keep continuous
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, publishPerm)
	if err != nil {
		return fmt.Errorf("vaultsync: create %q: %w", path, err)
	}
	return fillDurable(file, path, data)
}

// publishOnce writes data at path exactly once and makes it appear whole.
//
// The bytes go to a uniquely named temporary beside the destination and are
// fsynced there, then hard-linked onto the destination. One link carries both
// guarantees this package needs from a published record: it is atomic, so a
// reader never finds a partial document under a name that promises a complete
// one, and it refuses a name already taken, so a second publication under one
// identity fails rather than replacing bytes a peer may already have acted on.
//
// The temporary is uniquely named rather than derived from the destination so
// that one interrupted publication cannot wedge every later attempt behind a
// leftover name — and the deferred remove runs on every path out, including the
// refusal, because a temporary left in a channel is a file the transport would
// copy. That remove is best-effort: if it fails, or if the process dies between
// the create and the link, a <name>.*.tmp stays in the directory. Nothing reads
// it — validation names the manifest exactly and verifyNoExtraPayloads scans
// only payloads/ — so it is inert here and visible to the operator, which is
// the right side to fail on for a file this call could not clean up.
//
// This is the treatment a *record* needs, not every file. A payload is written
// straight to its name: a partial payload fails its hash, and a hash failure is
// already ErrIncompletePackage — the come-back-later answer. A partial manifest
// or receipt would instead parse as garbage, and garbage is a decision.
func publishOnce(path string, data []byte) error {
	//bn:direct-write creates a uniquely named temporary in the directory the caller already guarded; it is linked into place or removed before this returns
	file, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*"+tempSuffix)
	if err != nil {
		return fmt.Errorf("vaultsync: stage %q: %w", path, err)
	}
	tmp := file.Name()
	defer func() {
		//bn:direct-write removes only the temporary created immediately above
		_ = os.Remove(tmp)
	}()
	//bn:direct-write widens the 0600 os.CreateTemp default to the published mode, on a temporary this call created
	if err := os.Chmod(tmp, publishPerm); err != nil {
		_ = file.Close()
		return fmt.Errorf("vaultsync: stage %q: %w", path, err)
	}
	if err := fillDurable(file, tmp, data); err != nil {
		return err
	}
	//bn:direct-write hard-links staged bytes onto a destination inside the directory the caller guarded; the link failing because the name is taken is the immutability refusal itself
	if err := os.Link(tmp, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%w: %s", ErrPackageExists, path)
		}
		return fmt.Errorf("vaultsync: publish %q: %w", path, err)
	}
	return nil
}

// fillDurable writes data into an already-created file and closes it with the
// bytes on the disk rather than in a cache.
func fillDurable(file *os.File, path string, data []byte) error {
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("vaultsync: write %q: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("vaultsync: fsync %q: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("vaultsync: close %q: %w", path, err)
	}
	return nil
}

// discardPartial removes a package directory whose publication failed before
// the manifest was linked into place. Every caller is on that side of the commit
// point by construction, which is what makes the removal safe: with no manifest
// there is nothing an Accept can have succeeded against, so nothing is being
// retracted.
//
// Best-effort and silent: the failure the caller is about to report is the one
// worth acting on, and a directory left behind is inert — it has no manifest,
// so every reader already treats it as a publication in progress. A retry mints
// a new package identity rather than reusing this one, so nothing waits on the
// removal succeeding.
func discardPartial(dir string) {
	//bn:direct-write removes only a package directory this process exclusively created and failed to finish; it has no manifest so no reader has accepted it
	_ = os.RemoveAll(dir)
}

func encodeRecord(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("vaultsync: encode record: %w", err)
	}
	return append(data, '\n'), nil
}

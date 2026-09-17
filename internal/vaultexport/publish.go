package vaultexport

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// publishCommit builds and publishes the one package that describes one commit.
//
// One commit is one operation and one package. The commit already *is* the
// vault's record that these paths changed together — internal/noteops commits
// exactly one journal entry's paths — so the boundary a peer needs is read off
// the history rather than reconstructed. Batching commits was the alternative
// and it is wrong twice over: the protocol refuses a package that changes one
// path twice, so any two commits touching the same note could not be merged at
// all, and a boundary this producer invented would not be one the vault ever
// recorded.
func (e *exporter) publishCommit(
	ctx context.Context,
	commit string,
	info vaultgit.CommitInfo,
	portable []vaultgit.FileChange,
	blobs map[string][]byte,
) error {
	// Derived, not minted. A pass that published this commit and died before
	// recording the fact republishes it, and the peer sees one operation twice
	// under one identity rather than twice under two. See
	// vaultsync.DeriveOperationID.
	operationID := vaultsync.DeriveOperationID(e.local.VaultID, commit)

	sequence := e.state.Sequence + 1
	builder, err := vaultsync.NewBuilder(e.local, sequence, e.now())
	if err != nil {
		return err
	}
	builder.BeginOperation(operationID, info.When)
	for _, change := range portable {
		if err := e.addChange(builder, commit, change, blobs); err != nil {
			return err
		}
	}
	manifest, payloads, err := builder.Build()
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(portable))
	for _, change := range portable {
		paths = append(paths, change.Path)
	}
	return e.publish(ctx, manifest, payloads, publication{
		commit:      commit,
		operationID: operationID,
		paths:       paths,
	})
}

// publication is what one package is being published for: which commit it
// describes, the operation identity every path in it carries, and whether it is
// one slice of a baseline rather than a whole commit.
type publication struct {
	commit      string
	operationID string
	paths       []string
	baseline    bool
}

// addChange records one path's transition into the package under construction.
//
// The prior expectation is what this producer believed the path held, taken from
// the parent commit rather than from the working tree. That is what makes a
// conflict detectable on the peer: a peer whose copy holds something else edited
// the same note from the same starting point, and the package has to make that
// checkable rather than silently overwrite it.
func (e *exporter) addChange(
	builder *vaultsync.Builder,
	commit string,
	change vaultgit.FileChange,
	blobs map[string][]byte,
) error {
	if err := checkChangeModes(commit, change); err != nil {
		return err
	}
	var predecessors []string
	if previous, known := e.state.Paths[change.Path]; known {
		predecessors = append(predecessors, previous)
	}
	prior := vaultsync.Absent()
	if change.OldOID != "" {
		content, read := blobs[change.OldOID]
		if !read {
			return fmt.Errorf("%w: %s changed %q and its prior content could not be read", ErrCorruptState, commit, change.Path)
		}
		prior = vaultsync.Existing(vaultsync.HashBytes(content))
	}
	if change.Kind == vaultgit.ChangeDeleted {
		builder.Delete(change.Path, prior, predecessors...)
		return nil
	}
	content, read := blobs[change.NewOID]
	if !read {
		return fmt.Errorf("%w: %s changed %q and its content could not be read", ErrCorruptState, commit, change.Path)
	}
	builder.Write(change.Path, content, prior, predecessors...)
	return nil
}

// checkChangeModes refuses a change whose either side is not a regular file.
//
// A symlink or a submodule is content this protocol has no way to describe, and
// the peer would have to invent a meaning for it. Halting names the path so a
// person can decide; skipping it would leave a replica quietly incomplete while
// every receipt said otherwise.
func checkChangeModes(commit string, change vaultgit.FileChange) error {
	for _, mode := range []string{change.OldMode, change.NewMode} {
		if mode == "" || regularFileMode(mode) {
			continue
		}
		return fmt.Errorf("%w: %s gives %q mode %s", ErrUnsupportedEntry, commit, change.Path, mode)
	}
	return nil
}

// publish reserves a sequence, publishes, and records the outcome.
//
// The three steps are in this order because only this order is recoverable. The
// reservation is durable before the package directory can exist, so a pass that
// dies mid-publication leaves behind a name the next pass can go and look for;
// without it the next pass would know a sequence might have been used and have
// no way to find out. See resolveReservation for the three answers that search
// can give.
func (e *exporter) publish(ctx context.Context, manifest *vaultsync.Manifest, payloads map[string][]byte, spec publication) error {
	if err := e.checkPathCollisions(ctx, spec.commit, spec.paths); err != nil {
		return err
	}
	reserved := e.state.clone()
	reserved.Pending = &Reservation{
		Sequence:  manifest.Sequence,
		PackageID: manifest.PackageID,
		Commit:    spec.commit,
		Baseline:  spec.baseline,
	}
	if err := e.commitState(reserved); err != nil {
		return err
	}

	dir, err := vaultsync.Publish(e.channelDir, manifest, payloads)
	if err != nil {
		return err
	}

	next := e.state.clone()
	next.Pending = nil
	next.Sequence = manifest.Sequence
	next.Branch = e.history.Branch()
	// A baseline slice does not advance the cursor: the commit it describes is
	// only fully sent once every remaining path has been. exportBaseline sets
	// Commit when that happens.
	if !spec.baseline {
		next.Commit = spec.commit
	}
	for _, path := range spec.paths {
		next.Paths[path] = spec.operationID
	}
	if err := e.commitState(next); err != nil {
		return err
	}
	e.result.Packages = append(e.result.Packages, Published{
		PackageID: manifest.PackageID,
		Dir:       dir,
		Sequence:  manifest.Sequence,
		Commit:    spec.commit,
		Changes:   len(spec.paths),
	})
	return nil
}

// checkPathCollisions refuses a package that would send a path the peer cannot
// tell apart from one this device already sent.
//
// vaultsync refuses a collision *within* one manifest, which is everything a
// receiver can see for itself. It is not everything there is: this producer emits
// one package per commit, so `Archive/Notes.md` published a year ago and
// `archive/notes.md` published today are two manifests that each validate and one
// file on the replica, where the second silently overwrites the first. Only the
// producer holds both halves of that, and it holds them in the path ledger.
//
// The liveness check is what keeps this from refusing the legitimate case. A path
// entry survives its own deletion — a resurrection has to name its tombstone — so
// a note removed and later recreated under a different fold is a ledger collision
// and not a real one: the peer applies the tombstone before the write, and ends up
// with exactly one file. Asking the commit what it still holds under the older
// name separates the two, and it costs nothing at all unless the keys clash.
//
// Both halves of vaultsync's rule are carried across packages, not just the
// first: a name clash, and a name one package sends as a file while another
// sends it as a directory. The second is the less obvious one and no less fatal —
// a file `Archive` sent last year and `archive/notes.md` sent today have no key
// in common and are still one name on the replica, which cannot apply the second
// package at all.
func (e *exporter) checkPathCollisions(ctx context.Context, commit string, paths []string) error {
	// Rebuilt per package rather than carried on the exporter: the ledger is the
	// answer and it moves under every publication, skip, and recovered
	// reservation. One index build is nothing beside the git processes each
	// package already costs, and a cached one that drifted would fail open.
	index := newCollisionIndex(e.state.Paths)
	for _, p := range paths {
		if err := e.checkRivals(ctx, commit, p, index.rivals(p)); err != nil {
			return err
		}
		// Added after its own comparison, so the outgoing paths of one package are
		// checked against each other too. A baseline cuts one tree into several
		// manifests and vaultsync only sees one at a time.
		index.add(p)
	}
	return nil
}

// checkRivals asks the commit what it holds under each of one path's rivals, and
// refuses on the first that is still a clash.
//
// One question for all of them rather than one per rival. The rival set is
// usually empty and occasionally enormous — a folded name covering a directory's
// worth of tombstones, every one of them dead — and paying a process per dead
// rival, on every pass, forever, is the shape this avoids.
func (e *exporter) checkRivals(ctx context.Context, commit, path string, rivals []rival) error {
	if len(rivals) == 0 {
		return nil
	}
	names := make([]string, 0, len(rivals))
	for _, r := range rivals {
		names = append(names, r.path)
	}
	kinds, err := e.history.EntryKinds(ctx, commit, names)
	if err != nil {
		return err
	}
	for _, r := range rivals {
		// A rival the commit no longer holds is a tombstone the peer applies before
		// this write, and the two never coexist.
		if r.kind.stillClashes(kinds[r.path]) {
			return collisionRefusal(commit, path, r)
		}
	}
	return nil
}

// resolveReservation determines the outcome of a publication the previous pass
// did not live to record, and folds it into the state.
//
// Three answers, three different corrections, and the difference matters because
// a sequence number is this device's position in its own stream.
//
//   - No directory. The publication never created anything, so the sequence was
//     never spent and is reused. This is the common case: the reservation is
//     written before Publish is called at all.
//   - A directory with a manifest. The manifest's arrival *is* the commit point
//     of publication, so the package is published and the peer may already have
//     it. The sequence is spent, the cursor advances to the commit it described,
//     and the path ledger is rebuilt from the manifest itself — which is exact,
//     because the manifest says precisely which paths that operation touched.
//   - A directory with no manifest. Publication was interrupted after the
//     package directory existed and before its manifest landed, so the identity
//     is burned: a peer reads that directory as an incomplete package forever,
//     and the directory cannot be reused because immutability there is an
//     exclusive mkdir. The sequence is spent and the commit is re-exported under
//     a fresh identity, which is why it is reported in BurnedSequences.
func (e *exporter) resolveReservation() error {
	pending := e.state.Pending
	if pending == nil {
		return nil
	}
	dir := filepath.Join(e.channelDir, pending.PackageID)
	info, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		next := e.state.clone()
		next.Pending = nil
		return e.commitState(next)
	case err != nil:
		return fmt.Errorf("vaultexport: inspect reserved package %q: %w", dir, err)
	case !info.IsDir():
		return fmt.Errorf("%w: %s is not a directory", ErrChannelConflict, dir)
	}

	manifest, err := vaultsync.ReadManifest(dir)
	if errors.Is(err, vaultsync.ErrIncompletePackage) {
		next := e.state.clone()
		next.Pending = nil
		next.Sequence = pending.Sequence
		e.result.BurnedSequences = append(e.result.BurnedSequences, pending.Sequence)
		return e.commitState(next)
	}
	if err != nil {
		return err
	}
	return e.adoptPublished(manifest, pending)
}

// adoptPublished folds a package that did land into the state.
func (e *exporter) adoptPublished(manifest *vaultsync.Manifest, pending *Reservation) error {
	// The directory carries this device's reserved name, so the manifest inside
	// it must be the package that reservation described. Anything else means a
	// second writer is using this channel, and continuing would interleave two
	// producers' sequences under one device identity.
	if manifest.PackageID != pending.PackageID ||
		manifest.Sequence != pending.Sequence ||
		manifest.OriginDevice != e.local.DeviceID ||
		manifest.VaultID != e.local.VaultID {
		return fmt.Errorf("%w: %s holds a package this device did not reserve", ErrChannelConflict, pending.PackageID)
	}
	next := e.state.clone()
	next.Pending = nil
	next.Sequence = pending.Sequence
	next.Branch = e.history.Branch()
	if !pending.Baseline {
		next.Commit = pending.Commit
	}
	// Rebuilt from the manifest rather than recomputed from the commit: the
	// manifest is what the peer actually received, so it is the only correct
	// answer to "which operation last touched this path".
	for _, operation := range manifest.Operations {
		for _, change := range operation.Changes {
			next.Paths[change.Path] = operation.OperationID
		}
	}
	changes := 0
	for _, operation := range manifest.Operations {
		changes += len(operation.Changes)
	}
	e.result.Packages = append(e.result.Packages, Published{
		PackageID: manifest.PackageID,
		Dir:       filepath.Join(e.channelDir, manifest.PackageID),
		Sequence:  manifest.Sequence,
		Commit:    pending.Commit,
		Changes:   changes,
	})
	return e.commitState(next)
}

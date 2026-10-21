package vaultimport

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/vaultgit"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// hold records the conflict durably and keeps every version of every contested
// path that this vault can still reach.
//
// The state is re-read rather than taken from the session, because this is
// reached from two places and one of them has just been through a rollback: an
// applier that declared an attempt and had it resolved underneath it holds a
// record that still names the attempt, and writing that back would re-declare an
// application that is over. Reading again costs one file and removes the whole
// class.
func (a *applier) hold(found *disposition) error {
	state, err := loadState(a.root, a.local)
	if err != nil {
		return err
	}
	store := &retention{root: a.root, packageID: a.manifest.PackageID, payloads: a.payloadIndex()}
	paths := make([]PathConflict, 0, len(found.conflicts))
	for _, contested := range found.conflicts {
		kept, err := store.keep(contested)
		if err != nil {
			return err
		}
		paths = append(paths, kept)
	}
	next := state.clone()
	next.Conflicts[a.manifest.PackageID] = &ConflictRecord{
		PackageID:    a.manifest.PackageID,
		OperationID:  a.operation.OperationID,
		OriginDevice: a.manifest.OriginDevice,
		Sequence:     a.manifest.Sequence,
		DetectedAt:   a.opts.now(),
		Paths:        paths,
	}
	if err := a.saveState(next); err != nil {
		return err
	}
	a.state = next
	return store.prune(paths)
}

// payloadIndex maps a content digest to the package bytes carrying it, which is
// what lets the remote branch — and, for a rename, the baseline — be retained
// without opening the package again.
func (a *applier) payloadIndex() map[string][]byte {
	index := make(map[string][]byte, len(a.changes))
	for _, planned := range a.changes {
		if planned.data != nil {
			index[vaultsync.HashBytes(planned.data)] = planned.data
		}
	}
	return index
}

// retention writes the contested content into this package's conflict directory.
//
// The store is content-addressed, so the three branches of a rename conflict —
// where the remote bytes and the baseline bytes are the same bytes — cost one
// file, and re-detecting an unchanged conflict on every pass rewrites the same
// names rather than growing the directory. Everything goes through
// vault.WriteStateFile, which creates the directories, guards the location, and
// fsyncs; nothing here opens or removes a file itself.
type retention struct {
	root      string
	packageID string
	payloads  map[string][]byte
	// history is opened once, lazily, and only when a baseline has to be looked
	// for in the content history.
	history *committed
}

// keep fills in where each of a conflict's three branches was retained.
func (s *retention) keep(contested PathConflict) (PathConflict, error) {
	remote, err := s.write(s.payloads[contested.Remote.SHA256], contested.Remote)
	if err != nil {
		return contested, err
	}
	local, err := s.write(s.read(contested.Path, contested.Local), contested.Local)
	if err != nil {
		return contested, err
	}
	baseline, err := s.write(s.baseline(contested), contested.Baseline)
	if err != nil {
		return contested, err
	}
	contested.Remote, contested.Local, contested.Baseline = remote, local, baseline
	return contested, nil
}

// write retains one branch's bytes, and is a no-op for a branch that is a
// deletion or whose bytes could not be found.
func (s *retention) write(data []byte, branch Branch) (Branch, error) {
	if !branch.Exists || data == nil {
		return branch, nil
	}
	if vaultsync.HashBytes(data) != branch.SHA256 {
		return branch, nil
	}
	dir, err := conflictDir(s.root, s.packageID)
	if err != nil {
		return branch, err
	}
	dest := filepath.Join(dir, branch.SHA256)
	if err := vault.WriteStateFile(dest, data, statePerm); err != nil {
		return branch, fmt.Errorf("vaultimport: retain %s for %s: %w", branch.SHA256, s.packageID, err)
	}
	branch.Bytes = branch.SHA256
	return branch, nil
}

// prune removes retained bytes this package's record has stopped naming.
//
// It runs after the record is saved, which is release's ordering one size
// smaller and holds for the same reason: removing first would invite a
// resolution from versions that are already gone, where removing after leaves,
// at worst, bytes nothing points at until the next pass over this package.
//
// It is needed because a hold is re-decided on every pass and the local branch
// is re-read from the vault each time. A person editing a contested note while
// they think about it would otherwise leave one dead copy of it per edit, inside
// a directory collectRetained will never touch — the record still names the
// package, so the sweep correctly skips it — and the only bound on that is how
// long the conflict stays unresolved.
//
// A failure here joins the refusal its caller is already raising, rather than
// going to a cleanup channel the way every other unremovable file in this
// package does. Both of hold's callers are refusing, so there is no Result to
// carry it on and the alternative is silence; the refusal still wraps
// ErrConflict, so a drain still classifies the package as held rather than
// failed. A caller that ever holds without refusing would need the other
// treatment.
func (s *retention) prune(kept []PathConflict) error {
	dir, err := conflictDir(s.root, s.packageID)
	if err != nil {
		return err
	}
	if err := vault.GuardMutation(s.root, dir); err != nil {
		return err
	}
	named := retainedNames(kept)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("vaultimport: list retained branches for %s: %w", s.packageID, err)
	}
	var failures []error
	for _, entry := range entries {
		if _, still := named[entry.Name()]; still {
			continue
		}
		// Not vault content: one retained copy of a note inside this package's own
		// scratch directory under `.bnotes/sync`, which the content history and the
		// portable scope both exclude wholesale.
		//bn:direct-write removes one retained branch the state file written just above has stopped naming
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			failures = append(failures, fmt.Errorf("vaultimport: discard retained branch %s of %s: %w",
				entry.Name(), s.packageID, err))
		}
	}
	return errors.Join(failures...)
}

// retainedNames is the set of retained files one record names, which is what
// prune keeps.
func retainedNames(kept []PathConflict) map[string]struct{} {
	named := make(map[string]struct{}, len(kept)*3)
	for _, contested := range kept {
		for _, branch := range []Branch{contested.Remote, contested.Local, contested.Baseline} {
			if branch.Bytes != "" {
				named[branch.Bytes] = struct{}{}
			}
		}
	}
	return named
}

// read reads the local branch off the disk, reporting nothing rather than
// failing: the local side is the version that exists nowhere else, so a read
// that loses a race with the person editing it must not turn a held conflict
// into a refused import. write re-checks the digest, so bytes that changed under
// this are dropped rather than recorded as the branch they are not.
func (s *retention) read(rel string, branch Branch) []byte {
	if !branch.Exists {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	return data
}

// readRetained reads one branch back out of the store, or reports nothing.
//
// It is how a resolution reaches a version that is no longer on the disk — the
// peer's, most of all, which exists here only as the copy the hold kept. The
// digest is checked against the branch rather than against the file name, so a
// record naming a retained copy that has since been replaced by something else
// answers nothing rather than the wrong content. Bytes being empty is the
// ordinary case for a branch that is a deletion or one this vault never had.
func readRetained(root, packageID string, branch Branch) []byte {
	if !branch.Exists || branch.Bytes == "" {
		return nil
	}
	dir, err := conflictDir(root, packageID)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, branch.Bytes))
	if err != nil || vaultsync.HashBytes(data) != branch.SHA256 {
		return nil
	}
	return data
}

// Version is how a surface outside this package reaches one version of a
// contested path.
//
// It exists because "show me both versions" cannot be answered from anywhere
// else. The peer's bytes live nowhere but the copy the hold kept, and this
// vault's own version is on the disk right up until somebody edits it, after
// which the retained copy is the only place the version the hold recorded still
// exists. Which of the two places answers is decided by the digest rather than
// by the caller, so passing the branch a resolution was planned against always
// shows the version that resolution is about — see branchContent, which is the
// one implementation of that rule and is also what the revision installs content
// from.
//
// It is a wrapper rather than a second reader on purpose: the digest check is the
// thing that makes either copy trustworthy, and a second implementation of it is
// a second chance to leave it out.
//
// Nothing is returned for a branch that is a deletion, one this vault never had,
// or one whose bytes are in neither place — Branch.Exists distinguishes the first
// two from the third, and a caller offering a merge has to treat "no bytes" as a
// version it cannot show rather than as an empty file.
func Version(root, packageID string, contested PathConflict, branch Branch) []byte {
	if !branch.Exists {
		return nil
	}
	return branchContent(root, packageID, contested, branch)
}

// baseline finds the bytes both replicas last agreed on, and is best-effort by
// construction.
//
// Two places can still hold them. The package itself does when the operation is
// a rename, since the content it creates is the content it moved. And the
// content history does whenever the local divergence is an uncommitted edit —
// which is the common case this session is about, because a committed local
// change would have been exported and the two would be arguing through
// predecessors rather than over a path.
//
// A baseline older than this vault's history, or one from a vault with no
// history at all, is recorded by digest and not by content. That is the honest
// answer: the identity is always kept, so a person who has the bytes elsewhere
// can recognise them, and a three-way merge that cannot find its base says so
// rather than merging against the wrong one.
func (s *retention) baseline(contested PathConflict) []byte {
	if !contested.Baseline.Exists {
		return nil
	}
	if data, carried := s.payloads[contested.Baseline.SHA256]; carried {
		return data
	}
	if s.history == nil {
		s.history = openCommitted(s.root)
	}
	return s.history.bytes(contested.Path, contested.Baseline.SHA256)
}

// committed is the content history read as a source of baseline bytes, opened
// once per held package and silent about every failure.
//
// Silent is deliberate and is the same judgement Result.Cleanup encodes. This
// runs while a conflict is being written down; a vault with no Git, a branch
// with no commits, or a tree listing that fails are all reasons the baseline
// cannot be shown, and none of them is a reason to refuse an import that was
// going to be held anyway.
type committed struct {
	ctx     context.Context
	history *vaultgit.History
	tip     string
	tree    map[string]vaultgit.TreeFile
	ready   bool
}

func openCommitted(root string) *committed {
	ctx := context.Background()
	source := &committed{ctx: ctx}
	history, err := vaultgit.OpenHistory(ctx, root, vaultgit.HistoryOptions{})
	if err != nil {
		return source
	}
	tip, err := history.Tip(ctx)
	if err != nil {
		return source
	}
	files, err := history.Tree(ctx, tip)
	if err != nil {
		return source
	}
	source.tree = make(map[string]vaultgit.TreeFile, len(files))
	for _, file := range files {
		source.tree[file.Path] = file
	}
	source.history, source.tip, source.ready = history, tip, true
	return source
}

// bytes returns the committed content of rel if it is the content sha256 names.
//
// The digest is checked before the bytes are read, which is what makes this
// affordable on a path whose committed version is not the baseline: BlobDigest
// hashes without holding the blob, so the common miss costs no memory.
func (c *committed) bytes(rel, sha256 string) []byte {
	if !c.ready {
		return nil
	}
	file, held := c.tree[rel]
	if !held {
		return nil
	}
	digest, _, err := c.history.BlobDigest(c.ctx, file.OID)
	if err != nil || digest != sha256 {
		return nil
	}
	blobs, err := c.history.Blobs(c.ctx, []string{file.OID})
	if err != nil {
		return nil
	}
	return blobs[file.OID]
}

// release drops a package's hold from the record a caller is building, and
// reports whether there was one to drop. The caller saves that record and then
// calls discard.
//
// The two are separate because the order between them is the guarantee. The
// record is what names the retained bytes, so it stops naming them first: a
// crash in between leaves files nothing points at, which the next hold on the
// same package overwrites and a sweep collects. The other order leaves a durable
// record naming content that is gone, which is a conflict a person is invited to
// resolve from versions that no longer exist.
func (a *applier) release(next *State) bool {
	if _, held := next.Conflicts[a.manifest.PackageID]; !held {
		return false
	}
	delete(next.Conflicts, a.manifest.PackageID)
	return true
}

// discard removes the branches a released hold had retained.
//
// The failure is reported rather than returned, because by the time this runs
// the package has been applied or has stopped conflicting — the vault is right
// either way, and refusing over a directory that is safe to delete would undo a
// real application. See Result.Cleanup.
func (a *applier) discard(released bool) {
	if !released {
		return
	}
	a.cleanup = errors.Join(a.cleanup, discardRetained(a.root, a.manifest.PackageID))
}

// discardRetained removes one package's retained branches. The caller must have
// already written the state that stopped naming them.
func discardRetained(root, packageID string) error {
	dir, err := conflictDir(root, packageID)
	if err != nil {
		return err
	}
	if err := vault.GuardMutation(root, dir); err != nil {
		return err
	}
	// Nothing in internal/vault removes a tree, and this is not vault content: it
	// is this package's own scratch directory under `.bnotes/sync`, which the
	// content history and the portable scope both exclude wholesale.
	//bn:direct-write removes only this package's own retained-branch directory, after the state file that named it was rewritten without it
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("vaultimport: discard retained branches for %s: %w", packageID, err)
	}
	return nil
}

// retain records the conflict behind a refusal the transaction raised rather
// than inspect.
//
// It is the other end of the race inspect documents. Every expectation is read
// again under its path's own lock, so an edit that lands between the inspection
// and the staging check is refused there — correctly, and with the contested
// content untouched, but with nothing written down about it. Inspecting again
// here recovers the record: the rollback has already run by the time this is
// called, so the disk is back to what it was and the second reading is about the
// same disagreement.
//
// A second inspection that finds nothing is not an error. It means the edit that
// caused the refusal was itself undone in the meantime, and the package is
// simply refused with no hold — which the next delivery or the next drain will
// apply.
func (a *applier) retain(cause error) error {
	if !errors.Is(cause, ErrConflict) {
		return cause
	}
	found, err := a.inspect()
	if err != nil {
		return errors.Join(cause, err)
	}
	if len(found.conflicts) == 0 {
		return cause
	}
	if err := a.hold(found); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

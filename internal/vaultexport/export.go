package vaultexport

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// defaultMaxPackages bounds how many packages one call publishes, so a vault
// with a long unexported history makes progress in bounded passes rather than
// holding the state lock for the length of its whole history. The cursor is
// durable after every package, so the next call resumes exactly where this one
// stopped.
const defaultMaxPackages = 64

// defaultMaxBaselineBytes bounds one baseline package. A baseline is the only
// package built from something other than a single commit, so it is the only one
// whose size this producer chooses; every incremental package is exactly as large
// as the commit it describes.
const defaultMaxBaselineBytes int64 = 32 << 20

// Options configure one export pass.
type Options struct {
	// Root is the vault root. Required.
	Root string
	// ChannelDir overrides where packages are published. It defaults to this
	// vault's outgoing channel, and a caller should leave it alone outside
	// tests: a device writes into exactly one channel.
	ChannelDir string
	// GitExecutable overrides the git found on PATH.
	GitExecutable string
	// Now is the clock seam, used only for the manifest's descriptive
	// CreatedAt. Nothing here orders anything by time.
	Now func() time.Time
	// MaxPackages bounds how many packages this pass publishes. Zero means
	// defaultMaxPackages.
	MaxPackages int
	// MaxBaselineBytes bounds one baseline package's payload bytes. Zero means
	// defaultMaxBaselineBytes. A single file larger than the budget is still
	// sent, alone: a vault must not become unexportable because one attachment
	// is big.
	MaxBaselineBytes int64
	// AuthorizeDeletions is retained for source compatibility.
	// Deprecated: committed regular-file deletions are exported automatically.
	AuthorizeDeletions []string
}

// SkipReason says why a commit produced no package.
type SkipReason string

const (
	// SkipImported is a commit that applied a peer's operation. Publishing it
	// would send the peer its own change back.
	SkipImported SkipReason = "imported"
	// SkipPrivate is a commit that changed nothing portable — a sidecar state
	// file, a journal entry, this vault's own machine-local configuration.
	SkipPrivate SkipReason = "private"
)

// Skipped is one commit the cursor advanced past without publishing.
type Skipped struct {
	Commit string
	Reason SkipReason
}

// Published is one package this pass put into the channel.
type Published struct {
	// PackageID is the identity it was published under.
	PackageID string
	// Dir is the package directory in the channel.
	Dir string
	// Sequence is its position in this device's stream.
	Sequence uint64
	// Commit is the commit it describes. For a baseline package it is the
	// commit the baseline is pinned to, and several packages share it.
	Commit string
	// Changes is how many paths it carries.
	Changes int
}

// Result reports one pass.
type Result struct {
	// Baseline is whether this pass was describing a first export rather than
	// replaying commits.
	Baseline bool
	// Packages are what was published, in publication order.
	Packages []Published
	// Skipped are the commits the cursor advanced past without publishing.
	Skipped []Skipped
	// Cursor is the commit the export state now names, empty while a baseline
	// is still unfinished. It is reported when the pass halts too, and it is the
	// cursor as of the halt rather than as of the last full pass: the commits
	// before the refused one were published and recorded durably, and a caller
	// told only that the pass failed would have no way to learn how far it got.
	Cursor string
	// PendingCommits is how many commits this pass left unexported, which is
	// non-zero when MaxPackages bound the pass.
	PendingCommits int
	// PendingPaths is how many baseline paths are still to be sent.
	PendingPaths int
	// BurnedSequences are sequence numbers spent on a publication that was
	// interrupted before its manifest landed. The package directories they name
	// are inert — a peer reads one as an incomplete package forever — and the
	// commits they described are re-exported under fresh identities. They are
	// reported rather than swallowed because they leave a visible gap in this
	// device's stream and an operator deserves to know where it came from.
	BurnedSequences []uint64
}

// Export publishes as much of the vault's committed history as it can into the
// outgoing channel, advancing a durable cursor as it goes.
//
// It is safe to call repeatedly and safe to interrupt. Every package is followed
// by a durable state write, and every state write is preceded by a reservation,
// so a pass that dies at any point leaves the next one able to tell what
// happened: the sequence was never spent, or it was spent on a package that
// landed, or it was spent on one that did not.
func Export(ctx context.Context, opts Options) (*Result, error) {
	root, err := filepath.Abs(strings.TrimSpace(opts.Root))
	if err != nil || strings.TrimSpace(opts.Root) == "" {
		return nil, fmt.Errorf("vaultexport: vault root is required")
	}
	local, err := vaultsync.Load(root)
	if err != nil {
		return nil, err
	}
	history, err := vaultgit.OpenHistory(ctx, root, vaultgit.HistoryOptions{GitExecutable: opts.GitExecutable})
	if err != nil {
		return nil, err
	}

	// One lock for the whole pass. The cursor, the sequence, and the reservation
	// are read, decided, and written together, and a second exporter that
	// interleaved with this one would publish two packages under one sequence.
	barrier, err := joinBarrier(root)
	if err != nil {
		return nil, err
	}
	defer barrier()
	release, err := vault.LockPaths(root, StatePath(root))
	if err != nil {
		return nil, fmt.Errorf("vaultexport: lock export state: %w", err)
	}
	defer release()

	exp, err := newExporter(root, local, history, opts)
	if err != nil {
		return nil, err
	}
	// The result comes back even when the pass halts, and a caller must read it
	// either way. Everything in it is already durable by the time it is reported —
	// a published package, an advanced cursor, a burned sequence — so discarding it
	// on error would not undo any of them, it would only make them unreportable.
	// BurnedSequences is the one that cannot be recovered afterwards: the gap it
	// names is visible in this device's stream forever and nothing else records
	// where it came from.
	runErr := exp.run(ctx)
	return exp.result, runErr
}

// exporter is one pass. It exists so the state, the channel, and the budget are
// carried rather than threaded through every function, and so every state
// transition goes through commitState.
type exporter struct {
	root        string
	local       *vaultsync.Enrollment
	history     *vaultgit.History
	channelDir  string
	now         func() time.Time
	maxPackages int
	maxBytes    int64

	state  *State
	result *Result
}

func newExporter(root string, local *vaultsync.Enrollment, history *vaultgit.History, opts Options) (*exporter, error) {
	state, err := loadState(root, local)
	if err != nil {
		return nil, err
	}
	exp := &exporter{
		root:        root,
		local:       local,
		history:     history,
		channelDir:  strings.TrimSpace(opts.ChannelDir),
		now:         opts.Now,
		maxPackages: opts.MaxPackages,
		maxBytes:    opts.MaxBaselineBytes,
		state:       state,
		result:      &Result{},
	}
	if exp.channelDir == "" {
		exp.channelDir = vaultsync.OutgoingDir(root)
	}
	if exp.now == nil {
		exp.now = time.Now
	}
	if exp.maxPackages <= 0 {
		exp.maxPackages = defaultMaxPackages
	}
	if exp.maxBytes <= 0 {
		exp.maxBytes = defaultMaxBaselineBytes
	}
	return exp, nil
}

// run resolves whatever the last pass left behind, then exports.
//
// The branch is checked first, before anything writes to the record. Recovering a
// reservation records the current branch as the cursor's, so a check made
// afterwards would be comparing the branch against itself and could never fire —
// and the one pass that most needs the check is exactly the interrupted one,
// because a person who switched branches is the likeliest reason it was
// interrupted at all.
func (e *exporter) run(ctx context.Context) error {
	// Deferred here rather than at the point the cursor moves, so every path out
	// of this pass reports it — including the halts, since Export returns its
	// result alongside the error. resolveReservation can adopt a package and
	// advance the cursor durably *before* the epoch check refuses, and a Cursor
	// left empty on that path would say "a baseline is still unfinished", which is
	// a different vault state entirely.
	defer func() { e.result.Cursor = e.state.Commit }()
	if err := e.checkBranch(); err != nil {
		return err
	}
	if err := e.resolveReservation(); err != nil {
		return err
	}
	if err := e.checkEnrollmentEpoch(); err != nil {
		return err
	}
	tip, err := e.history.Tip(ctx)
	if err != nil {
		// A vault between `git init` and its first snapshot has nothing to say.
		// That is not a failure and must not look like one, or enrolling a fresh
		// vault would report an error on its first synchronization.
		if errors.Is(err, vaultgit.ErrUnbornBranch) {
			return nil
		}
		return err
	}
	if e.state.Commit == "" {
		return e.exportBaseline(ctx, tip)
	}
	return e.exportCommits(ctx, tip)
}

// checkEnrollmentEpoch refuses a cursor from a previous enrollment generation.
//
// The epoch advances when a vault is restored from a backup, which is exactly
// when the history the cursor describes may not be the history this vault has
// any more. A cursor with nothing behind it — a fresh vault, or one after Reset —
// simply adopts the current epoch.
func (e *exporter) checkEnrollmentEpoch() error {
	if e.state.Epoch == e.local.Epoch {
		return nil
	}
	if e.state.Commit == "" && e.state.Baseline == "" {
		next := e.state.clone()
		next.Epoch = e.local.Epoch
		return e.commitState(next)
	}
	return fmt.Errorf("%w: cursor is at epoch %d, this vault is at %d; Reset re-baselines",
		ErrEpochChanged, e.state.Epoch, e.local.Epoch)
}

// checkBranch refuses a cursor recorded against a different branch.
func (e *exporter) checkBranch() error {
	if e.state.Branch == "" || e.state.Branch == e.history.Branch() {
		return nil
	}
	return fmt.Errorf("%w: cursor follows %s, HEAD is on %s", ErrBranchChanged, e.state.Branch, e.history.Branch())
}

// exportCommits replays the commits between the cursor and the tip.
func (e *exporter) exportCommits(ctx context.Context, tip string) error {
	contained, err := e.history.Contains(ctx, e.state.Commit, tip)
	if err != nil {
		return err
	}
	if !contained {
		return fmt.Errorf("%w: %s is not an ancestor of %s; Reset re-baselines",
			ErrHistoryRewritten, e.state.Commit, tip)
	}
	commits, err := e.history.CommitsAfter(ctx, e.state.Commit, tip)
	if err != nil {
		return err
	}
	e.result.PendingCommits = len(commits)
	previous := e.state.Commit
	for _, commit := range commits {
		if len(e.result.Packages) >= e.maxPackages {
			break
		}
		info, err := e.history.Inspect(ctx, commit)
		if err != nil {
			return err
		}
		// The chain is checked explicitly rather than inferred from rev-list's
		// output. A merge has two parents and cannot be replayed as one operation;
		// a commit whose parent is not the one before it means the range is not a
		// line at all. Either way the peer would be told a sequence of operations
		// that never happened in that order.
		if len(info.Parents) != 1 || info.Parents[0] != previous {
			return fmt.Errorf("%w: %s has %d parents and does not follow %s",
				ErrUnsupportedHistory, commit, len(info.Parents), previous)
		}
		if err := e.exportCommit(ctx, commit, info); err != nil {
			return err
		}
		previous = commit
		e.result.PendingCommits--
	}
	return nil
}

// exportCommit publishes one commit, or advances past it.
func (e *exporter) exportCommit(ctx context.Context, commit string, info vaultgit.CommitInfo) error {
	changes, err := e.history.Changes(ctx, commit)
	if err != nil {
		return err
	}
	authority, err := readCommitAuthority(ctx, e.history, changes)
	if err != nil {
		return err
	}
	portable := portableChanges(changes)

	imported, isImport, err := e.importedOperation(authority.entryIDs)
	if err != nil {
		return fmt.Errorf("%s: %w", commit, err)
	}
	if isImport {
		return e.skipImported(commit, portable, imported, authority.entryIDs)
	}
	if len(portable) == 0 {
		return e.skipCommit(commit, SkipPrivate, portable, "")
	}
	blobs, err := e.history.Blobs(ctx, changedBlobOIDs(portable))
	if err != nil {
		return err
	}
	return e.publishCommit(ctx, commit, info, portable, blobs)
}

// importedOperation reports the peer operation a commit applied, if it applied
// one.
//
// A commit either is an applied import or is not; a commit naming both an
// imported journal entry and a local one is refused rather than guessed at.
// internal/noteops commits exactly one entry's paths, so the mixed shape cannot
// arise from this codebase — and if it ever does, treating it as an import would
// silently drop the local half and treating it as local would echo the peer's.
func (e *exporter) importedOperation(entryIDs []string) (Import, bool, error) {
	var found []Import
	for _, id := range entryIDs {
		if record, known := e.state.Imported[id]; known {
			found = append(found, record)
		}
	}
	if len(found) == 0 {
		return Import{}, false, nil
	}
	if len(found) != len(entryIDs) {
		return Import{}, false, fmt.Errorf("%w: commit carries both imported and local journal entries", ErrUnsupportedHistory)
	}
	for _, record := range found[1:] {
		if record.OperationID != found[0].OperationID {
			return Import{}, false, fmt.Errorf("%w: commit carries two different imported operations", ErrUnsupportedHistory)
		}
	}
	return found[0], true, nil
}

// skipImported advances past a commit that applied a peer operation, recording
// the peer's operation as what last touched each of its paths.
//
// That ledger update is the point of skipping rather than ignoring. A later
// local edit to one of these paths must name the peer's operation as its causal
// predecessor, which is how the peer learns this device had seen its change
// before making its own. Dropping the edge would make every local edit after an
// import look like a conflict.
//
// The consumed ledger entries are deleted in the same write that advances the
// cursor, so the record cannot outlive its usefulness and cannot be lost before
// it is used.
func (e *exporter) skipImported(commit string, portable []vaultgit.FileChange, imported Import, entryIDs []string) error {
	next := e.state.clone()
	for _, change := range portable {
		next.Paths[change.Path] = imported.OperationID
	}
	for _, id := range entryIDs {
		delete(next.Imported, id)
	}
	next.Commit = commit
	next.Branch = e.history.Branch()
	e.result.Skipped = append(e.result.Skipped, Skipped{Commit: commit, Reason: SkipImported})
	return e.commitState(next)
}

// skipCommit advances past a commit that produced no package. operationID, when
// set, is recorded as what last touched each portable path.
func (e *exporter) skipCommit(commit string, reason SkipReason, portable []vaultgit.FileChange, operationID string) error {
	next := e.state.clone()
	if operationID != "" {
		for _, change := range portable {
			next.Paths[change.Path] = operationID
		}
	}
	next.Commit = commit
	next.Branch = e.history.Branch()
	e.result.Skipped = append(e.result.Skipped, Skipped{Commit: commit, Reason: reason})
	return e.commitState(next)
}

// commitState makes a record durable and only then adopts it in memory, so a
// failed write can never leave this pass believing something a restart would
// not.
func (e *exporter) commitState(next *State) error {
	if err := saveState(e.root, next); err != nil {
		return err
	}
	e.state = next
	return nil
}

// portableChanges filters a commit's changes to the portable content scope, in a
// fixed path order.
//
// vaultsync.Portable is the only filter, and it is applied to every path without
// exception: this vault's own export cursor, its undo journal, its sidecar state,
// its machine-local configuration overlay, and the synchronization channels
// themselves are all under `.bnotes/`, which that policy excludes wholesale.
func portableChanges(changes []vaultgit.FileChange) []vaultgit.FileChange {
	portable := make([]vaultgit.FileChange, 0, len(changes))
	for _, change := range changes {
		if vaultsync.Portable(change.Path) {
			portable = append(portable, change)
		}
	}
	sort.Slice(portable, func(i, j int) bool { return portable[i].Path < portable[j].Path })
	return portable
}

// changedBlobOIDs lists every blob a change set references, both sides.
//
// Both sides, in one read, because a package carries the producer's *prior*
// belief about each path as well as its new content — and that prior belief is a
// SHA-256 of the old bytes, which only the old blob can supply. Asking for them
// separately would double the process count on exactly the commits that change
// the most.
func changedBlobOIDs(changes []vaultgit.FileChange) []string {
	seen := map[string]struct{}{}
	oids := make([]string, 0, len(changes)*2)
	add := func(oid string) {
		if oid == "" {
			return
		}
		if _, known := seen[oid]; known {
			return
		}
		seen[oid] = struct{}{}
		oids = append(oids, oid)
	}
	for _, change := range changes {
		add(change.OldOID)
		add(change.NewOID)
	}
	return oids
}

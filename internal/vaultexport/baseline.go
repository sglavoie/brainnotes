package vaultexport

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// exportBaseline describes the whole portable tree of one commit to a peer that
// holds nothing.
//
// A baseline is the one export that is not a commit, because there is no commit
// that says "here is the vault". Replaying the whole history from the root
// instead would be the alternative and it is worse in every respect: it would
// publish every intermediate state of every note, it would need deletion
// authority for every removal a vault ever made, and it would halt on the first
// commit predating the journal entirely.
//
// Every change in it expects the path to be *absent*. A peer that already holds
// the note therefore gets a conflict rather than a silent overwrite, which is
// the correct answer: this producer is saying "I believe you have nothing", and
// a peer that has something is evidence the belief was wrong.
func (e *exporter) exportBaseline(ctx context.Context, tip string) error {
	e.result.Baseline = true
	commit, err := e.baselineCommit(ctx, tip)
	if err != nil {
		return err
	}
	files, err := e.baselineFiles(ctx, commit)
	if err != nil {
		return err
	}
	for len(files) > 0 && len(e.result.Packages) < e.maxPackages {
		slice := e.nextSlice(files)
		if err := e.publishBaselineSlice(ctx, commit, slice, len(files)-len(slice)); err != nil {
			return err
		}
		files = files[len(slice):]
	}
	e.result.PendingPaths = len(files)
	if len(files) == 0 {
		return e.finishBaseline(commit)
	}
	return nil
}

// baselineCommit pins the baseline to one commit and keeps it pinned across
// passes. See State.Baseline for why re-reading the tip each pass would drop
// edits made to paths an earlier slice already carried.
func (e *exporter) baselineCommit(ctx context.Context, tip string) (string, error) {
	if e.state.Baseline == "" {
		next := e.state.clone()
		next.Baseline = tip
		next.Branch = e.history.Branch()
		if err := e.commitState(next); err != nil {
			return "", err
		}
		return tip, nil
	}
	// The pinned commit must still be on this branch. If it is not, an unfinished
	// baseline was describing a history that has been rewritten, and the slices
	// already sent describe content the vault no longer claims.
	contained, err := e.history.Contains(ctx, e.state.Baseline, tip)
	if err != nil {
		return "", err
	}
	if !contained {
		return "", fmt.Errorf("%w: unfinished baseline %s is not an ancestor of %s; Reset re-baselines",
			ErrHistoryRewritten, e.state.Baseline, tip)
	}
	return e.state.Baseline, nil
}

// baselineFiles lists the portable content of the pinned commit that has not
// been sent yet, in a fixed order.
//
// "Not sent yet" is read off the path ledger rather than from a separate
// progress record, because the ledger already answers exactly that question: an
// entry exists for a path if and only if some package carried it. That keeps one
// fact in one place, and it makes an interrupted baseline recover through the
// same reservation machinery every other package uses.
func (e *exporter) baselineFiles(ctx context.Context, commit string) ([]vaultgit.TreeFile, error) {
	tree, err := e.history.Tree(ctx, commit)
	if err != nil {
		return nil, err
	}
	files := make([]vaultgit.TreeFile, 0, len(tree))
	for _, file := range tree {
		if !vaultsync.Portable(file.Path) {
			continue
		}
		if !regularFileMode(file.Mode) {
			return nil, fmt.Errorf("%w: %s holds %q with mode %s", ErrUnsupportedEntry, commit, file.Path, file.Mode)
		}
		if _, sent := e.state.Paths[file.Path]; sent {
			continue
		}
		files = append(files, file)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// nextSlice takes as many files as fit the byte budget, always at least one.
//
// Always at least one, because a vault must not become unexportable for holding
// a single attachment larger than the budget. The budget bounds how much this
// producer chooses to hold at once, not what it is willing to send.
func (e *exporter) nextSlice(files []vaultgit.TreeFile) []vaultgit.TreeFile {
	var total int64
	for i, file := range files {
		if i > 0 && total+file.Size > e.maxBytes {
			return files[:i]
		}
		total += file.Size
	}
	return files
}

// publishBaselineSlice publishes one slice of the baseline as one operation.
//
// The operation identity is derived from the commit and the slice's first path,
// so re-running an interrupted baseline with the same budget produces the same
// identities. It does not have to: slices carry disjoint path sets, so a slice
// re-cut under a different budget is a different operation over paths no other
// operation claimed, and nothing collapses or duplicates.
func (e *exporter) publishBaselineSlice(ctx context.Context, commit string, slice []vaultgit.TreeFile, remaining int) error {
	oids := make([]string, 0, len(slice))
	for _, file := range slice {
		oids = append(oids, file.OID)
	}
	blobs, err := e.history.Blobs(ctx, oids)
	if err != nil {
		return err
	}

	operationID := vaultsync.DeriveOperationID(e.local.VaultID, "baseline", commit, slice[0].Path, strconv.Itoa(remaining))
	sequence := e.state.Sequence + 1
	builder, err := vaultsync.NewBuilder(e.local, sequence, e.now())
	if err != nil {
		return err
	}
	builder.BeginOperation(operationID, e.now())
	paths := make([]string, 0, len(slice))
	for _, file := range slice {
		content, read := blobs[file.OID]
		if !read {
			return fmt.Errorf("%w: %s holds %q and its content could not be read", ErrCorruptState, commit, file.Path)
		}
		// Absent, and no predecessors. This producer is telling a peer it believes
		// holds nothing; claiming to have seen an earlier operation on a path it
		// has never described would be a causal edge pointing at nothing.
		builder.Write(file.Path, content, vaultsync.Absent())
		paths = append(paths, file.Path)
	}
	manifest, payloads, err := builder.Build()
	if err != nil {
		return err
	}
	return e.publish(ctx, manifest, payloads, publication{
		commit:      commit,
		operationID: operationID,
		paths:       paths,
		baseline:    true,
	})
}

// finishBaseline turns the pinned baseline into an ordinary cursor. From here
// the next pass is an incremental replay of whatever landed after it, including
// any edit made to a path an earlier slice already carried.
func (e *exporter) finishBaseline(commit string) error {
	next := e.state.clone()
	next.Baseline = ""
	next.Commit = commit
	next.Branch = e.history.Branch()
	// Result.Cursor is not set here: run's deferred read reports it from the
	// record, which is the only copy that survives a halt, and one assignment
	// cannot disagree with the record the way two could.
	return e.commitState(next)
}

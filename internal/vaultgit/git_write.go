package vaultgit

import (
	"context"
	"errors"
	"strings"
	"time"
)

// stageAdd records the working-tree content of paths into indexFile.
func (g *gitExec) stageAdd(ctx context.Context, indexFile string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	_, err := g.run(ctx, gitCall{
		args:      []string{"update-index", "--add", "--replace", "-z", "--stdin"},
		stdin:     nulList(paths),
		indexFile: indexFile,
	})
	return err
}

// stageRemove drops paths from indexFile whether or not they still exist on
// disk.
func (g *gitExec) stageRemove(ctx context.Context, indexFile string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	_, err := g.run(ctx, gitCall{
		args:      []string{"update-index", "--force-remove", "-z", "--stdin"},
		stdin:     nulList(paths),
		indexFile: indexFile,
	})
	return err
}

// writeTree turns indexFile into a tree object.
func (g *gitExec) writeTree(ctx context.Context, indexFile string) (string, error) {
	out, err := g.run(ctx, gitCall{args: []string{"write-tree"}, indexFile: indexFile})
	if err != nil {
		return "", err
	}
	tree := strings.TrimSpace(string(out))
	if tree == "" {
		return "", errors.New("git write-tree: empty result")
	}
	return tree, nil
}

// commitTree writes a commit object. It is plumbing on purpose: it runs no
// hook, reads no index, and touches no ref, so building a commit can never have
// a side effect on the user's repository. The ref move is a separate,
// compare-and-swapped step.
func (g *gitExec) commitTree(ctx context.Context, tree, parent, message string, who identity, when time.Time) (string, error) {
	args := []string{"commit-tree", tree}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	args = append(args, "-m", message)
	out, err := g.run(ctx, gitCall{args: args, author: &who, when: when})
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(string(out))
	if commit == "" {
		return "", errors.New("git commit-tree: empty result")
	}
	return commit, nil
}

// updateRefCAS moves ref from oldOID to newOID, failing if it is not currently
// at oldOID. An empty oldOID means the ref must not exist yet.
//
// This is the whole concurrency story for two BrainNotes processes snapshotting
// one vault: they may race all the way to here, and the loser's ref update
// fails rather than discarding the winner's commit. The loser's objects are
// already written and reachable from its own commit, so nothing it computed is
// lost — it retries from a re-read HEAD.
func (g *gitExec) updateRefCAS(ctx context.Context, ref, newOID, oldOID string) error {
	if oldOID == "" {
		oldOID = nullOID
	}
	_, err := g.run(ctx, gitCall{args: []string{"update-ref", "--create-reflog", ref, newOID, oldOID}})
	return err
}

// setIndexEntry pins one path in an index to an exact object, independent of
// what the working tree currently holds. `--cacheinfo` is given in its
// three-argument form: the newer `<mode>,<oid>,<path>` form splits on commas
// and silently records a truncated path for a note whose name contains one.
func (g *gitExec) setIndexEntry(ctx context.Context, indexFile string, entry treeEntry) error {
	_, err := g.run(ctx, gitCall{
		args:      []string{"update-index", "--add", "--replace", "--cacheinfo", entry.mode, entry.oid, entry.path},
		indexFile: indexFile,
	})
	return err
}

// pushBranch pushes ref to the same ref on the remote.
//
// There is no --force and no --force-with-lease anywhere in this package: the
// content history is the recovery layer, so an engine that can rewrite it is an
// engine that can destroy the thing it exists to protect. A rejected push means
// the remote holds commits this clone does not, and the correct response is to
// record the failure and let a person reconcile — never to overwrite.
func (g *gitExec) pushBranch(ctx context.Context, remote, ref string) error {
	_, err := g.run(ctx, gitCall{
		args:   []string{"push", "--porcelain", "--atomic", remote, ref + ":" + ref},
		remote: true,
	})
	return err
}

package vaultgit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/sglavoie/brainnotes/internal/agent"
)

// stageAdditions records working-tree content, skipping only what the vault
// itself will not hand over.
//
// An unreadable addition is reported rather than fatal, and the distinction is
// deliberate: recording a file is purely additive, so failing to record one
// leaves the history exactly as correct as it was a moment ago. A vault holds
// notes a user has deliberately chmod'd to 0000, and refusing to snapshot the
// entire vault because of one of them would trade a real safety net for a
// theoretical completeness guarantee.
//
// That tolerance is bounded by the third return value, which is the repair for
// the failure mode it used to hide. A path git refused to stage is only a skip
// when the engine can independently confirm it cannot read the bytes either;
// anything else means git failed for a reason that has nothing to do with this
// path — an index it cannot write, an object store that will not take a write —
// and that reason applies to every other path in the same pass. Turning it into
// a skip made a repository-wide failure look like an empty vault: nothing
// staged, no error, no commit, and a caller told the snapshot succeeded.
//
// The batch is tried first because it is one process for the whole vault. Only
// when it fails does the engine fall back to one invocation per path, which is
// slow but is the only way to learn which path was the problem. Re-staging a
// path the failed batch already staged is idempotent.
func (m *Manager) stageAdditions(ctx context.Context, paths []string) (staged []string, skipped []SkippedPath, err error) {
	if len(paths) == 0 {
		return nil, nil, nil
	}
	if batchErr := m.git.stageAdd(ctx, m.indexPath, paths); batchErr == nil {
		return paths, nil, nil
	}
	for _, path := range paths {
		stageErr := m.git.stageAdd(ctx, m.indexPath, []string{path})
		if stageErr == nil {
			staged = append(staged, path)
			continue
		}
		detail := agent.SanitizedDiagnostic(stageErr)
		if !m.contentUnreadable(path) {
			return staged, skipped, fmt.Errorf("%s could not be staged although the vault hands its content over on request: %s", path, detail)
		}
		skipped = append(skipped, SkippedPath{Path: path, Reason: detail})
	}
	return staged, skipped, nil
}

// contentUnreadable reports whether the engine, asking on its own account, also
// cannot read this path's bytes.
//
// It is the question that separates the one staging failure this engine may
// tolerate from every other one, and it is asked by opening the file rather than
// by reading git's diagnostic. A message is a string a future git is free to
// reword; whether the vault will hand over the content is the fact the tolerance
// was justified by in the first place.
//
// A path that vanished between the scan and the staging answers yes, which is
// the right answer for both callers: an external scan offers to record whatever
// is there and there is nothing there, and an exact snapshot required a path the
// vault no longer holds and must refuse.
func (m *Manager) contentUnreadable(rel string) bool {
	f, err := os.Open(filepath.Join(m.root, filepath.FromSlash(rel)))
	if err != nil {
		return true
	}
	defer func() { _ = f.Close() }()
	// Opening a directory succeeds on Unix and fails only on the read, so the
	// content has to actually be asked for. An empty file answers io.EOF, which
	// is a file that reads fine and holds nothing.
	var probe [1]byte
	if _, err := f.Read(probe[:]); err != nil && !errors.Is(err, io.EOF) {
		return true
	}
	return false
}

// misstagedAdditions reports which of paths the private index does not hold
// with exactly the object and mode the working tree currently has.
//
// Path membership is not the question, and asking it was a hole. The index is
// seeded from HEAD, so every already-tracked path arrives with an entry of its
// own before anything is staged: an `update-index --add` that reported success
// without doing the work left HEAD's entry sitting under the requested path, the
// readback found it, and the snapshot went on to build a tree identical to the
// tip and return a nil success with the change absent from the history. Only
// comparing the entry against the content the snapshot is required to record
// separates "the index holds this file" from "the index holds this file's old
// version".
//
// A path whose required entry cannot be established at all — it vanished, it is
// neither a regular file nor a symlink, git would not hash it — is an error
// rather than a mismatch, because the caller must fail closed either way and the
// two failures deserve different diagnostics.
func (m *Manager) misstagedAdditions(ctx context.Context, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	entries, err := m.git.lsFilesStaged(ctx, m.indexPath, paths)
	if err != nil {
		return nil, err
	}
	required, err := m.requiredIndexEntries(ctx, paths)
	if err != nil {
		return nil, err
	}
	trustExec := m.executableBitTrusted(ctx)
	var wrong []string
	for _, path := range paths {
		staged, present := entries[path]
		want := required[path]
		if !present || staged.oid != want.oid || !modeRecords(want.mode, staged.mode, trustExec) {
			wrong = append(wrong, path)
		}
	}
	return wrong, nil
}

// requiredIndexEntries reports the object and mode the private index must hold
// for each path if the tree this snapshot builds is to record the working tree.
func (m *Manager) requiredIndexEntries(ctx context.Context, paths []string) (map[string]treeEntry, error) {
	required := make(map[string]treeEntry, len(paths))
	hashable := make([]string, 0, len(paths))
	for _, path := range paths {
		abs := filepath.Join(m.root, filepath.FromSlash(path))
		info, err := os.Lstat(abs)
		if err != nil {
			return nil, fmt.Errorf("%s could not be inspected to confirm what the index must hold: %s", path, agent.SanitizedDiagnostic(err))
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(abs)
			if err != nil {
				return nil, fmt.Errorf("%s could not be read to confirm what the index must hold: %s", path, agent.SanitizedDiagnostic(err))
			}
			oid, err := m.git.hashBlob(ctx, []byte(target))
			if err != nil {
				return nil, err
			}
			required[path] = treeEntry{mode: "120000", oid: oid, path: path}
		case info.Mode().IsRegular():
			required[path] = treeEntry{mode: regularIndexMode(info.Mode()), path: path}
			hashable = append(hashable, path)
		default:
			return nil, fmt.Errorf("%s is neither a regular file nor a symbolic link, so what the index must hold for it cannot be established", path)
		}
	}
	if len(hashable) == 0 {
		return required, nil
	}
	oids, err := m.git.hashWorktreeContent(ctx, hashable)
	if err != nil {
		return nil, err
	}
	for _, path := range hashable {
		oid, ok := oids[path]
		if !ok {
			return nil, fmt.Errorf("%s was not hashed, so what the index must hold for it cannot be established", path)
		}
		entry := required[path]
		entry.oid = oid
		required[path] = entry
	}
	return required, nil
}

// regularIndexMode is git's own rule for a regular file: any executable bit
// makes it 100755 and nothing else matters.
func regularIndexMode(mode os.FileMode) string {
	if mode.Perm()&0o111 != 0 {
		return "100755"
	}
	return "100644"
}

// modeRecords reports whether a staged mode records the required one.
//
// The two are compared exactly wherever the executable bit means anything. On a
// repository with `core.fileMode` off — a vault living on a filesystem that does
// not carry the bit — git deliberately keeps whatever mode the entry already
// had, so demanding equality there would block the vault over a permission it
// was never going to record in the first place. The file *type* is still
// compared, because a regular file staged as a symlink is not a permissions
// question.
func modeRecords(required, staged string, trustExec bool) bool {
	if required == staged {
		return true
	}
	if trustExec {
		return false
	}
	return isRegularIndexMode(required) && isRegularIndexMode(staged)
}

func isRegularIndexMode(mode string) bool {
	return mode == "100644" || mode == "100755"
}

// executableBitTrusted reports the repository's `core.fileMode`, read once.
//
// Every caller reaches it holding the safety lock, and the value is a property
// of the repository rather than of the pass, so one read serves the process.
func (m *Manager) executableBitTrusted(ctx context.Context) bool {
	m.execBitOnce.Do(func() {
		m.execBitTrusted = m.git.configBool(ctx, "core.fileMode", true)
	})
	return m.execBitTrusted
}

// unremovedDeletions reports which of paths the private index still holds after
// a removal was staged.
//
// It is the other half of the readback. A removal that never reached the index
// leaves the tip's entry in the tree, and for a plan that removes without adding
// that comes out as a tree identical to HEAD — no commit, no error, and a caller
// told the deletion was recorded. An unmerged entry surfaces as an error from
// lsFilesStaged, which is the same refusal by a different route.
func (m *Manager) unremovedDeletions(ctx context.Context, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	entries, err := m.git.lsFilesStaged(ctx, m.indexPath, paths)
	if err != nil {
		return nil, err
	}
	var present []string
	for _, path := range paths {
		if _, ok := entries[path]; ok {
			present = append(present, path)
		}
	}
	return present, nil
}

// refreshUserIndex updates the user's real index for the paths this snapshot
// just committed, and only where doing so cannot lose their work.
//
// Without it, an engine commit leaves `git status` showing a staged revert:
// the user's index still holds the old HEAD's entry for a note whose content
// the new HEAD has moved past, which git renders as `MM`, `AD`, or a staged
// deletion. That is alarming and, worse, invites the user to "fix" it with a
// checkout that discards the snapshot.
//
// The guard is that a path is only refreshed when the user's index still agrees
// with the *old* HEAD for it. If they staged something of their own — a
// different version, a deliberate `git add` — the entry differs and is left
// exactly as they left it. Every failure here is ignored: this is cosmetic
// repair of a file the user owns, and it must never turn a landed commit into
// a reported failure.
func (m *Manager) refreshUserIndex(ctx context.Context, oldHead, newCommit string, added, removed []string) {
	paths := append(append([]string{}, added...), removed...)
	if len(paths) == 0 {
		return
	}
	real, err := m.git.lsFilesStaged(ctx, "", paths)
	if err != nil {
		return
	}
	var old map[string]treeEntry
	if oldHead == "" {
		old = map[string]treeEntry{}
	} else if old, err = m.git.lsTree(ctx, oldHead, paths); err != nil {
		return
	}
	next, err := m.git.lsTree(ctx, newCommit, paths)
	if err != nil {
		return
	}
	for _, path := range paths {
		current, inReal := real[path]
		previous, inOld := old[path]
		if inReal != inOld {
			continue
		}
		if inReal && (current.oid != previous.oid || current.mode != previous.mode) {
			continue
		}
		if entry, ok := next[path]; ok {
			_ = m.git.setIndexEntry(ctx, "", entry)
			continue
		}
		_ = m.git.stageRemove(ctx, "", []string{path})
	}
}

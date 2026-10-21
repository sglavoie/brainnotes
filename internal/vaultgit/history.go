package vaultgit

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/brainnotes/internal/agent"
)

// Git file modes this package is willing to call content. Anything else in a
// tree — a symlink, a gitlink, a directory entry a recursive listing should
// never have produced — is reported as-is and refused by the caller, which is
// the layer that knows what refusing costs.
const (
	// ModeRegular is a non-executable regular file, which is what every note,
	// attachment, and template is.
	ModeRegular = "100644"
	// ModeExecutable is a regular file with the execute bit, which a vault can
	// legitimately hold (a script filed beside its notes).
	ModeExecutable = "100755"
)

// ErrUnbornBranch reports a branch that exists as a HEAD symref but has no
// commit yet. It is not a repository fault: it is what a vault looks like
// between `git init` and its first snapshot, and a reader's right response is to
// have nothing to report rather than to fail.
var ErrUnbornBranch = errors.New("vaultgit: the branch has no commits yet")

// History is a read-only view of a vault's committed content history.
//
// It is a separate opening from Manager rather than a set of methods on one,
// because a reader must be able to prove it changed nothing. Open resolves a
// recovery intent — the one case where opening a Manager mutates — and a Manager
// owns the private index, the block marker, and the external-edit watch schedule
// that decides when the next autosave is due. A reader that acquired one would
// be an observation with the power to disturb what it observes.
//
// OpenHistory performs the same two structural refusals (no repository, a `.git`
// that is not a directory) and the same top-level and detached-HEAD checks, then
// stops. It takes no lock, writes no state, and never touches the working tree:
// every method here reads committed objects, so uncommitted edits are invisible
// to it by construction. That last property is not an implementation detail —
// it is why an exporter built on this cannot publish a half-typed note.
//
// A History is safe for concurrent use: it holds no mutable state.
type History struct {
	root   string
	git    *gitExec
	branch string
	// state is the same safety state directory a Manager owns, opened for
	// reading only. Nothing here writes through it: the two methods that use it
	// (Blocked, Watch) are file reads, and every mutator on safetyState is
	// reached from Manager alone.
	state *safetyState
}

// HistoryOptions configures a read-only opening. It is deliberately not Options:
// an author identity, a remote, and an autosave policy are all inputs to
// *writing*, and a reader that accepted them would invite a caller to believe
// they meant something here.
type HistoryOptions struct {
	// GitExecutable overrides the git found on PATH.
	GitExecutable string
}

// OpenHistory prepares a read-only view of the vault's content history.
func OpenHistory(ctx context.Context, vaultRoot string, opts HistoryOptions) (*History, error) {
	root, git, err := openWorkingTree(vaultRoot, opts.GitExecutable)
	if err != nil {
		return nil, err
	}
	gitDir, err := resolveGitDir(ctx, git, root)
	if err != nil {
		return nil, err
	}
	// The same refusal Open makes, for a different reason. A detached HEAD has no
	// branch, so a reader has no stable thing to record a cursor against: the next
	// pass would have no way to ask whether what it read last time is still on the
	// line of history it is following.
	ref, err := git.symbolicHead(ctx)
	if err != nil {
		return nil, safetyErrorf(KindRepositoryState, "open", "the repository HEAD could not be read: %s", agent.SanitizedDiagnostic(err))
	}
	if ref == "" {
		return nil, safetyErrorf(KindRepositoryState, "open", "the repository HEAD is detached, so there is no branch to read a history from")
	}
	return &History{root: root, git: git, branch: ref, state: newSafetyState(gitDir)}, nil
}

// Root reports the vault root this view reads.
func (h *History) Root() string { return h.root }

// Blocked reports the persisted block marker: whether this vault's mutation
// snapshots are currently refused, and the record explaining why.
//
// It is here rather than only on Manager because the question "is this vault
// safe to write to" is one an observer has to be able to ask. A status command
// or a background companion that had to open a Manager to find out would resolve
// a recovery intent as a side effect of asking — an observation with the power
// to disturb what it observes, which is exactly what this type exists to avoid.
//
// It fails closed the way the marker itself does: an unreadable marker is
// reported as blocked with a record that says so, alongside the decode error. A
// caller must treat a non-nil error as *more* reason to stop, not less.
func (h *History) Blocked() (BlockRecord, bool, error) { return h.state.LoadBlock() }

// Watch reports the persisted debounce clock as it stands on disk.
//
// A zero FirstChangeAt means no burst is open: everything the working tree holds
// has reached the content history, or nothing has changed since the last
// snapshot closed one. A non-zero one means there are edits saved to disk that
// the history does not carry yet, which is the distinction an observer needs to
// tell "written locally" from "recorded in the history" — and which it must not
// answer by scanning the working tree, because that races the very autosave it
// is describing.
//
// It is a pure read with no failure mode: a missing, corrupt, or
// future-versioned record reports an empty state, because none of those are
// statements that a burst is open. That is the same reading
// [safetyState.LoadWatchState] gives every writer in this package, so an
// observer and the autosave scheduler never disagree about whether a vault is
// mid-burst.
func (h *History) Watch() WatchState { return h.state.LoadWatchState() }

// Branch reports the full ref HEAD pointed at when this view was opened, as in
// `refs/heads/main`. A caller that persists a cursor should persist this beside
// it: a cursor from another branch describes a different line of history and
// must not be advanced against this one.
func (h *History) Branch() string { return h.branch }

// Tip resolves the branch to its current commit, reporting ErrUnbornBranch when
// the branch has no commits yet.
func (h *History) Tip(ctx context.Context) (string, error) {
	oid, err := h.git.resolveBranch(ctx, h.branch)
	if err != nil {
		return "", fmt.Errorf("vaultgit: resolve %s: %w", h.branch, err)
	}
	if oid == "" {
		return "", fmt.Errorf("%w: %s", ErrUnbornBranch, h.branch)
	}
	return oid, nil
}

// Contains reports whether commit is an ancestor of tip, or is tip itself.
//
// This is the history-rewrite question. A cursor naming a commit the branch no
// longer contains means the history it described was rebased, amended, filtered,
// or reset out from under the reader — and the reader cannot tell which. What it
// can tell is that continuing would be wrong, because the commits it already
// reported are no longer the commits this branch says happened.
func (h *History) Contains(ctx context.Context, commit, tip string) (bool, error) {
	if err := checkObjectNames(commit, tip); err != nil {
		return false, err
	}
	contained, err := h.git.isAncestor(ctx, commit, tip)
	if err != nil {
		return false, fmt.Errorf("vaultgit: check ancestry: %w", err)
	}
	return contained, nil
}

// CommitsAfter lists the commits reachable from tip but not from base, oldest
// first. base is exclusive and tip is inclusive; an empty base means the whole
// history up to tip.
//
// The order is the point. A caller replaying history has to publish the oldest
// unseen commit first, because the causal edges it derives from earlier commits
// are what a peer uses to order later ones.
//
// No limit is applied here even for a caller that wants one: `rev-list -n`
// selects before `--reverse` reverses, so a limited reversed walk returns the
// *newest* commits in reverse order, which is the exact opposite of what a
// replaying caller needs. Callers bound their own work by taking a prefix of
// this slice, which costs one object name per commit and no object reads.
func (h *History) CommitsAfter(ctx context.Context, base, tip string) ([]string, error) {
	if err := checkObjectNames(tip); err != nil {
		return nil, err
	}
	args := []string{"rev-list", "--reverse"}
	if base == "" {
		args = append(args, tip)
	} else {
		if err := checkObjectNames(base); err != nil {
			return nil, err
		}
		args = append(args, base+".."+tip)
	}
	out, err := h.git.run(ctx, gitCall{args: args})
	if err != nil {
		return nil, fmt.Errorf("vaultgit: list commits: %w", err)
	}
	var commits []string
	for _, line := range strings.Split(string(out), "\n") {
		oid := strings.TrimSpace(line)
		if oid == "" {
			continue
		}
		if !validObjectName(oid) {
			return nil, fmt.Errorf("vaultgit: rev-list returned an unreadable object name")
		}
		commits = append(commits, oid)
	}
	return commits, nil
}

// CommitInfo is the metadata a reader needs about one commit.
type CommitInfo struct {
	// OID is the commit's object name.
	OID string
	// Parents are its parent object names, in git's order. A root commit has
	// none; anything other than exactly one is a merge, which a linear replay
	// cannot express.
	Parents []string
	// When is the author timestamp, in UTC.
	//
	// It is descriptive only. Nothing in this codebase orders anything by it:
	// two devices' clocks disagree, and a reader that sorted by time would
	// reorder causally dependent changes on whichever machine was running fast.
	When time.Time
}

// Inspect reports one commit's parents and author time.
func (h *History) Inspect(ctx context.Context, commit string) (CommitInfo, error) {
	if err := checkObjectNames(commit); err != nil {
		return CommitInfo{}, err
	}
	// %H, then the parent list, then the author epoch. --no-show-signature keeps a
	// repository that sets log.showSignature from prepending verification output
	// to the first line; --no-patch keeps the diff out.
	out, err := h.git.run(ctx, gitCall{args: []string{
		"show", "--no-patch", "--no-show-signature", "--format=%H%n%P%n%at", commit,
	}})
	if err != nil {
		return CommitInfo{}, fmt.Errorf("vaultgit: inspect commit: %w", err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 3 {
		return CommitInfo{}, fmt.Errorf("vaultgit: unreadable commit record")
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(lines[2]), 10, 64)
	if err != nil {
		return CommitInfo{}, fmt.Errorf("vaultgit: unreadable commit timestamp")
	}
	info := CommitInfo{OID: strings.TrimSpace(lines[0]), When: time.Unix(seconds, 0).UTC()}
	info.Parents = append(info.Parents, strings.Fields(lines[1])...)
	if !validObjectName(info.OID) {
		return CommitInfo{}, fmt.Errorf("vaultgit: unreadable commit object name")
	}
	return info, nil
}

// ChangeKind is what one commit did to one path.
type ChangeKind string

const (
	// ChangeAdded is a path the commit created.
	ChangeAdded ChangeKind = "added"
	// ChangeModified is a path whose content or mode the commit changed.
	ChangeModified ChangeKind = "modified"
	// ChangeDeleted is a path the commit removed.
	ChangeDeleted ChangeKind = "deleted"
)

// FileChange is one path's transition across one commit.
type FileChange struct {
	// Path is vault-relative and slash-separated, exactly as git records it.
	Path string
	// Kind is what happened to it.
	Kind ChangeKind
	// OldMode and OldOID describe the path in the parent, empty for an addition.
	OldMode, OldOID string
	// NewMode and NewOID describe it in this commit, empty for a deletion.
	NewMode, NewOID string
}

// Changes reports what one commit did, against its single parent, or against the
// empty tree for a root commit.
//
// A merge is not a commit this reports on, and the caller must establish that
// before asking: `diff-tree` without `-m`, `-c`, or `--first-parent` emits
// nothing at all for a commit with two parents, so a merge comes back here as an
// empty change set rather than as an error. Ask Inspect for the parents first and
// refuse there — internal/vaultexport does, before it ever calls this — because a
// merge silently reported as "changed nothing" is a commit the caller would
// advance straight past.
//
// Rename detection is off. A rename reported as a pair would be a change this
// reader has to re-derive an intent from, and it already has one: the commit
// that performed the rename recorded a delete and an add, and both sides have to
// be authorized on their own terms. Detecting the pair would let a delete travel
// on the strength of an add's evidence.
func (h *History) Changes(ctx context.Context, commit string) ([]FileChange, error) {
	if err := checkObjectNames(commit); err != nil {
		return nil, err
	}
	out, err := h.git.run(ctx, gitCall{args: []string{
		"diff-tree", "-r", "-z", "--no-renames", "--no-commit-id", "--root", commit,
	}})
	if err != nil {
		return nil, fmt.Errorf("vaultgit: read commit changes: %w", err)
	}
	fields := nulFields(out)
	changes := make([]FileChange, 0, len(fields)/2)
	for i := 0; i+1 < len(fields); i += 2 {
		change, err := parseDiffTreeRecord(fields[i], fields[i+1])
		if err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}
	if len(fields)%2 != 0 {
		return nil, fmt.Errorf("vaultgit: diff-tree record is missing its path")
	}
	return changes, nil
}

// parseDiffTreeRecord parses the raw pair `:<oldmode> <newmode> <oldoid>
// <newoid> <status>` and the path that follows it.
func parseDiffTreeRecord(meta, path string) (FileChange, error) {
	head := strings.Fields(strings.TrimPrefix(meta, ":"))
	if len(head) != 5 || path == "" {
		return FileChange{}, fmt.Errorf("vaultgit: unreadable diff-tree record")
	}
	change := FileChange{
		Path:    path,
		OldMode: head[0], NewMode: head[1],
		OldOID: head[2], NewOID: head[3],
	}
	switch status := head[4]; {
	case strings.HasPrefix(status, "A"):
		change.Kind = ChangeAdded
		change.OldMode, change.OldOID = "", ""
	case strings.HasPrefix(status, "D"):
		change.Kind = ChangeDeleted
		change.NewMode, change.NewOID = "", ""
	case strings.HasPrefix(status, "M"), strings.HasPrefix(status, "T"):
		// A type change — a file replaced by a symlink, or the reverse — is a
		// modification here. It is not one a caller can necessarily act on, but
		// the mode says so plainly and refusing on the mode is the caller's call.
		change.Kind = ChangeModified
	default:
		// R and C are off, and U means an unmerged index, which a commit cannot
		// contain. Anything reaching here is a status this reader does not
		// understand, and guessing at it would misreport what a commit did.
		return FileChange{}, fmt.Errorf("vaultgit: diff-tree reported status %q, which this reader does not understand", status)
	}
	return change, nil
}

// TreeFile is one blob in a commit's tree.
type TreeFile struct {
	// Path is vault-relative and slash-separated.
	Path string
	// Mode is git's file mode, which a caller must check before treating the
	// entry as content.
	Mode string
	// OID is the blob's object name.
	OID string
	// Size is the blob's size in bytes, which lets a caller budget before it
	// reads anything.
	Size int64
}

// Tree lists every non-directory entry a commit holds, recursively.
//
// Sizes come back with the listing rather than from a second pass, because the
// only caller that wants a whole tree wants it in order to decide how much of it
// to read at once — and asking that question after reading the bytes would be
// asking it too late.
func (h *History) Tree(ctx context.Context, commit string) ([]TreeFile, error) {
	if err := checkObjectNames(commit); err != nil {
		return nil, err
	}
	out, err := h.git.run(ctx, gitCall{args: []string{
		"ls-tree", "-r", "-l", "-z", "--full-tree", commit,
	}})
	if err != nil {
		return nil, fmt.Errorf("vaultgit: read commit tree: %w", err)
	}
	records := nulFields(out)
	files := make([]TreeFile, 0, len(records))
	for _, record := range records {
		file, err := parseLsTreeLongRecord(record)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

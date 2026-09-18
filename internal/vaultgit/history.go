package vaultgit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/agent"
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
	oid, err := h.git.revParse(ctx, h.branch)
	if err != nil {
		// revParse used --verify --quiet, so an unborn branch and a genuinely
		// broken repository both arrive here. Distinguishing them by asking whether
		// the ref file exists would race; asking git whether the repository is
		// otherwise readable does not.
		if _, headErr := h.git.symbolicHead(ctx); headErr == nil {
			return "", fmt.Errorf("%w: %s", ErrUnbornBranch, h.branch)
		}
		return "", fmt.Errorf("vaultgit: resolve %s: %w", h.branch, err)
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
	for _, parent := range strings.Fields(lines[1]) {
		info.Parents = append(info.Parents, parent)
	}
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

// Object kinds EntryKinds reports, spelled the way Git spells them.
const (
	// KindBlob is a file: a regular file, an executable, or a symlink.
	KindBlob = "blob"
	// KindTree is a directory.
	KindTree = "tree"
	// KindSubmodule is a gitlink. Git names it two ways depending on whether the
	// superproject happens to have the pointed-at commit — `commit` when it can
	// read it, `submodule` when it cannot — and EntryKinds reports this one name
	// for both, because the difference is about what is in this object database
	// and the question was about what is under a name.
	KindSubmodule = "submodule"
)

// EntryKinds reports what kind of object each named path a commit's tree holds
// is, by path. A path the tree does not hold is absent from the result.
//
// It is the few-paths form of Tree, and it exists so a caller with a question
// about a handful of names does not have to list a whole vault to answer it. The
// comparison is exact and case-sensitive, which is the point: the caller asking
// is asking what is under *that* name, not under some name that looks like it on
// a folding filesystem.
//
// It reports the kind rather than a yes or no because "is this name occupied"
// and "is there still a file here" are different questions with the same answer
// only some of the time — a name that was a file and is now a directory is
// occupied and holds no file — and a caller that could only ask the first would
// have to treat the directory as the file it replaced.
//
// The lookup is `cat-file --batch-check` over `<commit>:<path>` rather than
// `ls-tree` with the paths as operands, and that is a correctness choice, not a
// performance one. ls-tree operands are *pathspecs*: ask it about `archive` and
// `archive/notes.md` together and it answers about the file only, because the
// deeper pathspec makes it descend through the directory it would otherwise have
// reported. A caller comparing a name against the names underneath it — which is
// the only reason to call this — would get silence for the very entry it asked
// about. A revision like `<commit>:<path>` resolves one exact path and has no
// such rule. It also puts every path on stdin, so a name beginning with a dash is
// data, and one process answers the whole set however large it is.
//
// A path holding a newline or a carriage return cannot be framed on that stdin —
// Git reads the first as two records and eats the second with the newline — so
// those are asked about one at a time through ls-tree instead, where the name is
// a single operand nothing parses. That form is safe precisely because it is one
// path: with nothing deeper alongside it, there is no pathspec to descend
// through. A path holding a NUL gets no answer at all: it cannot be framed on the
// stdin and cannot reach an argv either, and the only safe answer to a name this
// package cannot ask about is no answer.
//
// A commit that does not exist is an error on both roads rather than a tree that
// holds nothing — ls-tree refuses it, and the batch leads with a question about
// the commit's own tree — because "holds nothing" is the answer that lets a
// caller act. The one input that is neither is the empty path, which is dropped:
// `<commit>:` is the root tree, and answering "tree" for a name nobody asked
// about would be worse than saying nothing.
func (h *History) EntryKinds(ctx context.Context, commit string, paths []string) (map[string]string, error) {
	if err := checkObjectNames(commit); err != nil {
		return nil, err
	}
	kinds := make(map[string]string, len(paths))
	batched := make([]string, 0, len(paths))
	for _, p := range paths {
		switch {
		case p == "":
		case batchCheckSafe(p):
			batched = append(batched, p)
		default:
			kind, err := h.entryKindByListing(ctx, commit, p)
			if err != nil {
				return nil, err
			}
			if kind != "" {
				kinds[p] = kind
			}
		}
	}
	if len(batched) == 0 {
		return kinds, nil
	}
	if err := h.batchEntryKinds(ctx, commit, batched, kinds); err != nil {
		return nil, err
	}
	return kinds, nil
}

// batchEntryKinds asks one `cat-file --batch-check` about every framed path and
// records what it holds.
//
// The answers correlate with the questions by position rather than by the name
// Git echoes back, because Git echoes the name only for the paths it could not
// find — and reads that echo back out of the same bytes this package just wrote,
// which would make a name the authority on itself.
//
// The commit's own tree is the first question asked, so a commit that does not
// exist is an error rather than a tree that holds nothing. `cat-file` answers
// `missing` for every path of an absent commit and exits zero, and in the caller
// this method was written for — a check that refuses to publish a path a peer
// cannot hold beside one it already has — "holds nothing" is the answer that
// publishes. A wrong cursor must not read as a clean one.
func (h *History) batchEntryKinds(ctx context.Context, commit string, paths []string, kinds map[string]string) error {
	var stdin bytes.Buffer
	stdin.WriteString(commit)
	stdin.WriteString("^{tree}\n")
	for _, p := range paths {
		stdin.WriteString(commit)
		stdin.WriteByte(':')
		stdin.WriteString(p)
		stdin.WriteByte('\n')
	}
	out, err := h.git.run(ctx, gitCall{args: []string{"cat-file", "--batch-check"}, stdin: stdin.Bytes()})
	if err != nil {
		return fmt.Errorf("vaultgit: look up a commit's paths: %w", err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != len(paths)+1 {
		return fmt.Errorf("vaultgit: %d answers for %d paths and the commit's tree", len(lines), len(paths))
	}
	kind, err := parseBatchCheckRecord(lines[0])
	if err != nil {
		return err
	}
	if kind != KindTree {
		return fmt.Errorf("vaultgit: %s names no tree to look paths up in", commit)
	}
	for i, line := range lines[1:] {
		kind, err := parseBatchCheckRecord(line)
		if err != nil {
			return err
		}
		if kind != "" {
			kinds[paths[i]] = kind
		}
	}
	return nil
}

// parseBatchCheckRecord reads one `cat-file --batch-check` answer, reporting an
// empty kind for an object Git could not find.
//
// `<oid> SP <type> SP <size>` for what is there and a record ending in `missing`
// for what is not, plus the one two-field shape Git has: `<oid> SP submodule`,
// for a gitlink whose commit this object database does not hold. Anything else is
// a Git this package does not understand, and guessing at it would be guessing
// about whether a path collides.
func parseBatchCheckRecord(record string) (string, error) {
	fields := strings.Fields(record)
	switch {
	// Checked before the field count, because Git echoes the name it could not
	// find and a name with spaces in it makes that record any width at all. A
	// found object never ends in `missing`: its last field is a size.
	case len(fields) >= 2 && fields[len(fields)-1] == "missing":
		return "", nil
	case len(fields) == 2 && fields[1] == KindSubmodule:
		return KindSubmodule, nil
	case len(fields) == 3:
		return normalizeEntryKind(fields[1]), nil
	default:
		return "", fmt.Errorf("vaultgit: unreadable cat-file record")
	}
}

// entryKindByListing answers for one path whose name cannot go on stdin.
func (h *History) entryKindByListing(ctx context.Context, commit, path string) (string, error) {
	out, err := h.git.run(ctx, gitCall{args: []string{
		"ls-tree", "-l", "-z", "--full-tree", commit, "--", path,
	}})
	if err != nil {
		return "", fmt.Errorf("vaultgit: look up a commit's paths: %w", err)
	}
	for _, record := range nulFields(out) {
		file, err := parseLsTreeLongRecord(record)
		if err != nil {
			return "", err
		}
		if file.Path == path {
			return entryKindOfMode(file.Mode), nil
		}
	}
	return "", nil
}

// entryKindOfMode names an ls-tree mode the way cat-file would, so the two ways
// into EntryKinds cannot give one tree two different answers.
func entryKindOfMode(mode string) string {
	switch mode {
	case "040000":
		return KindTree
	case "160000":
		return KindSubmodule
	default:
		return KindBlob
	}
}

// normalizeEntryKind folds cat-file's other name for a gitlink into the one
// EntryKinds reports, for the same reason.
func normalizeEntryKind(kind string) string {
	// A `<commit>:<path>` revision resolves to a tree entry, so the only way this
	// object type is a commit is a gitlink.
	if kind == "commit" {
		return KindSubmodule
	}
	return kind
}

// batchCheckSafe reports whether `cat-file --batch-check` would read a path back
// as itself. Records are newline-framed and Git strips a carriage return before
// the newline with them, so a name holding either is a name this framing loses;
// and Git reads the framed record as a C string, so a NUL truncates it and makes
// Git answer confidently about a *shorter* path than the one asked about — the
// one failure here that is worse than an error, because it produces a kind for a
// name the tree does not hold. A leading double quote is not a problem the way it
// is for `hash-object --stdin-paths`: the record begins with the commit, so the
// quote is never the first byte of a record and nothing C-unquotes it.
func batchCheckSafe(path string) bool {
	return !strings.ContainsAny(path, "\n\r\x00")
}

// parseLsTreeLongRecord parses `<mode> SP <type> SP <oid> SP <size> TAB <path>`.
// The size column is blank-padded for alignment and is `-` for anything that is
// not a blob, which is why it is read as fields rather than by offset.
func parseLsTreeLongRecord(record string) (TreeFile, error) {
	tab := strings.IndexByte(record, '\t')
	if tab < 0 {
		return TreeFile{}, fmt.Errorf("vaultgit: unreadable ls-tree record")
	}
	head := strings.Fields(record[:tab])
	if len(head) != 4 || record[tab+1:] == "" {
		return TreeFile{}, fmt.Errorf("vaultgit: unreadable ls-tree record header")
	}
	file := TreeFile{Path: record[tab+1:], Mode: head[0], OID: head[2]}
	if head[3] != "-" {
		size, err := strconv.ParseInt(head[3], 10, 64)
		if err != nil {
			return TreeFile{}, fmt.Errorf("vaultgit: unreadable ls-tree size")
		}
		file.Size = size
	}
	return file, nil
}

// Blobs reads the exact committed bytes of the named objects.
//
// One `cat-file --batch` for the whole set, with the object names on stdin. That
// is not only about process count: object names are hex, but feeding a list
// through stdin keeps this call shaped like every other bulk read in this
// package, so no future edit is tempted to put a *path* in argv here.
//
// The whole result is held in memory, so the caller must bound what it asks for.
// Tree reports sizes for exactly this reason, and a caller replaying one commit
// at a time is bounded by that commit.
func (h *History) Blobs(ctx context.Context, oids []string) (map[string][]byte, error) {
	blobs := make(map[string][]byte, len(oids))
	if len(oids) == 0 {
		return blobs, nil
	}
	if err := checkObjectNames(oids...); err != nil {
		return nil, err
	}
	var stdin bytes.Buffer
	for _, oid := range oids {
		stdin.WriteString(oid)
		stdin.WriteByte('\n')
	}
	out, err := h.git.run(ctx, gitCall{args: []string{"cat-file", "--batch"}, stdin: stdin.Bytes()})
	if err != nil {
		return nil, fmt.Errorf("vaultgit: read blobs: %w", err)
	}
	rest := out
	for range oids {
		oid, data, remainder, err := nextBatchRecord(rest)
		if err != nil {
			return nil, err
		}
		blobs[oid] = data
		rest = remainder
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("vaultgit: cat-file returned more records than were requested")
	}
	return blobs, nil
}

// nextBatchRecord consumes one `<oid> SP blob SP <size> LF <contents> LF` record
// and returns what follows it.
//
// The size in the header is authoritative: contents are copied by length rather
// than scanned for the terminator, because a note is arbitrary bytes and may
// contain any number of newlines.
func nextBatchRecord(out []byte) (oid string, data, rest []byte, err error) {
	newline := bytes.IndexByte(out, '\n')
	if newline < 0 {
		return "", nil, nil, fmt.Errorf("vaultgit: cat-file record has no header")
	}
	head := strings.Fields(string(out[:newline]))
	if len(head) != 3 {
		// `<name> missing` is the two-field shape, and it means the object this
		// reader was told about by a tree or a diff is not in the repository.
		return "", nil, nil, fmt.Errorf("vaultgit: cat-file could not read a requested object")
	}
	if head[1] != "blob" {
		return "", nil, nil, fmt.Errorf("vaultgit: cat-file returned a %s where a blob was expected", head[1])
	}
	size, err := strconv.ParseInt(head[2], 10, 64)
	if err != nil || size < 0 {
		return "", nil, nil, fmt.Errorf("vaultgit: cat-file returned an unreadable size")
	}
	body := out[newline+1:]
	if int64(len(body)) < size+1 {
		return "", nil, nil, fmt.Errorf("vaultgit: cat-file record is truncated")
	}
	return head[0], body[:size], body[size+1:], nil
}

// BlobDigest reports a blob's SHA-256 and byte length without holding it in
// memory, which is how a reader asks what a path *held* when it does not want
// the bytes — checking a deletion against the record that authorized it, most of
// all, where the content is on its way out and only its identity matters.
func (h *History) BlobDigest(ctx context.Context, oid string) (string, int64, error) {
	if err := checkObjectNames(oid); err != nil {
		return "", 0, err
	}
	sum, size, err := h.git.blobDigest(ctx, oid)
	if err != nil {
		return "", 0, fmt.Errorf("vaultgit: hash blob: %w", err)
	}
	return sum, size, nil
}

// checkObjectNames refuses anything that is not a full hex object name before it
// can reach argv.
//
// Every object name this package hands to git came from git, so this should
// never fire. It is here because the alternative is a caller-supplied string —
// a cursor read back off a disk, most of all — reaching `rev-list` as `--all`
// or as a `..` range of its own devising, and a corrupt state file must fail as
// a corrupt state file rather than as a surprisingly broad query.
func checkObjectNames(names ...string) error {
	for _, name := range names {
		if !validObjectName(name) {
			return fmt.Errorf("vaultgit: %q is not a Git object name", name)
		}
	}
	return nil
}

// validObjectName reports whether name is a full lowercase hex object name, of
// either the SHA-1 or the SHA-256 length. Abbreviations are refused: an
// abbreviation is ambiguous by construction, and a cursor that resolved to a
// different commit after the repository grew would be a silent rewrite.
func validObjectName(name string) bool {
	if len(name) != 40 && len(name) != 64 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

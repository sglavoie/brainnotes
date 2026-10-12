package vaultgit

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
)

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

package vaultgit

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
)

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

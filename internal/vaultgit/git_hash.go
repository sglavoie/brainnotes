package vaultgit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
)

// hashWorktreeContent reports the blob object name Git would record for each
// path's current working-tree bytes.
//
// It is what makes an index readback mean something. `ls-files --stage` says
// which object an entry names; only re-hashing the file says whether that object
// is the content the snapshot was asked to record. Git does the hashing rather
// than this package, because the object name depends on the repository's own
// attributes and clean filters — computing it here would be a second, subtly
// different implementation whose disagreement with Git would read as corruption.
//
// The batch form is `--stdin-paths`, which reads newline-separated paths and
// treats a leading double quote as the start of a C-quoted name. A vault holds
// note names that break both of those, so any path that does is hashed on its
// own with `--path=`, where the name is one argv element and nothing parses it.
func (g *gitExec) hashWorktreeContent(ctx context.Context, paths []string) (map[string]string, error) {
	out := make(map[string]string, len(paths))
	batched := make([]string, 0, len(paths))
	for _, path := range paths {
		if stdinPathsSafe(path) {
			batched = append(batched, path)
			continue
		}
		oid, err := g.hashOneWorktreePath(ctx, path)
		if err != nil {
			return nil, err
		}
		out[path] = oid
	}
	if len(batched) == 0 {
		return out, nil
	}
	var stdin bytes.Buffer
	for _, path := range batched {
		stdin.WriteString(path)
		stdin.WriteByte('\n')
	}
	res, err := g.run(ctx, gitCall{args: []string{"hash-object", "--stdin-paths"}, stdin: stdin.Bytes()})
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(res), "\n"), "\n")
	if len(lines) != len(batched) {
		return nil, fmt.Errorf("git hash-object: %d object names for %d paths", len(lines), len(batched))
	}
	for i, line := range lines {
		oid := strings.TrimSpace(line)
		if oid == "" {
			return nil, errors.New("git hash-object: empty object name")
		}
		out[batched[i]] = oid
	}
	return out, nil
}

// stdinPathsSafe reports whether `hash-object --stdin-paths` would read a path
// back as itself. A newline ends the record, a carriage return may be stripped
// with it, and a leading double quote makes Git C-unquote the rest of the line.
func stdinPathsSafe(path string) bool {
	return !strings.ContainsAny(path, "\n\r") && !strings.HasPrefix(path, `"`)
}

// hashOneWorktreePath hashes a single path, naming it twice: `--path=` supplies
// the name the attributes and filters are chosen by, and the operand after `--`
// is the file to read.
func (g *gitExec) hashOneWorktreePath(ctx context.Context, path string) (string, error) {
	out, err := g.run(ctx, gitCall{args: []string{"hash-object", "--path=" + path, "--", path}})
	if err != nil {
		return "", err
	}
	oid := strings.TrimSpace(string(out))
	if oid == "" {
		return "", errors.New("git hash-object: empty object name")
	}
	return oid, nil
}

// hashBlob reports the object name of content that is not read from a file.
//
// It exists for symbolic links. Git records one as a blob holding the link
// target, and `hash-object` on the link path would follow it and hash whatever
// it points at instead. No filter applies to a symlink blob, so the content is
// handed over on stdin with no `--path`.
func (g *gitExec) hashBlob(ctx context.Context, content []byte) (string, error) {
	out, err := g.run(ctx, gitCall{args: []string{"hash-object", "--stdin"}, stdin: content})
	if err != nil {
		return "", err
	}
	oid := strings.TrimSpace(string(out))
	if oid == "" {
		return "", errors.New("git hash-object: empty object name")
	}
	return oid, nil
}

// blobDigest reports the sha256 and byte length of a blob's contents.
//
// It is sha256 rather than the object name on purpose: the journal records a
// sha256 of the file's bytes, and a deletion is authorized by comparing the two.
// A git object name is a sha1 over a header plus the content and cannot be
// compared to it, so the blob has to be re-read and re-hashed to prove the
// content the deletion would drop is the content the journal snapshotted.
func (g *gitExec) blobDigest(ctx context.Context, oid string) (string, int64, error) {
	hasher := newCountingHasher()
	if _, err := g.run(ctx, gitCall{args: []string{"cat-file", "blob", oid}, stdout: hasher}); err != nil {
		return "", 0, err
	}
	return hasher.sum(), hasher.size, nil
}

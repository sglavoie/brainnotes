package vaultgit

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// treeEntry is one path's state in a tree or in an index.
type treeEntry struct {
	mode string
	oid  string
	path string
}

// revParse resolves a single revision to an object name.
func (g *gitExec) revParse(ctx context.Context, rev string) (string, error) {
	out, err := g.run(ctx, gitCall{args: []string{"rev-parse", "--verify", "--quiet", rev}})
	if err != nil {
		return "", err
	}
	oid := strings.TrimSpace(string(out))
	if oid == "" {
		return "", fmt.Errorf("git rev-parse: %q does not resolve", rev)
	}
	return oid, nil
}

// isAncestor reports whether commit is reachable from tip.
//
// This is how the engine proves, after a restart, that an interrupted snapshot
// actually landed. Reachability rather than equality is the right test: another
// writer committing on top of the snapshot does not un-land it, and demanding
// `tip == commit` would turn every such case into a spurious block.
//
// `merge-base --is-ancestor` answers through its exit status rather than its
// output — zero for yes, one for no, anything else (a missing object, a corrupt
// repository) for a genuine failure. Keeping those three apart is load-bearing:
// reporting an unreadable answer as "no" merely keeps an intent alive, which is
// safe, but reporting it as "yes" would retire the only durable record that a
// snapshot was never proved to have landed.
func (g *gitExec) isAncestor(ctx context.Context, commit, tip string) (bool, error) {
	if commit == "" || tip == "" {
		return false, errors.New("vaultgit: merge-base requires two object names")
	}
	if _, err := g.run(ctx, gitCall{args: []string{"merge-base", "--is-ancestor", commit, tip}}); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// symbolicHead reports the full ref HEAD points at, or an empty string when
// HEAD is detached.
func (g *gitExec) symbolicHead(ctx context.Context) (string, error) {
	out, err := g.run(ctx, gitCall{args: []string{"symbolic-ref", "--quiet", "HEAD"}})
	if err != nil {
		var gerr *gitError
		// A detached HEAD exits non-zero with no output rather than failing.
		if errors.As(err, &gerr) && strings.TrimSpace(gerr.detail) == "" {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// readTree seeds an index file from a commit or tree, discarding whatever it
// held. Every classification and every staging pass starts here, which is what
// makes the private index temporary in the sense that matters: it carries no
// state from a previous run, so a snapshot that failed part-way cannot leave
// staged entries that the next classification would misread as the user's.
func (g *gitExec) readTree(ctx context.Context, indexFile, rev string) error {
	_, err := g.run(ctx, gitCall{args: []string{"read-tree", rev}, indexFile: indexFile})
	return err
}

// statusEntry is one line of `git status --porcelain=v1 -z`.
type statusEntry struct {
	// x is the index-versus-HEAD column, y the worktree-versus-index column.
	x, y byte
	path string
	// origin is the rename source, set only when git reported one.
	origin string
}

// status reports the working tree against indexFile. The index is always seeded
// from HEAD first, so the X column is blank for everything and every real change
// shows up in Y — a rename is reported as a delete plus an untracked add rather
// than as a pair, which is exactly the conservative reading this engine wants:
// the delete side has to be proven on its own.
//
// A fresh HEAD-seeded index also carries no stat cache, so git compares content
// rather than trusting mtimes. That costs a full hash of the tracked tree on
// every pass and buys determinism: the same working tree always classifies the
// same way, and a note touched twice inside one filesystem timestamp tick can
// never be reported as unchanged.
func (g *gitExec) status(ctx context.Context, indexFile string) ([]statusEntry, error) {
	out, err := g.run(ctx, gitCall{
		args: []string{
			"status", "--porcelain=v1", "-z",
			"--untracked-files=all", "--no-renames",
			"--ignore-submodules=all",
		},
		indexFile: indexFile,
	})
	if err != nil {
		return nil, err
	}
	fields := nulFields(out)
	entries := make([]statusEntry, 0, len(fields))
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if len(field) < 4 {
			return nil, fmt.Errorf("git status: unreadable record %q", field)
		}
		entry := statusEntry{x: field[0], y: field[1], path: field[3:]}
		// `--no-renames` should prevent this, but the format reserves a second
		// NUL-delimited field for a rename source and a future git could still
		// emit one. Consuming it keeps the parser aligned; the caller refuses the
		// entry as ambiguous.
		if entry.x == 'R' || entry.x == 'C' {
			if i+1 >= len(fields) {
				return nil, fmt.Errorf("git status: rename record missing its source")
			}
			i++
			entry.origin = fields[i]
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// lsTree reports the entries a commit holds for the given paths. Paths go after
// `--`, so a name beginning with a dash is a path rather than an option.
func (g *gitExec) lsTree(ctx context.Context, rev string, paths []string) (map[string]treeEntry, error) {
	entries := make(map[string]treeEntry, len(paths))
	if len(paths) == 0 {
		return entries, nil
	}
	// ls-tree takes its pathspec on the command line, so it is one of the two
	// places a note name reaches argv. It is safe there: the name follows `--`
	// so a leading dash is a path, and GIT_LITERAL_PATHSPECS makes a name
	// containing glob characters select itself rather than a pattern.
	err := eachPathChunk(paths, func(chunk []string) error {
		args := append([]string{"ls-tree", "-r", "-z", "--full-tree", rev, "--"}, chunk...)
		out, err := g.run(ctx, gitCall{args: args})
		if err != nil {
			return err
		}
		for _, record := range nulFields(out) {
			entry, err := parseLsTreeRecord(record)
			if err != nil {
				return err
			}
			entries[entry.path] = entry
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// eachPathChunk feeds paths to fn in batches small enough that a vault whose
// whole tree changed at once cannot exceed the platform argument limit.
func eachPathChunk(paths []string, fn func([]string) error) error {
	const chunk = 256
	for start := 0; start < len(paths); start += chunk {
		end := start + chunk
		if end > len(paths) {
			end = len(paths)
		}
		if err := fn(paths[start:end]); err != nil {
			return err
		}
	}
	return nil
}

// parseLsTreeRecord parses `<mode> SP <type> SP <oid> TAB <path>`.
func parseLsTreeRecord(record string) (treeEntry, error) {
	tab := strings.IndexByte(record, '\t')
	if tab < 0 {
		return treeEntry{}, fmt.Errorf("git ls-tree: unreadable record")
	}
	head := strings.Fields(record[:tab])
	if len(head) != 3 {
		return treeEntry{}, fmt.Errorf("git ls-tree: unreadable record header")
	}
	return treeEntry{mode: head[0], oid: head[2], path: record[tab+1:]}, nil
}

// lsFilesStaged reports what an index holds for the given paths, which is how
// the engine decides whether the user staged something it must not clobber.
func (g *gitExec) lsFilesStaged(ctx context.Context, indexFile string, paths []string) (map[string]treeEntry, error) {
	entries := make(map[string]treeEntry, len(paths))
	if len(paths) == 0 {
		return entries, nil
	}
	err := eachPathChunk(paths, func(chunk []string) error {
		args := append([]string{"ls-files", "--stage", "-z", "--"}, chunk...)
		out, err := g.run(ctx, gitCall{args: args, indexFile: indexFile})
		if err != nil {
			return err
		}
		for _, record := range nulFields(out) {
			tab := strings.IndexByte(record, '\t')
			if tab < 0 {
				return fmt.Errorf("git ls-files: unreadable record")
			}
			head := strings.Fields(record[:tab])
			if len(head) != 3 {
				return fmt.Errorf("git ls-files: unreadable record header")
			}
			if stage, convErr := strconv.Atoi(head[2]); convErr != nil || stage != 0 {
				// A non-zero stage is an unmerged path. Reporting it as an ordinary
				// entry would let the engine believe the index agrees with HEAD
				// while it is mid-conflict, so it is surfaced as an error instead.
				return fmt.Errorf("git ls-files: unmerged entry at stage %s", head[2])
			}
			entries[record[tab+1:]] = treeEntry{mode: head[0], oid: head[1], path: record[tab+1:]}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// configBool reads a boolean from the repository configuration, falling back
// when the key is unset or unreadable. Local plumbing runs with a null global
// config, so the answer is the repository's own.
func (g *gitExec) configBool(ctx context.Context, key string, fallback bool) bool {
	out, err := g.run(ctx, gitCall{args: []string{"config", "--type=bool", "--get", key}})
	if err != nil {
		return fallback
	}
	switch strings.TrimSpace(string(out)) {
	case "true":
		return true
	case "false":
		return false
	default:
		return fallback
	}
}

// remoteConfigured reports whether a remote of this name exists.
func (g *gitExec) remoteConfigured(ctx context.Context, name string) (bool, error) {
	out, err := g.run(ctx, gitCall{args: []string{"remote"}})
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == name {
			return true, nil
		}
	}
	return false, nil
}

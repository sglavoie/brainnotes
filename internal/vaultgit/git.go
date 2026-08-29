package vaultgit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/agent"
)

const (
	// maxStderrBytes bounds the diagnostic kept from a failed git invocation.
	maxStderrBytes = 4096
	// commandWaitDelay bounds how long Wait blocks on inherited pipes after the
	// process itself is gone. It matters most for push, whose ssh child can
	// outlive the git process holding the pipe open.
	commandWaitDelay = time.Second
	// nullOID is the all-zero object name. `git update-ref <ref> <new> <old>`
	// treats it as "the ref must not exist", which is how a create is expressed
	// as a compare-and-swap.
	nullOID = "0000000000000000000000000000000000000000"
)

// gitExec runs the git executable against one working tree. It is the only
// place in this package that starts a process, and it never uses a shell: argv
// is always a slice, and any value that could contain a note name is passed
// NUL-delimited on stdin rather than as an argument.
type gitExec struct {
	// exe is the absolute path to git, resolved once at Open.
	exe string
	// dir is the working tree git runs in. Every relative path in every command
	// is interpreted against it.
	dir string
}

// identity is the author and committer stamped on engine commits.
type identity struct {
	name  string
	email string
}

// gitCall describes one invocation.
type gitCall struct {
	// args are the arguments after the executable, argv-style.
	args []string
	// stdin is written to the child. Path lists go here, never in args.
	stdin []byte
	// indexFile, when set, becomes GIT_INDEX_FILE, so the command reads and
	// writes a private index instead of the user's.
	indexFile string
	// when, when non-zero, stamps GIT_AUTHOR_DATE and GIT_COMMITTER_DATE.
	when time.Time
	// author, when set, stamps the author and committer identity.
	author *identity
	// remote widens the environment for credentials. See buildEnv.
	remote bool
	// stdout, when set, receives the child's standard output instead of it being
	// buffered. It is how a blob is hashed without holding it in memory.
	stdout io.Writer
}

// plumbingPassthrough is the environment this package forwards to a local git
// invocation. Everything else is dropped, so a stray GIT_* variable in the
// user's shell cannot redirect a snapshot to another repository or another
// index. HOME is forwarded because git wants one, not because its config is
// read: buildEnv points GIT_CONFIG_GLOBAL at the null device for local work.
var plumbingPassthrough = []string{
	"PATH", "HOME", "TMPDIR",
	// Windows needs these to start a process at all.
	"SYSTEMROOT", "USERPROFILE", "PATHEXT", "COMSPEC", "TEMP", "TMP",
}

// remotePassthrough adds what an authenticated push needs. A push is the one
// operation that legitimately depends on the user's environment: the remote is
// reached over SSH, so the agent socket has to survive, and the user's global
// config may hold `core.sshCommand` or a `url.*.insteadOf` rewrite that the
// repository config does not repeat.
var remotePassthrough = []string{
	"SSH_AUTH_SOCK", "SSH_AGENT_PID", "USER", "LOGNAME",
	"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME",
	"APPDATA", "LOCALAPPDATA", "PROGRAMDATA",
}

// buildEnv assembles the child environment from an allowlist plus forced
// values. Two profiles exist and the difference is deliberate.
//
// Local plumbing is hermetic: GIT_CONFIG_NOSYSTEM and a null GIT_CONFIG_GLOBAL
// mean the user's `~/.gitconfig` cannot change how the engine classifies or
// stages anything. That matters concretely here — this user's global
// core.excludesFile matches `BRAIN[A-Z]*` under core.ignoreCase, and a global
// setting that changes what `git status` reports would change what the engine
// believes is pending. The repository's own config still applies, which is where
// core.ignoreCase and the remote URL live.
//
// A remote call keeps the user's global config for the reasons above. It gets
// GIT_TERMINAL_PROMPT=0 either way, so a missing credential is an immediate
// failure with a retry record rather than a process blocked forever on a prompt
// nobody will ever see — this runs under a LaunchAgent.
func buildEnv(c gitCall) []string {
	names := plumbingPassthrough
	if c.remote {
		names = append(append([]string{}, plumbingPassthrough...), remotePassthrough...)
	}
	env := make([]string, 0, len(names)+16)
	for _, name := range names {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	env = append(env,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		// The engine always names its own index, so no command here has a reason
		// to take the repository's index.lock opportunistically. Refusing the
		// optional lock is what keeps a classification pass from contending with
		// a `git` the user is running by hand.
		"GIT_OPTIONAL_LOCKS=0",
		// Every pathspec this package passes is an exact path taken from git's
		// own output or from a journal record, never a pattern. Without this a
		// note named `Draft [2].md` or `TODO*.md` would be read as a glob and
		// could select — or fail to select — a different note entirely.
		"GIT_LITERAL_PATHSPECS=1",
		"GIT_PAGER=cat",
		"PAGER=cat",
		"LC_ALL=C",
		"LANG=C",
		"TZ=UTC",
	)
	if !c.remote {
		env = append(env, "GIT_CONFIG_GLOBAL="+os.DevNull)
	}
	if c.indexFile != "" {
		env = append(env, "GIT_INDEX_FILE="+c.indexFile)
	}
	if c.author != nil {
		env = append(env,
			"GIT_AUTHOR_NAME="+c.author.name,
			"GIT_AUTHOR_EMAIL="+c.author.email,
			"GIT_COMMITTER_NAME="+c.author.name,
			"GIT_COMMITTER_EMAIL="+c.author.email,
		)
	}
	if !c.when.IsZero() {
		stamp := c.when.UTC().Format("2006-01-02T15:04:05Z")
		env = append(env, "GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
	}
	return env
}

// run invokes git and returns its standard output. A failure is wrapped with a
// sanitized, bounded excerpt of stderr — the same single-line boundary
// `internal/agent` applies to every other subprocess in this codebase — so a
// path or a git hint can appear in a log but a control character, a multi-line
// dump, or an unbounded blob cannot.
func (g *gitExec) run(ctx context.Context, c gitCall) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("vaultgit: context is required")
	}
	if g == nil || g.exe == "" {
		return nil, errors.New("vaultgit: git executable is not resolved")
	}
	for _, arg := range c.args {
		if strings.IndexByte(arg, 0) >= 0 {
			return nil, errors.New("vaultgit: git argument contains a NUL byte")
		}
	}

	cmd := exec.CommandContext(ctx, g.exe, c.args...)
	cmd.Dir = g.dir
	cmd.Env = buildEnv(c)
	configureCommandCancellation(cmd)
	cmd.WaitDelay = commandWaitDelay
	cmd.Stdin = bytes.NewReader(c.stdin)

	var buffered bytes.Buffer
	if c.stdout != nil {
		cmd.Stdout = c.stdout
	} else {
		cmd.Stdout = &buffered
	}
	stderr := &boundedBuffer{limit: maxStderrBytes}
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("vaultgit: git %s canceled: %w", commandName(c.args), ctxErr)
		}
		detail := agent.SanitizedDiagnostic(errors.New(stderr.String()))
		if detail == "" {
			detail = agent.SanitizedDiagnostic(err)
		}
		return nil, &gitError{name: commandName(c.args), detail: detail, cause: err}
	}
	return buffered.Bytes(), nil
}

// commandName reports the git subcommand for diagnostics. Only the leading
// non-flag token is used: the rest of argv can contain a path.
func commandName(args []string) string {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return arg
		}
	}
	return "git"
}

type gitError struct {
	name   string
	detail string
	cause  error
}

func (e *gitError) Error() string {
	if e.detail == "" {
		return fmt.Sprintf("git %s failed: %v", e.name, e.cause)
	}
	return fmt.Sprintf("git %s failed: %s", e.name, e.detail)
}

func (e *gitError) Unwrap() error { return e.cause }

// boundedBuffer keeps at most limit bytes and records that it dropped the rest,
// so a git command that fails noisily cannot consume memory proportional to its
// own complaint.
type boundedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	if remaining := b.limit - b.buf.Len(); remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = b.buf.Write(p[:remaining])
		if remaining < len(p) {
			b.truncated = true
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return written, nil
}

func (b *boundedBuffer) String() string {
	if b.truncated {
		return b.buf.String() + " …"
	}
	return b.buf.String()
}

// countingHasher hashes a stream and counts it, so a blob's sha256 and size are
// obtained in one pass without the blob ever being held in memory. A vault
// holds attachments; some of them are tens of megabytes.
type countingHasher struct {
	h    hash.Hash
	size int64
}

func newCountingHasher() *countingHasher { return &countingHasher{h: sha256.New()} }

func (c *countingHasher) Write(p []byte) (int, error) {
	c.size += int64(len(p))
	return c.h.Write(p)
}

func (c *countingHasher) sum() string { return hex.EncodeToString(c.h.Sum(nil)) }

// nulFields splits NUL-delimited output. A trailing empty field from the final
// delimiter is dropped; interior empty fields are impossible because git never
// emits an empty path.
func nulFields(out []byte) []string {
	fields := strings.Split(string(out), "\x00")
	for len(fields) > 0 && fields[len(fields)-1] == "" {
		fields = fields[:len(fields)-1]
	}
	return fields
}

// nulList encodes paths for a command that reads them from stdin. This is the
// only safe way to hand a note name to git: `--stdin -z` makes a leading dash,
// a newline, a comma, and a non-UTF-8 byte sequence all data. The comma matters
// specifically — `update-index --cacheinfo <mode>,<oid>,<path>` splits on
// commas and silently records the wrong path for a note whose name has one.
func nulList(paths []string) []byte {
	var buf bytes.Buffer
	for _, p := range paths {
		buf.WriteString(p)
		buf.WriteByte(0)
	}
	return buf.Bytes()
}

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

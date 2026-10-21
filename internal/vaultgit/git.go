package vaultgit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/brainnotes/internal/agent"
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
	env = append(env, engineConfigEnv()...)
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

// engineConfig is configuration the engine forces on every git invocation,
// local or remote, through the GIT_CONFIG_COUNT channel. Configuration passed
// that way outranks the repository's own file, so a vault whose .git/config
// weakens it cannot opt the engine out.
//
// core.fsync is the reason the list exists. Git's default
// (`committed,-loose-object`) leaves the loose objects that hash-object,
// write-tree and commit-tree create unsynced, while update-ref then moves the
// branch, the ref directory is synced, and the recovery intent is retired. A
// power loss in that window would leave a durable branch naming an object that
// never reached the disk. `committed` restores loose objects; `reference` is
// added because `committed` is only `objects`, and syncing the ref's directory
// entry does not flush the ref file's own bytes. core.fsyncMethod keeps git's
// default.
var engineConfig = [][2]string{
	{"core.fsync", "committed,reference"},
}

// engineConfigEnv renders engineConfig as GIT_CONFIG_COUNT/KEY_n/VALUE_n. The
// parent's GIT_CONFIG_* variables are never forwarded (both passthrough lists
// are allowlists), so numbering from zero cannot clobber an inherited entry.
func engineConfigEnv() []string {
	env := make([]string, 0, 1+2*len(engineConfig))
	env = append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(len(engineConfig)))
	for i, kv := range engineConfig {
		n := strconv.Itoa(i)
		env = append(env, "GIT_CONFIG_KEY_"+n+"="+kv[0], "GIT_CONFIG_VALUE_"+n+"="+kv[1])
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

package vaultfixture

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sglavoie/brainnotes/internal/config"
)

// This file is the verification-time counterpart of the production storage
// guard in internal/vault: that one refuses a mutation to storage `bn` must not
// write to, this one refuses a *verification* run against a vault a session must
// not touch.
//
// It exists because of a real incident. A refactoring session verified `bn
// rename-key` by running the built binary after a plain `cd` into a scratch
// directory, and $BNOTES_VAULT was set in the ambient shell to the developer's
// real vault. `cd` does not override the environment, so the equivalence matrix
// renamed a frontmatter key in 214 real notes. The notes were recovered from the
// journal, but two accidental commits and two journal entries had to be cleaned
// up afterwards by hand.
//
// The rule enforced here is therefore the rule that incident violated: a
// mutating verification may only ever resolve an isolated temporary vault that
// the run itself created and named, and never the real vault.
//
// The manual half of the same rule is scripts/vault_sandbox.py, which is the
// only sanctioned way to run a mutating `bn` command by hand during
// verification. Both implement the same three refusals. Keeping a second Go
// copy is what this package removed: internal/cli's fixtures and the browser
// harness now both refuse through the functions below.

// EnvVault is the environment variable that overrides a working directory when
// `bn` resolves its vault. Spelled here rather than imported because
// internal/config keeps it unexported; TestAmbientEnvIsWhatBnResolves pins the
// two spellings together so a rename cannot make this guard read a dead name.
const EnvVault = "BNOTES_VAULT"

// realVaultDirNames are directory names that identify a real vault wherever
// they appear in a resolved path. The temp-directory rule below already refuses
// every path outside the system temporary directory; this list is the
// belt-and-braces half, so a real vault reached through a symlink or a bind
// mount under the temp directory is still refused, and so the refusal message
// names the vault that was actually damaged.
var realVaultDirNames = []string{"BrainNotesVault"}

// IsolationError reports why resolved is not the isolated temporary vault the
// caller asserted, or nil when it is.
//
// It fails closed on every input it cannot prove safe: an unasserted sandbox and
// an unresolvable vault are both refusals, because "no evidence of isolation" is
// what the incident looked like from inside the process.
func IsolationError(resolved, sandbox string) error {
	if strings.TrimSpace(sandbox) == "" {
		return errors.New("no isolated vault was asserted; a mutating verification must name the sandbox it is allowed to write to")
	}
	if strings.TrimSpace(resolved) == "" {
		return fmt.Errorf("no vault resolved, so a mutating verification cannot be proved to target the sandbox %q", sandbox)
	}
	resolvedPath, err := NormalizePath(resolved)
	if err != nil {
		return fmt.Errorf("resolve %q: %w", resolved, err)
	}
	sandboxPath, err := NormalizePath(sandbox)
	if err != nil {
		return fmt.Errorf("resolve sandbox %q: %w", sandbox, err)
	}
	if err := refuseRealVault(resolvedPath); err != nil {
		return err
	}
	if err := refuseOutsideTempDir(resolvedPath, sandboxPath); err != nil {
		return err
	}
	return refuseOutsideSandbox(resolvedPath, sandboxPath)
}

// refuseRealVault refuses a path that names a real vault at any depth.
func refuseRealVault(resolved string) error {
	for _, name := range realVaultDirNames {
		if pathHasElement(resolved, name) {
			return fmt.Errorf(
				"refusing to verify against %q: it is the real vault %q, which no test or manual verification may write to",
				resolved, name)
		}
	}
	return nil
}

// refuseOutsideTempDir refuses a vault or a sandbox that is not throwaway
// storage. An isolated vault is one the run created and will delete; anything
// outside the system temporary directory outlives the verification and is
// somebody's data.
func refuseOutsideTempDir(resolved, sandbox string) error {
	tempDir, err := NormalizePath(os.TempDir())
	if err != nil {
		return fmt.Errorf("resolve the system temporary directory: %w", err)
	}
	// Ordered rather than a map, so the same input always produces the same
	// refusal message.
	for _, candidate := range []struct{ label, path string }{
		{"asserted sandbox", sandbox},
		{"resolved vault", resolved},
	} {
		if !pathWithin(candidate.path, tempDir) {
			return fmt.Errorf(
				"refusing to verify against %q: the %s is outside the system temporary directory %q, so it is not an isolated vault",
				candidate.path, candidate.label, tempDir)
		}
	}
	return nil
}

// refuseOutsideSandbox refuses a vault that is isolated but is not the one the
// caller asserted. A verification that writes to a temporary vault it did not
// build proves nothing about the command under test.
func refuseOutsideSandbox(resolved, sandbox string) error {
	if !pathWithin(resolved, sandbox) {
		return fmt.Errorf(
			"refusing to verify against %q: it is outside the asserted isolated vault %q", resolved, sandbox)
	}
	return nil
}

// NormalizePath makes a path comparable: absolute and symlink-free.
//
// It resolves the longest existing ancestor and re-attaches the rest, because
// EvalSymlinks fails outright on a path whose leaf does not exist yet and this
// guard must reach a verdict *before* its caller creates that leaf. Falling back
// to a lexical clean there would be wrong rather than merely approximate: macOS
// reaches the temp tree through /var -> /private/var, so an unresolved
// /var/folders/... candidate compares as outside a /private/var/folders/...
// temporary root, and every legitimate not-yet-created sandbox is refused.
func NormalizePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current, remainder := abs, ""
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			return filepath.Join(resolved, remainder), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			// Not even the root resolved; nothing better than lexical is available.
			return filepath.Clean(abs), nil
		}
		remainder = filepath.Join(filepath.Base(current), remainder)
		current = parent
	}
}

// pathHasElement reports whether any path segment of p equals name.
func pathHasElement(p, name string) bool {
	for _, element := range strings.Split(filepath.ToSlash(p), "/") {
		if element == name {
			return true
		}
	}
	return false
}

// pathWithin reports whether child is dir itself or lives under it.
func pathWithin(child, dir string) bool {
	rel, err := filepath.Rel(dir, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// AmbientVaultRoot reports the vault root `bn` would resolve right now from
// process state alone, with no --vault flag: $BNOTES_VAULT if it is set, else
// the nearest ancestor of the working directory holding brainnotes.toml. It
// returns "" when nothing resolves.
//
// It reads process state deliberately. config.Load takes the same inputs as
// injectable options and also decodes the file, and a guard that decoded would
// mistake an intentionally invalid fixture config for an isolation failure. The
// two resolutions are pinned together by TestAmbientEnvIsWhatBnResolves.
func AmbientVaultRoot() string {
	if env := strings.TrimSpace(os.Getenv(EnvVault)); env != "" {
		return env
	}
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, config.FileName)); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

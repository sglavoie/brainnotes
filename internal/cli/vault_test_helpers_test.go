package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
)

// testPropertyDefinitions declares the list and wikilink properties the CLI
// fixtures exercise. Nothing is injected at load time, so a fixture that
// mutates one of these keys must define it explicitly.
const testPropertyDefinitions = `[frontmatter.properties.categories]
type  = "list"
items = "wikilink"

[frontmatter.properties.tags]
type  = "list"
items = "string"

[frontmatter.properties.parent]
type = "wikilink"
`

// markVaultRoot writes body as root's brainnotes.toml, which is what makes root
// a vault root. It returns the config path so callers can rewrite it later.
//
// Every fixture in this package becomes a vault here, so this is where the
// isolation guard belongs: a fixture root that is not throwaway storage is
// refused before it can be written to. See vault_isolation_test.go.
func markVaultRoot(t *testing.T, root, body string) string {
	t.Helper()
	requireSandboxVaultRoot(t, root)
	return markVaultRootUnguarded(t, root, body)
}

// markVaultRootUnguarded is markVaultRoot without the isolation check. It exists
// for the guard's own regression test, which has to build a fixture that
// deliberately looks like the real vault; nothing else may use it.
func markVaultRootUnguarded(t *testing.T, root, body string) string {
	t.Helper()
	path := filepath.Join(root, config.FileName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", config.FileName, err)
	}
	return path
}

// useVaultRoot marks root as a vault root and points $BNOTES_VAULT at it so
// commands resolve it without a --vault flag.
//
// Pointing the environment at a fixture is the step that makes an ambient vault
// reachable, so the full isolation check runs here: after this returns, the
// vault this process would resolve is provably the fixture and not somebody's
// real notes.
func useVaultRoot(t *testing.T, root, body string) string {
	t.Helper()
	path := markVaultRoot(t, root, body)
	t.Setenv(bnotesVaultEnv, root)
	requireIsolatedVault(t, root)
	return path
}

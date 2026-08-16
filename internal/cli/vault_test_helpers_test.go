package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
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
func markVaultRoot(t *testing.T, root, body string) string {
	t.Helper()
	path := filepath.Join(root, config.FileName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", config.FileName, err)
	}
	return path
}

// useVaultRoot marks root as a vault root and points $BNOTES_VAULT at it so
// commands resolve it without a --vault flag.
func useVaultRoot(t *testing.T, root, body string) string {
	t.Helper()
	path := markVaultRoot(t, root, body)
	t.Setenv("BNOTES_VAULT", root)
	return path
}

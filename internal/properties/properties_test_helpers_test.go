package properties

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// testVault writes files (relative path -> contents) into a fresh temp root and
// returns the root plus every note loaded from it, in the order the files were
// named.
func testVault(t *testing.T, files map[string]string) (string, []*vault.Note) {
	t.Helper()

	root := t.TempDir()
	notes := make([]*vault.Note, 0, len(files))
	for rel, contents := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		note := &vault.Note{Path: abs}
		if err := note.Load(); err != nil {
			// A note with unparseable frontmatter is a legitimate fixture: Build
			// must skip it rather than fail, so keep the handle unloaded.
			notes = append(notes, note)
			continue
		}
		notes = append(notes, note)
	}
	return root, notes
}

func frontmatterConfig(props map[string]config.PropertyDefinition) *config.FrontmatterConfig {
	return &config.FrontmatterConfig{Properties: props}
}

func propertyByName(t *testing.T, index *Index, name string) *Property {
	t.Helper()

	prop, ok := index.Property(name)
	if !ok {
		t.Fatalf("property %q not indexed; have %v", name, index.Names())
	}
	return prop
}

func valueTexts(values []ValueCount) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value.Value.Text)
	}
	return out
}

func rowPaths(rows []NoteRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.RelPath)
	}
	return out
}

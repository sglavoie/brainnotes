package frontmatter

import (
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"os"
	"path/filepath"
	"testing"
)

func defaultCfg() *config.FrontmatterConfig {
	return &config.FrontmatterConfig{
		WikilinkProperties: []string{"categories", "org", "parent", "area"},
		ListProperties:     []string{"categories", "tags", "topics", "org", "type"},
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	p := filepath.Join("testdata", name)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return b
}

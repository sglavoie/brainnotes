package frontmatter

import (
	"github.com/sglavoie/brainnotes/internal/config"
	"os"
	"path/filepath"
	"testing"
)

func defaultCfg() *config.FrontmatterConfig {
	return &config.FrontmatterConfig{
		Properties: map[string]config.PropertyDefinition{
			"categories": {Type: config.KindList, Items: config.KindWikilink},
			"org":        {Type: config.KindList, Items: config.KindWikilink},
			"tags":       {Type: config.KindList, Items: config.KindString},
			"topics":     {Type: config.KindList, Items: config.KindString},
			"type":       {Type: config.KindList, Items: config.KindString},
			"parent":     {Type: config.KindWikilink},
			"area":       {Type: config.KindWikilink},
		},
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

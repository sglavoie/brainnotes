package query

import (
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func TestGraphTitles(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		_, notes := newVault(t, map[string]string{
			"Alpha.md": "---\ntitle: Alpha\n---\n",
			"Beta.md":  "---\ntitle: Beta\n---\n",
		})
		var all []*vault.Note
		for _, n := range notes {
			all = append(all, n)
		}
		g := BuildGraph(all)
		if _, ok := g.Titles["alpha"]; !ok {
			t.Error("expected alpha in titles")
		}
		if _, ok := g.Titles["beta"]; !ok {
			t.Error("expected beta in titles")
		}
	})

	t.Run("nil-safe", func(t *testing.T) {
		g := BuildGraph(nil)
		if len(g.Titles) != 0 {
			t.Errorf("expected empty titles, got %d entries", len(g.Titles))
		}
		g = BuildGraph([]*vault.Note{nil, nil})
		if len(g.Titles) != 0 {
			t.Errorf("expected empty titles with nil notes, got %d entries", len(g.Titles))
		}
	})

	t.Run("case-folding", func(t *testing.T) {
		_, notes := newVault(t, map[string]string{
			"UPPER.md": "---\ntitle: x\n---\n",
		})
		var all []*vault.Note
		for _, n := range notes {
			all = append(all, n)
		}
		g := BuildGraph(all)
		if _, ok := g.Titles["upper"]; !ok {
			t.Error("expected lowercased key 'upper'")
		}
	})
}

func TestUnresolvedLinks(t *testing.T) {
	t.Run("resolved bare link", func(t *testing.T) {
		_, notes := newVault(t, map[string]string{
			"Alpha.md": "---\ntitle: Alpha\n---\nSee [[Beta]].\n",
			"Beta.md":  "---\ntitle: Beta\n---\n",
		})
		var all []*vault.Note
		for _, n := range notes {
			all = append(all, n)
		}
		g := BuildGraph(all)
		broken := UnresolvedLinks(notes["Alpha.md"], g)
		if len(broken) != 0 {
			t.Errorf("expected 0 broken links, got %d", len(broken))
		}
	})

	t.Run("unresolved bare link with line number", func(t *testing.T) {
		_, notes := newVault(t, map[string]string{
			"Alpha.md": "---\ntitle: Alpha\n---\nLine one.\nSee [[Missing]].\n",
		})
		var all []*vault.Note
		for _, n := range notes {
			all = append(all, n)
		}
		g := BuildGraph(all)
		broken := UnresolvedLinks(notes["Alpha.md"], g)
		if len(broken) != 1 {
			t.Fatalf("expected 1 broken link, got %d", len(broken))
		}
		if broken[0].Link.Title() != "Missing" {
			t.Errorf("expected title Missing, got %q", broken[0].Link.Title())
		}
		if broken[0].Line != 2 {
			t.Errorf("expected line 2, got %d", broken[0].Line)
		}
		if broken[0].FrontmatterKey != "" {
			t.Errorf("expected empty FrontmatterKey, got %q", broken[0].FrontmatterKey)
		}
	})

	t.Run("resolved alias link uses Title", func(t *testing.T) {
		_, notes := newVault(t, map[string]string{
			"Alpha.md": "---\ntitle: Alpha\n---\nSee [[Beta|my alias]].\n",
			"Beta.md":  "---\ntitle: Beta\n---\n",
		})
		var all []*vault.Note
		for _, n := range notes {
			all = append(all, n)
		}
		g := BuildGraph(all)
		broken := UnresolvedLinks(notes["Alpha.md"], g)
		if len(broken) != 0 {
			t.Errorf("expected 0 broken links, got %d", len(broken))
		}
	})

	t.Run("resolved heading anchor ignores anchor", func(t *testing.T) {
		_, notes := newVault(t, map[string]string{
			"Alpha.md": "---\ntitle: Alpha\n---\nSee [[Beta#Section]].\n",
			"Beta.md":  "---\ntitle: Beta\n---\n",
		})
		var all []*vault.Note
		for _, n := range notes {
			all = append(all, n)
		}
		g := BuildGraph(all)
		broken := UnresolvedLinks(notes["Alpha.md"], g)
		if len(broken) != 0 {
			t.Errorf("expected 0 broken links, got %d", len(broken))
		}
	})

	t.Run("folder-prefixed link resolves by title", func(t *testing.T) {
		_, notes := newVault(t, map[string]string{
			"Alpha.md":    "---\ntitle: Alpha\n---\nSee [[Sub/Beta]].\n",
			"Sub/Beta.md": "---\ntitle: Beta\n---\n",
		})
		var all []*vault.Note
		for _, n := range notes {
			all = append(all, n)
		}
		g := BuildGraph(all)
		broken := UnresolvedLinks(notes["Alpha.md"], g)
		if len(broken) != 0 {
			t.Errorf("expected 0 broken links (folder-prefixed resolves by title), got %d", len(broken))
		}
	})

	t.Run("frontmatter scalar", func(t *testing.T) {
		_, notes := newVault(t, map[string]string{
			"Alpha.md": "---\ncategories: \"[[Books]]\"\n---\n",
		})
		var all []*vault.Note
		for _, n := range notes {
			all = append(all, n)
		}
		g := BuildGraph(all)
		broken := UnresolvedLinks(notes["Alpha.md"], g)
		if len(broken) != 1 {
			t.Fatalf("expected 1 broken link, got %d", len(broken))
		}
		if broken[0].FrontmatterKey != "categories" {
			t.Errorf("expected FrontmatterKey 'categories', got %q", broken[0].FrontmatterKey)
		}
		if broken[0].Line != 0 {
			t.Errorf("expected line 0 for frontmatter link, got %d", broken[0].Line)
		}
	})

	t.Run("frontmatter list", func(t *testing.T) {
		_, notes := newVault(t, map[string]string{
			"Alpha.md": "---\ncategories:\n  - \"[[Books]]\"\n  - \"[[Apps]]\"\n---\n",
		})
		var all []*vault.Note
		for _, n := range notes {
			all = append(all, n)
		}
		g := BuildGraph(all)
		broken := UnresolvedLinks(notes["Alpha.md"], g)
		if len(broken) != 2 {
			t.Fatalf("expected 2 broken links, got %d", len(broken))
		}
		for _, bl := range broken {
			if bl.FrontmatterKey != "categories" {
				t.Errorf("expected FrontmatterKey 'categories', got %q", bl.FrontmatterKey)
			}
		}
	})

	t.Run("code-fenced link not flagged", func(t *testing.T) {
		_, notes := newVault(t, map[string]string{
			"Alpha.md": "---\ntitle: Alpha\n---\n```\n[[Missing]]\n```\n",
		})
		var all []*vault.Note
		for _, n := range notes {
			all = append(all, n)
		}
		g := BuildGraph(all)
		broken := UnresolvedLinks(notes["Alpha.md"], g)
		if len(broken) != 0 {
			t.Errorf("expected 0 broken links (code-fenced), got %d", len(broken))
		}
	})

	t.Run("media embed not flagged", func(t *testing.T) {
		_, notes := newVault(t, map[string]string{
			"Alpha.md": "---\ntitle: Alpha\n---\n![[diagram.png]]\n![[video.mp4]]\n",
		})
		var all []*vault.Note
		for _, n := range notes {
			all = append(all, n)
		}
		g := BuildGraph(all)
		broken := UnresolvedLinks(notes["Alpha.md"], g)
		if len(broken) != 0 {
			t.Errorf("expected 0 broken links (media embeds), got %d", len(broken))
		}
	})

	t.Run("empty target not flagged", func(t *testing.T) {
		_, notes := newVault(t, map[string]string{
			"Alpha.md": "---\ntitle: Alpha\n---\n[[  ]]\n",
		})
		var all []*vault.Note
		for _, n := range notes {
			all = append(all, n)
		}
		g := BuildGraph(all)
		broken := UnresolvedLinks(notes["Alpha.md"], g)
		if len(broken) != 0 {
			t.Errorf("expected 0 broken links (empty target), got %d", len(broken))
		}
	})
}

// TestBrokenLinksPredicateRejectsNil verifies that the BrokenLinks predicate
// returns false for a nil note rather than panicking, consistent with all
// other graph-based predicates (Orphan, MinBacklinks, etc.).
func TestBrokenLinksPredicateRejectsNil(t *testing.T) {
	g := BuildGraph(nil)
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("BrokenLinks should not panic on nil note, got: %v", r)
		}
	}()
	if BrokenLinks(g)(nil) {
		t.Errorf("BrokenLinks(g)(nil) = true, want false")
	}
}

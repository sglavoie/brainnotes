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

	t.Run("NFC link resolves NFD-named note", func(t *testing.T) {
		// The note lives on disk under a DECOMPOSED (NFD) filename, as macOS
		// stores accented names. The link is written in COMPOSED (NFC) form,
		// as Obsidian writes wikilinks. A byte-wise compare would flag this as
		// broken; NFC-normalizing both sides makes it resolve.
		const nfdName = "Que\u0301bec.md" // e + U+0301 combining acute
		const nfcLink = "Qu\u00e9bec"     // precomposed é (U+00E9)
		_, notes := newVault(t, map[string]string{
			"Alpha.md": "---\ntitle: Alpha\n---\nSee [[" + nfcLink + "]].\n",
			nfdName:    "# quebec\n",
		})
		var all []*vault.Note
		for _, n := range notes {
			all = append(all, n)
		}
		g := BuildGraph(all)
		broken := UnresolvedLinks(notes["Alpha.md"], g)
		if len(broken) != 0 {
			t.Errorf("expected NFC link to resolve NFD-named note, got %d broken: %+v", len(broken), broken)
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
		if broken[0].Line != 5 {
			t.Errorf("expected file line 5, got %d", broken[0].Line)
		}
		if broken[0].FrontmatterKey != "" {
			t.Errorf("expected empty FrontmatterKey, got %q", broken[0].FrontmatterKey)
		}
	})

	t.Run("unresolved bare link line number without frontmatter", func(t *testing.T) {
		_, notes := newVault(t, map[string]string{
			"Alpha.md": "Line one.\nSee [[Missing]].\n",
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
		if broken[0].Line != 2 {
			t.Errorf("expected file line 2, got %d", broken[0].Line)
		}
	})

	t.Run("unresolved bare link line number with CRLF frontmatter", func(t *testing.T) {
		_, notes := newVault(t, map[string]string{
			"Alpha.md": "---\r\ntitle: Alpha\r\n---\r\nLine one.\r\nSee [[Missing]].\r\n",
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
		if broken[0].Line != 5 {
			t.Errorf("expected file line 5, got %d", broken[0].Line)
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

	t.Run("folder-prefixed link resolves by exact path", func(t *testing.T) {
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
			t.Errorf("expected 0 broken links for an exact folder-qualified target, got %d", len(broken))
		}
	})

	t.Run("folder-prefixed duplicate title stays broken when path is wrong", func(t *testing.T) {
		_, notes := newVault(t, map[string]string{
			"Alpha.md":      "---\ntitle: Alpha\n---\nSee [[Projects/Beta]].\n",
			"Areas/Beta.md": "---\ntitle: Beta\n---\n",
			"Beta.md":       "---\ntitle: Beta\n---\n",
		})
		var all []*vault.Note
		for _, n := range notes {
			all = append(all, n)
		}
		g := BuildGraph(all)
		broken := UnresolvedLinks(notes["Alpha.md"], g)
		if len(broken) != 1 {
			t.Fatalf("expected 1 broken link for wrong folder-qualified duplicate title, got %d (%v)", len(broken), broken)
		}
		if broken[0].Link.Target != "Projects/Beta" {
			t.Fatalf("expected broken target Projects/Beta, got %q", broken[0].Link.Target)
		}
		if got := g.ForwardLinksForNote(notes["Alpha.md"]); len(got) != 0 {
			t.Fatalf("expected no graph forward links for unresolved target, got %v", got)
		}
		if !DeadEnd(g)(notes["Alpha.md"]) {
			t.Fatalf("expected Alpha to be a dead-end when its only link is unresolved")
		}
	})

	t.Run("parent-escape path stays broken", func(t *testing.T) {
		_, notes := newVault(t, map[string]string{
			"Alpha.md": "---\ntitle: Alpha\n---\nSee [[../Beta]] and [[Folder/../Beta]].\n",
			"Beta.md":  "---\ntitle: Beta\n---\n",
		})
		all := []*vault.Note{notes["Alpha.md"], notes["Beta.md"]}
		g := BuildGraph(all)
		broken := UnresolvedLinks(notes["Alpha.md"], g)
		if len(broken) != 2 {
			t.Fatalf("expected 2 broken parent-escape links, got %d (%v)", len(broken), broken)
		}
		for _, bl := range broken {
			if bl.Line != 4 {
				t.Fatalf("expected broken parent-escape link on line 4, got %d", bl.Line)
			}
		}
		if !BrokenLinks(g)(notes["Alpha.md"]) {
			t.Fatalf("BrokenLinks should match a note whose only folder-qualified links escape via ..")
		}
	})

	t.Run("markdown extension links resolve", func(t *testing.T) {
		_, notes := newVault(t, map[string]string{
			"Alpha.md":     "---\nrelated: \"[[Beta.md]]\"\n---\nSee [[Beta.md]] and [[Sub/Gamma.MD]] and [[Alpha.md]].\n",
			"Beta.md":      "---\ntitle: Beta\n---\n",
			"Sub/Gamma.md": "---\ntitle: Gamma\n---\n",
		})
		var all []*vault.Note
		for _, n := range notes {
			all = append(all, n)
		}
		g := BuildGraph(all)
		broken := UnresolvedLinks(notes["Alpha.md"], g)
		if len(broken) != 0 {
			t.Errorf("expected 0 broken links for explicit .md wikilinks, got %d: %v", len(broken), broken)
		}
	})

	t.Run("missing markdown extension link still broken", func(t *testing.T) {
		_, notes := newVault(t, map[string]string{
			"Alpha.md": "---\ntitle: Alpha\n---\nSee [[Missing.md]].\n",
		})
		var all []*vault.Note
		for _, n := range notes {
			all = append(all, n)
		}
		g := BuildGraph(all)
		broken := UnresolvedLinks(notes["Alpha.md"], g)
		if len(broken) != 1 {
			t.Fatalf("expected 1 broken .md link, got %d", len(broken))
		}
		if broken[0].Link.Title() != "Missing.md" {
			t.Errorf("expected original broken title Missing.md, got %q", broken[0].Link.Title())
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

func TestIsMediaTarget(t *testing.T) {
	tests := []struct {
		title string
		want  bool
	}{
		{title: "diagram.png", want: true},
		{title: "VIDEO.MP4", want: true},
		{title: "note.md", want: false},
		{title: "noextension", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			if got := IsMediaTarget(tt.title); got != tt.want {
				t.Fatalf("IsMediaTarget(%q) = %v, want %v", tt.title, got, tt.want)
			}
		})
	}
}

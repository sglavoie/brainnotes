package cli

import (
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

// relinkTarget is one note the index can rewrite a link to.
type relinkTarget struct {
	key      string
	relNoExt string
	title    string
}

// relinkIndex is the two lookups a deterministic rewrite is allowed to use: an
// exact normalized path key, and a normalized title that must resolve to
// exactly one target.
type relinkIndex struct {
	byPathKey  map[string]relinkTarget
	byTitleKey map[string][]relinkTarget
}

func newRelinkIndex(vaultRoot string, g *query.Graph) *relinkIndex {
	idx := &relinkIndex{
		byPathKey:  make(map[string]relinkTarget),
		byTitleKey: make(map[string][]relinkTarget),
	}
	if g == nil {
		return idx
	}
	keys := make([]string, 0, len(g.Titles))
	for key := range g.Titles {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		absPath, ok := g.PathsByTitle[key]
		if !ok || absPath == "" {
			continue
		}
		relNoExt := relinkRelativeNoExt(vaultRoot, absPath)
		if relNoExt == "" {
			continue
		}
		title := g.OriginalTitles[key]
		if title == "" {
			title = path.Base(relNoExt)
		}
		target := relinkTarget{
			key:      key,
			relNoExt: relNoExt,
			title:    title,
		}
		idx.byPathKey[key] = target
		titleKey := relinkNormalizeTitle(title)
		if titleKey == "" {
			continue
		}
		idx.byTitleKey[titleKey] = append(idx.byTitleKey[titleKey], target)
	}
	for titleKey := range idx.byTitleKey {
		sort.Slice(idx.byTitleKey[titleKey], func(i, j int) bool {
			return idx.byTitleKey[titleKey][i].relNoExt < idx.byTitleKey[titleKey][j].relNoExt
		})
	}
	return idx
}

func relinkRelativeNoExt(vaultRoot, absPath string) string {
	rel, err := filepath.Rel(vaultRoot, absPath)
	if err != nil {
		rel = absPath
	}
	rel = filepath.ToSlash(rel)
	ext := path.Ext(rel)
	return strings.TrimSuffix(rel, ext)
}

func (idx *relinkIndex) resolves(link wikilink.Link) bool {
	return len(idx.resolve(link)) > 0
}

func (idx *relinkIndex) resolve(link wikilink.Link) []relinkTarget {
	if idx == nil {
		return nil
	}
	if link.Folder != "" {
		return idx.resolveFolderLink(link)
	}

	titleKey := relinkNormalizeTitle(link.Title())
	if titleKey == "" {
		return nil
	}
	return append([]relinkTarget(nil), idx.byTitleKey[titleKey]...)
}

// resolveFolderLink resolves a folder-prefixed link: the exact path key first,
// then the title, and only when the title names exactly one target. A bare
// [[Title]] is resolved by resolve itself, where an ambiguous title is allowed
// to return every candidate.
func (idx *relinkIndex) resolveFolderLink(link wikilink.Link) []relinkTarget {
	pathKey := relinkNormalizeTarget(link.Target)
	if pathKey == "" {
		return nil
	}
	if target, ok := idx.byPathKey[pathKey]; ok {
		return []relinkTarget{target}
	}
	titleKey := relinkNormalizeTitle(link.Title())
	if titleKey == "" {
		return nil
	}
	if targets := idx.byTitleKey[titleKey]; len(targets) == 1 {
		return append([]relinkTarget(nil), targets...)
	}
	return nil
}

func (idx *relinkIndex) deterministicTarget(link wikilink.Link) (string, bool) {
	if idx.resolves(link) {
		return "", false
	}

	title := relinkTargetTitle(link.Target)
	if title == "" || relinkIsMediaEmbed(title) {
		return "", false
	}

	if target, ok := idx.byPathKey[relinkNormalizeTarget(link.Target)]; ok {
		return relinkRenderTarget(link, target), true
	}

	titleKey := relinkNormalizeTitle(title)
	targets := idx.byTitleKey[titleKey]
	if len(targets) != 1 {
		return "", false
	}
	return relinkRenderTarget(link, targets[0]), true
}

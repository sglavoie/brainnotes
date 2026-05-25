package query

import (
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

type Graph struct {
	Forward         map[string][]string
	Backward        map[string][]string
	Titles          map[string]struct{}
	PathsByTitle    map[string]string
	OriginalTitles  map[string]string
	NoteKeysByTitle map[string][]string
	Root            string
}

func BuildGraph(notes []*vault.Note) *Graph {
	return BuildGraphForRoot(guessGraphRoot(notes), notes)
}

func BuildGraphForRoot(root string, notes []*vault.Note) *Graph {
	g := &Graph{
		Forward:         make(map[string][]string),
		Backward:        make(map[string][]string),
		Titles:          make(map[string]struct{}),
		PathsByTitle:    make(map[string]string),
		OriginalTitles:  make(map[string]string),
		NoteKeysByTitle: make(map[string][]string),
		Root:            filepath.Clean(root),
	}
	if root == "" {
		g.Root = ""
	}

	for _, n := range notes {
		if n == nil {
			continue
		}
		key := g.keyForNote(n)
		if key == "" {
			continue
		}
		titleKey := noteTitleKey(noteTitle(n))
		g.Titles[key] = struct{}{}
		g.PathsByTitle[key] = n.Path
		g.OriginalTitles[key] = noteTitle(n)
		g.NoteKeysByTitle[titleKey] = append(g.NoteKeysByTitle[titleKey], key)
	}
	for titleKey, keys := range g.NoteKeysByTitle {
		g.NoteKeysByTitle[titleKey] = sortedUniqueStrings(keys)
	}

	for _, n := range notes {
		if n == nil {
			continue
		}
		srcKey := g.keyForNote(n)
		if srcKey == "" {
			continue
		}

		targets := make(map[string]struct{})
		backSeen := make(map[string]struct{})

		body, err := n.Body()
		if err == nil {
			for _, link := range wikilink.Parse(body) {
				g.recordLink(link, n.Path, srcKey, targets, backSeen)
			}
		}

		fm, err := n.Front()
		if err == nil {
			for _, k := range fm.Keys() {
				v, _ := fm.Get(k)
				g.scanValue(v, n.Path, srcKey, targets, backSeen)
			}
		}

		if len(targets) > 0 {
			sorted := make([]string, 0, len(targets))
			for t := range targets {
				sorted = append(sorted, t)
			}
			sort.Strings(sorted)
			g.Forward[srcKey] = sorted
		}
	}

	return g
}

func (g *Graph) scanValue(v any, path, srcKey string, targets, backSeen map[string]struct{}) {
	switch vv := v.(type) {
	case string:
		for _, link := range wikilink.Parse(vv) {
			g.recordLink(link, path, srcKey, targets, backSeen)
		}
	case []any:
		for _, item := range vv {
			g.scanValue(item, path, srcKey, targets, backSeen)
		}
	case map[string]any:
		for _, item := range vv {
			g.scanValue(item, path, srcKey, targets, backSeen)
		}
	}
}

func (g *Graph) recordLink(link wikilink.Link, srcPath, srcKey string, targets, backSeen map[string]struct{}) {
	title := link.Title()
	if title == "" {
		return
	}

	targetKeys := g.resolveLink(link)
	if len(targetKeys) == 0 {
		g.recordBacklink(unresolvedLinkKey(link), srcPath, backSeen)
		return
	}

	for _, targetKey := range targetKeys {
		if targetKey == "" || targetKey == srcKey {
			continue
		}
		g.recordBacklink(targetKey, srcPath, backSeen)
		if _, exists := g.Titles[targetKey]; exists {
			targets[targetKey] = struct{}{}
		}
	}
}

func (g *Graph) recordBacklink(targetKey, srcPath string, backSeen map[string]struct{}) {
	if targetKey == "" {
		return
	}
	if _, dup := backSeen[targetKey]; dup {
		return
	}
	g.Backward[targetKey] = append(g.Backward[targetKey], srcPath)
	backSeen[targetKey] = struct{}{}
}

func (g *Graph) resolveLink(link wikilink.Link) []string {
	if g == nil {
		return nil
	}
	if link.Folder != "" {
		targetKey := linkTargetKey(link.Target)
		if targetKey == "" {
			return nil
		}
		if _, ok := g.Titles[targetKey]; ok {
			return []string{targetKey}
		}
		titleKey := linkTitleKey(link.Title())
		if titleKey == "" {
			return nil
		}
		if keys := g.NoteKeysByTitle[titleKey]; len(keys) == 1 {
			return append([]string(nil), keys...)
		}
		return nil
	}

	titleKey := linkTitleKey(link.Title())
	if titleKey == "" {
		return nil
	}
	return append([]string(nil), g.NoteKeysByTitle[titleKey]...)
}

func unresolvedLinkKey(link wikilink.Link) string {
	if link.Folder != "" {
		return linkTargetKey(link.Target)
	}
	return linkTitleKey(link.Title())
}

func (g *Graph) ForwardLinks(title string) []string {
	return g.forwardLinksForKeys(g.lookupKeys(title))
}

func (g *Graph) BackLinks(title string) []string {
	return g.backLinksForKeys(g.lookupKeys(title))
}

func (g *Graph) OutDegree(title string) int {
	return len(g.ForwardLinks(title))
}

func (g *Graph) InDegree(title string) int {
	return len(g.BackLinks(title))
}

func (g *Graph) ForwardLinksForNote(n *vault.Note) []string {
	return g.forwardLinksForKey(g.keyForNote(n))
}

func (g *Graph) BackLinksForNote(n *vault.Note) []string {
	return g.backLinksForKey(g.keyForNote(n))
}

func (g *Graph) KeyForNote(n *vault.Note) string {
	return g.keyForNote(n)
}

func (g *Graph) OutDegreeForNote(n *vault.Note) int {
	return len(g.ForwardLinksForNote(n))
}

func (g *Graph) InDegreeForNote(n *vault.Note) int {
	return len(g.BackLinksForNote(n))
}

func (g *Graph) keyForNote(n *vault.Note) string {
	if n == nil {
		return ""
	}
	return notePathKey(g.Root, n.Path)
}

func (g *Graph) lookupKeys(query string) []string {
	if g == nil {
		return nil
	}
	if strings.Contains(query, "/") || strings.Contains(query, "\\") {
		key := linkTargetKey(query)
		if key == "" {
			return nil
		}
		return []string{key}
	}
	titleKey := linkTitleKey(query)
	if titleKey == "" {
		return nil
	}
	if keys := g.NoteKeysByTitle[titleKey]; len(keys) > 0 {
		return append([]string(nil), keys...)
	}
	if _, ok := g.Backward[titleKey]; ok {
		return []string{titleKey}
	}
	if _, ok := g.Forward[titleKey]; ok {
		return []string{titleKey}
	}
	return nil
}

func (g *Graph) forwardLinksForKey(key string) []string {
	if g == nil || key == "" {
		return nil
	}
	return append([]string(nil), g.Forward[key]...)
}

func (g *Graph) backLinksForKey(key string) []string {
	if g == nil || key == "" {
		return nil
	}
	return sortedUniqueStrings(g.Backward[key])
}

func (g *Graph) forwardLinksForKeys(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	out := make(map[string]struct{})
	for _, key := range keys {
		for _, target := range g.Forward[key] {
			out[target] = struct{}{}
		}
	}
	return mapKeysSorted(out)
}

func (g *Graph) backLinksForKeys(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	out := make(map[string]struct{})
	for _, key := range keys {
		for _, path := range g.Backward[key] {
			out[path] = struct{}{}
		}
	}
	return mapKeysSorted(out)
}

func noteTitle(n *vault.Note) string {
	if n == nil {
		return ""
	}
	base := filepath.Base(n.Path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func noteTitleKey(title string) string {
	return strings.ToLower(title)
}

func linkTitleKey(title string) string {
	title = normalizeMarkdownLinkTitle(title)
	if title == "" {
		return ""
	}
	return strings.ToLower(title)
}

func linkTargetKey(target string) string {
	target = strings.TrimSpace(strings.ReplaceAll(target, "\\", "/"))
	target = strings.TrimPrefix(path.Clean("/"+target), "/")
	target = normalizeMarkdownLinkTitle(target)
	if target == "" || target == "." {
		return ""
	}
	return strings.ToLower(target)
}

func notePathKey(root, notePath string) string {
	if notePath == "" {
		return ""
	}
	if root == "" {
		return noteTitleKey(strings.TrimSuffix(filepath.Base(notePath), filepath.Ext(notePath)))
	}
	rel, err := filepath.Rel(root, notePath)
	if err != nil {
		return noteTitleKey(strings.TrimSuffix(filepath.Base(notePath), filepath.Ext(notePath)))
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return noteTitleKey(strings.TrimSuffix(filepath.Base(notePath), filepath.Ext(notePath)))
	}
	return linkTargetKey(filepath.ToSlash(rel))
}

func normalizeMarkdownLinkTitle(title string) string {
	ext := filepath.Ext(title)
	if !strings.EqualFold(ext, ".md") {
		return title
	}
	stem := strings.TrimSuffix(title, ext)
	if stem == "" {
		return title
	}
	return stem
}

func sortedUniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := append([]string(nil), values...)
	sort.Strings(out)
	n := 1
	for i := 1; i < len(out); i++ {
		if out[i] == out[i-1] {
			continue
		}
		out[n] = out[i]
		n++
	}
	return out[:n]
}

func mapKeysSorted(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func guessGraphRoot(notes []*vault.Note) string {
	root := ""
	for _, n := range notes {
		if n == nil || n.Path == "" {
			continue
		}
		dir := filepath.Dir(n.Path)
		if root == "" {
			root = dir
			continue
		}
		root = commonAncestor(root, dir)
	}
	return root
}

func commonAncestor(a, b string) string {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	for {
		if isSameOrAncestor(a, b) {
			return a
		}
		parent := filepath.Dir(a)
		if parent == a {
			return a
		}
		a = parent
	}
}

func isSameOrAncestor(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

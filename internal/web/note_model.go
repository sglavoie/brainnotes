package web

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

const backlinkSnippetRunes = 120

func breadcrumbsForNote(root string, note *vault.Note, notePath string) []breadcrumbItem {
	if note == nil {
		return nil
	}

	items := []breadcrumbItem{{Label: "All notes", Path: libraryRoutePath}}
	vaultPath := noteVaultPath(root, note)
	folder := folderFromSlashPath(vaultPath)
	if folder != "" {
		parts := strings.Split(folder, "/")
		current := ""
		for _, part := range parts {
			if part == "" {
				continue
			}
			if current == "" {
				current = part
			} else {
				current += "/" + part
			}
			items = append(items, breadcrumbItem{
				Label: part,
				Path:  filterURL("folder", current),
			})
		}
	}

	title := note.Title()
	if title == "" {
		title = "Note"
	}
	items = append(items, breadcrumbItem{
		Label:   title,
		Path:    notePath,
		Current: true,
	})
	return items
}

func tableOfContentsForMarkdown(source, notePath string) []tableOfContentsItem {
	md := goldmark.New(goldmark.WithParserOptions(parser.WithAutoHeadingID()))
	sourceBytes := []byte(source)
	doc := md.Parser().Parse(text.NewReader(sourceBytes))

	var items []tableOfContentsItem
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || node.Kind() != ast.KindHeading {
			return ast.WalkContinue, nil
		}

		heading, ok := node.(*ast.Heading)
		if !ok {
			return ast.WalkContinue, nil
		}
		label := normalizeSnippet(string(heading.Text(sourceBytes)))
		if label == "" {
			return ast.WalkContinue, nil
		}
		id := nodeAttributeString(node, "id")
		if id == "" {
			id = "heading"
		}

		items = append(items, tableOfContentsItem{
			ID:    id,
			Level: heading.Level,
			Text:  label,
			Path:  notePathWithFragment(notePath, id),
		})
		return ast.WalkContinue, nil
	})
	return items
}

func copyPayloadsForNote(root string, note *vault.Note, notePath string) noteCopyPayloads {
	if note == nil {
		return noteCopyPayloads{}
	}

	return noteCopyPayloads{
		Wikilink:     "[[" + wikilinkTargetForNote(root, note) + "]]",
		VaultPath:    noteVaultPath(root, note),
		RoutePath:    notePath,
		MarkdownLink: fmt.Sprintf("[%s](%s)", note.Title(), notePath),
	}
}

// wikilinkTargetForNote is the text that goes between the brackets of a link to
// this note: its vault-relative path without the extension, in exact case.
//
// It is the path rather than the title because that is the form the graph
// resolves for every note. A title only resolves while it matches the filename,
// so a note whose frontmatter renames it would get a link that renders broken —
// which is precisely the note a person is most likely to reach for a completion
// on.
func wikilinkTargetForNote(root string, note *vault.Note) string {
	vaultPath := noteVaultPath(root, note)
	target := strings.TrimSuffix(vaultPath, filepath.Ext(vaultPath))
	if target == "" || target == "." {
		target = note.Title()
	}
	return filepath.ToSlash(target)
}

func backlinkOccurrencesForNote(graph *query.Graph, targetKey string, source *vault.Note) []backlinkOccurrence {
	if graph == nil || targetKey == "" || source == nil {
		return nil
	}

	var occurrences []backlinkOccurrence
	if body, err := source.Body(); err == nil {
		occurrences = append(occurrences, backlinkBodyOccurrences(graph, targetKey, source.Path, body)...)
	}
	if fm, err := source.Front(); err == nil {
		occurrences = append(occurrences, backlinkFrontmatterOccurrences(graph, targetKey, fm)...)
	}

	sort.SliceStable(occurrences, func(i, j int) bool {
		left := occurrences[i]
		right := occurrences[j]
		leftFront := left.FrontmatterKey != ""
		rightFront := right.FrontmatterKey != ""
		if leftFront != rightFront {
			return leftFront
		}
		if left.Line != right.Line {
			return left.Line < right.Line
		}
		if left.Location != right.Location {
			return left.Location < right.Location
		}
		return left.Snippet < right.Snippet
	})
	return occurrences
}

func backlinkBodyOccurrences(graph *query.Graph, targetKey, sourcePath, body string) []backlinkOccurrence {
	if body == "" {
		return nil
	}

	lineOffset := noteBodyLineOffset(sourcePath)
	var out []backlinkOccurrence
	for _, link := range wikilink.Parse(body) {
		if !wikilinkMatchesGraphKey(graph, link, targetKey) {
			continue
		}
		line := lineOffset + lineFromOffset(body, link.Start)
		out = append(out, backlinkOccurrence{
			Location: fmt.Sprintf("line %d", line),
			Line:     line,
			Snippet:  truncateBacklinkSnippet(snippetLineForOffset(body, link.Start)),
		})
	}
	return out
}

func backlinkFrontmatterOccurrences(graph *query.Graph, targetKey string, fm *frontmatter.Map) []backlinkOccurrence {
	if fm == nil || fm.IsEmpty() {
		return nil
	}

	keys := fm.Keys()
	sort.Strings(keys)
	var out []backlinkOccurrence
	for _, key := range keys {
		value, ok := fm.Get(key)
		if !ok {
			continue
		}
		out = append(out, scanBacklinkFrontmatterValue(graph, targetKey, key, value)...)
	}
	return out
}

func scanBacklinkFrontmatterValue(graph *query.Graph, targetKey, key string, value any) []backlinkOccurrence {
	switch typed := value.(type) {
	case string:
		var out []backlinkOccurrence
		for _, link := range wikilink.Parse(typed) {
			if !wikilinkMatchesGraphKey(graph, link, targetKey) {
				continue
			}
			out = append(out, backlinkOccurrence{
				Location:       "frontmatter." + key,
				FrontmatterKey: key,
				Snippet:        truncateBacklinkSnippet(normalizeSnippet(typed)),
			})
		}
		return out
	case []string:
		var out []backlinkOccurrence
		for _, item := range typed {
			out = append(out, scanBacklinkFrontmatterValue(graph, targetKey, key, item)...)
		}
		return out
	case []any:
		var out []backlinkOccurrence
		for _, item := range typed {
			out = append(out, scanBacklinkFrontmatterValue(graph, targetKey, key, item)...)
		}
		return out
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for nestedKey := range typed {
			keys = append(keys, nestedKey)
		}
		sort.Strings(keys)
		var out []backlinkOccurrence
		for _, nestedKey := range keys {
			out = append(out, scanBacklinkFrontmatterValue(graph, targetKey, key+"."+nestedKey, typed[nestedKey])...)
		}
		return out
	default:
		return nil
	}
}

func wikilinkMatchesGraphKey(graph *query.Graph, link wikilink.Link, targetKey string) bool {
	if graph == nil || targetKey == "" {
		return false
	}
	for _, key := range graph.ResolveTarget(link.Target) {
		if key == targetKey {
			return true
		}
	}
	return false
}

func noteVaultPath(root string, note *vault.Note) string {
	if note == nil || note.Path == "" {
		return ""
	}
	if root != "" {
		rel, err := filepath.Rel(root, note.Path)
		if err == nil && rel != "" && !relEscapesRoot(rel) && !filepath.IsAbs(rel) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(filepath.Base(note.Path))
}

func folderFromSlashPath(slashPath string) string {
	if slashPath == "" || slashPath == "." {
		return ""
	}
	idx := strings.LastIndex(slashPath, "/")
	if idx < 0 {
		return ""
	}
	return slashPath[:idx]
}

func nodeAttributeString(node ast.Node, name string) string {
	value, ok := node.AttributeString(name)
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case []byte:
		return string(typed)
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

func snippetLineForOffset(source string, offset int) string {
	if offset < 0 {
		offset = 0
	}
	if offset > len(source) {
		offset = len(source)
	}
	lineStart := strings.LastIndexByte(source[:offset], '\n') + 1
	lineEnd := strings.IndexByte(source[offset:], '\n')
	if lineEnd < 0 {
		lineEnd = len(source)
	} else {
		lineEnd += offset
	}
	return normalizeSnippet(source[lineStart:lineEnd])
}

func normalizeSnippet(source string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(source)), " ")
}

func truncateBacklinkSnippet(source string) string {
	if utf8.RuneCountInString(source) <= backlinkSnippetRunes {
		return source
	}
	count := 0
	for i := range source {
		if count == backlinkSnippetRunes-3 {
			return source[:i] + "..."
		}
		count++
	}
	return source
}

func lineFromOffset(source string, offset int) int {
	line := 1
	for i := range []byte(source) {
		if i >= offset {
			break
		}
		if source[i] == '\n' {
			line++
		}
	}
	return line
}

func noteBodyLineOffset(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	raw := strings.TrimPrefix(string(data), "\ufeff")
	startLen, ok := frontmatterOpenDelimiterLen(raw)
	if !ok {
		return 0
	}
	bodyOff, ok := frontmatterBodyOffset(raw[startLen:])
	if !ok {
		return 0
	}
	return strings.Count(raw[:startLen+bodyOff], "\n")
}

func frontmatterOpenDelimiterLen(source string) (int, bool) {
	if !strings.HasPrefix(source, "---") {
		return 0, false
	}
	nl := strings.IndexByte(source, '\n')
	if nl < 0 {
		return 0, false
	}
	if isFrontmatterDelimiterLine(source[:nl]) {
		return nl + 1, true
	}
	return 0, false
}

func frontmatterBodyOffset(source string) (int, bool) {
	for i := 0; i < len(source); {
		eol := strings.IndexByte(source[i:], '\n')
		var line string
		var lineEnd int
		if eol == -1 {
			line = source[i:]
			lineEnd = len(source)
		} else {
			line = source[i : i+eol]
			lineEnd = i + eol + 1
		}
		if isFrontmatterDelimiterLine(line) {
			return lineEnd, true
		}
		if eol == -1 {
			break
		}
		i = lineEnd
	}
	return 0, false
}

func isFrontmatterDelimiterLine(line string) bool {
	line = strings.TrimRight(line, "\r")
	line = strings.TrimRight(line, " \t")
	return line == "---"
}

func notePathWithFragment(notePath, id string) string {
	if id == "" {
		return notePath
	}
	fragment := "#" + url.PathEscape(id)
	if notePath == "" {
		return fragment
	}
	return notePath + fragment
}

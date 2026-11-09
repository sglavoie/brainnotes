package web

import (
	"regexp"
	"strings"

	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/wikilink"
)

type passageThought struct {
	Title   string
	Path    string
	Quote   string
	Thought string
}

// The ordinary Markdown created by passage writing is its durable provenance.
// Resolve the source wikilink against the current graph, so rename/move keeps
// working. No browser sidecar or source-note mutation is required.
var passageThoughtPattern = regexp.MustCompile(`(?s)^\s*(?:# [^\n]*\n+)?From (\[\[[^\n]*\]\])\n\n((?:> [^\n]*(?:\n|$))+)\n(.+)$`)
var passageEscapePattern = regexp.MustCompile("\\\\([\\\\`*_[\\]<>#~|+&.()-])")

func parsePassageThought(body string) (source, quote, thought string) {
	if len(body) > 128*1024 {
		return "", "", ""
	}
	match := passageThoughtPattern.FindStringSubmatch(strings.ReplaceAll(body, "\r\n", "\n"))
	if match == nil {
		return "", "", ""
	}
	links := wikilink.Parse(match[1])
	if len(links) != 1 {
		return "", "", ""
	}
	lines := strings.Split(strings.TrimSuffix(match[2], "\n"), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, "> ")
	}
	quote = passageEscapePattern.ReplaceAllString(strings.Join(lines, "\n"), "$1")
	if len([]rune(quote)) > 8000 {
		return "", "", ""
	}
	thought = strings.TrimSpace(match[3])
	if len([]rune(thought)) > 240 {
		thought = string([]rune(thought)[:240]) + "…"
	}
	return links[0].Target, quote, thought
}

func passageThoughtsForNote(snapshot vaultSnapshot, note *vault.Note) []passageThought {
	if snapshot.graph == nil {
		return nil
	}
	paths := snapshot.graph.BackLinksForNote(note)
	if len(paths) == 0 {
		return nil
	}
	byPath := make(map[string]*vault.Note, len(snapshot.notes))
	for _, candidate := range snapshot.notes {
		if candidate != nil {
			byPath[candidate.Path] = candidate
		}
	}
	thoughts := make([]passageThought, 0)
	for _, path := range paths {
		if thought, ok := passageThoughtForSource(snapshot, note, byPath[path]); ok {
			thoughts = append(thoughts, thought)
		}
		if len(thoughts) == 50 {
			break
		}
	}
	return thoughts
}

func passageThoughtForSource(snapshot vaultSnapshot, note, candidate *vault.Note) (passageThought, bool) {
	if candidate == nil {
		return passageThought{}, false
	}
	body, err := candidate.Body()
	if err != nil {
		return passageThought{}, false
	}
	source, quote, thought := parsePassageThought(body)
	targets := snapshot.graph.ResolveTarget(source)
	if source == "" || len(targets) != 1 || targets[0] != snapshot.graph.KeyForNote(note) {
		return passageThought{}, false
	}
	route, err := notePathForKey(snapshot.graph.KeyForNote(candidate))
	if err != nil {
		return passageThought{}, false
	}
	return passageThought{candidate.Title(), route, quote, thought}, true
}

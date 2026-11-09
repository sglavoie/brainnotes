package web

import (
	"net/http"
	"strings"

	"github.com/sglavoie/brainnotes/internal/brief"
	"github.com/sglavoie/brainnotes/internal/recall"
)

// A peek reads only the served snapshot. It neither records an open nor loads
// attachments, runs agents, or writes recall state.
func (s *Server) handleNotePeek(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	snapshot, err := s.state.currentSnapshot()
	if err != nil {
		http.Error(w, "Notes are temporarily unavailable.", http.StatusServiceUnavailable)
		return
	}
	note, key, ok := noteForRoute(snapshot, strings.TrimSpace(r.URL.Query().Get("key")))
	if !ok {
		http.NotFound(w, r)
		return
	}
	document, err := recall.ExtractDocument(s.vault.Root, note, recall.ExtractOptions{})
	if err != nil {
		http.Error(w, "This note could not be previewed.", http.StatusServiceUnavailable)
		return
	}
	path, err := notePathForKey(key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	context := ""
	if other, _, found := noteForRoute(snapshot, r.URL.Query().Get("context")); found {
		context = other.Title()
	}
	passages, truncated := peekPassages(document, context)
	writeManagementJSON(w, http.StatusOK, struct {
		Title     string        `json:"title"`
		Excerpt   string        `json:"excerpt"`
		Path      string        `json:"path"`
		Passages  []peekPassage `json:"passages"`
		Truncated bool          `json:"truncated"`
		Context   string        `json:"context,omitempty"`
	}{document.Title, brief.RepresentativeExcerpt(document), path, passages, truncated, context})
}

type peekPassage struct {
	Text     string `json:"text"`
	Heading  bool   `json:"heading"`
	Relevant bool   `json:"relevant"`
}

// Plain text only; no embedded media or executable Markdown is loaded by a preview.
// Keep the full extraction bounded for long notes, prioritizing a passage that
// actually mentions the other note when a connection supplies context.
func peekPassages(document recall.Document, context string) ([]peekPassage, bool) {
	passages := make([]peekPassage, 0, 12)
	remaining := 8000
	truncated := false
	for _, block := range connectionBlocks(document.Blocks, context) {
		if len(passages) >= 12 || remaining == 0 {
			truncated = true
			break
		}
		text := []rune(block.Text)
		if len(text) > remaining {
			text = text[:remaining]
			truncated = true
		}
		remaining -= len(text)
		passages = append(passages, peekPassage{string(text), block.Kind == recall.BlockHeading, context != "" && strings.Contains(strings.ToLower(block.Text), strings.ToLower(context))})
	}
	return passages, truncated
}

func connectionBlocks(blocks []recall.MarkdownBlock, context string) []recall.MarkdownBlock {
	if context == "" {
		return blocks
	}
	selected := make(map[int]bool)
	matches := 0
	for i, block := range blocks {
		if !matchesConnection(block, context) {
			continue
		}
		selected[i] = true
		// A bare list of links explains the graph, but not the idea. Bring the
		// nearest substantial prose along, including across a section heading.
		if len([]rune(block.Text)) < 160 {
			if neighbor := connectionNeighbor(blocks, i); neighbor >= 0 {
				selected[neighbor] = true
			}
		}
		matches++
		if matches == 2 {
			break
		}
	}
	if matches == 0 {
		return blocks
	}
	result := make([]recall.MarkdownBlock, 0, len(selected))
	for i, block := range blocks {
		if selected[i] {
			result = append(result, connectionExcerpt(block, context))
		}
	}
	return result
}

func connectionNeighbor(blocks []recall.MarkdownBlock, index int) int {
	for distance := 1; distance <= 6; distance++ {
		for _, candidate := range []int{index - distance, index + distance} {
			if candidate < 0 || candidate >= len(blocks) {
				continue
			}
			block := blocks[candidate]
			if block.Kind == recall.BlockProse && len([]rune(block.Text)) >= 60 {
				return candidate
			}
		}
	}
	return -1
}

func matchesConnection(block recall.MarkdownBlock, context string) bool {
	return block.Kind != recall.BlockHeading && strings.Contains(strings.ToLower(block.Text), strings.ToLower(context))
}

// Context prose must leave room for the matching passages even when a nearby
// paragraph is longer than the entire preview budget.
func connectionExcerpt(block recall.MarkdownBlock, context string) recall.MarkdownBlock {
	if !matchesConnection(block, context) {
		text := []rune(block.Text)
		if len(text) > 1600 {
			block.Text = string(text[:1600]) + "…"
		}
	}
	return block
}

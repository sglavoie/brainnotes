package brief

import (
	"regexp"
	"strings"

	"github.com/sglavoie/brainnotes/internal/recall"
)

// Use the same context as the connection explorer: the bridge for an indirect
// connection, otherwise the source note. Selection and ranking are unchanged.
func (selector *DeterministicSelector) connectionCardExcerpt(result recall.Result, source NoteRef) string {
	context := strings.TrimSpace(result.BridgeTitle)
	if context == "" {
		context = strings.TrimSpace(source.Title)
	}
	if context != "" {
		mention := regexp.MustCompile("(?i)" + regexp.QuoteMeta(context))
		for _, block := range selector.blocksByPath[result.Path] {
			if block.Kind != recall.BlockProse {
				continue
			}
			if match := mention.FindStringIndex(block.Text); match != nil {
				return excerptAtMention(block.Text, match[0])
			}
		}
	}
	return selector.excerptForPath(result.Path)
}

// A mention late in a paragraph must survive the card's Unicode-safe bound.
// Keep nearby words rather than clipping every passage from its opening.
func excerptAtMention(text string, offset int) string {
	before := []rune(text[:offset])
	if len(before) <= 60 {
		return BoundCardExcerpt(text)
	}
	lead := string(before[len(before)-60:])
	if space := strings.IndexAny(lead, " \t\n"); space >= 0 {
		lead = lead[space+1:]
	}
	return BoundCardExcerpt("… " + lead + text[offset:])
}

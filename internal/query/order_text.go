package query

import (
	"sort"
	"strings"

	"github.com/sglavoie/brainnotes/internal/vault"
)

// orderTextMatches preserves the browser's title-hit, title, folder, key order.
func orderTextMatches(notes []*vault.Note, root, search string, descending bool) []*vault.Note {
	type rank struct {
		titleHit           bool
		title, folder, key string
	}
	ranks := make(map[*vault.Note]rank, len(notes))
	needle := FoldText(search)
	for _, note := range notes {
		folder, _ := note.Folder(root)
		ranks[note] = rank{
			titleHit: needle != "" && strings.Contains(FoldText(note.Title()), needle),
			title:    strings.ToLower(note.Title()), folder: strings.ToLower(folder),
			key: notePathKey(root, note.Path),
		}
	}
	less := func(a, b rank) bool {
		if a.titleHit != b.titleHit {
			return a.titleHit
		}
		if a.title != b.title {
			return a.title < b.title
		}
		if a.folder != b.folder {
			return a.folder < b.folder
		}
		return a.key < b.key
	}
	sort.SliceStable(notes, func(i, j int) bool {
		if descending {
			return less(ranks[notes[j]], ranks[notes[i]])
		}
		return less(ranks[notes[i]], ranks[notes[j]])
	})
	return notes
}

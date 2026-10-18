package web

import (
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

const historyComparisonFileLimit = 128 << 10
const historyComparisonTotalLimit = 1 << 20
const historyComparisonCountLimit = 32

type historyTextComparison struct {
	Current  string `json:"current"`
	Restored string `json:"restored"`
}

// Comparisons use the same current-file digests the restore receipt pins. Large,
// binary and private state paths remain reviewable by their path and effect.
func (s *Server) addHistoryComparisons(response *historyPreviewResponse, pins []propertyPreviewFileState) {
	digests := make(map[string]string, len(pins))
	for _, pin := range pins {
		digests[pin.RelPath] = pin.Digest
	}
	remaining := historyComparisonTotalLimit
	attempted := 0
	for i := range response.Changes {
		change := &response.Changes[i]
		change.ComparisonUnavailable = "Text comparison is unavailable for this file type."
		if change.Class != "note" && change.Class != "template" {
			continue
		}
		if remaining <= 0 || attempted >= historyComparisonCountLimit {
			change.ComparisonUnavailable = "Text comparison limit reached for this entry."
			continue
		}
		attempted++
		comparison, err := journal.ReadRestoreText(s.vault.Root, noteops.JournalRoot(s.vault.Root), response.EntryID, change.Path, min(historyComparisonFileLimit, int64(remaining/2)))
		if err != nil {
			change.ComparisonUnavailable = "Text comparison unavailable: " + err.Error()
			continue
		}
		if comparison.CurrentDigest != digests[change.Path] {
			change.ComparisonUnavailable = "This file changed while reading the preview. Preview again."
			continue
		}
		change.Comparison = &historyTextComparison{Current: comparison.Current, Restored: comparison.Restored}
		change.ComparisonUnavailable = ""
		remaining -= len(comparison.Current) + len(comparison.Restored)
	}
}

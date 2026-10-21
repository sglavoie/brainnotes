package web

import (
	"github.com/sglavoie/brainnotes/internal/journal"
	"testing"
)

func TestHistoryActionSummary(t *testing.T) {
	classifier := historyPathClassifier{}
	note := journal.FileRecord{Path: "Notes/Writing.md", Before: journal.FileState{Kind: journal.KindFile}, After: journal.FileState{Kind: journal.KindFile}}
	for _, tc := range []struct{ command, want string }{
		{"bn web source save", "Edited Writing"}, {"bn capture", "Captured to Writing"},
		{"bn web trash", "Trashed Writing"}, {"bn web undo", "Restored Writing"},
		{"future-command", "Changed Writing"}, {"bn", "Changed Writing"}, {"bn web", "Changed Writing"},
	} {
		entry := journal.Entry{Command: tc.command, Files: []journal.FileRecord{note}}
		if got := historyActionSummary(entry, classifier); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.command, got, tc.want)
		}
	}
}

func TestHistoryMoveSummaryCountsDestinations(t *testing.T) {
	entry := journal.Entry{Command: "bn web move", Files: []journal.FileRecord{
		{Path: "Old.md", After: journal.FileState{Kind: journal.KindMissing}},
		{Path: "Backlink.md", Before: journal.FileState{Kind: journal.KindFile}, After: journal.FileState{Kind: journal.KindFile}},
		{Path: "New.md", Before: journal.FileState{Kind: journal.KindMissing}, After: journal.FileState{Kind: journal.KindFile}},
		{Path: ".bnotes/state.json", After: journal.FileState{Kind: journal.KindFile}},
	}}
	if got := historyActionSummary(entry, historyPathClassifier{}); got != "Moved New" {
		t.Fatal(got)
	}
}

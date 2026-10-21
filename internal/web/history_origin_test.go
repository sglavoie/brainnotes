package web

import (
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/journal"
)

// Every command literal this repository journals, classified. The table is the
// contract: a session that adds a mutating entry point and gives it a new
// command token has to decide what origin it is, and this test is where that
// decision is recorded. An unrecognized command is `unknown` rather than
// whichever bucket happens to match loosely, so a new token that nobody
// classified shows up as unclassified instead of being quietly filed as a
// browser save.
func TestHistoryOriginForCommand(t *testing.T) {
	cases := []struct {
		command string
		want    historyOrigin
	}{
		{"bn web source save", historyOriginBrowser},
		{"bn web capture", historyOriginBrowser},
		{"bn web create", historyOriginBrowser},
		{"bn web rename", historyOriginBrowser},
		{"bn web move", historyOriginBrowser},
		{"bn web trash", historyOriginBrowser},
		{"bn web frontmatter set", historyOriginBrowser},
		{"bn web frontmatter unset", historyOriginBrowser},
		{"bn web frontmatter set atomic", historyOriginBrowser},
		{"bn web bulk trash", historyOriginBrowser},
		{"bn web bulk move", historyOriginBrowser},
		{"bn web bulk frontmatter set", historyOriginBrowser},
		{"bn web pins set", historyOriginBrowser},
		{"bn web pins remove", historyOriginBrowser},
		{"bn web pins reorder", historyOriginBrowser},
		{"bn web synthesize", historyOriginBrowser},
		{"bn cockpit fix", historyOriginBrowser},
		{"bn serve exempt dead end", historyOriginBrowser},
		{"bn serve clean long tail", historyOriginBrowser},
		{"bn serve property set", historyOriginBrowser},
		{"bn serve property rename", historyOriginBrowser},
		{"bn serve property list add", historyOriginBrowser},

		{"bn serve setting set", historyOriginSettings},
		{"bn serve setting unset", historyOriginSettings},
		{"bn serve template save", historyOriginSettings},
		{"bn serve template delete", historyOriginSettings},
		{"bn serve view save", historyOriginSettings},
		{"bn serve view rename", historyOriginSettings},
		{"bn serve view delete", historyOriginSettings},
		{"bn serve property schema upsert", historyOriginSettings},
		{"bn serve property schema delete", historyOriginSettings},
		{"bn config migrate", historyOriginSettings},

		{"bn sync apply", historyOriginImport},
		{"bn-vault-sync", historyOriginCompanion},
		{"bn sync resolve", historyOriginResolution},
		{"bn serve sync resolve", historyOriginResolution},

		{"bn web undo", historyOriginRestore},
		{"bn undo entry-2026-01-01", historyOriginRestore},

		{"bn new Some Note", historyOriginTerminal},
		{"bn mv Old.md New.md", historyOriginTerminal},

		{"", historyOriginUnknown},
		{"   ", historyOriginUnknown},
		{"some-other-tool", historyOriginUnknown},
	}

	for _, tc := range cases {
		if got := historyOriginForCommand(tc.command); got != tc.want {
			t.Errorf("historyOriginForCommand(%q) = %q, want %q", tc.command, got, tc.want)
		}
	}
}

// `bn serve property schema upsert` is also `bn serve property set`'s prefix
// family and also `bn serve `, so the order of the rule table is load-bearing.
// This pins the pairs that would flip if the table were reordered or sorted.
func TestHistoryOriginRuleOrderSeparatesOverlappingPrefixes(t *testing.T) {
	pairs := []struct {
		specific string
		general  string
	}{
		{"bn serve sync resolve", "bn serve property set"},
		{"bn serve property schema upsert", "bn serve property set"},
		{"bn sync resolve", "bn sync apply"},
		{"bn web undo", "bn web source save"},
	}
	for _, pair := range pairs {
		specific := historyOriginForCommand(pair.specific)
		general := historyOriginForCommand(pair.general)
		if specific == general {
			t.Errorf("%q and %q both classify as %q; the specific rule is being shadowed",
				pair.specific, pair.general, specific)
		}
	}
}

func TestParseHistoryOrigin(t *testing.T) {
	for _, origin := range historyOrigins {
		got, ok := parseHistoryOrigin(string(origin))
		if !ok || got != origin {
			t.Errorf("parseHistoryOrigin(%q) = (%q, %v), want (%q, true)", origin, got, ok, origin)
		}
		if origin.label() == "" {
			t.Errorf("origin %q has no label", origin)
		}
	}
	if got, ok := parseHistoryOrigin("  BROWSER "); !ok || got != historyOriginBrowser {
		t.Errorf("parseHistoryOrigin with padding/case = (%q, %v), want browser", got, ok)
	}
	if _, ok := parseHistoryOrigin("not-an-origin"); ok {
		t.Error("parseHistoryOrigin accepted an unknown origin")
	}
	if _, ok := parseHistoryOrigin(""); ok {
		t.Error("parseHistoryOrigin accepted an empty origin")
	}
}

func testHistoryClassifier() historyPathClassifier {
	return newHistoryPathClassifier(&config.Config{
		Attachments: config.AttachmentsConfig{Dir: "attachments"},
		Templates:   config.TemplatesConfig{Dir: "Templates"},
	})
}

func TestHistoryPathClassifierClassifies(t *testing.T) {
	classifier := testHistoryClassifier()
	cases := []struct {
		rel  string
		want historyPathClass
	}{
		{"Notes/Plan.md", historyPathNote},
		{"Plan.md", historyPathNote},
		{"Plan.MD", historyPathNote},
		{"attachments/diagram.png", historyPathAttachment},
		{"attachments/nested/scan.pdf", historyPathAttachment},
		// An attachment keeps its class by location, not by extension: a markdown
		// file filed under the attachments directory is an attachment.
		{"attachments/notes.md", historyPathAttachment},
		{"Templates/Daily.md", historyPathTemplate},
		{config.FileName, historyPathConfiguration},
		{config.LocalFileName, historyPathConfiguration},
		{".bnotes/journal/entry/manifest.json", historyPathState},
		// Nothing under .bnotes is a note, whatever it is called.
		{".bnotes/source-saves/record.md", historyPathState},
		{"README.txt", historyPathOther},
		{"", historyPathOther},
		// A configuration file name that is not at the root is not the vault's
		// configuration.
		{"Archive/" + config.FileName, historyPathOther},
		// A directory whose name merely starts with the attachments directory is
		// not inside it.
		{"attachments-old/diagram.png", historyPathOther},
	}
	for _, tc := range cases {
		if got := classifier.classify(tc.rel); got != tc.want {
			t.Errorf("classify(%q) = %q, want %q", tc.rel, got, tc.want)
		}
	}
}

// An unset attachments or templates directory must match nothing. The empty
// string as a prefix would otherwise make every path in the vault an
// attachment, which is the kind of failure that looks like working software.
func TestHistoryPathClassifierWithUnsetDirectories(t *testing.T) {
	classifier := newHistoryPathClassifier(&config.Config{})
	if got := classifier.classify("Notes/Plan.md"); got != historyPathNote {
		t.Errorf("classify with unset dirs = %q, want note", got)
	}
	if got := classifier.classify("somewhere/diagram.png"); got != historyPathOther {
		t.Errorf("classify with unset dirs = %q, want other", got)
	}
	if got := newHistoryPathClassifier(nil).classify("Notes/Plan.md"); got != historyPathNote {
		t.Errorf("classify with nil config = %q, want note", got)
	}
}

// Recorded and Travels answer two different questions and this is where they
// come apart: a note is both, the local configuration overlay is neither, and
// the pair is what lets the surface say honestly which restored paths reach the
// other replica.
func TestHistoryPathClassifierFacts(t *testing.T) {
	classifier := testHistoryClassifier()
	cases := []struct {
		rel          string
		wantRecorded bool
		wantTravels  bool
	}{
		{"Notes/Plan.md", true, true},
		{"attachments/diagram.png", true, true},
		{config.FileName, true, true},
		// The machine-local overlay is in neither: it is deliberately kept out of
		// the content history, so restoring it changes this machine and leaves no
		// commit behind at all.
		{config.LocalFileName, false, false},
		// The journal is the case that shows why these are two questions rather
		// than one. It *is* in the content history — vaultgit keeps journal
		// manifests so the undo record survives a restore from the remote — and it
		// is not portable, so the commit stays on this machine.
		{".bnotes/journal/entry/manifest.json", true, false},
		{".bnotes/index/state.json", false, false},
		{"backups/2026-01-01/Plan.md", false, false},
	}
	for _, tc := range cases {
		facts := classifier.facts(tc.rel)
		if facts.Recorded != tc.wantRecorded || facts.Travels != tc.wantTravels {
			t.Errorf("facts(%q) = {recorded:%v travels:%v}, want {recorded:%v travels:%v}",
				tc.rel, facts.Recorded, facts.Travels, tc.wantRecorded, tc.wantTravels)
		}
	}
}

func TestHistoryEntryFactsCountsByClass(t *testing.T) {
	classifier := testHistoryClassifier()
	entry := journal.Entry{
		Command: "bn web source save",
		Files: []journal.FileRecord{
			{Path: "Notes/Plan.md"},
			{Path: "Notes/Other.md"},
			{Path: "attachments/diagram.png"},
		},
	}
	facts := classifier.entryFacts(entry)
	if facts.Origin != historyOriginBrowser {
		t.Fatalf("origin = %q, want browser", facts.Origin)
	}
	if facts.Classes[historyPathNote] != 2 || facts.Classes[historyPathAttachment] != 1 {
		t.Fatalf("classes = %#v, want 2 notes and 1 attachment", facts.Classes)
	}

	counts := historyClassCounts(facts.Classes)
	if len(counts) != 2 {
		t.Fatalf("counts = %#v, want two entries", counts)
	}
	// Fixed order, so two responses describing the same mix read the same way.
	if counts[0].Class != string(historyPathNote) || counts[1].Class != string(historyPathAttachment) {
		t.Fatalf("counts order = %#v, want note before attachment", counts)
	}
	if counts[0].Count != 2 || counts[0].Label != historyPathNote.label() {
		t.Fatalf("note count = %#v", counts[0])
	}
	if historyClassCounts(map[historyPathClass]int{}) != nil {
		t.Error("historyClassCounts of an empty map should be nil")
	}
}

func TestParseHistoryPathClass(t *testing.T) {
	for _, class := range historyPathClasses {
		got, ok := parseHistoryPathClass(string(class))
		if !ok || got != class {
			t.Errorf("parseHistoryPathClass(%q) = (%q, %v), want (%q, true)", class, got, ok, class)
		}
		if class.label() == "" {
			t.Errorf("class %q has no label", class)
		}
	}
	if _, ok := parseHistoryPathClass("notes"); ok {
		t.Error("parseHistoryPathClass accepted a near-miss name")
	}
}

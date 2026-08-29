package vaultgit

import "testing"

func TestInScope(t *testing.T) {
	cases := []struct {
		name string
		rel  string
		want bool
	}{
		{"markdown note", "Inbox/Some Note.md", true},
		{"note with spaces and unicode", "Zettel/Ünïcodé — note.md", true},
		{"note with a comma", "Inbox/co,mma,file.md", true},
		{"note that looks like a glob", "Inbox/Draft [2].md", true},
		{"attachment", "attachments/diagram.png", true},
		{"vault config", "brainnotes.toml", true},
		{"journal manifest", ".bnotes/journal/entry-1/manifest.json", true},
		{"journal snapshot", ".bnotes/journal/entry-1/snapshots/ab.snap", true},
		{"allowlisted state", ".bnotes/recall.json", true},
		{"allowlisted cockpit state", ".bnotes/cockpit.json", true},

		{"empty", "", false},
		{"absolute", "/etc/passwd", false},
		{"escaping", "../outside.md", false},
		{"unclean", "Inbox/./note.md", false},
		{"dot git", ".git/config", false},
		{"nested dot git", "Inbox/.git/config", false},
		{"ds store", "Inbox/.DS_Store", false},
		{"local backups", "backups/2026-08-01/note.md", false},
		{"code index", ".codegraph/graph.db", false},
		{"legacy cache", ".vault-cache/thumb.png", false},
		{"index cache", ".bnotes/index/notes.json", false},
		{"lock directory", ".bnotes/locks/note.lock", false},
		{"unknown bnotes state", ".bnotes/something-new.json", false},
		{"journal root itself", ".bnotes/journal", false},
		{"note scratch temp", "Inbox/.note-abc123.tmp", false},
		{"txn scratch temp", "Inbox/.txn-custody-abc123.tmp", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := InScope(tc.rel); got != tc.want {
				t.Fatalf("InScope(%q) = %v, want %v", tc.rel, got, tc.want)
			}
		})
	}
}

// A file whose name merely starts like scratch but is durable content must stay
// in scope: the exclusion is for `.tmp` files the transaction layer creates, not
// for every name beginning with a dot and a prefix.
func TestInScopeScratchNeedsTmpSuffix(t *testing.T) {
	if !InScope("Inbox/.note-keeper.md") {
		t.Fatal("a .md file starting with .note- must stay in scope")
	}
	if InScope("Inbox/.note-keeper.tmp") {
		t.Fatal("a .note-*.tmp scratch file must be out of scope")
	}
}

func TestJournalEntryID(t *testing.T) {
	cases := []struct {
		rel        string
		wantID     string
		wantIsMani bool
	}{
		{".bnotes/journal/entry-1/manifest.json", "entry-1", true},
		{".bnotes/journal/entry-1/snapshots/ab.snap", "entry-1", false},
		{".bnotes/journal/entry-1", "", false},
		{".bnotes/recall.json", "", false},
		{"Inbox/note.md", "", false},
	}
	for _, tc := range cases {
		id, isManifest := journalEntryID(tc.rel)
		if id != tc.wantID || isManifest != tc.wantIsMani {
			t.Fatalf("journalEntryID(%q) = (%q, %v), want (%q, %v)", tc.rel, id, isManifest, tc.wantID, tc.wantIsMani)
		}
	}
}

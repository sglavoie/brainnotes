package vaultgit

import (
	"errors"
	"testing"
)

func status(x, y byte, path string) statusEntry {
	return statusEntry{x: x, y: y, path: path}
}

func TestClassifyPendingRecognizedStates(t *testing.T) {
	changes, err := classifyPending([]statusEntry{
		status('?', '?', "Inbox/new.md"),
		status(' ', 'M', "Inbox/edited.md"),
		status(' ', 'D', "Inbox/gone.md"),
	})
	if err != nil {
		t.Fatalf("classifyPending: %v", err)
	}
	want := []pendingChange{
		{path: "Inbox/edited.md", kind: changeModified, tracked: true},
		{path: "Inbox/gone.md", kind: changeDeleted, tracked: true},
		{path: "Inbox/new.md", kind: changeAdded},
	}
	if len(changes) != len(want) {
		t.Fatalf("got %d changes, want %d: %+v", len(changes), len(want), changes)
	}
	for i := range want {
		if changes[i] != want[i] {
			t.Fatalf("change %d = %+v, want %+v", i, changes[i], want[i])
		}
	}
}

// Every code the engine does not explicitly understand must be refused. A type
// change in particular looks harmless and is not: a note replaced by a symlink
// or a directory would otherwise be committed as if the user had edited it.
func TestClassifyPendingRefusesAmbiguity(t *testing.T) {
	cases := []struct {
		name  string
		entry statusEntry
		kind  Kind
	}{
		{"type change", status(' ', 'T', "Inbox/now-a-symlink.md"), KindAmbiguousChange},
		{"staged rename", status('R', ' ', "Inbox/renamed.md"), KindAmbiguousChange},
		{"copy", status('C', ' ', "Inbox/copied.md"), KindAmbiguousChange},
		{"unexpected staged change", status('M', ' ', "Inbox/staged.md"), KindAmbiguousChange},
		{"unknown code", status(' ', 'X', "Inbox/mystery.md"), KindAmbiguousChange},
		{"both deleted", status('D', 'D', "Inbox/conflict.md"), KindRepositoryState},
		{"both added", status('A', 'A', "Inbox/conflict.md"), KindRepositoryState},
		{"unmerged", status('U', 'U', "Inbox/conflict.md"), KindRepositoryState},
		{"unmerged theirs", status('A', 'U', "Inbox/conflict.md"), KindRepositoryState},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := classifyPending([]statusEntry{tc.entry})
			if err == nil {
				t.Fatalf("classifyPending accepted %+v", tc.entry)
			}
			if got := KindOf(err); got != tc.kind {
				t.Fatalf("kind = %q, want %q (%v)", got, tc.kind, err)
			}
		})
	}
}

// One ambiguous path poisons the whole pass. Committing the rest and silently
// leaving the ambiguous one behind would make a partial snapshot look complete.
func TestClassifyPendingRefusesWholeSetOnAmbiguity(t *testing.T) {
	_, err := classifyPending([]statusEntry{
		status('?', '?', "Inbox/fine.md"),
		status(' ', 'T', "Inbox/broken.md"),
	})
	if !Blocks(err) {
		t.Fatalf("an ambiguous change must block the whole pass, got %v", err)
	}
	var safety *SafetyError
	if !errors.As(err, &safety) {
		t.Fatalf("want *SafetyError, got %T", err)
	}
	if len(safety.Paths) != 1 || safety.Paths[0] != "Inbox/broken.md" {
		t.Fatalf("paths = %v, want only the ambiguous path", safety.Paths)
	}
}

func TestInTrackedScope(t *testing.T) {
	// An out-of-scope path that git already tracks stays in scope, so it can
	// never fall out of the history because the policy narrowed underneath it.
	tracked := pendingChange{path: "backups/old.md", kind: changeModified, tracked: true}
	if !inTrackedScope(tracked) {
		t.Fatal("a tracked path must stay in scope regardless of policy")
	}
	untracked := pendingChange{path: "backups/old.md", kind: changeAdded}
	if inTrackedScope(untracked) {
		t.Fatal("an untracked out-of-scope addition must not be picked up")
	}
}

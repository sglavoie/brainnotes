package vaultexport

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// TestDeletionWithoutAuthorityHalts is the deletion-authority requirement. A
// tombstone is the one change a peer cannot undo by re-reading its own copy, so
// it travels only on the strength of a record saying the removal was intended.
func TestDeletionWithoutAuthorityHalts(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})
	cursor := f.state().Commit

	f.remove("Beta.md")
	f.commit("remove Beta.md by hand")

	_, err := f.export(Options{})
	if !errors.Is(err, ErrUnauthorizedDeletion) {
		t.Fatalf("error: got %v, want ErrUnauthorizedDeletion", err)
	}
	if !strings.Contains(err.Error(), "Beta.md") {
		t.Fatalf("the refusal must name the path: %v", err)
	}
	if got := f.state().Commit; got != cursor {
		t.Fatalf("the cursor advanced past a refused commit: %q, want %q", got, cursor)
	}
	if paths := f.channelPaths(); len(paths) != len(sortedSeed()) {
		t.Fatalf("the refused commit published something: %v", paths)
	}
}

// TestDeletionWithAuthorityTravelsAsATombstone covers the authorized case: the
// commit that removes the note is the commit that carries the journal record,
// and the tombstone reaches the peer carrying the bytes it expected to find.
func TestDeletionWithAuthorityTravelsAsATombstone(t *testing.T) {
	f := newFixture(t)
	baseline := f.mustExport(Options{})
	body := f.vault.Read(t, "Beta.md")
	baselineOperation := f.operationFor(baseline, "Beta.md")

	f.journalDeletion("20260304T050611-rm", "Beta.md", body)
	f.remove("Beta.md")
	head := f.commit("bn rm Beta.md")

	result := f.mustExport(Options{})
	if len(result.Packages) != 1 || result.Packages[0].Commit != head {
		t.Fatalf("packages: %+v", result.Packages)
	}
	change := f.changeFor(result, "Beta.md")
	if !change.Tombstone() {
		t.Fatalf("Beta.md travelled as %q, want a tombstone", change.Action)
	}
	if !change.ExpectExists || change.ExpectSHA256 != vaultsync.HashBytes([]byte(body)) {
		t.Fatalf("tombstone expectation: exists=%v sha=%q", change.ExpectExists, change.ExpectSHA256)
	}
	// The tombstone is causally after the operation that last wrote the path, so
	// a peer that edited it concurrently has a conflict rather than a silent loss.
	requirePredecessors(t, change, baselineOperation)
	// And the ledger keeps the path, so a note recreated later can name the
	// tombstone rather than look like a change that never saw the delete.
	if _, kept := f.state().Paths["Beta.md"]; !kept {
		t.Fatal("the path ledger dropped a deleted path")
	}
}

// TestAuthorityMustDescribeTheBytesBeingRemoved refuses a record that does not
// describe the file the deletion actually drops. The hash and the size are both
// checked, which is internal/vaultgit's rule restated rather than loosened.
func TestAuthorityMustDescribeTheBytesBeingRemoved(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	// A record about a stale version of the note: the right path, the wrong bytes.
	f.journalDeletion("20260304T050611-rm", "Beta.md", "some older version of Beta\n")
	f.remove("Beta.md")
	f.commit("bn rm Beta.md with a stale record")

	if _, err := f.export(Options{}); !errors.Is(err, ErrUnauthorizedDeletion) {
		t.Fatalf("error: got %v, want ErrUnauthorizedDeletion", err)
	}
}

// TestAuthorityMustBeInTheSameCommit refuses a record already in the history. A
// manifest that is merely present proves a transition that already happened; it
// must not explain a different commit's deletion, or a note deleted, recreated,
// and deleted again by hand passes as intended.
func TestAuthorityMustBeInTheSameCommit(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})
	body := f.vault.Read(t, "Beta.md")

	f.journalDeletion("20260304T050611-rm", "Beta.md", body)
	f.commit("record the journal entry on its own")
	f.remove("Beta.md")
	f.commit("remove the note in a later commit")

	if _, err := f.export(Options{}); !errors.Is(err, ErrUnauthorizedDeletion) {
		t.Fatalf("error: got %v, want ErrUnauthorizedDeletion", err)
	}
}

// TestAuthorizeDeletionsIsTheCallersOverride mirrors vaultgit.CommitRestore's
// deliberate exception: a restore commits deletions on a person's authority
// rather than a journal's, so it leaves a commit no record can ever explain.
func TestAuthorizeDeletionsIsTheCallersOverride(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})
	parent := f.state().Commit

	f.remove("Beta.md")
	head := f.commit("restore an older state")

	// Another commit's approval is not this one's.
	if _, err := f.export(Options{AuthorizeDeletions: []string{parent}}); !errors.Is(err, ErrUnauthorizedDeletion) {
		t.Fatalf("error: got %v, want ErrUnauthorizedDeletion", err)
	}

	before := f.state().clone()
	result := f.mustExport(Options{AuthorizeDeletions: []string{head}})
	if !f.changeFor(result, "Beta.md").Tombstone() {
		t.Fatal("the approved deletion did not travel as a tombstone")
	}

	// A person approves one commit, once. Rewound to where it started, the same
	// pass without the option refuses again, which is what "never persisted"
	// means in the only place it could have been remembered.
	f.putState(before)
	if _, err := f.export(Options{}); !errors.Is(err, ErrUnauthorizedDeletion) {
		t.Fatalf("the approval outlived its pass: got %v, want ErrUnauthorizedDeletion", err)
	}
}

// TestUndecodableJournalManifestAuthorizesNothing covers one unreadable record
// among many. It contributes no proof and is not itself an error: the deletion
// it would have explained stays unexplained and halts the export, which is the
// conservative outcome.
func TestUndecodableJournalManifestAuthorizesNothing(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	f.write(journalPrefix+"20260304T050611-rm/"+journal.ManifestFileName, "{not json\n")
	f.remove("Beta.md")
	f.commit("remove Beta.md with a damaged record")

	if _, err := f.export(Options{}); !errors.Is(err, ErrUnauthorizedDeletion) {
		t.Fatalf("error: got %v, want ErrUnauthorizedDeletion", err)
	}
}

// TestAuthorityFromTheRealRecorder is the fidelity test the rest of this file
// rests on.
//
// Every other deletion test writes its undo manifest by hand, which proves what
// this parser accepts and nothing at all about what a vault contains. Here the
// record comes from internal/journal's own recorder, doing what a real `bn rm`
// does — capture, mutate, finalize — so the two halves of the deletion-authority
// contract are checked against each other rather than against one test's idea of
// the format. A field renamed on that side fails here rather than silently
// authorizing nothing on a user's vault.
func TestAuthorityFromTheRealRecorder(t *testing.T) {
	f := newFixture(t)
	baseline := f.mustExport(Options{})
	body := f.vault.Read(t, "Beta.md")
	baselineOperation := f.operationFor(baseline, "Beta.md")

	const entryID = "20260304T050611-rm"
	recorder, err := journal.NewRecorder(
		f.root(),
		filepath.Join(f.root(), filepath.FromSlash(config.DefaultJournalDir)),
		"bn rm",
		journal.WithClock(f.clock.Now),
		journal.WithIDGenerator(func(time.Time) (string, error) { return entryID, nil }),
	)
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}
	if err := recorder.Capture("Beta.md"); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	f.remove("Beta.md")
	entry, err := recorder.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if entry.ID != entryID {
		t.Fatalf("the recorder wrote entry %q, want %q", entry.ID, entryID)
	}
	head := f.commit("bn rm Beta.md")

	result := f.mustExport(Options{})
	if len(result.Packages) != 1 || result.Packages[0].Commit != head {
		t.Fatalf("packages: %+v", result.Packages)
	}
	change := f.changeFor(result, "Beta.md")
	if !change.Tombstone() {
		t.Fatalf("Beta.md travelled as %q, want a tombstone", change.Action)
	}
	if !change.ExpectExists || change.ExpectSHA256 != vaultsync.HashBytes([]byte(body)) {
		t.Fatalf("tombstone expectation: exists=%v sha=%q", change.ExpectExists, change.ExpectSHA256)
	}
	requirePredecessors(t, change, baselineOperation)
	// The snapshot the recorder wrote is in the same commit and is not portable,
	// so it neither travels nor counts as a second record for the entry.
	requirePortableOnly(t, "the channel", f.channelPaths())
}

// TestAManifestCommittedUnderAnotherEntrysNameCountsForNothing binds an entry
// identity to the path its manifest was committed at.
//
// The directory name is what internal/journal looks an entry up by, so it is the
// identity the rest of this vault means by it; the `id` field is bytes in a file.
// Trusting the field alone would let an ordinary local commit claim an entry
// identity it does not carry — and the import ledger is keyed on exactly that
// identity, so the commit would be read as an applied import and skipped, quietly
// dropping a local change that was never sent anywhere.
func TestAManifestCommittedUnderAnotherEntrysNameCountsForNothing(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	const imported = "20260304T050610-apply"
	peerOperation := "op-" + strings.Repeat("ab", 16)
	if err := RecordImport(f.root(), imported, Import{OperationID: peerOperation, OriginDevice: f.peer.DeviceID}); err != nil {
		t.Fatalf("RecordImport: %v", err)
	}

	// A local change, journalled under a local entry whose manifest names the
	// imported one.
	const edited = "this device wrote this\n"
	f.write("Beta.md", edited)
	record := journal.Entry{
		ID:        imported,
		Timestamp: f.clock.Now(),
		Command:   "bn set",
		Files: []journal.FileRecord{{
			Path:   "Beta.md",
			Before: journal.FileState{Kind: journal.KindMissing},
			After: journal.FileState{
				Kind:   journal.KindFile,
				SHA256: vaultsync.HashBytes([]byte(edited)),
				Size:   int64(len(edited)),
			},
		}},
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatalf("encode journal entry: %v", err)
	}
	f.write(journalPrefix+"20260304T050612-set/"+journal.ManifestFileName, string(data)+"\n")
	head := f.commit("a local edit under a misnamed record")

	result := f.mustExport(Options{})
	if len(result.Skipped) != 0 {
		t.Fatalf("the local commit was skipped as an import: %+v", result.Skipped)
	}
	if got := f.payload(result, "Beta.md"); got != edited {
		t.Fatalf("Beta.md payload: got %q, want %q", got, edited)
	}
	if f.operationFor(result, "Beta.md") == peerOperation {
		t.Fatal("the local change travelled under the peer's operation identity")
	}
	if got := f.state().Commit; got != head {
		t.Fatalf("cursor: got %q, want %q", got, head)
	}
	// And the ledger entry is still there, unconsumed, waiting for the commit that
	// really does apply that operation.
	if got := f.state().Imported[imported].OperationID; got != peerOperation {
		t.Fatalf("the import ledger entry was consumed by the wrong commit: %q", got)
	}
}

// TestSnapshotIsNotARecord pins the parser to the manifest alone. A snapshot
// under the same entry is the *content* a deletion removed; counting it as a
// record would make an entry look present in a commit that carried only its
// snapshots.
func TestSnapshotIsNotARecord(t *testing.T) {
	for _, tc := range []struct {
		rel   string
		entry string
		want  bool
	}{
		{rel: ".bnotes/journal/entry-1/manifest.json", entry: "entry-1", want: true},
		{rel: ".bnotes/journal/entry-1/snapshots/Beta.md", want: false},
		{rel: ".bnotes/journal/entry-1/snapshots/manifest.json", want: false},
		{rel: ".bnotes/journal/manifest.json", want: false},
		{rel: ".bnotes/journal/entry-1/", want: false},
		{rel: ".bnotes/sync/enrollment.json", want: false},
		{rel: "Notes/manifest.json", want: false},
	} {
		entry, ok := journalManifestEntry(tc.rel)
		if ok != tc.want || entry != tc.entry {
			t.Errorf("journalManifestEntry(%q) = (%q, %v), want (%q, %v)", tc.rel, entry, ok, tc.entry, tc.want)
		}
	}
}

// TestDeletionOfPrivateStateNeedsNoAuthority covers the commit that removes a
// sidecar file. It is not portable content, so no peer is being told anything
// and no record is required.
func TestDeletionOfPrivateStateNeedsNoAuthority(t *testing.T) {
	f := newFixture(t)
	f.write(".bnotes/recall.json", "{\"version\":1}\n")
	f.commit("record local state")
	f.mustExport(Options{})

	f.remove(".bnotes/recall.json")
	head := f.commit("drop local state")

	result := f.mustExport(Options{})
	if len(result.Packages) != 0 {
		t.Fatalf("removing private state published %d packages", len(result.Packages))
	}
	if result.Cursor != head {
		t.Fatalf("cursor: got %q, want %q", result.Cursor, head)
	}
}

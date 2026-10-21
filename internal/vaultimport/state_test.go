package vaultimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// TestLoadStateRefusesARecordFromAnotherSchemaVersion is the fail-closed half of
// the version field, and it is about a downgrade rather than an upgrade.
//
// A record written by a newer build carries fields this one does not know; a
// record written by an older one is missing fields this one acts on — the origin
// sequence a dependency is matched against, and the convergence flag that
// explains an application with no recovery entry. Reading either would be a
// wrong answer rather than a failure, and the wrong answers are the expensive
// kind: a dependent held forever, or a converged path rolled back to a baseline
// this vault never had.
func TestLoadStateRefusesARecordFromAnotherSchemaVersion(t *testing.T) {
	r := enroll(t)
	record := newState(r.local)
	record.Version = stateVersion - 1
	writeRecord(t, r, record)

	_, err := LoadState(r.root())
	requireIs(t, err, ErrUnsupportedVersion, "loading a record from the previous schema version")
}

// TestLoadStateRefusesAPackageRecordedAsBothAppliedAndHeld is the one check
// across the two ledgers, and the only one whose absence would be acted on
// rather than noticed.
//
// Applied and held are contradictory answers to what happened to a package. A
// record holding both would answer a redelivery from the first map while a drain
// pass re-decides it from the second, so which one wins would depend on how the
// package arrived.
func TestLoadStateRefusesAPackageRecordedAsBothAppliedAndHeld(t *testing.T) {
	r := enroll(t)
	packageID := mustPackageID(t)
	record := newState(r.local)
	record.Applied[packageID] = Record{
		OperationID:  mustOperationID(t),
		OriginDevice: r.peer.DeviceID,
		Sequence:     1,
		JournalEntry: "2026-01-01T00-00-00Z-entry",
	}
	record.Conflicts[packageID] = &ConflictRecord{
		PackageID:    packageID,
		OperationID:  mustOperationID(t),
		OriginDevice: r.peer.DeviceID,
		Sequence:     1,
		Paths:        []PathConflict{{Path: "Alpha.md", Kind: ConflictConcurrentEdit}},
	}
	writeRecord(t, r, record)

	_, err := LoadState(r.root())
	requireIs(t, err, ErrCorruptState, "loading a record that both applied and held one package")
}

// TestLoadStateRefusesAnAppliedRecordThatContradictsItself covers the pairing
// convergence is defined by. It is not two fields that happen to agree: a
// converged application wrote nothing, so it has no recovery entry and only a
// converged receipt; an ordinary one has an entry it can be undone from and only
// an ordinary receipt. A record that claims otherwise did not come out of this
// package, and reading it would answer a redelivery with a promise the vault
// cannot keep.
func TestLoadStateRefusesAnAppliedRecordThatContradictsItself(t *testing.T) {
	packageID := mustPackageID(t)
	cases := []struct {
		name   string
		record Record
	}{
		{
			name:   "converged and naming an entry that can undo it",
			record: Record{Converged: true, JournalEntry: "2026-01-01T00-00-00Z-entry"},
		},
		{
			name:   "converged and holding an ordinary acknowledgment",
			record: Record{Converged: true, Receipt: &vaultsync.Receipt{PackageID: packageID}},
		},
		{
			name:   "applied and naming no entry at all",
			record: Record{},
		},
		{
			name:   "holding a receipt for some other package",
			record: Record{JournalEntry: "2026-01-01T00-00-00Z-entry", Receipt: &vaultsync.Receipt{PackageID: mustPackageID(t)}},
		},
		{
			name:   "applied and holding a converged acknowledgment",
			record: Record{JournalEntry: "2026-01-01T00-00-00Z-entry", Receipt: &vaultsync.Receipt{PackageID: packageID, Converged: true}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := enroll(t)
			applied := tc.record
			applied.OperationID = mustOperationID(t)
			applied.OriginDevice = r.peer.DeviceID
			applied.Sequence = 1
			record := newState(r.local)
			record.Applied[packageID] = applied
			writeRecord(t, r, record)

			_, err := LoadState(r.root())
			requireIs(t, err, ErrCorruptState, "loading a record "+tc.name)
		})
	}
}

// writeRecord puts a record on disk without going through saveState, which
// validates. These tests are about what a *reader* does with a file some other
// build — or some other version of this one — left behind, so the writer cannot
// be the thing that refuses it.
func writeRecord(t *testing.T, r *replicas, record *State) {
	t.Helper()
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatalf("encode the record: %v", err)
	}
	statePath := StatePath(r.root())
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("create the state directory: %v", err)
	}
	if err := os.WriteFile(statePath, append(data, '\n'), statePerm); err != nil {
		t.Fatalf("write the record: %v", err)
	}
}

func mustPackageID(t *testing.T) string {
	t.Helper()
	packageID, err := vaultsync.NewPackageID()
	if err != nil {
		t.Fatalf("NewPackageID: %v", err)
	}
	return packageID
}

func mustOperationID(t *testing.T) string {
	t.Helper()
	operationID, err := vaultsync.NewOperationID()
	if err != nil {
		t.Fatalf("NewOperationID: %v", err)
	}
	return operationID
}

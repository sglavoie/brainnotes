package vaultexport

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/vaultfixture"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// objectName is a syntactically valid Git object name, for the record-shape
// tests that are not about any particular commit.
var objectName = strings.Repeat("a", 40)

// validRecord is the smallest record that passes, for a table that mutates one
// field at a time.
func validRecord() *State {
	return &State{
		Version:  stateVersion,
		VaultID:  "vault-" + strings.Repeat("0", 32),
		DeviceID: "device-" + strings.Repeat("1", 32),
		Epoch:    1,
		Paths:    map[string]string{},
		Imported: map[string]Import{},
	}
}

// TestValidateRefusesRecordsThatContradictThemselves pins the strictness the
// record needs. A cursor that is not an object name has to fail here, as a
// corrupt state file, rather than inside a rev-list argument as a surprisingly
// broad query.
func TestValidateRefusesRecordsThatContradictThemselves(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*State)
		want   error
	}{
		{name: "valid", mutate: func(*State) {}},
		{
			name:   "newer version",
			mutate: func(s *State) { s.Version = stateVersion + 1 },
			want:   ErrUnsupportedVersion,
		},
		{
			name:   "older version",
			mutate: func(s *State) { s.Version = stateVersion - 1 },
			want:   ErrUnsupportedVersion,
		},
		{name: "no vault", mutate: func(s *State) { s.VaultID = "  " }, want: ErrCorruptState},
		{name: "no device", mutate: func(s *State) { s.DeviceID = "" }, want: ErrCorruptState},
		{name: "no epoch", mutate: func(s *State) { s.Epoch = 0 }, want: ErrCorruptState},
		{
			name:   "cursor is not an object name",
			mutate: func(s *State) { s.Commit, s.Branch = "--all", "refs/heads/main" },
			want:   ErrCorruptState,
		},
		{
			name:   "abbreviated cursor",
			mutate: func(s *State) { s.Commit, s.Branch = objectName[:7], "refs/heads/main" },
			want:   ErrCorruptState,
		},
		{
			name:   "baseline is not an object name",
			mutate: func(s *State) { s.Baseline, s.Branch = "HEAD", "refs/heads/main" },
			want:   ErrCorruptState,
		},
		{
			name: "both a cursor and an unfinished baseline",
			mutate: func(s *State) {
				s.Commit, s.Baseline, s.Branch = objectName, objectName, "refs/heads/main"
			},
			want: ErrCorruptState,
		},
		{
			name:   "cursor names no branch",
			mutate: func(s *State) { s.Commit = objectName },
			want:   ErrCorruptState,
		},
		{
			name:   "ledger names private state",
			mutate: func(s *State) { s.Paths[".bnotes/sync/export.json"] = "op-x" },
			want:   ErrCorruptState,
		},
		{
			name:   "ledger entry names no operation",
			mutate: func(s *State) { s.Paths["Alpha.md"] = " " },
			want:   ErrCorruptState,
		},
		{
			name:   "import ledger holds an unnamed entry",
			mutate: func(s *State) { s.Imported[""] = Import{OperationID: "op-x"} },
			want:   ErrCorruptState,
		},
		{
			name:   "import ledger names no operation",
			mutate: func(s *State) { s.Imported["entry-1"] = Import{} },
			want:   ErrCorruptState,
		},
		{
			name:   "reservation has no sequence",
			mutate: func(s *State) { s.Pending = &Reservation{PackageID: "pkg-x", Commit: objectName} },
			want:   ErrCorruptState,
		},
		{
			name:   "reservation names no package",
			mutate: func(s *State) { s.Pending = &Reservation{Sequence: 1, Commit: objectName} },
			want:   ErrCorruptState,
		},
		{
			name:   "reservation commit is not an object name",
			mutate: func(s *State) { s.Pending = &Reservation{Sequence: 1, PackageID: "pkg-x", Commit: "HEAD"} },
			want:   ErrCorruptState,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			record := validRecord()
			tc.mutate(record)
			err := record.validate()
			switch {
			case tc.want == nil && err != nil:
				t.Fatalf("validate: %v, want nil", err)
			case tc.want != nil && !errors.Is(err, tc.want):
				t.Fatalf("validate: got %v, want %v", err, tc.want)
			}
		})
	}
}

// TestLoadStateTreatsAbsenceAsAFirstRun pins the one indistinguishable pair that
// should be indistinguishable: a vault that has never exported and a vault whose
// record is gone both mean the peer has been told nothing, and the remedy for
// both is a baseline.
func TestLoadStateTreatsAbsenceAsAFirstRun(t *testing.T) {
	f := newFixture(t)

	record, err := LoadState(f.root())
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if record.Commit != "" || record.Baseline != "" || record.Sequence != 0 {
		t.Fatalf("a vault that never exported reports %+v", record)
	}
	if record.VaultID != f.local.VaultID || record.DeviceID != f.local.DeviceID {
		t.Fatalf("the fresh record does not carry this vault's identity: %+v", record)
	}
	if _, err := os.Stat(StatePath(f.root())); !os.IsNotExist(err) {
		t.Fatalf("reading the state wrote it: %v", err)
	}
}

// TestLoadStateRefusesAnUnsupportedVersion refuses a record a newer build wrote.
// Downgrading it would silently drop whatever that build recorded, and a dropped
// cursor is a whole history re-sent.
func TestLoadStateRefusesAnUnsupportedVersion(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	data, err := os.ReadFile(StatePath(f.root()))
	if err != nil {
		t.Fatalf("read the record: %v", err)
	}
	newer := strings.Replace(string(data), "\"version\": 1", "\"version\": 2", 1)
	if newer == string(data) {
		t.Fatal("the record does not carry a version field to raise")
	}
	if err := os.WriteFile(StatePath(f.root()), []byte(newer), 0o644); err != nil {
		t.Fatalf("write the record: %v", err)
	}

	if _, err := LoadState(f.root()); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("error: got %v, want ErrUnsupportedVersion", err)
	}
	if _, err := f.export(Options{}); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("Export error: got %v, want ErrUnsupportedVersion", err)
	}
}

// TestLoadStateRefusesUndecodableBytes distinguishes damage from a newer writer.
func TestLoadStateRefusesUndecodableBytes(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	if err := os.WriteFile(StatePath(f.root()), []byte("{not json\n"), 0o644); err != nil {
		t.Fatalf("damage the record: %v", err)
	}
	if _, err := LoadState(f.root()); !errors.Is(err, ErrCorruptState) {
		t.Fatalf("error: got %v, want ErrCorruptState", err)
	}
}

// TestRecordImportIsIdempotentAndRefusesConflict covers the applier's side of
// the echo-suppression contract: a retried apply must be safe, and a confused one
// must not silently overwrite what the first said.
func TestRecordImportIsIdempotentAndRefusesConflict(t *testing.T) {
	f := newFixture(t)
	record := Import{OperationID: "op-" + strings.Repeat("c", 32), OriginDevice: f.peer.DeviceID}

	if err := RecordImport(f.root(), "entry-1", record); err != nil {
		t.Fatalf("RecordImport: %v", err)
	}
	if err := RecordImport(f.root(), "entry-1", record); err != nil {
		t.Fatalf("re-recording the same import: %v", err)
	}
	if got := f.state().Imported["entry-1"]; got != record {
		t.Fatalf("ledger: got %+v, want %+v", got, record)
	}

	other := Import{OperationID: "op-" + strings.Repeat("d", 32), OriginDevice: f.peer.DeviceID}
	if err := RecordImport(f.root(), "entry-1", other); !errors.Is(err, ErrCorruptState) {
		t.Fatalf("conflicting re-record: got %v, want ErrCorruptState", err)
	}
	if got := f.state().Imported["entry-1"]; got != record {
		t.Fatalf("the refused record overwrote the ledger: %+v", got)
	}
}

// TestRecordImportRequiresBothIdentities refuses a ledger entry that names
// nothing: an entry with no operation could never suppress an echo, and one with
// no journal entry could never be consumed.
func TestRecordImportRequiresBothIdentities(t *testing.T) {
	f := newFixture(t)
	if err := RecordImport(f.root(), "  ", Import{OperationID: "op-x"}); err == nil {
		t.Fatal("an unnamed journal entry was accepted")
	}
	if err := RecordImport(f.root(), "entry-1", Import{}); err == nil {
		t.Fatal("an unnamed operation was accepted")
	}
	if _, err := os.Stat(StatePath(f.root())); !os.IsNotExist(err) {
		t.Fatalf("a refused record was written anyway: %v", err)
	}
}

// TestResetKeepsIdentitySequenceAndImports pins what Reset is careful not to
// throw away. The sequence must not restart, or two different packages take one
// position in this device's stream; the import ledger describes operations the
// peer produced and this vault applied, and discarding it would make the next
// baseline claim the peer's own content as this device's.
func TestResetKeepsIdentitySequenceAndImports(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})
	record := Import{OperationID: "op-" + strings.Repeat("e", 32), OriginDevice: f.peer.DeviceID}
	if err := RecordImport(f.root(), "entry-1", record); err != nil {
		t.Fatalf("RecordImport: %v", err)
	}
	before := f.state()

	if err := Reset(f.root()); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	after := f.state()
	requireNoCursor(t, after)
	if after.Sequence != before.Sequence {
		t.Fatalf("sequence: got %d, want %d", after.Sequence, before.Sequence)
	}
	if after.VaultID != before.VaultID || after.DeviceID != before.DeviceID {
		t.Fatalf("Reset changed the identity: %+v", after)
	}
	if got := after.Imported["entry-1"]; got != record {
		t.Fatalf("Reset dropped the import ledger: %+v", after.Imported)
	}
}

// TestResetSpendsALiveReservation covers the one thing Reset must not hand back.
//
// A reservation is a number claimed before a publication, and Reset does not look
// in the channel, so it cannot know whether the package that claimed it landed. A
// Reset that merely dropped the reservation would release that number to the next
// pass while a complete package carrying it may already be travelling — two
// different packages at one position in this device's stream, which is exactly
// what keeping the sequence across a Reset exists to prevent.
func TestResetSpendsALiveReservation(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	f.write("Alpha.md", "edited\n")
	head := f.commit("edit Alpha")
	spent := f.state().Sequence
	f.reserve(&Reservation{Sequence: spent + 1, PackageID: "pkg-" + strings.Repeat("3", 32), Commit: head})

	if err := Reset(f.root()); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	after := f.state()
	requireNoCursor(t, after)
	if after.Sequence != spent+1 {
		t.Fatalf("sequence after Reset: got %d, want the reserved number %d spent", after.Sequence, spent+1)
	}
	// And the next pass publishes above it rather than at it.
	for _, published := range f.mustExport(Options{}).Packages {
		if published.Sequence <= spent+1 {
			t.Fatalf("the re-baseline reused sequence %d", published.Sequence)
		}
	}
}

// TestLoadStateReturnsACopy keeps a status reader from editing the record a later
// export will write.
func TestLoadStateReturnsACopy(t *testing.T) {
	f := newFixture(t)
	f.mustExport(Options{})

	borrowed, err := LoadState(f.root())
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	borrowed.Paths["Alpha.md"] = "op-tampered"
	borrowed.Commit = objectName

	fresh, err := LoadState(f.root())
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if fresh.Paths["Alpha.md"] == "op-tampered" || fresh.Commit == objectName {
		t.Fatal("LoadState handed out the record rather than a copy of it")
	}
}

// TestExportRefusesAnUnenrolledVault: a vault that synchronizes with nothing has
// no identity to publish under, and minting one here would make enrollment a side
// effect of asking for a status.
func TestExportRefusesAnUnenrolledVault(t *testing.T) {
	v := vaultfixture.New(t, vaultfixture.Options{Name: "solo"})

	_, err := Export(context.Background(), Options{Root: v.Root, ChannelDir: t.TempDir()})
	if !errors.Is(err, vaultsync.ErrNotEnrolled) {
		t.Fatalf("error: got %v, want ErrNotEnrolled", err)
	}
	if _, err := os.Stat(StatePath(v.Root)); !os.IsNotExist(err) {
		t.Fatalf("the refusal wrote a record: %v", err)
	}
}

// TestExportRequiresARoot refuses an empty root before it can resolve to the
// process's working directory, which would be somebody's real vault.
func TestExportRequiresARoot(t *testing.T) {
	vaultfixture.ScrubEnv(t)
	if _, err := Export(context.Background(), Options{Root: "   "}); err == nil {
		t.Fatal("an empty vault root was accepted")
	}
}

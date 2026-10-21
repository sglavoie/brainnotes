package vaultexport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/vaultfixture"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// fixture is one enrolled, Git-backed vault plus the channel it publishes into.
//
// The channel lives beside the vault rather than inside it. Options.ChannelDir
// defaults to the vault's own `.bnotes/sync/outgoing`, which is correct in
// production and useless in a test: every assertion about what did *not* travel
// would then be made about a directory that is itself excluded from travelling,
// and a package written into the vault would keep changing the working tree
// under the history being read. TestPrivateStateNeverEntersPackages covers the
// default location directly instead.
type fixture struct {
	t       *testing.T
	pair    *vaultfixture.Pair
	vault   *vaultfixture.Vault
	clock   *vaultfixture.Clock
	local   *vaultsync.Enrollment
	peer    *vaultsync.Enrollment
	channel string
}

// newFixture enrolls a disposable vault as one of two replicas, through the
// real enrollment flow rather than by writing a record by hand.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	pair := vaultfixture.NewPair(t, nil)
	clock := vaultfixture.NewClock(t)

	local, err := vaultsync.Establish(pair.Local.Root, vaultsync.Options{Label: "mac", Now: clock.Now})
	if err != nil {
		t.Fatalf("Establish: %v", err)
	}
	clock.Tick()
	peer, err := vaultsync.Join(pair.Peer.Root, local.Invite(), vaultsync.Options{Label: "pi", Now: clock.Now})
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	clock.Tick()
	local, err = vaultsync.RecordPeer(pair.Local.Root, vaultsync.Peer{DeviceID: peer.DeviceID, Label: peer.Label})
	if err != nil {
		t.Fatalf("RecordPeer: %v", err)
	}
	return &fixture{
		t:       t,
		pair:    pair,
		vault:   pair.Local,
		clock:   clock,
		local:   local,
		peer:    peer,
		channel: filepath.Join(pair.Dir, "channel"),
	}
}

// root is the enrolled vault's root.
func (f *fixture) root() string { return f.vault.Root }

// write creates or replaces a vault-relative path without committing it.
func (f *fixture) write(rel, body string) {
	f.t.Helper()
	f.vault.Write(f.t, rel, body)
}

// remove deletes a vault-relative path without committing the removal.
func (f *fixture) remove(rel string) {
	f.t.Helper()
	if err := os.Remove(f.vault.Path(rel)); err != nil {
		f.t.Fatalf("remove %q: %v", rel, err)
	}
}

// commit records one commit of everything internal/vaultgit's scope policy
// would have staged.
//
// `.bnotes/sync` is excluded because that policy excludes it: the enrollment
// record and the export cursor are never in the content history, and a fixture
// that committed them would be asserting against a vault this codebase does not
// produce — and would lose them to any test that rewinds the branch.
// `.bnotes/journal` and `.bnotes/recall.json` are in scope and are staged, which
// is what makes the deletion-authority and private-path tests real.
func (f *fixture) commit(message string) string {
	f.t.Helper()
	f.vault.Git(f.t, "add", "-A", "--", ".", ":(exclude).bnotes/sync")
	f.vault.Git(f.t, "commit", "-q", "-m", message)
	return f.vault.Head(f.t)
}

// commitForced records one commit of exactly the named paths, so a test can hand
// the exporter a history this codebase would never have produced.
//
// What makes it forced is the pathspec, not the `-f`. This fixture stages with
// plain `git add`, which knows nothing about internal/vaultgit's scope policy, so
// naming a path the engine would have left out is the whole of it; `-f` is there
// only so a path some future ignore file covers still reaches the commit.
func (f *fixture) commitForced(message string, paths ...string) string {
	f.t.Helper()
	f.vault.Git(f.t, append([]string{"add", "-f", "--"}, paths...)...)
	f.vault.Git(f.t, "commit", "-q", "-m", message)
	return f.vault.Head(f.t)
}

// journalDeletion writes the undo manifest that authorizes removing rel while
// it held before. It is written but not committed: the commit that removes the
// note must be the commit that carries the record, which is the binding
// checkDeletionAuthority enforces.
func (f *fixture) journalDeletion(entryID, rel, before string) {
	f.t.Helper()
	f.writeJournalEntry(journal.Entry{
		ID:        entryID,
		Timestamp: f.clock.Now(),
		Command:   "bn rm",
		Files: []journal.FileRecord{{
			Path: rel,
			Before: journal.FileState{
				Kind:     journal.KindFile,
				SHA256:   vaultsync.HashBytes([]byte(before)),
				Size:     int64(len(before)),
				Snapshot: "snapshots/" + entryID,
			},
			After: journal.FileState{Kind: journal.KindMissing},
		}},
	})
}

// journalWrite writes an undo manifest for an entry that created a path holding
// after, which authorizes nothing and is how a test proves an import is
// recognized by its entry identity rather than by what it deleted.
//
// The record is the shape internal/journal's own recorder would have written —
// a file state carries its hash and its size or that package refuses to finalize
// it — so a fixture that shortened it would be proving the exporter reads
// something no vault contains. TestAuthorityFromTheRealRecorder is the assertion
// that the two agree; this helper is how the other tests stay cheap.
func (f *fixture) journalWrite(entryID, rel, after string) {
	f.t.Helper()
	f.writeJournalEntry(journal.Entry{
		ID:        entryID,
		Timestamp: f.clock.Now(),
		Command:   "bn apply",
		Files: []journal.FileRecord{{
			Path:   rel,
			Before: journal.FileState{Kind: journal.KindMissing},
			After: journal.FileState{
				Kind:   journal.KindFile,
				SHA256: vaultsync.HashBytes([]byte(after)),
				Size:   int64(len(after)),
			},
		}},
	})
}

// writeJournalEntry puts one entry's manifest where internal/journal keeps it.
func (f *fixture) writeJournalEntry(entry journal.Entry) {
	f.t.Helper()
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		f.t.Fatalf("encode journal entry: %v", err)
	}
	f.write(journalPrefix+entry.ID+"/"+journal.ManifestFileName, string(data)+"\n")
}

// export runs one pass with the fixture's channel and clock filled in.
func (f *fixture) export(opts Options) (*Result, error) {
	f.t.Helper()
	opts.Root = f.root()
	if opts.ChannelDir == "" {
		opts.ChannelDir = f.channel
	}
	if opts.Now == nil {
		opts.Now = f.clock.Now
	}
	return Export(context.Background(), opts)
}

// mustExport runs one pass and fails the test if it halts.
func (f *fixture) mustExport(opts Options) *Result {
	f.t.Helper()
	result, err := f.export(opts)
	if err != nil {
		f.t.Fatalf("Export: %v", err)
	}
	return result
}

// state reads the durable export record back off disk, which is the only thing
// a restart would see.
func (f *fixture) state() *State {
	f.t.Helper()
	record, err := loadState(f.root(), f.local)
	if err != nil {
		f.t.Fatalf("loadState: %v", err)
	}
	return record
}

// putState replaces the durable export record, for the tests that reconstruct
// what an interrupted pass left behind.
func (f *fixture) putState(record *State) {
	f.t.Helper()
	if err := saveState(f.root(), record); err != nil {
		f.t.Fatalf("saveState: %v", err)
	}
}

// manifest reads a published package's manifest.
func (f *fixture) manifest(dir string) *vaultsync.Manifest {
	f.t.Helper()
	manifest, err := vaultsync.ReadManifest(dir)
	if err != nil {
		f.t.Fatalf("ReadManifest %q: %v", dir, err)
	}
	return manifest
}

// paths returns every path a result's packages carry, sorted and deduplicated,
// which is what "what travelled" means for an assertion.
func (f *fixture) paths(result *Result) []string {
	f.t.Helper()
	seen := map[string]struct{}{}
	var paths []string
	for _, published := range result.Packages {
		for _, p := range f.manifest(published.Dir).Paths() {
			if _, known := seen[p]; known {
				continue
			}
			seen[p] = struct{}{}
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	return paths
}

// changeFor returns the single change a result carries for one path.
func (f *fixture) changeFor(result *Result, path string) vaultsync.Change {
	f.t.Helper()
	var found []vaultsync.Change
	for _, published := range result.Packages {
		for _, change := range f.manifest(published.Dir).Changes() {
			if change.Path == path {
				found = append(found, change)
			}
		}
	}
	if len(found) != 1 {
		f.t.Fatalf("expected exactly one change for %q, got %d", path, len(found))
	}
	return found[0]
}

// payload returns the bytes a write-change carries.
func (f *fixture) payload(result *Result, path string) string {
	f.t.Helper()
	for _, published := range result.Packages {
		manifest := f.manifest(published.Dir)
		for _, change := range manifest.Changes() {
			if change.Path != path || change.Payload == "" {
				continue
			}
			data, err := vaultsync.ReadPayload(published.Dir, manifest, change.Payload)
			if err != nil {
				f.t.Fatalf("ReadPayload %q: %v", change.Payload, err)
			}
			return string(data)
		}
	}
	f.t.Fatalf("no payload for %q", path)
	return ""
}

// operationFor returns the operation identity a result gave one path.
func (f *fixture) operationFor(result *Result, path string) string {
	f.t.Helper()
	for _, published := range result.Packages {
		for _, operation := range f.manifest(published.Dir).Operations {
			for _, change := range operation.Changes {
				if change.Path == path {
					return operation.OperationID
				}
			}
		}
	}
	f.t.Fatalf("no operation for %q", path)
	return ""
}

// channelPaths returns every path published into the channel so far, across
// every pass, which is the set a "never travels" assertion has to be made over:
// a path that leaked in an earlier pass is just as leaked.
func (f *fixture) channelPaths() []string {
	f.t.Helper()
	entries, err := os.ReadDir(f.channel)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		f.t.Fatalf("read channel: %v", err)
	}
	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "pkg-") {
			continue
		}
		paths = append(paths, f.manifest(filepath.Join(f.channel, entry.Name())).Paths()...)
	}
	sort.Strings(paths)
	return paths
}

// publishByHand publishes one package the exporter did not, so a test can
// reconstruct what a pass that died between publishing and recording left in the
// channel.
func (f *fixture) publishByHand(sequence uint64, operationID, path, body string, prior vaultsync.Expectation) *vaultsync.Manifest {
	f.t.Helper()
	builder, err := vaultsync.NewBuilder(f.local, sequence, f.clock.Now())
	if err != nil {
		f.t.Fatalf("NewBuilder: %v", err)
	}
	builder.BeginOperation(operationID, f.clock.Now())
	builder.Write(path, []byte(body), prior)
	manifest, payloads, err := builder.Build()
	if err != nil {
		f.t.Fatalf("Build: %v", err)
	}
	if _, err := vaultsync.Publish(f.channel, manifest, payloads); err != nil {
		f.t.Fatalf("Publish: %v", err)
	}
	return manifest
}

// reserve replaces the durable record with one naming a publication in flight,
// which is the only state an interrupted pass can be recognized from.
func (f *fixture) reserve(pending *Reservation) {
	f.t.Helper()
	record := f.state().clone()
	record.Pending = pending
	f.putState(record)
}

// removeAll deletes a fixture path. It is a named wrapper so the test files read
// as intent rather than as filesystem calls.
func removeAll(path string) error { return os.RemoveAll(path) }

// symlink creates a symbolic link, for the tests that ask what the exporter does
// with content the protocol cannot describe.
func symlink(target, link string) error { return os.Symlink(target, link) }

// ledgerPaths returns every path the durable record remembers an operation for.
func (f *fixture) ledgerPaths() []string {
	f.t.Helper()
	var paths []string
	for p := range f.state().Paths {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// requirePaths fails unless the result carries exactly these paths.
func requirePaths(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("paths:\n got %v\nwant %v", got, want)
	}
}

// requireBaselinePin fails unless the durable record is mid-baseline against
// commit: pinned to it, with no cursor, because a partially-sent commit is not
// yet accounted for.
func (f *fixture) requireBaselinePin(commit string) {
	f.t.Helper()
	state := f.state()
	if state.Baseline != commit || state.Commit != "" {
		f.t.Fatalf("state: baseline=%q commit=%q, want baseline %q and no cursor",
			state.Baseline, state.Commit, commit)
	}
}

// requireNoCursor fails unless the record claims to have sent nothing: no
// cursor, no pinned baseline, no branch, no reservation, and an empty path
// ledger. It is what Reset is supposed to leave behind.
func requireNoCursor(t *testing.T, record *State) {
	t.Helper()
	switch {
	case record.Commit != "", record.Baseline != "", record.Branch != "", record.Pending != nil:
		t.Fatalf("the record still claims a cursor: %+v", record)
	}
	if len(record.Paths) != 0 {
		t.Fatalf("the record still holds %d path ledger entries", len(record.Paths))
	}
}

// requireNotPortable fails for any path the portable policy would let travel. It
// is the assertion about the policy itself, made before any export runs, so a
// leak is attributed to the scope rule rather than to the exporter.
func requireNotPortable(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if vaultsync.Portable(p) {
			t.Errorf("%s is portable, so it would travel", p)
		}
	}
}

// requirePortableOnly fails for any path in what that is not portable content.
func requirePortableOnly(t *testing.T, what string, paths []string) {
	t.Helper()
	for _, p := range paths {
		if !vaultsync.Portable(p) {
			t.Errorf("%s names %q, which is not portable content", what, p)
		}
	}
}

// requireSkipped fails unless exactly these commits were skipped, in order.
func requireSkipped(t *testing.T, got, want []Skipped) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("skipped: got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("skipped[%d]: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// requirePredecessors fails unless a change names exactly these operations as
// its causal predecessors, which is the edge a peer uses to tell a change that
// had seen its own from one that had not.
func requirePredecessors(t *testing.T, change vaultsync.Change, want ...string) {
	t.Helper()
	if strings.Join(change.Predecessors, "|") != strings.Join(want, "|") {
		t.Fatalf("predecessors for %q: got %v, want %v", change.Path, change.Predecessors, want)
	}
}

// seedFiles is the vault every export test starts from: the fixture default
// content, minus nothing. Named here so a test that asserts the whole baseline
// says which files it expected and why.
func seedFiles() []string {
	return []string{"Alpha.md", "Beta.md", "Notes/Gamma.md", vaultfixture.UnrelatedNote, "brainnotes.toml"}
}

// sortedSeed is seedFiles in the order a baseline publishes them.
func sortedSeed() []string {
	files := seedFiles()
	sort.Strings(files)
	return files
}

package vaultimport

import (
	"errors"
	"sync"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// TestConcurrentImportsOfOnePathLeaveExactlyOneWinner is the race half of this
// session's verification, and the reason it is a test rather than a read of the
// locking document: two packages that both describe the same starting point for
// one note cannot both be right.
//
// Exactly one of them reaches the disk, and which one is genuinely a race. The
// import guard decides it: it is taken without waiting, before either package is
// read, so the loser is refused having touched nothing at all rather than
// discovering the disagreement halfway down a transaction.
//
// Refused is not resolved, and the second half of this test is the part that
// matters. Retrying the loser afterwards reaches ErrConflict — vault.FileTxn
// stages it against the content the winner committed and refuses it there, before
// anything is written — because the note now holds content its producer never
// saw, and that is a disagreement a person resolves rather than a write that
// quietly discards the winner. That refusal is the same one a second delivery
// would have met with no race at all, which is the point: serializing the two
// imports changes when the conflict is found, never whether.
func TestConcurrentImportsOfOnePathLeaveExactlyOneWinner(t *testing.T) {
	r := enroll(t)
	start := r.believes(t, "Alpha.md")
	const firstBody = "---\ntitle: Alpha\n---\n\nOne of two concurrent rewrites.\n"
	const secondBody = "---\ntitle: Alpha\n---\n\nThe other of two concurrent rewrites.\n"

	// Both packages are built up front, on this goroutine: a package is the
	// producer's artifact and building one concurrently would be racing the
	// fixture rather than the code under test.
	_, first := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(firstBody), start)
	})
	_, second := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(secondBody), start)
	})

	outcomes := applyConcurrently(t, r, first, second)
	books := tally(t, outcomes)
	if landed := books.applied + books.stranded; landed != 1 {
		t.Fatalf("%d of two conflicting packages reached the disk, want exactly 1", landed)
	}
	books.require(t, r)

	got := r.pair.Local.Read(t, "Alpha.md")
	if got != firstBody && got != secondBody {
		t.Fatalf("Alpha.md holds %q, which is neither package's content", got)
	}

	for _, refused := range books.refused {
		_, err := r.apply(t, refused.dir)
		requireIs(t, err, ErrConflict, "retrying the package that lost the race")
	}
	if now := r.pair.Local.Read(t, "Alpha.md"); now != got {
		t.Fatalf("retrying the loser changed Alpha.md from %q to %q", got, now)
	}
	books.require(t, r)
}

// TestConcurrentImportsOfDisjointPathsCostAtMostARetry pins what a concurrent
// import costs when the packages have nothing in common.
//
// They are never merged and never queued. Imports in this vault are serialized at
// the import guard, which is taken without waiting, so a second import that
// reaches it while the first holds it is refused as contention — and the property
// that matters is that the refusal loses nothing. Both notes end up holding
// exactly what their packages said, and the vault's books balance at every step.
//
// Disjoint paths buying no concurrency is the deliberate price of the guard, and
// what it buys back is this session's subject: the guard is what tells a recovery
// pass whether the unresolved attempt it can see belongs to a live process or a
// dead one. Per-path concurrency would make that question unanswerable, and a
// retry is cheap next to a rollback over somebody's newer edit.
func TestConcurrentImportsOfDisjointPathsCostAtMostARetry(t *testing.T) {
	r := enroll(t)
	const epsilonBody = "---\ntitle: Epsilon\n---\n\nTwo.\n"
	first := r.deliverNote(t, "Notes/Delta.md", deltaBody)
	second := r.deliverNote(t, "Notes/Epsilon.md", epsilonBody)
	byDir := map[string]packaged{first.dir: first, second.dir: second}

	outcomes := applyConcurrently(t, r, first.dir, second.dir)
	books := tally(t, outcomes)
	for _, refused := range books.refused {
		if errors.Is(refused.err, ErrConflict) {
			t.Fatalf("two disjoint packages were reported as a conflict: %v", refused.err)
		}
		// Refused at the safety door, which runs before any artifact exists.
		if note := byDir[refused.dir]; exists(t, r.pair.Local, note.path) {
			t.Fatalf("%s is on disk after an import that was refused before it began", note.path)
		}
	}
	books.require(t, r)

	// Whatever lost the race is retried, and the vault ends holding both notes.
	for _, refused := range books.refused {
		r.mustApply(t, refused.dir)
		books.applied++
	}
	books.refused = nil
	books.require(t, r)

	for _, note := range []packaged{first, second} {
		if got := r.pair.Local.Read(t, note.path); got != note.body {
			t.Fatalf("%s holds %q, want the packaged body", note.path, got)
		}
	}
	// An import whose snapshot was refused leaves its bytes uncommitted on
	// purpose, so this is the one assertion the race can legitimately suspend.
	if books.stranded == 0 {
		if dirty := dirtyInScope(t, r.pair.Local); len(dirty) != 0 {
			t.Fatalf("the vault is dirty after two imports: %v", dirty)
		}
	}
}

// packaged is one note and the package that carries it, so a test can ask what
// a given package was supposed to do after the fact.
type packaged struct {
	dir  string
	path string
	body string
}

// deliverNote builds and delivers a package creating one note.
func (r *replicas) deliverNote(t *testing.T, path, body string) packaged {
	t.Helper()
	_, dir := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write(path, []byte(body), vaultsync.Absent())
	})
	return packaged{dir: dir, path: path, body: body}
}

// outcome is one package's fate, carrying the package back so a test can retry
// exactly the one that was refused.
type outcome struct {
	dir string
	err error
}

// applyConcurrently applies every package at once and returns the outcomes in
// the order the packages were given.
//
// The clock is left at its default rather than wired to the fixture's:
// vaultfixture.Clock is not safe for concurrent use, and a receipt timestamp is
// not what either of these tests is about.
func applyConcurrently(t *testing.T, r *replicas, dirs ...string) []outcome {
	t.Helper()
	outcomes := make([]outcome, len(dirs))

	var wg sync.WaitGroup
	for i, dir := range dirs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Apply(r.root(), dir, Options{Command: "bn sync apply --test"})
			outcomes[i] = outcome{dir: dir, err: err}
		}()
	}
	wg.Wait()
	return outcomes
}

// ledger counts what a round of concurrent imports did, split by the only
// distinction that changes what the vault holds afterwards: whether the bytes
// reached the disk.
type ledger struct {
	// applied is the imports that finished, commit and receipt included.
	applied int
	// stranded is the imports whose content landed and whose snapshot did not,
	// so the vault holds them, the journal can undo them, and no commit covers
	// them.
	stranded int
	// refused is the imports that changed nothing and may be retried as they are.
	refused []outcome
}

// tally classifies each outcome.
//
// The snapshot question is asked before the busy question and that ordering is
// load-bearing, for the same reason classifyCommit's arms are ordered: a
// *SnapshotError unwraps to the engine refusal that caused it, so a snapshot
// blocked by a busy lock answers yes to both — and treating it as ordinary
// contention would report an import whose bytes are on this disk as one that
// never started.
func tally(t *testing.T, outcomes []outcome) *ledger {
	t.Helper()
	books := &ledger{}
	for _, o := range outcomes {
		switch {
		case o.err == nil:
			books.applied++
		case noteops.IsSnapshotFailure(o.err):
			books.stranded++
		// vault.IsTxnLockBusy is deliberately absent. A path lock that timed out
		// before the commit did change nothing, but the export ledger takes a lock
		// too, *after* the bytes are durable — so the predicate cannot tell the two
		// apart and would file a landed import as one that never started. A path
		// lock that genuinely times out here falls through to the loud arm below,
		// which is the honest answer to a wait this test did not expect.
		//
		// ErrImportBusy is the earliest of these refusals and the safest: the
		// import guard is taken before the package is read, so an import refused
		// there has not looked at the vault, let alone written to it. It is also
		// now the arm a concurrent import normally takes — the guard is upstream
		// of the safety lock, so the vaultgit refusal above is reachable only when
		// something outside this package holds that lock.
		case errors.Is(o.err, ErrImportBusy), vaultgit.IsBusy(o.err), errors.Is(o.err, ErrConflict):
			books.refused = append(books.refused, o)
		default:
			t.Fatalf("a concurrent import failed for an unrecognized reason: %v", o.err)
		}
	}
	return books
}

// require asserts that the vault's three records of an import agree with the
// tally, everywhere an import is recorded.
//
// Checking all three together is the point. A refused import that left a journal
// entry, or an applied one that left no ledger record, is the failure this
// package exists to prevent, and either would be invisible to a check that only
// looked at the notes. An import that landed without committing counts toward
// the recovery entry and the provenance record and not toward the commit, which
// is precisely the state that makes it recoverable rather than lost.
func (l *ledger) require(t *testing.T, r *replicas) {
	t.Helper()
	recovered := l.applied + l.stranded
	if entries := r.entries(t); len(entries) != recovered {
		t.Fatalf("the vault holds %d recovery entries, want %d", len(entries), recovered)
	}
	if imported := r.imported(t); len(imported) != recovered {
		t.Fatalf("the export ledger holds %d import records, want %d: %v", len(imported), recovered, imported)
	}
	if got, want := commitCount(t, r.pair.Local), l.applied+1; got != want {
		t.Fatalf("the vault holds %d commits, want %d", got, want)
	}
}

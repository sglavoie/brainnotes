package index

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/vault"
)

// This file covers the two claims the index makes about running alongside a
// writer, both of which are easy to state and impossible to inspect by reading
// the code:
//
//  1. A reader never observes a revision that was never on disk. The cache
//     hands out bytes it read at some earlier moment, so the only thing that
//     keeps it honest is that every set of bytes it can hand out was, at some
//     point, the file's actual content.
//  2. A writer does not slow down because readers are busy. The index
//     deliberately takes no lock in the vault hierarchy — not even the shared
//     guard — so a refresh must never appear in a writer's critical path. That
//     is a claim about wall-clock, so it is measured rather than argued.

const (
	concurrentReaders = 4
	writerRevisions   = 25
)

// mutation is one revision the writer produced: the exact bytes it wrote, so a
// reader's observation can be checked against the set of revisions that ever
// existed rather than against a re-read of a file that has moved on since.
type mutation struct {
	hash string
	rev  int
}

// writeRevision performs one real frontmatter mutation through vault.Note,
// which is the write path every mutating bn command shares: load, set a
// property, Save. Save takes the path lock and compare-and-swaps against the
// revision it parsed, so this is the exact shape readers have to stay honest
// against.
//
// The bytes are marshalled and hashed *before* Save writes them and handed to
// the caller to record first. Recording afterwards would leave a window in
// which a reader legitimately observes a revision the test does not yet know
// about, and the test would flake instead of the code being wrong.
func writeRevision(v *vault.Vault, abs string, rev int, record func(mutation)) error {
	n := vault.NewNote(v.Root, abs)
	front, err := n.Front()
	if err != nil {
		return fmt.Errorf("rev %d: front: %w", rev, err)
	}
	front.Set("rev", rev)
	fmCfg := &v.Config().Frontmatter
	data, err := n.Marshal(fmCfg)
	if err != nil {
		return fmt.Errorf("rev %d: marshal: %w", rev, err)
	}
	record(mutation{hash: vault.HashBytes(data), rev: rev})
	if err := n.Save(fmCfg); err != nil {
		return fmt.Errorf("rev %d: save: %w", rev, err)
	}
	if got := n.SourceHash(); got != vault.HashBytes(data) {
		return fmt.Errorf("rev %d: Save wrote %q, not the marshalled bytes %q", rev, got, vault.HashBytes(data))
	}
	return nil
}

// concurrencyVault builds a vault with one note the writer churns and a set of
// notes it never touches, so a reader's observation is checked on both axes:
// the churned note must show a revision that really existed, and every other
// note must show its one and only revision.
func concurrencyVault(t *testing.T) (*vault.Vault, string, observation) {
	t.Helper()

	files := map[string]string{"Notes/Target.md": "---\nstatus: wip\n---\ntarget\n"}
	for i := 0; i < 11; i++ {
		files[fmt.Sprintf("Notes/Fixed%02d.md", i)] = fmt.Sprintf("---\nrating: %d\nstatus: done\n---\nfixed %d\n", i, i)
	}
	v := testVault(t, files)
	backdate(t, v.Root, time.Minute)

	fixed := diskObservation(t, v)
	delete(fixed, "Notes/Target.md")
	return v, filepath.Join(v.Root, "Notes", "Target.md"), fixed
}

// TestConcurrentLoadsNeverObserveARevisionThatNeverExisted is claim (1).
//
// Readers run their own indexes and a shared one at the same time, so both the
// cross-process path (two indexes contending for one state file) and the
// in-process path (two goroutines through one Index.mu) are exercised against
// the same writer.
func TestConcurrentLoadsNeverObserveARevisionThatNeverExisted(t *testing.T) {
	v, target, fixed := concurrencyVault(t)

	var mu sync.Mutex
	// The revision that was on disk before the writer started counts as valid;
	// a reader that gets there first has observed nothing wrong.
	revisions := map[string]int{diskObservation(t, v)["Notes/Target.md"]: -1}
	record := func(m mutation) {
		mu.Lock()
		revisions[m.hash] = m.rev
		mu.Unlock()
	}
	revisionOf := func(hash string) (int, bool) {
		mu.Lock()
		defer mu.Unlock()
		rev, ok := revisions[hash]
		return rev, ok
	}

	var problems []string
	var problemsMu sync.Mutex
	fail := func(format string, args ...any) {
		problemsMu.Lock()
		defer problemsMu.Unlock()
		if len(problems) < 20 {
			problems = append(problems, fmt.Sprintf(format, args...))
		}
	}

	// How many distinct revisions the readers actually caught. Without it the
	// test could pass by never overlapping the writer at all, which is the one
	// way a concurrency test lies.
	observed := map[string]bool{}
	var observedMu sync.Mutex
	note := func(hash string) {
		observedMu.Lock()
		observed[hash] = true
		observedMu.Unlock()
	}

	// Every reader silences its logger. A walk that stats a file and reads it
	// after a rename has landed can record a size from one inode and bytes from
	// the next, which the state-file validator rejects on the next process
	// start; that is a legitimate self-heal under concurrent writes, not a
	// failure this test is asserting on.
	newReader := func() *Index {
		t.Helper()
		ix, err := New(v, WithLogger(nil))
		if err != nil {
			t.Fatalf("new index: %v", err)
		}
		return ix
	}

	shared := newReader()
	indexes := make([]*Index, concurrentReaders)
	for i := range indexes {
		if i < 2 {
			indexes[i] = shared
			continue
		}
		indexes[i] = newReader()
	}

	check := func(ix *Index) {
		result, err := ix.Load()
		if err != nil {
			fail("load returned an error, which only a walk failure may do: %v", err)
			return
		}
		if len(result.Failed) != 0 {
			fail("Failed = %v: an atomic rename must never make a note unreadable", result.Failed)
		}
		got := resultObservation(t, v.Root, result)
		for rel, hash := range fixed {
			if got[rel] != hash {
				fail("%s = %q, want its only revision %q", rel, got[rel], hash)
			}
		}
		hash, seen := got["Notes/Target.md"]
		if !seen {
			fail("the target note vanished from a load: it is only ever replaced by rename")
			return
		}
		note(hash)
		rev, known := revisionOf(hash)
		if !known {
			fail("target hash %q was never on disk: the cache handed out a revision the writer did not write", hash)
			return
		}
		// The bytes and the hash have to travel together. A cache that paired an
		// entry's hash with another entry's data would satisfy the check above
		// and still be returning a note nobody ever wrote.
		for _, n := range result.Notes {
			if filepath.Base(n.Path) != "Target.md" {
				continue
			}
			front, err := n.Front()
			if err != nil {
				fail("target front: %v", err)
				return
			}
			carried, present := front.Get("rev")
			if rev < 0 {
				if present {
					fail("target hashes to the pre-writer revision but carries rev=%v", carried)
				}
				return
			}
			if got := fmt.Sprint(carried); got != strconv.Itoa(rev) {
				fail("target hashes to revision %d but its frontmatter says rev=%s: the cached bytes and hash disagree", rev, got)
			}
		}
	}

	done := make(chan struct{})
	var readers sync.WaitGroup
	for _, ix := range indexes {
		readers.Add(1)
		go func(ix *Index) {
			defer readers.Done()
			for {
				select {
				case <-done:
					// One last pass after the writer has stopped, so a cache
					// that latched onto a stale revision forever is caught
					// rather than merely being tolerated mid-run.
					check(ix)
					return
				default:
					check(ix)
				}
			}
		}(ix)
	}

	var writeErr error
	for rev := 0; rev < writerRevisions; rev++ {
		if err := writeRevision(v, target, rev, record); err != nil {
			writeErr = err
			break
		}
	}
	close(done)
	readers.Wait()

	if writeErr != nil {
		t.Fatalf("writer: %v", writeErr)
	}
	for _, p := range problems {
		t.Error(p)
	}
	if len(observed) < 2 {
		t.Errorf("readers only ever saw %d distinct revision(s) of the target across %d writes: they did not overlap the writer, so nothing above was actually tested", len(observed), writerRevisions)
	}

	// And the settled state: after everything stops, a warm index must agree
	// with a direct read, so no reader ended up pinned to an old revision.
	final := newReader()
	compareObservations(t, "a concurrent writer finished", "warm index", diskObservation(t, v), resultObservation(t, v.Root, mustLoad(t, final)))
}

// TestWriterIsNotSlowedByConcurrentIndexActivity is claim (2), and it is the
// locking assertion: the index takes no lock in the vault hierarchy, so a
// writer's Save — which does take the path lock — must cost the same whether or
// not readers are walking the vault at the time.
//
// If a refresh ever started taking the shared vault guard, or blocking on the
// note's own path lock, this is the test that would notice; the inflation in
// that case is a multiple of a whole vault walk per write, not a few percent.
// The tolerance is therefore set wide on purpose. It is there to catch a
// structural regression, not to police scheduler noise, and a wide bound that
// never flakes is worth more here than a tight one that does.
func TestWriterIsNotSlowedByConcurrentIndexActivity(t *testing.T) {
	if runtime.GOMAXPROCS(0) < 4 {
		t.Skip("needs at least 4 usable cores: with readers and writer time-slicing on fewer, the measurement is about the scheduler rather than about locking")
	}

	v, target, _ := concurrencyVault(t)
	discard := func(mutation) {}

	timeWrites := func(from int) (time.Duration, error) {
		start := time.Now()
		for rev := from; rev < from+writerRevisions; rev++ {
			if err := writeRevision(v, target, rev, discard); err != nil {
				return 0, err
			}
		}
		return time.Since(start), nil
	}

	// Warm the filesystem caches first so the baseline is not paying for the
	// vault's first touch.
	if _, err := timeWrites(0); err != nil {
		t.Fatalf("warmup: %v", err)
	}
	baseline, err := timeWrites(writerRevisions)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	done := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < concurrentReaders; i++ {
		ix, err := New(v, WithLogger(nil))
		if err != nil {
			t.Fatalf("new index: %v", err)
		}
		readers.Add(1)
		go func(ix *Index) {
			defer readers.Done()
			for {
				select {
				case <-done:
					return
				default:
					if _, err := ix.Load(); err != nil {
						return
					}
				}
			}
		}(ix)
	}

	loaded, err := timeWrites(2 * writerRevisions)
	close(done)
	readers.Wait()
	if err != nil {
		t.Fatalf("under load: %v", err)
	}

	// A floor as well as a factor: at these sizes the baseline is small enough
	// that ordinary jitter is a large multiple of it, and the regression this
	// guards against is not subtle.
	allowed := 10*baseline + time.Second
	if loaded > allowed {
		t.Errorf("writer took %v with %d indexes loading concurrently, against a %v baseline (budget %v): the index is contending with the writer, which it can only do by taking a lock in the vault hierarchy",
			loaded, concurrentReaders, baseline, allowed)
	}
	t.Logf("writer: %v baseline, %v under %d concurrent index loads", baseline, loaded, concurrentReaders)
}

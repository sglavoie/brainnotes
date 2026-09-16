package index

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// RelDir is the vault-relative directory holding every file this package
// writes. Deleting it is the whole repair: `rm -rf .bnotes/index`.
const RelDir = ".bnotes/index"

const (
	stateFileName = "index.gob"
	lockFileName  = "index.lock"
)

// staleWindow is how recently a file may have been modified before its cached
// entry stops being trustworthy on stat alone.
//
// This is the sharpest correctness edge in the package, and it is deliberately
// not configurable. vault.Note carries a SourceHash precisely so a concurrent
// external edit is rejected rather than silently overwritten — but size and
// mtime cannot see an edit that lands inside a single mtime tick while
// preserving byte length. HFS+ and several network filesystems have
// one-second mtime granularity, and `sed -i s/draft/done/` is exactly that
// shape: same length, same second. Re-reading anything touched inside the
// window closes that hole for the cost of re-reading the handful of notes a
// person just edited. Making it a knob would only offer users a way to turn a
// silent-stale-read back on.
const staleWindow = 2 * time.Second

// FileError is one note the walk visited but could not turn into a usable
// note: an unreadable file or unparseable frontmatter. It mirrors the
// classification internal/web's loadVaultNotes applies, so the note set this
// package returns is the same set that path returns.
type FileError struct {
	// RelPath is the vault-relative, slash-separated path.
	RelPath string
	Err     error
}

func (e FileError) Error() string {
	return e.RelPath + ": " + e.Err.Error()
}

// Result is one Load: the healthy notes in walk order, the notes that could
// not be loaded, and whether the vault's contents differ from the state this
// index held before the walk.
type Result struct {
	Notes  []*vault.Note
	Failed []FileError
	// Changed reports whether this walk observed different note contents than
	// the previous one. A cold index — nothing on disk, or state that had to
	// be rebuilt — reports true, because there is no earlier observation to
	// claim equality with.
	Changed bool
}

// Option configures an Index.
type Option func(*Index)

// WithClock replaces the clock the stale window is measured against. Tests use
// it to place a file's mtime definitively inside or outside the window without
// sleeping.
func WithClock(now func() time.Time) Option {
	return func(ix *Index) {
		if now != nil {
			ix.now = now
		}
	}
}

// WithLogger replaces the sink failure classes are reported to. Passing nil
// silences the index entirely.
func WithLogger(logf func(format string, args ...any)) Option {
	return func(ix *Index) { ix.logf = logf }
}

// WithReader replaces the function used to read a note's bytes.
//
// It exists because a caller may hold a guarantee about an individual read that
// the index would otherwise drop on its behalf: internal/web bounds every note
// read so one stalled file cannot block an HTTP handler, and refuses a path it
// has already quarantined. Reading through the caller's function is what keeps
// that guarantee true once the index is doing the reading.
//
// A reader error is classified exactly like an unreadable file: the note lands
// in Result.Failed and its cached entry is dropped, so the next Load tries
// again rather than resurrecting a stale revision. Passing nil restores
// os.ReadFile.
func WithReader(read func(path string) ([]byte, error)) Option {
	return func(ix *Index) {
		if read == nil {
			read = os.ReadFile
		}
		ix.readFile = read
	}
}

// Index is a vault-scoped cache of note bytes. It is safe for concurrent use;
// Load serializes against itself so two callers cannot interleave a walk with
// a rebuild of the same in-process map.
// WithMemoryOnly disables all persistent cache reads and writes.
func WithMemoryOnly() Option {
	return func(ix *Index) { ix.memoryOnly = true }
}

type Index struct {
	memoryOnly bool
	vault      *vault.Vault
	root       string
	dir        string

	now  func() time.Time
	logf func(format string, args ...any)
	// readFile reads one note's bytes. It is os.ReadFile unless a caller
	// replaced it through WithReader.
	readFile func(path string) ([]byte, error)
	// parseNote turns one cached revision into a fresh note. It is always
	// vault.NewNoteFromSource in production; it is a field so the cost test can
	// count how many parses a load actually performs. That count is the whole
	// point of the seam: a warm load performs zero reads and one parse per
	// walked note, and nothing else in the package can observe the second half
	// of that sentence.
	parseNote func(root, path string, data []byte) (*vault.Note, error)

	mu sync.Mutex
	// entries maps a vault-relative slash path to the bytes last read from it.
	entries map[string]entry
	// primed records that disk state has already been consulted, so a missing
	// state file is read once rather than on every Load.
	primed bool
	// cold records that the current entries came from nothing — a first run or
	// a rebuild — which is what makes the next Load report Changed.
	cold   bool
	logged map[string]bool
}

// entry is one cached file revision. Data is treated as immutable once stored:
// every snapshot parses a fresh *vault.Note from it rather than sharing a Note
// across snapshots, because a Note is mutable and the callers above this
// package treat a returned snapshot as immutable. This cache therefore removes
// the read and never the parse — a warm load still re-parses every cached
// note's frontmatter — and the read is the whole win.
type entry struct {
	Rel     string
	Size    int64
	ModTime int64 // Unix nanoseconds, as reported by os.Stat.
	Hash    string
	Data    []byte
}

// New returns an index for v. It performs no I/O: the state file is read on
// the first Load.
func New(v *vault.Vault, opts ...Option) (*Index, error) {
	if v == nil {
		return nil, errors.New("index: nil vault")
	}
	if v.Root == "" {
		return nil, errors.New("index: vault has no root")
	}
	ix := &Index{
		vault:     v,
		root:      v.Root,
		dir:       Dir(v.Root),
		now:       time.Now,
		logf:      log.Printf,
		readFile:  os.ReadFile,
		parseNote: vault.NewNoteFromSource,

		entries: map[string]entry{},
		cold:    true,
		logged:  map[string]bool{},
	}
	for _, opt := range opts {
		opt(ix)
	}
	return ix, nil
}

// Dir returns the directory holding the index state for a vault at root.
func Dir(root string) string {
	return filepath.Join(root, filepath.FromSlash(RelDir))
}

// Load walks the vault and returns a fresh snapshot, reading from disk only the
// notes whose cached revision cannot be trusted.
//
// The walk is v.Walk(vault.WalkFilter{}, ...) and nothing else, because Walk is
// the only place that encodes hidden-directory pruning, the configured ignore
// list, the templates directory, and symlink rejection. A hand-rolled
// filepath.WalkDir here would fork all four and drift the moment one of them
// changes.
//
// Per visited file: stat it, and treat the cached entry as clean only when its
// size and mtime both match and that mtime is older than staleWindow.
// Otherwise re-read and re-hash. Entries the walk did not visit are dropped, so
// a deleted or newly-ignored note leaves the cache with it.
//
// Every walked note is then parsed into a fresh *vault.Note, clean entry or
// not. A warm load costs one stat and one frontmatter parse per note and zero
// reads; a cold one adds the read. What this cache removes is the read alone.
//
// The only error this returns is a walk failure, which is a vault problem
// rather than an index problem. Everything the index itself can get wrong
// degrades to a direct read.
//
// It must never be called inside a vault.FileTxn. The index takes no lock in
// the vault hierarchy, but path locks are non-re-entrant (see
// docs/internals/locking.md) and a refresh mid-transaction is the shape that
// turns a future read into a self-deadlock.
func (ix *Index) Load() (Result, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	ix.prime()

	now := ix.now()
	next := make(map[string]entry, len(ix.entries))
	result := Result{Changed: ix.cold}
	dirty := ix.cold

	err := ix.vault.Walk(vault.WalkFilter{}, func(note *vault.Note) error {
		rel, ok := ix.rel(note.Path)
		if !ok {
			return nil
		}
		info, statErr := os.Stat(note.Path)
		if statErr != nil {
			// A file the walk listed but cannot be stat'd is reported like any
			// other unloadable note and its cached entry is dropped, so the
			// next Load re-reads rather than resurrecting a stale revision.
			result.Failed = append(result.Failed, FileError{RelPath: rel, Err: statErr})
			dirty = true
			return nil
		}

		cached, hadCached := ix.entries[rel]
		fresh := cached
		if !hadCached || !clean(cached, info, now) {
			data, readErr := ix.readFile(note.Path)
			if readErr != nil {
				result.Failed = append(result.Failed, FileError{RelPath: rel, Err: readErr})
				if hadCached {
					dirty = true
				}
				return nil
			}
			fresh = entry{
				Rel: rel,
				// The size of the bytes actually read, not the size the stat
				// above reported. The two differ only when a concurrent writer
				// lands between the stat and the read, and taking the stat's
				// value there is wrong twice over: it records a size that does
				// not describe Data — which validEntry rejects, costing the
				// next process its warm start — and it can pair the new file's
				// size with the new file's mtime while holding the old bytes,
				// which is exactly the stale read clean() exists to prevent.
				// Recording len(data) makes the entry self-consistent, and a
				// torn pair then fails clean()'s size comparison against the
				// next fresh stat and is re-read.
				Size:    int64(len(data)),
				ModTime: info.ModTime().UnixNano(),
				Hash:    vault.HashBytes(data),
				Data:    data,
			}
			if !hadCached || cached.Hash != fresh.Hash {
				result.Changed = true
			}
			// Metadata alone can go stale without the contents changing — a
			// touch, or a rewrite to identical bytes. That is not Changed for a
			// caller, but it is worth persisting: writing the new mtime is what
			// stops the next Load from re-reading the same file forever.
			if !hadCached || cached.Size != fresh.Size || cached.ModTime != fresh.ModTime || cached.Hash != fresh.Hash {
				dirty = true
			}
		}

		next[rel] = fresh
		// A fresh Note per snapshot, parsed from the cached bytes — including
		// on a warm load, where the bytes were never re-read. The frontmatter
		// YAML parse therefore happens on every load of every note and is not
		// part of what this cache saves; sharing one Note across snapshots
		// would save it and would put a mutable object behind an
		// immutable-once-returned contract. BenchmarkWarmIndexLoad and
		// TestLoadCostsAreOneReadPerMissAndOneParsePerNote measure both halves.
		parsed, parseErr := ix.parseNote(ix.root, note.Path, fresh.Data)
		if parseErr != nil {
			result.Failed = append(result.Failed, FileError{RelPath: rel, Err: parseErr})
			return nil
		}
		result.Notes = append(result.Notes, parsed)
		return nil
	})
	if err != nil {
		return Result{}, fmt.Errorf("index: walk notes: %w", err)
	}

	for rel := range ix.entries {
		if _, kept := next[rel]; !kept {
			result.Changed = true
			dirty = true
		}
	}

	ix.entries = next
	ix.cold = false
	if dirty {
		ix.persist()
	}
	return result, nil
}

// Reset discards the in-process cache and the state file, so the next Load
// rebuilds from disk. It is the programmatic form of `rm -rf .bnotes/index`.
func (ix *Index) Reset() {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.entries = map[string]entry{}
	ix.cold = true
	// Unprimed rather than primed-and-empty, so the next Load consults disk
	// again. That keeps Reset honest for a caller that dropped the state file
	// out from under this process, and it is what makes a repeated-corruption
	// test able to re-observe the same failure class.
	ix.primed = false
	// Even discarding a cache file is a mutation of the vault's storage, so it
	// goes through the same preflight as every other one. A refusal simply leaves
	// the stale file alone, which the in-memory reset above has already made
	// harmless — the same outcome the ignored error below has always produced.
	if ix.memoryOnly {
		return
	}
	if err := vault.GuardMutation(ix.root, ix.dir); err == nil {
		_ = os.Remove(filepath.Join(ix.dir, stateFileName))
	}
}

// clean reports whether a cached entry may be trusted on stat alone: same
// size, same mtime, and an mtime old enough that no edit can still be landing
// inside its tick.
func clean(cached entry, info os.FileInfo, now time.Time) bool {
	if cached.Size != info.Size() || cached.ModTime != info.ModTime().UnixNano() {
		return false
	}
	// A clock that has gone backwards makes the age negative, which fails this
	// comparison and forces a re-read — the safe direction.
	return now.Sub(info.ModTime()) >= staleWindow
}

// rel converts an absolute note path to the vault-relative slash path used as
// a cache key, reporting false for anything that does not resolve underneath
// the root.
func (ix *Index) rel(abs string) (string, bool) {
	rel, err := filepath.Rel(ix.root, abs)
	if err != nil {
		return "", false
	}
	slash := filepath.ToSlash(rel)
	if slash == "." || slash == ".." || len(slash) >= 3 && slash[:3] == "../" {
		return "", false
	}
	return slash, true
}

// warnOnce reports a failure class at most once for the life of the process.
// The class, not the message, is the key: a vault whose state file is
// unreadable would otherwise log on every single Load.
func (ix *Index) warnOnce(class string, err error) {
	if ix.logf == nil || ix.logged[class] {
		return
	}
	ix.logged[class] = true
	ix.logf("index: %s; rebuilding from the vault (%v)", class, err)
}

package web

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/index"
	"github.com/sglavoie/sglavoie_brainnotes/internal/properties"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// noteSource produces the classified note load a snapshot is built from. The
// two implementations differ only in where the bytes come from — a direct read
// of every note, or internal/index's cache of the bytes it last read — so
// everything above this interface, including the fingerprint that gates every
// derived cache, is computed by the same code either way.
//
// It exists so the index can be switched off. A test that injects a noteLoader
// is asking for the direct path by construction: the index reads bytes itself
// and has no noteLoader to route a read through, so an index source that
// accepted one would silently ignore it.
type noteSource interface {
	loadNotes(v *vault.Vault, load noteLoader, timeout time.Duration, quarantined map[string]struct{}) (loadedVault, error)
}

// directNoteSource reads every note on every load. It is loadVaultNotes and
// nothing else.
type directNoteSource struct{}

func (directNoteSource) loadNotes(v *vault.Vault, load noteLoader, timeout time.Duration, quarantined map[string]struct{}) (loadedVault, error) {
	return loadVaultNotes(v, load, timeout, quarantined)
}

// indexNoteSource serves note bytes from internal/index, so a walk over an idle
// vault stats each note instead of re-reading and re-parsing it.
//
// The two guarantees the direct path holds are preserved rather than
// reimplemented: the index reads through this source's own reader, which
// refuses a quarantined path outright and bounds every other read by the same
// timeout, and the classified result is fingerprinted by fingerprintLoad — the
// same function, over the same records — so a fingerprint from one source is
// comparable with one from the other.
type indexNoteSource struct {
	ix *index.Index

	// timeout and quarantined are the per-load values the reader closure needs.
	// They are fields rather than arguments because the reader is bound once, at
	// index.New; loadNotes is only ever called with vaultState.mu held, so the
	// assignment cannot race a read.
	timeout     time.Duration
	quarantined map[string]struct{}
}

func newIndexNoteSource(v *vault.Vault) (*indexNoteSource, error) {
	source := &indexNoteSource{timeout: noteLoadTimeout}
	ix, err := index.New(v, index.WithReader(source.read))
	if err != nil {
		return nil, err
	}
	source.ix = ix
	return source, nil
}

// read is the reader the index calls for a note whose cached revision it cannot
// trust. It carries the bounded-read and quarantine rules loadVaultNotes
// applies, so moving the read into the index does not move it out from under
// them.
func (s *indexNoteSource) read(path string) ([]byte, error) {
	if _, quarantined := s.quarantined[path]; quarantined {
		return nil, errNoteLoadTimeout
	}
	return boundedReadFile(path, s.timeout)
}

func (s *indexNoteSource) loadNotes(v *vault.Vault, load noteLoader, timeout time.Duration, quarantined map[string]struct{}) (loadedVault, error) {
	if load != nil || s.ix == nil {
		return loadVaultNotes(v, load, timeout, quarantined)
	}
	if timeout <= 0 {
		timeout = noteLoadTimeout
	}
	s.timeout = timeout
	s.quarantined = quarantined

	loaded, err := s.ix.Load()
	if err != nil {
		return loadedVault{}, fmt.Errorf("web: walk notes: %w", err)
	}

	var result loadedVault
	for _, note := range loaded.Notes {
		rel := displayPath(note.Path, v.Root)
		// A cached entry is served without a read, so the reader's quarantine
		// check cannot be the only one: a note quarantined after it was last
		// cached would otherwise come back as healthy.
		if _, skip := quarantined[note.Path]; skip {
			result.skipped = append(result.skipped, diagnosticFileError{
				Path:  rel,
				Error: errNoteLoadTimeout.Error(),
			})
			continue
		}
		result.loaded = append(result.loaded, diagnosticLoadedNote{note: note, rel: rel})
	}
	for _, failed := range loaded.Failed {
		if errors.Is(failed.Err, errNoteLoadTimeout) {
			result.skipped = append(result.skipped, diagnosticFileError{
				Path:  failed.RelPath,
				Error: errNoteLoadTimeout.Error(),
			})
			abs := filepath.Join(v.Root, filepath.FromSlash(failed.RelPath))
			if _, already := quarantined[abs]; !already {
				result.timedOut = append(result.timedOut, abs)
			}
			continue
		}
		result.parseErrors = append(result.parseErrors, diagnosticFileError{
			Path:  failed.RelPath,
			Error: failed.Err.Error(),
		})
	}

	sortDiagnosticFileErrors(result.parseErrors)
	sortDiagnosticFileErrors(result.skipped)
	sort.Strings(result.timedOut)
	result.fingerprint = fingerprintLoad(result)
	return result, nil
}

type vaultSnapshot struct {
	notes    []*vault.Note
	graph    *query.Graph
	loadedAt time.Time
	// serial identifies this exact load. It keys derived caches so a rebuilt
	// snapshot never reuses intelligence computed from the previous notes.
	serial int64

	// parseErrors and skipped record the diagnostics gathered while loading this
	// snapshot: frontmatter/YAML parse failures and notes whose read timed out
	// (or were already quarantined). They ride along with the snapshot so callers
	// can surface a partial view without re-walking the vault.
	parseErrors []diagnosticFileError
	skipped     []diagnosticFileError
}

type vaultState struct {
	vault *vault.Vault
	ttl   time.Duration
	now   func() time.Time
	// load is nil in production: the note source picks its own read path. A
	// non-nil loader is a test injecting a blocking or failing read, which also
	// forces the direct source — see noteSource.
	load        noteLoader
	loadTimeout time.Duration
	// source decides where a snapshot's note bytes come from. It defaults to the
	// index and degrades to the direct path if the index cannot even be
	// constructed, because an index failure is never a request error.
	source noteSource

	mu       sync.Mutex
	snapshot vaultSnapshot
	serial   int64
	// loadFingerprint is the fingerprint of the load that last advanced serial.
	// A rebuild whose fingerprint matches it observed the same vault, so serial
	// holds and every derived cache keyed by it survives.
	loadFingerprint string
	// quarantined holds absolute paths whose reads timed out. It lives on the
	// state rather than the snapshot so it survives invalidate(); once a path
	// stalls it is never re-read for the life of the process.
	quarantined map[string]struct{}

	// propertyIndex caches the property intelligence derived from one snapshot
	// under one configuration generation, keyed by both so a note reload or a
	// configuration reload rebuilds it exactly once.
	propertyIndex    *properties.Index
	propertyIndexKey derivedCacheKey

	// relatedCorpus caches immutable recall documents and the static ranking
	// indexes derived from one snapshot under one configuration generation.
	relatedCorpus    *relatedCorpus
	relatedCorpusKey derivedCacheKey
}

// derivedCacheKey pairs a note load with the configuration generation that
// defined a derived cache's semantics.
type derivedCacheKey struct {
	snapshot   int64
	generation int64
}

// relatedCorpus is immutable after publication. documents, notePaths, and the
// ranker's static indexes are therefore safe to share across requests.
type relatedCorpus struct {
	documents []recall.Document
	notePaths []string
	ranker    *recall.Ranker
}

func newVaultState(v *vault.Vault, ttl time.Duration) (*vaultState, error) {
	if v == nil {
		return nil, fmt.Errorf("web: nil vault")
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("web: invalid ttl %s", ttl)
	}

	state := &vaultState{
		vault:       v,
		ttl:         ttl,
		now:         time.Now,
		loadTimeout: noteLoadTimeout,
		quarantined: make(map[string]struct{}),
		source:      directNoteSource{},
	}
	// index.New performs no I/O, so the only way it fails is a vault this
	// constructor already rejected. Falling back rather than propagating keeps
	// the package's rule intact: the index is an optimization, and no index
	// failure may become a request error.
	if source, err := newIndexNoteSource(v); err == nil {
		state.source = source
	}
	return state, nil
}

func (s *vaultState) currentSnapshot() (vaultSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	if s.snapshot.graph != nil && now.Sub(s.snapshot.loadedAt) < s.ttl {
		return s.snapshot, nil
	}

	result, err := s.source.loadNotes(s.vault, s.load, s.loadTimeout, s.quarantined)
	if err != nil {
		return vaultSnapshot{}, fmt.Errorf("web: load notes: %w", err)
	}

	// Quarantine paths that newly timed out so later refreshes skip them without
	// re-attempting the stalled read. The quarantine outlives snapshot
	// invalidation because it is stored on the state, not the snapshot.
	for _, p := range result.timedOut {
		s.quarantined[p] = struct{}{}
	}

	notes := make([]*vault.Note, 0, len(result.loaded))
	for _, item := range result.loaded {
		notes = append(notes, item.note)
	}

	// serial keys the derived caches, so it advances only when the load actually
	// observed a different vault. loadedAt refreshes unconditionally: it throttles
	// the walk itself, which must still run once per TTL to notice an edit. On an
	// idle vault this means a rebuild costs the walk and nothing more — the
	// property index and the related corpus live for the life of the process.
	if result.changedFrom(s.loadFingerprint) {
		s.serial++
		s.loadFingerprint = result.fingerprint
	}
	s.snapshot = vaultSnapshot{
		notes:       notes,
		graph:       query.BuildGraphForRoot(s.vault.Root, notes),
		loadedAt:    now,
		serial:      s.serial,
		parseErrors: result.parseErrors,
		skipped:     result.skipped,
	}
	return s.snapshot, nil
}

// propertyIndexFor returns the property intelligence for snapshot under the
// given configuration generation, building it at most once per (load,
// generation) pair. The index is immutable once returned, so concurrent
// requests share one value.
func (s *vaultState) propertyIndexFor(snapshot vaultSnapshot, fm *config.FrontmatterConfig, generation int64) *properties.Index {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := derivedCacheKey{snapshot: snapshot.serial, generation: generation}
	if s.propertyIndex != nil && s.propertyIndexKey == key {
		return s.propertyIndex
	}

	index := properties.Build(s.vault.Root, snapshot.notes, fm)
	s.propertyIndex = index
	s.propertyIndexKey = key
	return index
}

// relatedCorpusFor returns the recall corpus and prebuilt static ranking
// indexes for snapshot under one configuration generation. Request-specific
// clocks and recall signals are bound later through Ranker.WithOptions.
func (s *vaultState) relatedCorpusFor(snapshot vaultSnapshot, metadataProperties []string, generation int64) (*relatedCorpus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := derivedCacheKey{snapshot: snapshot.serial, generation: generation}
	if s.relatedCorpus != nil && s.relatedCorpusKey == key {
		return s.relatedCorpus, nil
	}

	documents, err := recall.ExtractCorpus(s.vault.Root, snapshot.notes, recall.ExtractOptions{
		MetadataProperties: append([]string(nil), metadataProperties...),
	})
	if err != nil {
		return nil, err
	}
	ranker, err := recall.BuildRanker(documents, snapshot.graph, recall.RankOptions{})
	if err != nil {
		return nil, err
	}
	notePaths := make([]string, len(documents))
	for index := range documents {
		notePaths[index] = documents[index].Path
	}

	corpus := &relatedCorpus{
		documents: documents,
		notePaths: notePaths,
		ranker:    ranker,
	}
	s.relatedCorpus = corpus
	s.relatedCorpusKey = key
	return corpus, nil
}

func (s *vaultState) invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.snapshot = vaultSnapshot{}
}

// refreshSnapshot forces a fresh bounded load and returns it, replacing the
// cached snapshot. The process-lifetime quarantine survives (it lives on the
// state, not the snapshot), so a note that has already timed out is skipped
// without a re-read and the load stays bounded. Content-addressed cockpit flows
// (fix preview/apply and the item triage lookup) call this so a concurrent
// on-disk edit is reflected in the generated task ids immediately instead of up
// to one TTL later; the read-only cockpit surfaces keep using the cached
// currentSnapshot so an end-to-end sweep only pays the one bounded wait.
func (s *vaultState) refreshSnapshot() (vaultSnapshot, error) {
	s.invalidate()
	return s.currentSnapshot()
}

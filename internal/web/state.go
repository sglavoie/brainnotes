package web

import (
	"fmt"
	"sync"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/properties"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

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
	vault       *vault.Vault
	ttl         time.Duration
	now         func() time.Time
	load        noteLoader
	loadTimeout time.Duration

	mu       sync.Mutex
	snapshot vaultSnapshot
	serial   int64
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

	return &vaultState{
		vault:       v,
		ttl:         ttl,
		now:         time.Now,
		load:        (*vault.Note).Load,
		loadTimeout: noteLoadTimeout,
		quarantined: make(map[string]struct{}),
	}, nil
}

func (s *vaultState) currentSnapshot() (vaultSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	if s.snapshot.graph != nil && now.Sub(s.snapshot.loadedAt) < s.ttl {
		return s.snapshot, nil
	}

	result, err := loadVaultNotes(s.vault, s.load, s.loadTimeout, s.quarantined)
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

	s.serial++
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

package web

import (
	"fmt"
	"sync"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

type vaultSnapshot struct {
	notes    []*vault.Note
	graph    *query.Graph
	loadedAt time.Time

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
	// quarantined holds absolute paths whose reads timed out. It lives on the
	// state rather than the snapshot so it survives invalidate(); once a path
	// stalls it is never re-read for the life of the process.
	quarantined map[string]struct{}
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

	s.snapshot = vaultSnapshot{
		notes:       notes,
		graph:       query.BuildGraphForRoot(s.vault.Root, notes),
		loadedAt:    now,
		parseErrors: result.parseErrors,
		skipped:     result.skipped,
	}
	return s.snapshot, nil
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

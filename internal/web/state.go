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
}

type vaultState struct {
	vault *vault.Vault
	ttl   time.Duration
	now   func() time.Time

	mu       sync.Mutex
	snapshot vaultSnapshot
}

func newVaultState(v *vault.Vault, ttl time.Duration) (*vaultState, error) {
	if v == nil {
		return nil, fmt.Errorf("web: nil vault")
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("web: invalid ttl %s", ttl)
	}

	return &vaultState{
		vault: v,
		ttl:   ttl,
		now:   time.Now,
	}, nil
}

func (s *vaultState) currentSnapshot() (vaultSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	if s.snapshot.graph != nil && now.Sub(s.snapshot.loadedAt) < s.ttl {
		return s.snapshot, nil
	}

	notes, err := s.vault.Notes()
	if err != nil {
		return vaultSnapshot{}, fmt.Errorf("web: load notes: %w", err)
	}

	s.snapshot = vaultSnapshot{
		notes:    notes,
		graph:    query.BuildGraphForRoot(s.vault.Root, notes),
		loadedAt: now,
	}
	return s.snapshot, nil
}

func (s *vaultState) invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.snapshot = vaultSnapshot{}
}

package vaultgit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

var (
	sharedMu       sync.Mutex
	sharedManagers = map[string]*Manager{}
)

// Shared returns the engine for a vault, reusing one Manager per vault root for
// the life of the process.
//
// Every mutation path in bn resolves its engine through here rather than opening
// one of its own. That is what makes the safety policy unavoidable instead of
// opt-in: a caller does not decide whether it has an engine, it asks for the one
// this vault has, and a new mutating code path gets the preflight and the
// snapshot by construction rather than by remembering to wire them.
//
// Reuse is not just an optimization. Open adjudicates a surviving recovery
// intent, which takes the safety lock; doing that on every note save would fork
// git for a question already answered, and on a `bn serve` handling a burst of
// edits it would serialize requests behind a lock they do not need.
//
// ErrUnmanaged is returned, and never cached, for a vault with no `.git`. It is
// re-derived from a single Lstat on each call so that a vault someone runs
// `git init` in starts being protected without restarting `bn serve`. A failure
// that is not ErrUnmanaged is not cached either: it describes a repository state
// that a person is expected to go and fix, and caching it would keep refusing
// after they had.
func Shared(ctx context.Context, vaultRoot string, opts Options) (*Manager, error) {
	root, err := filepath.Abs(vaultRoot)
	if err != nil {
		return nil, ErrUnmanaged
	}
	if _, err := os.Lstat(filepath.Join(root, ".git")); errors.Is(err, os.ErrNotExist) {
		// Drop any cached engine for a vault whose repository has gone away, so a
		// stale Manager cannot keep writing into a `.git` that is no longer there.
		sharedMu.Lock()
		delete(sharedManagers, root)
		sharedMu.Unlock()
		return nil, ErrUnmanaged
	}

	sharedMu.Lock()
	cached, ok := sharedManagers[root]
	sharedMu.Unlock()
	if ok {
		return cached, nil
	}

	manager, err := Open(ctx, root, opts)
	if err != nil {
		return nil, err
	}
	sharedMu.Lock()
	defer sharedMu.Unlock()
	// Another goroutine may have opened the same vault while this one was in
	// Open. Keeping the first winner means every caller in the process shares one
	// Manager, which is what the reuse argument above depends on.
	if existing, ok := sharedManagers[root]; ok {
		return existing, nil
	}
	sharedManagers[root] = manager
	return manager, nil
}

// ForgetShared drops the cached engine for a vault root. Tests use it so a
// fixture vault rebuilt at the same path is not served an engine that resolved
// the previous one.
func ForgetShared(vaultRoot string) {
	root, err := filepath.Abs(vaultRoot)
	if err != nil {
		return
	}
	sharedMu.Lock()
	defer sharedMu.Unlock()
	delete(sharedManagers, root)
}

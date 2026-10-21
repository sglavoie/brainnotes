package vaultimport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// A drain whose caller has already stopped applies nothing and says why.
func TestDrainContextStoppedBeforeStartAppliesNothing(t *testing.T) {
	r := enroll(t)
	r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	drained, err := DrainContext(ctx, r.root(), Options{Command: "bn sync drain --test", Now: r.clock.Now})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("DrainContext error = %v, want context.Canceled", err)
	}
	if drained != nil && len(drained.Applied) != 0 {
		t.Fatalf("a stopped drain applied %v", appliedIDs(drained))
	}
	if _, statErr := os.Stat(filepath.Join(r.root(), "Notes", "Delta.md")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("a stopped drain wrote the package's note: %v", statErr)
	}

	again := mustDrain(t, r)
	if len(again.Applied) != 1 {
		t.Fatalf("the next drain applied %v, want the one package", appliedIDs(again))
	}
}

// A stop raised while a package is being applied lets that package finish —
// content, journal and receipt — and stops before the next one, which stays in
// the channel untouched for the next drain.
func TestDrainContextStopsOnlyBetweenPackages(t *testing.T) {
	r := enroll(t)
	created, _ := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Notes/Delta.md", []byte(deltaBody), vaultsync.Absent())
	})
	edited, _ := r.deliver(t, func(b *vaultsync.Builder) {
		b.Write("Beta.md", []byte(betaOnceBody), r.believes(t, "Beta.md"))
	})
	betaBefore := r.pair.Local.Read(t, "Beta.md")

	// The stop is raised from inside the first application, once its content has
	// landed: the clock is read again for the bookkeeping that follows.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	landed := filepath.Join(r.root(), "Notes", "Delta.md")
	now := func() time.Time {
		if _, err := os.Stat(landed); err == nil {
			cancel()
		}
		return r.clock.Now()
	}

	drained, err := DrainContext(ctx, r.root(), Options{Command: "bn sync drain --test", Now: now})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("DrainContext error = %v, want context.Canceled", err)
	}
	if drained == nil {
		t.Fatal("a drain stopped part-way returned no report of what landed")
	}
	requireEqual(t, appliedIDs(drained), []string{created.PackageID}, "the packages applied before the stop")
	if drained.Unfinished != nil {
		t.Fatalf("the package applied before the stop was left unfinished: %v", drained.Unfinished)
	}
	if got := r.pair.Local.Read(t, "Beta.md"); got != betaBefore {
		t.Fatalf("the package after the stop changed Beta.md to %q", got)
	}

	// The package applied before the stop is still in the channel and is answered
	// from the ledger as a duplicate; only the one the stop skipped is new.
	again := mustDrain(t, r)
	var fresh []string
	for _, result := range again.Applied {
		if !result.Duplicate {
			fresh = append(fresh, result.PackageID)
		}
	}
	requireEqual(t, fresh, []string{edited.PackageID}, "the packages the next drain newly applied")
	if got := r.pair.Local.Read(t, "Beta.md"); got != betaOnceBody {
		t.Fatalf("the skipped package did not land on the next drain: Beta.md holds %q", got)
	}
}

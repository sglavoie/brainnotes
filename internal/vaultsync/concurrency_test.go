package vaultsync

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultfixture"
)

// racers is how many goroutines each test below starts. Small enough to stay
// inside the path lock's wait budget on a loaded machine, large enough that a
// lost update would show up rather than depending on a scheduler coincidence.
const racers = 8

// pollAttempts bounds the reader's spin in the publish/accept race. It is a
// bound on the test rather than on the protocol: the assertion is about what a
// reader sees, never about how quickly a publication finishes.
const pollAttempts = 2000

// countWinners reports how many racers succeeded, failing the test on any error
// other than the one losing this race is supposed to produce. An unexpected
// error is called out rather than counted as a loss: "eight racers, one winner"
// is only evidence if the other seven lost for the stated reason.
func countWinners(t *testing.T, errs []error, tolerated error) int {
	t.Helper()
	winners := 0
	for i, err := range errs {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, tolerated):
		default:
			t.Errorf("racer %d: unexpected error: %v", i, err)
		}
	}
	return winners
}

// soleWinner returns the one racer's result that succeeded, and additionally
// checks that every loser returned nothing — a refusal that still hands back a
// path is a caller invited to read a package it did not publish.
func soleWinner(t *testing.T, results []string, errs []error, tolerated error) string {
	t.Helper()
	won := ""
	for i, err := range errs {
		switch {
		case err == nil:
			if won != "" {
				t.Errorf("racer %d succeeded a second time, returning %q", i, results[i])
			}
			won = results[i]
		case errors.Is(err, tolerated):
			if results[i] != "" {
				t.Errorf("racer %d was refused but returned %q", i, results[i])
			}
		default:
			t.Errorf("racer %d: unexpected error: %v", i, err)
		}
	}
	if won == "" {
		t.Fatal("no racer succeeded")
	}
	return won
}

// TestConcurrentEstablishEnrollsOnce is the identity race: two processes — a
// CLI and a server, or two windows — enrolling one vault at the same moment. The
// vault must end up with exactly one identity, because a second one would be an
// identity the peer is not addressing.
func TestConcurrentEstablishEnrollsOnce(t *testing.T) {
	v := vaultfixture.New(t, vaultfixture.Options{Name: "mac"})
	clock := vaultfixture.NewClock(t)

	records := make([]*Enrollment, racers)
	errs := make([]error, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			records[i], errs[i] = Establish(v.Root, Options{Label: "mac", Now: clock.Now})
		}()
	}
	wg.Wait()

	if winners := countWinners(t, errs, ErrAlreadyEnrolled); winners != 1 {
		t.Fatalf("%d of %d concurrent Establish calls succeeded, want exactly 1", winners, racers)
	}

	loaded, err := Load(v.Root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for i, record := range records {
		if errs[i] == nil && !record.equal(loaded) {
			t.Errorf("the winning racer's record is not the one on disk:\n%+v\n%+v", record, loaded)
		}
	}
}

// TestConcurrentAdvanceEpochLosesNothing is the read-modify-write race on the
// enrollment record. Every advance must count: an epoch that moved once when two
// callers advanced it would leave this vault accepting packages produced against
// the history it just replaced.
func TestConcurrentAdvanceEpochLosesNothing(t *testing.T) {
	r := enroll(t)
	start := r.local.Epoch

	errs := make([]error, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = AdvanceEpoch(r.pair.Local.Root, Options{Now: r.clock.Tick})
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("racer %d: AdvanceEpoch: %v", i, err)
		}
	}
	loaded, err := Load(r.pair.Local.Root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := start + racers; loaded.Epoch != want {
		t.Errorf("Epoch = %d after %d concurrent advances, want %d", loaded.Epoch, racers, want)
	}
	if _, enrolled := loaded.Peer(r.peer.DeviceID); !enrolled {
		t.Error("a concurrent advance dropped the peer list")
	}
}

// TestConcurrentPublishOfOneIdentityPublishesOnce is the immutability guarantee
// under a race: the exclusive mkdir is what makes "published once" true, and a
// loser must neither replace the winner's bytes nor remove them on its way out.
func TestConcurrentPublishOfOneIdentityPublishesOnce(t *testing.T) {
	r := enroll(t)
	channel := r.channel(t)
	body := []byte("alpha body\n")
	manifest, payloads := r.draft(t, func(b *Builder) {
		b.Write("Alpha.md", body, Absent())
	})

	dirs := make([]string, racers)
	errs := make([]error, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dirs[i], errs[i] = Publish(channel, manifest, payloads)
		}()
	}
	wg.Wait()

	published := soleWinner(t, dirs, errs, ErrPackageExists)

	// The losers ran their whole refusal path while the winner was writing, so
	// this is the assertion that matters: the package is still complete.
	accepted, err := Accept(r.local, published)
	if err != nil {
		t.Fatalf("Accept after a publication race: %v", err)
	}
	data, err := ReadPayload(published, accepted, accepted.Changes()[0].Payload)
	if err != nil {
		t.Fatalf("ReadPayload: %v", err)
	}
	if string(data) != string(body) {
		t.Errorf("payload holds %q, want %q", data, body)
	}
}

// TestConcurrentPublishAndAcceptNeverSeesAPartialPackage is the transport's own
// race: a reader polling a channel while a package is being written must either
// find a complete package or find nothing it mistakes for one. A manifest
// written before its payloads would show up here as an accepted package whose
// bytes are missing.
func TestConcurrentPublishAndAcceptNeverSeesAPartialPackage(t *testing.T) {
	r := enroll(t)
	channel := r.channel(t)
	manifest, payloads := r.draft(t, func(b *Builder) {
		b.Write("Alpha.md", []byte("alpha body\n"), Absent())
		b.Write("Notes/Gamma.md", []byte("gamma body\n"), Absent())
	})
	dir := filepath.Join(channel, manifest.PackageID)

	var publishErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, publishErr = Publish(channel, manifest, payloads)
	}()

	// Poll until the package is readable, recording any answer other than
	// "complete" or "not yet" rather than failing on the spot.
	//
	// Nothing in this loop calls t.Fatal, and that is not a style preference: a
	// Fatal here would Goexit past wg.Wait, leaving the publisher running into a
	// finished test — where its own t.Errorf would panic the whole run with a
	// message about logging after completion, burying the failure that was
	// actually found. The reader's verdict is carried out of the loop instead and
	// reported once both goroutines have stopped.
	complete := false
	var sawMidPublication error
	for range pollAttempts {
		accepted, err := Accept(r.local, dir)
		if err == nil {
			complete = true
			if len(accepted.Payloads) != 2 {
				t.Errorf("accepted a package carrying %d payloads, want 2", len(accepted.Payloads))
			}
			break
		}
		if !errors.Is(err, ErrIncompletePackage) {
			sawMidPublication = err
			break
		}
	}
	wg.Wait()

	if publishErr != nil {
		t.Fatalf("Publish: %v", publishErr)
	}
	if sawMidPublication != nil {
		t.Fatalf("a reader polling a channel mid-publication saw: %v", sawMidPublication)
	}
	if !complete {
		// The publication has finished by now, so one more read settles it
		// rather than leaving the test's own timing as the verdict.
		if _, err := Accept(r.local, dir); err != nil {
			t.Fatalf("the package never became acceptable: %v", err)
		}
	}
}

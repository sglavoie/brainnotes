package vaultgit

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

// A broken file is reported once per broken version, not once per poll, and the
// last valid policy keeps running the vault the whole time.
func TestWatcherReportsOneDiagnosticPerInvalidConfigVersion(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setAutosave("30s")
	stub := &stubPolicySource{}
	stub.set(t, "30s")
	rec := &watchRecorder{}
	w := h.newReloadWatcher(rec, stub.source())
	ctx := context.Background()

	w.tick(ctx)
	rec.reset()

	stub.setInvalid("hash-broken-1", "config: autosave.min_interval \"5 minutes\" must be a Go duration")
	for i := 0; i < 5; i++ {
		h.advance(DefaultPollInterval)
		w.tick(ctx)
	}
	if got := rec.count(WatchEventConfigInvalid); got != 1 {
		t.Fatalf("five polls of one broken version produced %d diagnostics", got)
	}
	event := requireKind(t, rec, WatchEventConfigInvalid)
	if event.Policy != "30s" {
		t.Fatalf("the diagnostic reports policy %q, want the retained %q", event.Policy, "30s")
	}
	if event.Detail == "" {
		t.Fatal("the diagnostic carries no detail, so nobody can fix the file")
	}
	requirePolicy(t, h, "30s")

	// Editing a broken file into a differently broken file is a new version and is
	// reported again: the user is iterating and needs to see each answer.
	stub.setInvalid("hash-broken-2", "config: unknown keys: autosave.min_intervl")
	w.tick(ctx)
	if got := rec.count(WatchEventConfigInvalid); got != 2 {
		t.Fatalf("a second broken version produced %d diagnostics in total", got)
	}

	// And a source that cannot read its bytes at all has no fingerprint, so the
	// diagnostic itself stands in for one and is likewise reported once.
	stub.setUnreadable("read brainnotes.toml: permission denied")
	w.tick(ctx)
	w.tick(ctx)
	if got := rec.count(WatchEventConfigInvalid); got != 3 {
		t.Fatalf("an unreadable source produced %d diagnostics in total", got)
	}

	// Fixing the file recovers without a restart, and the recovery is announced.
	stub.set(t, "1m")
	w.tick(ctx)
	recovered := requireKind(t, rec, WatchEventConfigRecovered)
	if recovered.Policy != "1m" {
		t.Fatalf("the recovery event reports policy %q, want %q", recovered.Policy, "1m")
	}
	requireKind(t, rec, WatchEventConfigChanged)
	requirePolicy(t, h, "1m")

	// A valid file that stays valid says nothing further.
	before := rec.count(WatchEventConfigRecovered)
	w.tick(ctx)
	w.tick(ctx)
	if rec.count(WatchEventConfigRecovered) != before {
		t.Fatal("recovery was re-announced on a file that was never broken again")
	}
}

// Reverting a broken edit to the exact bytes that were valid before is a
// recovery even though the fingerprint is one the watcher has already acted on.
func TestWatcherRecoversWhenABrokenEditIsRevertedToTheSameBytes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setAutosave("1m")
	stub := &stubPolicySource{}
	stub.set(t, "1m")
	rec := &watchRecorder{}
	w := h.newReloadWatcher(rec, stub.source())
	ctx := context.Background()

	w.tick(ctx)
	valid := stub.snapshot

	stub.setInvalid("hash-broken", "config: autosave.min_interval \"\" requires a value")
	w.tick(ctx)
	requireKind(t, rec, WatchEventConfigInvalid)

	rec.reset()
	stub.snapshot, stub.err = valid, nil
	w.tick(ctx)
	requireKind(t, rec, WatchEventConfigRecovered)
	requireNoKind(t, rec, WatchEventConfigChanged)
	requirePolicy(t, h, "1m")
}

// The invalid file is itself a note-shaped thing in the vault: `brainnotes.toml`
// lives in the working tree, so a hand edit that breaks it is a pending change
// like any other. It must still be fingerprinted, reported, and snapshotted
// under the last valid policy — otherwise the one edit a user is most likely to
// get wrong is the one edit the vault would not preserve.
func TestWatcherSnapshotsAManuallyBrokenConfigUnderTheLastValidPolicy(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setAutosave("30s")
	rec := &watchRecorder{}
	h.writePolicyFile("30s\n")
	w := h.newReloadWatcher(rec, h.policySource())
	ctx := context.Background()

	// The valid file is committed like anything else in the vault.
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
	if !slices.Contains(h.tracked(), policyFile) {
		t.Fatalf("the configuration file is not in the history: %v", h.tracked())
	}

	rec.reset()
	h.advance(time.Second)
	h.writePolicyFile("5 minutes please\n")
	w.tick(ctx)

	requireKind(t, rec, WatchEventConfigInvalid)
	requireKind(t, rec, WatchEventPending)
	requirePolicy(t, h, "30s")

	// And the broken bytes reach the history on the retained policy's schedule,
	// so the user can recover what they typed.
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	event := requireKind(t, rec, WatchEventCommitted)
	if !slices.Contains(event.Paths, policyFile) {
		t.Fatalf("the snapshot does not name the broken configuration: %+v", event)
	}
	if got := rawGit(t, h.root, "show", "HEAD:"+policyFile); !strings.Contains(got, "5 minutes please") {
		t.Fatalf("the committed configuration is %q, not the bytes the user typed", got)
	}

	// Fixing it applies without a restart.
	rec.reset()
	h.advance(time.Second)
	h.writePolicyFile("off\n")
	w.tick(ctx)
	requireKind(t, rec, WatchEventConfigRecovered)
	requireKind(t, rec, WatchEventConfigChanged)
	requirePolicy(t, h, "off")
}

// --- Privacy ------------------------------------------------------------------

// The reload events are subject to the same rule as every other WatchEvent: a
// path, a count, an object name, a closed-vocabulary policy token, or a
// sanitized diagnostic, and never a note's bytes.
func TestWatcherReloadEventsCarryNoNoteContents(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setAutosave("30s")
	rec := &watchRecorder{}
	ctx := context.Background()

	const secret = "SENSITIVE-BODY-c0ffee-DO-NOT-LOG"
	h.write("private.md", "# heading\n\n"+secret+"\n")
	h.writePolicyFile("30s\n")
	w := h.newReloadWatcher(rec, h.policySource())
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)

	// A broken configuration is the event most likely to quote something back at
	// a person, and its diagnostic must still be one bounded line.
	rec.reset()
	h.advance(time.Second)
	h.writePolicyFile("not a duration\nand a second line\n\x00\x07\n")
	w.tick(ctx)
	invalid := requireKind(t, rec, WatchEventConfigInvalid)
	if strings.ContainsAny(invalid.Detail, "\n\r\x00") {
		t.Fatalf("the diagnostic is not a single sanitized line: %q", invalid.Detail)
	}

	// Then off, then on, so every reload event kind has been produced.
	h.advance(time.Second)
	h.writePolicyFile("off\n")
	w.tick(ctx)
	h.advance(time.Second)
	h.writePolicyFile("1m\n")
	w.tick(ctx)
	requireKind(t, rec, WatchEventConfigRecovered)
	requireKind(t, rec, WatchEventConfigChanged)

	for _, event := range rec.events {
		rendered := event.Message + "\x00" + event.Detail + "\x00" + event.Policy + "\x00" + strings.Join(event.Paths, "\x00")
		if strings.Contains(rendered, secret) {
			t.Fatalf("a %q event leaked a note body: %+v", event.Kind, event)
		}
	}
	for _, event := range rec.events {
		switch event.Kind {
		case WatchEventConfigChanged, WatchEventConfigInvalid, WatchEventConfigRecovered:
			switch event.Policy {
			case "off", "30s", "1m":
			default:
				t.Fatalf("a %q event reports policy %q, which is outside the closed vocabulary", event.Kind, event.Policy)
			}
		}
	}
}

// --- Without a source ---------------------------------------------------------

// A watcher with no PolicySource is the pre-reload watcher, exactly. Every other
// test in this package builds one, so this states the guarantee they rely on.
func TestWatcherWithoutAPolicySourceNeverChangesTheEnforcedPolicy(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.setAutosave("5m")
	rec := &watchRecorder{}
	w := h.newWatcher(rec)
	ctx := context.Background()

	h.write("note.md", "content")
	w.tick(ctx)
	h.advance(DebounceQuiet + time.Second)
	w.tick(ctx)
	requireKind(t, rec, WatchEventCommitted)
	requireNoKind(t, rec, WatchEventConfigChanged)
	requireNoKind(t, rec, WatchEventConfigInvalid)
	requirePolicy(t, h, "5m")
}

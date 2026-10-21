package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/vaultgit"
)

// cancelAfterEvent cancels once the guard logs one of the named events.
//
// It exists because cancelOnFirstWrite cannot be used here: the first record is
// `started`, written before the first poll begins, so cancelling on it cancels
// the context that poll's own Git calls run under. Naming the push instead is
// what lets a test say "one complete poll" — the push is the last thing a poll
// does, so by the time it is logged everything the poll was going to do has
// happened, and no sleep or poll-interval guess is involved.
type cancelAfterEvent struct {
	buf    *bytes.Buffer
	cancel context.CancelFunc
	after  []vaultgit.WatchEventKind
	fired  bool
}

func (w *cancelAfterEvent) Write(p []byte) (int, error) {
	n, err := w.buf.Write(p)
	if w.fired {
		return n, err
	}
	for _, kind := range w.after {
		if bytes.Contains(p, []byte(`"event":"`+string(kind)+`"`)) {
			w.fired = true
			w.cancel()
			break
		}
	}
	return n, err
}

// seedHistory gives the vault the state a restarting guard finds: a born branch
// with one engine commit on it, a remote to publish to, and a watch.json naming
// the policy that was last enforced. The record is written by the engine's own
// producers rather than by hand, so what the test sets up is what a running
// guard would have left behind.
func seedHistory(t *testing.T, root, published string) {
	t.Helper()
	initGit(t, root)

	remote := t.TempDir()
	runGit(t, remote, "-c", "init.defaultBranch=main", "init", "--bare", "-q")
	runGit(t, root, "remote", "add", "origin", remote)

	writeNote(t, root, "seed.md", "# Seed\n")
	ctx := context.Background()
	mgr, err := vaultgit.Open(ctx, root, vaultgit.Options{Autosave: parsePolicy(t, "5m")})
	if err != nil {
		t.Fatalf("open the engine: %v", err)
	}
	if _, err := mgr.CommitPending(ctx); err != nil {
		t.Fatalf("seed commit: %v", err)
	}
	if published != "5m" {
		// The completed transition: the previous guard published the new policy
		// because it had already taken the snapshot the switch owed.
		if err := mgr.SetAutosavePolicy(parsePolicy(t, published)); err != nil {
			t.Fatalf("publish %q: %v", published, err)
		}
	}
	if got := mgr.WatchStatus().AutosaveMinInterval; got != published {
		t.Fatalf("the seeded record publishes %q, want %q", got, published)
	}
}

func parsePolicy(t *testing.T, spelling string) config.AutosavePolicy {
	t.Helper()
	policy, err := config.ParseAutosaveMinInterval(spelling)
	if err != nil {
		t.Fatalf("parse %q: %v", spelling, err)
	}
	return policy
}

func writeNote(t *testing.T, root, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// runOnePoll starts the guard the way the LaunchAgent does and stops it once one
// poll has run to completion.
func runOnePoll(t *testing.T) []map[string]any {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stdout, stderr bytes.Buffer
	out := &cancelAfterEvent{
		buf:    &stdout,
		cancel: cancel,
		after:  []vaultgit.WatchEventKind{vaultgit.WatchEventPushed, vaultgit.WatchEventPushFailed},
	}
	if code := run(ctx, nil, out, &stderr); code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %q)", code, stderr.String())
	}
	return decodeLog(t, stdout.String())
}

func findEvent(records []map[string]any, kind vaultgit.WatchEventKind) map[string]any {
	for _, record := range records {
		if record["event"] == string(kind) {
			return record
		}
	}
	return nil
}

func loggedKinds(records []map[string]any) []string {
	kinds := make([]string, 0, len(records))
	for _, record := range records {
		if kind, ok := record["event"].(string); ok {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

// watchPolicy reads back the token the vault's durable record publishes, through
// the same accessor a status command would use.
func watchPolicy(t *testing.T, root string) string {
	t.Helper()
	mgr, err := vaultgit.Open(context.Background(), root, vaultgit.Options{})
	if err != nil {
		t.Fatalf("reopen the engine: %v", err)
	}
	return mgr.WatchStatus().AutosaveMinInterval
}

// A guard killed between the edit and the snapshot the edit owed leaves pending
// bytes outside history and a config file that already says `off`. Restarting is
// the only thing that will ever look at that vault again, so the restart is what
// has to finish the transition: take the owed snapshot under the outgoing
// policy, and only then let `off` become effective.
func TestRunResumesAnInterruptedOffTransitionOnRestart(t *testing.T) {
	root := newVault(t, "off")
	seedHistory(t, root, "5m")

	// The bytes the process that died never got to snapshot.
	writeNote(t, root, "pending.md", "# Pending\n")

	records := runOnePoll(t)
	if findEvent(records, vaultgit.WatchEventCommitted) == nil {
		t.Fatalf("the restart took no snapshot, saw %v", loggedKinds(records))
	}
	if !trackedContains(t, root, "pending.md") {
		t.Fatalf("pending.md is still outside history, tracked: %v", tracked(t, root))
	}

	// And only after that does the vault stop autosaving. The order is the whole
	// point: a `config-changed` without the commit before it would be the bug.
	changed := findEvent(records, vaultgit.WatchEventConfigChanged)
	if changed == nil {
		t.Fatalf("off never took effect, saw %v", loggedKinds(records))
	}
	if changed["autosave_min_interval"] != config.AutosaveOff {
		t.Fatalf("the policy that took effect is %v, want %q", changed["autosave_min_interval"], config.AutosaveOff)
	}
	if got := watchPolicy(t, root); got != config.AutosaveOff {
		t.Fatalf("the durable record still publishes %q, want %q", got, config.AutosaveOff)
	}
}

// The same file, the same startup path, and a vault whose switch to `off` already
// finished. Nothing is owed here, so a restart must not manufacture a commit —
// otherwise every guard restart on an off vault would snapshot the working tree
// the user turned autosaving off precisely to keep out of history.
func TestRunOnAnAlreadyOffVaultTakesNoSnapshotOnRestart(t *testing.T) {
	root := newVault(t, "off")
	seedHistory(t, root, config.AutosaveOff)
	before := runGit(t, root, "rev-parse", "HEAD")

	writeNote(t, root, "pending.md", "# Pending\n")

	records := runOnePoll(t)
	if findEvent(records, vaultgit.WatchEventCommitted) != nil {
		t.Fatalf("an already-off restart snapshotted the working tree, saw %v", loggedKinds(records))
	}
	if findEvent(records, vaultgit.WatchEventConfigChanged) != nil {
		t.Fatalf("an already-off restart re-applied its own policy, saw %v", loggedKinds(records))
	}
	if trackedContains(t, root, "pending.md") {
		t.Fatalf("pending.md was committed, tracked: %v", tracked(t, root))
	}
	if after := runGit(t, root, "rev-parse", "HEAD"); after != before {
		t.Fatalf("history moved from %s to %s", before, after)
	}
	if got := watchPolicy(t, root); got != config.AutosaveOff {
		t.Fatalf("the durable record publishes %q, want %q", got, config.AutosaveOff)
	}
}

// --- Logging ------------------------------------------------------------------

func decodeLog(t *testing.T, out string) []map[string]any {
	t.Helper()
	records := make([]map[string]any, 0, 8)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

// The reload events are the ones a person reads when a config edit did not do
// what they expected, so the policy has to be in the record — and an invalid
// file is a warning rather than an error, because the vault is still fully
// protected under the policy it retained.
func TestLogEventRendersTheReloadEvents(t *testing.T) {
	cases := []struct {
		event vaultgit.WatchEvent
		level string
	}{
		{vaultgit.WatchEvent{Kind: vaultgit.WatchEventConfigChanged, Vault: "/v", Message: "changed", Policy: "10m"}, "INFO"},
		{vaultgit.WatchEvent{Kind: vaultgit.WatchEventConfigRecovered, Vault: "/v", Message: "recovered", Policy: "5m"}, "INFO"},
		{vaultgit.WatchEvent{Kind: vaultgit.WatchEventConfigInvalid, Vault: "/v", Message: "invalid", Policy: "5m", Detail: "bad duration"}, "WARN"},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		logEvent(logger, tc.event, "")

		records := decodeLog(t, buf.String())
		if len(records) != 1 {
			t.Fatalf("%s produced %d records", tc.event.Kind, len(records))
		}
		record := records[0]
		if record["level"] != tc.level {
			t.Fatalf("%s logged at %v, want %s", tc.event.Kind, record["level"], tc.level)
		}
		if record["event"] != string(tc.event.Kind) {
			t.Fatalf("event field is %v, want %q", record["event"], tc.event.Kind)
		}
		if record["autosave_min_interval"] != tc.event.Policy {
			t.Fatalf("%s logged policy %v, want %q", tc.event.Kind, record["autosave_min_interval"], tc.event.Policy)
		}
	}

	// An event with no policy does not gain an empty field, because a log whose
	// shape depends on its content is one nobody can query.
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	logEvent(logger, vaultgit.WatchEvent{Kind: vaultgit.WatchEventStarted, Vault: "/v", Message: "started"}, "abc123")
	if _, present := decodeLog(t, buf.String())[0]["autosave_min_interval"]; present {
		t.Fatalf("an unrelated event carried a policy field: %q", buf.String())
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
)

// The guard's startup and its half of live reload.
//
// internal/vaultgit is tested against an injected policy source because it is
// deliberately ignorant of the file format. These are the tests of the thing
// that supplies it: a real brainnotes.toml on disk, read and decoded the way the
// running process reads and decodes it.

// newVault writes a vault whose brainnotes.toml holds the given autosave
// spelling, and points $BNOTES_VAULT at it.
func newVault(t *testing.T, autosave string) string {
	t.Helper()
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("BNOTES_VAULT", root)
	writeConfig(t, root, "[autosave]\nmin_interval = \""+autosave+"\"\n")
	return root
}

func writeConfig(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, config.FileName), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", config.FileName, err)
	}
}

func loadVault(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load(config.LoadOptions{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// initGit turns the vault into one the engine will watch, or skips.
func initGit(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{
		{"-c", "init.defaultBranch=main", "init", "-q"},
		{"config", "user.name", "Test User"},
		{"config", "user.email", "test@example.invalid"},
	} {
		runGit(t, root, args...)
	}
}

// runGit runs git the way a person would, and skips the test on a machine that
// has none rather than failing it.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	exe, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not available on PATH")
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// tracked lists every path the vault's history tip holds.
func tracked(t *testing.T, root string) []string {
	t.Helper()
	out := runGit(t, root, "ls-tree", "-r", "--name-only", "--full-tree", "HEAD")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func trackedContains(t *testing.T, root, want string) bool {
	t.Helper()
	for _, path := range tracked(t, root) {
		if path == want {
			return true
		}
	}
	return false
}

// --- policySource -------------------------------------------------------------

// The ordinary case: valid bytes in, a fingerprint and a resolved policy out.
func TestPolicySourceReportsTheResolvedPolicy(t *testing.T) {
	root := newVault(t, "10m")
	source := policySource(loadVault(t))

	snapshot, err := source()
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	if snapshot.Policy.Canonical != "10m" {
		t.Fatalf("resolved %q, want %q", snapshot.Policy.Canonical, "10m")
	}
	if snapshot.Policy.Disabled {
		t.Fatal("a duration resolved as disabled")
	}
	if snapshot.Hash == "" {
		t.Fatal("a readable file produced no fingerprint")
	}

	// The fingerprint is of the file, so an edit that changes the policy changes
	// it — that is what makes the watcher act at all.
	writeConfig(t, root, "[autosave]\nmin_interval = \"off\"\n")
	next, err := source()
	if err != nil {
		t.Fatalf("source after edit: %v", err)
	}
	if !next.Policy.Disabled || next.Policy.Canonical != config.AutosaveOff {
		t.Fatalf("resolved %+v, want off", next.Policy)
	}
	if next.Hash == snapshot.Hash {
		t.Fatal("two different files share a fingerprint")
	}

	// And an edit that does not touch the meaning still changes it, which the
	// watcher absorbs by comparing policies rather than trusting the hash alone.
	writeConfig(t, root, "# a comment\n[autosave]\nmin_interval = \"off\"\n")
	third, err := source()
	if err != nil {
		t.Fatalf("source after comment: %v", err)
	}
	if third.Hash == next.Hash {
		t.Fatal("a changed file kept its fingerprint")
	}
	if third.Policy != next.Policy {
		t.Fatalf("a comment changed the policy: %+v", third.Policy)
	}
}

// An unparseable value is refused, and the snapshot still carries the hash so
// the watcher can tell one broken version from the next.
func TestPolicySourceFingerprintsAnInvalidVersion(t *testing.T) {
	root := newVault(t, "5m")
	source := policySource(loadVault(t))

	writeConfig(t, root, "[autosave]\nmin_interval = \"5 minutes\"\n")
	first, err := source()
	if err == nil {
		t.Fatal("an unparseable interval was accepted")
	}
	if first.Hash == "" {
		t.Fatal("an invalid version has no fingerprint, so it would be reported on every poll")
	}
	if first.Policy != (config.AutosavePolicy{}) {
		t.Fatalf("a refused version still reported a policy: %+v", first.Policy)
	}

	// Polling the same broken file twice yields the same fingerprint, which is
	// exactly the property the once-per-version diagnostic depends on.
	again, err := source()
	if err == nil {
		t.Fatal("the second poll accepted what the first refused")
	}
	if again.Hash != first.Hash {
		t.Fatal("the same bytes produced two fingerprints")
	}

	// The whole file is validated, not just [autosave]: a version `bn` would
	// refuse is one the guard refuses too.
	writeConfig(t, root, "[autosave]\nmin_interval = \"5m\"\nnot_a_key = 1\n")
	if _, err := source(); err == nil {
		t.Fatal("a config with an unknown key was accepted")
	}

	// Below the minimum is refused for the same reason it is in `bn`: a floor
	// under 30s is a commit per keystroke.
	writeConfig(t, root, "[autosave]\nmin_interval = \"1s\"\n")
	if _, err := source(); err == nil {
		t.Fatal("an interval under the minimum was accepted")
	}
}

// With no bytes there is nothing to fingerprint, and saying so is what lets the
// watcher fall back to the diagnostic for deduplication.
func TestPolicySourceReportsAnUnreadableFileWithoutAFingerprint(t *testing.T) {
	root := newVault(t, "5m")
	source := policySource(loadVault(t))

	if err := os.Remove(filepath.Join(root, config.FileName)); err != nil {
		t.Fatalf("remove config: %v", err)
	}
	snapshot, err := source()
	if err == nil {
		t.Fatal("a missing config was accepted")
	}
	if snapshot.Hash != "" {
		t.Fatalf("unreadable bytes produced fingerprint %q", snapshot.Hash)
	}
	if !strings.Contains(err.Error(), config.FileName) {
		t.Fatalf("the diagnostic does not name the file: %v", err)
	}
}

// The vault a guard watches is fixed by the single-instance lock it holds inside
// it. An environment that changes under the running process must not silently
// move it to another vault's configuration.
func TestPolicySourceStaysOnTheVaultResolvedAtStartup(t *testing.T) {
	newVault(t, "10m")
	source := policySource(loadVault(t))

	other := t.TempDir()
	writeConfig(t, other, "[autosave]\nmin_interval = \"off\"\n")
	t.Setenv("BNOTES_VAULT", other)

	snapshot, err := source()
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	if snapshot.Policy.Canonical != "10m" {
		t.Fatalf("the source followed $BNOTES_VAULT to %q", snapshot.Policy.Canonical)
	}
}

// The hash is the vault's own content hash, so the fingerprint the guard
// computes is the one the rest of the system would compute for those bytes.
func TestPolicySourceFingerprintIsTheVaultContentHash(t *testing.T) {
	root := newVault(t, "5m")
	source := policySource(loadVault(t))

	snapshot, err := source()
	if err != nil {
		t.Fatalf("source: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, config.FileName))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if want := vault.HashBytes(data); snapshot.Hash != want {
		t.Fatalf("fingerprint %q, want %q", snapshot.Hash, want)
	}
}

// --- Startup ------------------------------------------------------------------

// A file that does not decode at startup is fatal. There is no last valid policy
// to retain yet, and a guard that invented one would be enforcing a floor the
// vault never asked for.
func TestRunRefusesToStartOnAnInvalidConfig(t *testing.T) {
	root := newVault(t, "5m")
	initGit(t, root)
	writeConfig(t, root, "[autosave]\nmin_interval = \"nope\"\n")

	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), &stdout, &stderr); code != 1 {
		t.Fatalf("exit code %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "bn-vault-guard:") {
		t.Fatalf("no diagnostic on stderr: %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("a refused startup logged events: %q", stdout.String())
	}
}

// A vault with no Git history is a supported vault with nothing to watch, and it
// exits 0 so the LaunchAgent does not restart it forever.
func TestRunExitsCleanlyWhenTheVaultHasNoHistory(t *testing.T) {
	newVault(t, "5m")

	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "no content history") {
		t.Fatalf("stderr does not explain the exit: %q", stderr.String())
	}
}

// cancelOnFirstWrite cancels as soon as the guard logs anything, which is the
// signal-driven shutdown without the signal and without a sleep: the first
// record is written from the loop's own goroutine, so the cancellation lands
// while the loop is running rather than before it starts.
type cancelOnFirstWrite struct {
	buf    *bytes.Buffer
	cancel context.CancelFunc
	fired  bool
}

func (w *cancelOnFirstWrite) Write(p []byte) (int, error) {
	n, err := w.buf.Write(p)
	if !w.fired {
		w.fired = true
		w.cancel()
	}
	return n, err
}

// A guard runs its configuration through startup, enters the loop, and leaves it
// on cancellation — the whole path the LaunchAgent exercises, ending in the
// clean exit that stops it being restarted.
func TestRunStartsUnderTheConfiguredPolicyAndStopsOnCancellation(t *testing.T) {
	root := newVault(t, "off")
	initGit(t, root)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stdout, stderr bytes.Buffer
	out := &cancelOnFirstWrite{buf: &stdout, cancel: cancel}
	if code := run(ctx, out, &stderr); code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %q)", code, stderr.String())
	}

	events := decodeLog(t, stdout.String())
	if len(events) == 0 {
		t.Fatal("the guard logged nothing at all")
	}
	if events[0]["event"] != string(vaultgit.WatchEventStarted) {
		t.Fatalf("the first event is %v, want %q", events[0]["event"], vaultgit.WatchEventStarted)
	}
	if last := events[len(events)-1]; last["event"] != string(vaultgit.WatchEventStopped) {
		t.Fatalf("the last event is %v, want %q", last["event"], vaultgit.WatchEventStopped)
	}
}

// The single-instance lock is per vault: a second guard reports and exits rather
// than doubling every scan and racing the first for the safety lock.
func TestRunRefusesASecondGuardOnTheSameVault(t *testing.T) {
	root := newVault(t, "5m")
	initGit(t, root)

	release, acquired, err := vault.TryLockPath(filepath.Join(root, guardLockRelPath))
	if err != nil {
		t.Fatalf("TryLockPath: %v", err)
	}
	if !acquired {
		t.Fatal("the lock was already held in a fresh vault")
	}
	defer release()

	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), &stdout, &stderr); code != 1 {
		t.Fatalf("exit code %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "another guard") {
		t.Fatalf("stderr does not explain the refusal: %q", stderr.String())
	}
}

// --- Restarting across a switch to off ------------------------------------------

// The unit tests in internal/vaultgit drive the watcher through an injected
// policy source, which can hand it any policy at any moment. That is the wrong
// shape for the case these two cover, because the case is about what startup
// itself decides: the guard reads brainnotes.toml *before* it opens the engine,
// so a file that already says `off` makes the engine start disabled, and from
// there no reload can tell an interrupted transition from a finished one. These
// run the real startup path against a real file to prove which of the two the
// vault gets.

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
	if code := run(ctx, out, &stderr); code != 0 {
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
		logEvent(logger, tc.event)

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
	logEvent(logger, vaultgit.WatchEvent{Kind: vaultgit.WatchEventStarted, Vault: "/v", Message: "started"})
	if _, present := decodeLog(t, buf.String())[0]["autosave_min_interval"]; present {
		t.Fatalf("an unrelated event carried a policy field: %q", buf.String())
	}
}

// Command bn-vault-guard keeps a BrainNotes vault's content history current
// while nothing is running `bn`.
//
// It is a private companion process, not a supported command. It takes no flags
// and defines no policy of its own: the vault it watches comes from the same
// resolution `bn` uses ($BNOTES_VAULT, then an upward search for
// brainnotes.toml), and every deadline it obeys is either a fixed constant in
// internal/vaultgit or the vault's own `[autosave] min_interval`.
//
// That one setting is the only thing here a person can tune, and it is
// deliberately the only one: it says how often the guard may add a commit of its
// own, not how quickly it reacts to a change. The quiet period, the
// maximum-latency bound, the poll interval, and the push interval stay fixed,
// because a knob on any of those would be a knob on how much of the vault is
// recoverable.
//
// It is also the one setting this process re-reads while running. The config
// file is polled before every scan, so `bn config set autosave.min_interval` and
// a hand edit both apply within one poll and neither needs the LaunchAgent
// restarted. A version that does not decode is refused and logged once, and the
// last valid policy stays in force until the file is valid again — the vault is
// never left without a policy, and a half-typed edit never becomes one. An
// invalid file at *startup* is still fatal, because there is no last valid
// policy to fall back on.
//
// It is installed by `just install` and run by a LaunchAgent. `bn --help` does
// not mention it and neither does the user documentation.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
)

// guardLockRelPath is the single-instance lock. It sits beside the per-note
// locks under `.bnotes/locks/`, which the vault `.gitignore` already excludes,
// so the guard's own coordination file can never become a pending change the
// guard would then try to commit.
const guardLockRelPath = ".bnotes/locks/vault-guard.lock"

func main() {
	// SIGINT and SIGTERM cancel the context rather than killing the process, so
	// the poll in flight finishes and the loop stops between units of work. That
	// is the whole of graceful shutdown here: every unit is one Manager call, and
	// none of them can be interrupted halfway into an inconsistent state — the
	// safety lock is released by the deferred release and an interrupted snapshot
	// is already covered by the fsynced intent record.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Stdout, os.Stderr))
}

func run(ctx context.Context, stdout, stderr io.Writer) int {
	cfg, err := config.Load(config.LoadOptions{})
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-guard: %v\n", err)
		return 1
	}

	// Single instance per vault, not per machine. Two guards on two different
	// vaults are a perfectly reasonable arrangement; two on the same vault would
	// double every scan and race each other for the safety lock on every poll.
	// The lock is wait-free: a second guard reports and exits rather than
	// queueing behind the first one forever.
	release, acquired, err := vault.TryLockPath(filepath.Join(cfg.Vault, guardLockRelPath))
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-guard: the single-instance lock could not be taken: %v\n", err)
		return 1
	}
	defer release()
	if !acquired {
		fmt.Fprintf(stderr, "bn-vault-guard: another guard is already watching %s\n", cfg.Vault)
		return 1
	}

	// The one policy this process carries, and it is the vault's rather than its
	// own: how long the guard must wait between two autosaves of its own making.
	// Every other deadline it obeys is still a fixed constant in internal/vaultgit.
	//
	// This is the startup value. It stays the enforced one only until the first
	// poll re-reads the file, which is what makes an edit to `[autosave]` apply
	// without restarting the guard.
	mgr, err := vaultgit.Open(ctx, cfg.Vault, vaultgit.Options{Autosave: cfg.AutosavePolicy()})
	if err != nil {
		if errors.Is(err, vaultgit.ErrUnmanaged) {
			// A vault with no Git repository is a supported BrainNotes vault; it just
			// has nothing for this process to do. Exiting non-zero would make the
			// LaunchAgent restart it forever.
			fmt.Fprintf(stderr, "bn-vault-guard: %s has no content history to watch\n", cfg.Vault)
			return 0
		}
		fmt.Fprintf(stderr, "bn-vault-guard: %v\n", err)
		return 1
	}

	// Structured because these lines are read by a health script and by a person
	// grepping a log file, and JSON is the format where "which paths changed" is
	// still answerable a month later. Content-free because the log lives in
	// ~/Library/Logs, which is neither encrypted nor covered by the vault's own
	// privacy boundary: every field written here is a path, a count, an object
	// name, or a sanitized diagnostic, and WatchEvent is the type that guarantees
	// it.
	logger := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	notifier := newNotifier()

	watcher := vaultgit.NewWatcher(mgr, vaultgit.WatcherOptions{
		Observe: func(event vaultgit.WatchEvent) {
			logEvent(logger, event)
			notifier.consider(event)
		},
		Policy: policySource(cfg),
	})
	if err := watcher.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "bn-vault-guard: %v\n", err)
		return 1
	}
	return 0
}

// policySource is the guard's half of live reload: it owns the file format so
// internal/vaultgit does not have to.
//
// It re-reads `brainnotes.toml` from the path the startup load resolved rather
// than re-resolving the vault, because the vault a guard watches is fixed for
// its lifetime — it holds a single-instance lock inside that vault — and a
// $BNOTES_VAULT that changed under a running process must not silently move it.
//
// The whole file is decoded rather than just its `[autosave]` table, and that is
// deliberate: it is the same validation `bn` and the server apply, so a version
// either of those would refuse is one the guard refuses too. What it returns is
// only the resolved policy, since that is the only part of the configuration
// this process acts on.
//
// The hash is filled in before the decode is attempted, so an invalid version is
// still identified by its bytes and reported once rather than once per poll. It
// is the *composite* fingerprint, covering both configuration layers and whether
// the overlay exists at all, because the guard acts on the merged policy: an
// `autosave.min_interval` written into brainnotes.local.toml is one this process
// reads and must therefore be one it notices changing.
func policySource(cfg *config.Config) vaultgit.PolicySource {
	root, origin := cfg.Vault, cfg.VaultOrigin
	return func() (vaultgit.PolicySnapshot, error) {
		sources, err := config.ReadSources(root)
		if err != nil {
			// No bytes, so no fingerprint. The watcher falls back to the diagnostic
			// for deduplication, which is stable for as long as this failure is.
			return vaultgit.PolicySnapshot{}, err
		}
		snapshot := vaultgit.PolicySnapshot{Hash: sources.Fingerprint().Composite}
		loaded, err := config.DecodeSources(root, origin, sources)
		if err != nil {
			return snapshot, err
		}
		snapshot.Policy = loaded.AutosavePolicy()
		return snapshot, nil
	}
}

// logEvent renders one WatchEvent as a structured record.
//
// The attribute set is fixed rather than derived from which fields happen to be
// populated, minus the empty ones: a log whose shape changes with its content
// is one nobody can write a query against.
func logEvent(logger *slog.Logger, event vaultgit.WatchEvent) {
	attrs := []any{
		slog.String("event", string(event.Kind)),
		slog.String("vault", event.Vault),
	}
	if event.PathCount > 0 {
		attrs = append(attrs, slog.Int("path_count", event.PathCount))
	}
	if len(event.Paths) > 0 {
		attrs = append(attrs, slog.Any("paths", event.Paths))
	}
	if event.Commit != "" {
		attrs = append(attrs, slog.String("commit", event.Commit))
	}
	if event.Branch != "" {
		attrs = append(attrs, slog.String("branch", event.Branch))
	}
	if event.Attempts > 0 {
		attrs = append(attrs, slog.Int("attempts", event.Attempts))
	}
	if event.Policy != "" {
		attrs = append(attrs, slog.String("autosave_min_interval", event.Policy))
	}
	if event.Detail != "" {
		attrs = append(attrs, slog.String("detail", event.Detail))
	}

	level := slog.LevelInfo
	switch event.Kind {
	case vaultgit.WatchEventBlocked, vaultgit.WatchEventError:
		level = slog.LevelError
	case vaultgit.WatchEventPushFailed, vaultgit.WatchEventPushFailing:
		level = slog.LevelWarn
	case vaultgit.WatchEventConfigInvalid:
		// A warning rather than an error: the vault is still fully protected under
		// the last valid policy, and the thing that needs fixing is a file the user
		// is in the middle of editing.
		level = slog.LevelWarn
	case vaultgit.WatchEventBusy, vaultgit.WatchEventPending:
		level = slog.LevelDebug
	}
	logger.LogAttrs(context.Background(), level, event.Message, toAttrs(attrs)...)
}

func toAttrs(values []any) []slog.Attr {
	attrs := make([]slog.Attr, 0, len(values))
	for _, value := range values {
		if attr, ok := value.(slog.Attr); ok {
			attrs = append(attrs, attr)
		}
	}
	return attrs
}

// notifier raises a desktop notification for the two conditions a person has to
// act on: the vault has latched fail-closed state, and the remote has been
// refusing the history for long enough that it is no longer a blip.
//
// Everything else is log-only. A notification for an ordinary snapshot would
// train the user to dismiss the ones that matter without reading them, which is
// the failure mode this whole plan exists to avoid.
type notifier struct{}

func newNotifier() *notifier { return &notifier{} }

func (n *notifier) consider(event vaultgit.WatchEvent) {
	switch event.Kind {
	case vaultgit.WatchEventBlocked:
		// Deliberately no paths. The rest of this system may log a filename; a
		// notification is rendered on a lock screen and in a shared-screen banner,
		// and the count plus "run the health check" is enough to act on.
		body := event.Message
		if event.PathCount > 0 {
			body = fmt.Sprintf("%s (%d path(s) involved)", body, event.PathCount)
		}
		notifyDesktop("BrainNotes vault blocked", body)
	case vaultgit.WatchEventPushFailing:
		notifyDesktop(
			"BrainNotes vault not reaching the remote",
			fmt.Sprintf("%d consecutive push attempts failed; local snapshots are still being taken", event.Attempts),
		)
	}
}

// Command bn-vault-sync keeps an enrolled BrainNotes vault exchanging changes
// with its peer while nothing is running `bn`.
//
// It is the second private companion, beside bn-vault-guard, and it is a
// separate process rather than another loop inside that one on purpose: the
// guard protects a vault whether or not it synchronizes with anything, and a
// vault that synchronizes still needs protecting when this process is not
// installed. They share no state, run on different schedules, and take different
// locks.
//
// Three ordinary modes, and the same work under all of them:
//
//	bn-vault-sync           run until stopped, one bounded pass every interval
//	bn-vault-sync -once     run one bounded pass, print it as JSON, exit
//	bn-vault-sync -check    print where synchronization has got to, exit
//	bn-vault-sync -version  print the build identifier, exit
//
// The explicit -inventory, -restore-prepare, -review and -accept-review modes
// are the offline recovery boundary documented in
// docs/internals/restore-reenrollment.md. They never run a companion worker.
//
// `-check` is the one an operator or a health script runs, and its exit status
// is the answer: 0 when the vault will make progress on its own, 1 when it needs
// a person, and 2 when it could not be read at all. The distinction between the
// last two is the point — a vault nobody can read is not a healthy vault, and a
// vault waiting on a conflict is not a broken one.
//
// The vault comes from the same resolution `bn` uses ($BNOTES_VAULT, then an
// upward search for brainnotes.toml). There is no configuration: the interval,
// the backoff, and the disconnection threshold are fixed constants in
// internal/vaultsyncd, because every one of them is a statement about how much
// divergence between two replicas is acceptable rather than a preference.
//
// A vault that is not enrolled is not an error and does not stop the loop. It is
// the ordinary state of almost every vault, a pass against one is a single
// status read, and enrollment is something a person does to a vault this process
// may already be watching — so it says so once and keeps looking, rather than
// exiting and needing to be started again afterwards.
//
// It is installed by `just install` and run by a LaunchAgent. `bn --help` does
// not mention it and neither does the user documentation.
//
// The LaunchAgent restarts the loop whenever it exits non-zero (KeepAlive with
// SuccessfulExit false). A loop that finds another companion already holding the
// vault exits 2, the same status -once gives for that refusal and the guard
// gives for its own duplicate: non-zero so the agent keeps retrying until the
// vault is free, rather than exiting 0 and leaving it unsynchronized after the
// other copy stops.
//
// Version skew. The loop writes its build identifier (internal/buildinfo) into
// companion.json on every pass and logs it on the "started" record. `-check`
// compares the recorded build with its own and, when both are known and differ,
// reports build_mismatch in its JSON and a warning on stderr: the installed
// binary is not the one the LaunchAgent is running, which a restart of the agent
// fixes. The mismatch does not change the exit status — it is not a reason the
// vault will fail to make progress.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"regexp"
	"syscall"

	"github.com/sglavoie/sglavoie_brainnotes/internal/buildinfo"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultrecovery"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsyncd"
)

// Exit statuses. They are named because `-check`'s are an interface: a health
// script switches on them, and changing one silently would change what that
// script believes about a vault.
const (
	exitOK = 0
	// exitNeedsPerson is `-check`'s "this vault will not fix itself", and every
	// other mode's ordinary failure.
	exitNeedsPerson = 1
	// exitUnreadable is "no answer could be produced": bad flags, an unresolvable
	// vault, or another companion already holding this one — in every mode,
	// including the installed loop.
	exitUnreadable = 2
)

func main() {
	// SIGINT and SIGTERM cancel the context rather than killing the process, so
	// the unit in flight finishes and the loop stops between units. Every unit is
	// one call into a package that is already crash-consistent, so this buys
	// tidiness rather than safety — but it is free.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

type modeOptions struct {
	version        bool
	check          bool
	once           bool
	inventory      bool
	prepareRestore bool
	recoverImport  bool
	reviewPath     string
	acceptPath     string
	// authorizeDeletion is a deprecated compatibility option for older scripts.
	authorizeDeletion string
}

// fullCommitID retains validation of the legacy one-shot compatibility option.
var fullCommitID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// buildID is this binary's build identifier. It is a variable so a test can be
// a different build from the companion record it reads.
var buildID = buildinfo.ID

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	options, code, proceed := parseModeOptions(args, stderr)
	if !proceed {
		return code
	}
	if options.version {
		fmt.Fprintf(stdout, "bn-vault-sync %s\n", buildID())
		return exitOK
	}
	cfg, err := config.Load(config.LoadOptions{})
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	return options.run(ctx, cfg.Vault, stdout, stderr)
}

func parseModeOptions(args []string, stderr io.Writer) (modeOptions, int, bool) {
	flags := flag.NewFlagSet("bn-vault-sync", flag.ContinueOnError)
	flags.SetOutput(stderr)
	version := flags.Bool("version", false, "print the build identifier and exit")
	check := flags.Bool("check", false, "report where synchronization has got to and exit")
	once := flags.Bool("once", false, "run one bounded pass and exit")
	inventory := flags.Bool("inventory", false, "print a portable restore-review inventory and exit")
	prepareRestore := flags.Bool("restore-prepare", false, "quarantine restored synchronization state under a fresh restore identity and print its inventory")
	recoverImport := flags.Bool("recover-import", false, "finish an interrupted import or owed conflict revision while the companion is stopped, and exit")
	reviewPath := flags.String("review", "", "compare this vault with a peer inventory file and print a review plan")
	acceptPath := flags.String("accept-review", "", "apply an explicitly reviewed plan while both peer services are stopped")
	authorizeDeletion := flags.String("authorize-deletion", "", "deprecated compatibility option; regular-file deletions now publish automatically")
	if err := flags.Parse(args); err != nil {
		// `-h` is a question this command can answer, and the flag set has already
		// answered it — on stderr, because SetOutput above sends every refusal
		// there and the usage is one, which keeps stdout carrying only the JSON a
		// `-check` or `-once` produces. Reporting it as "no answer could be
		// produced" would put a 2 in a health script's log for somebody who asked
		// for the usage.
		if errors.Is(err, flag.ErrHelp) {
			return modeOptions{}, exitOK, false
		}
		return modeOptions{}, exitUnreadable, false
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "bn-vault-sync: unexpected positional arguments")
		return modeOptions{}, exitUnreadable, false
	}
	options := modeOptions{
		version:        *version,
		check:          *check,
		once:           *once,
		inventory:      *inventory,
		prepareRestore: *prepareRestore,
		recoverImport:  *recoverImport,
		reviewPath:     *reviewPath,
		acceptPath:     *acceptPath,

		authorizeDeletion: *authorizeDeletion,
	}
	if options.selectedCount() > 1 {
		fmt.Fprintln(stderr, "bn-vault-sync: pass only one mode")
		return modeOptions{}, exitUnreadable, false
	}
	if options.authorizeDeletion != "" {
		if !options.once {
			fmt.Fprintln(stderr, "bn-vault-sync: -authorize-deletion is only accepted with -once")
			return modeOptions{}, exitUnreadable, false
		}
		if !fullCommitID.MatchString(options.authorizeDeletion) {
			fmt.Fprintln(stderr, "bn-vault-sync: -authorize-deletion needs a full lowercase commit ID (git rev-parse <commit>)")
			return modeOptions{}, exitUnreadable, false
		}
	}
	return options, exitOK, true
}

func (o modeOptions) selectedCount() int {
	count := 0
	for _, selected := range []bool{o.version, o.check, o.once, o.inventory, o.prepareRestore, o.recoverImport, o.reviewPath != "", o.acceptPath != ""} {
		if selected {
			count++
		}
	}
	return count
}

func (o modeOptions) run(ctx context.Context, root string, stdout, stderr io.Writer) int {
	switch {
	case o.check:
		return runCheck(ctx, root, stdout, stderr)
	case o.once:
		return runOnce(ctx, root, o.authorizeDeletion, stdout, stderr)
	case o.inventory:
		return runInventory(root, stdout, stderr)
	case o.prepareRestore:
		return runPrepareRestore(ctx, root, stdout, stderr)
	case o.recoverImport:
		return runRecoverImport(root, stdout, stderr)
	case o.reviewPath != "":
		return runReview(root, o.reviewPath, stdout, stderr)
	case o.acceptPath != "":
		return runAcceptReview(root, o.acceptPath, stdout, stderr)
	}
	return runLoop(ctx, root, stdout, stderr)
}

func runInventory(root string, stdout, stderr io.Writer) int {
	inventory, err := vaultrecovery.BuildInventory(root)
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	if err := writeJSON(stdout, inventory); err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	return exitOK
}

func runPrepareRestore(ctx context.Context, root string, stdout, stderr io.Writer) int {
	release, acquired, err := vault.TryLockPath(vaultsyncd.LockPath(root))
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	if !acquired {
		fmt.Fprintln(stderr, "bn-vault-sync: stop the synchronization companion before preparing a restore review")
		return exitNeedsPerson
	}
	defer release()
	operationID, err := vaultsync.NewRestoreOperationID()
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitNeedsPerson
	}
	if _, err := vaultsync.PrepareRestoreReviewContext(ctx, root, operationID, nil); err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitNeedsPerson
	}
	return runInventory(root, stdout, stderr)
}

func runReview(root, path string, stdout, stderr io.Writer) int {
	peer, err := vaultrecovery.ReadInventory(path)
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	plan, err := vaultrecovery.BuildPlan(root, peer, nil)
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitNeedsPerson
	}
	if err := writeJSON(stdout, plan); err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	return exitOK
}

func runAcceptReview(root, path string, stdout, stderr io.Writer) int {
	plan, err := vaultrecovery.ReadPlan(path)
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	release, acquired, err := vault.TryLockPath(vaultsyncd.LockPath(root))
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	if !acquired {
		fmt.Fprintln(stderr, "bn-vault-sync: stop the synchronization companion before accepting a restore review")
		return exitNeedsPerson
	}
	defer release()
	if err := vaultrecovery.ApplyPlan(root, plan, nil); err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitNeedsPerson
	}
	result, err := vaultrecovery.BuildInventory(root)
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	if err := writeJSON(stdout, result); err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	return exitOK
}

// runRecoverImport runs import recovery alone, for a vault quarantined by a
// restore review: a companion pass runs no worker under the marker, and
// re-enrollment refuses while an interrupted import is pending. It holds the
// companion lock like every review mode, and exits 1 when work recovery cannot
// finish by itself — a held conflict — is still left.
func runRecoverImport(root string, stdout, stderr io.Writer) int {
	release, acquired, err := vault.TryLockPath(vaultsyncd.LockPath(root))
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	if !acquired {
		fmt.Fprintln(stderr, "bn-vault-sync: stop the synchronization companion before recovering an import")
		return exitNeedsPerson
	}
	defer release()
	report, err := vaultrecovery.RecoverImport(root)
	if report != nil {
		if writeErr := writeJSON(stdout, report); writeErr != nil {
			err = errors.Join(err, writeErr)
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitNeedsPerson
	}
	if report.Remaining != "" {
		fmt.Fprintf(stderr, "bn-vault-sync: %s\n", report.Remaining)
		return exitNeedsPerson
	}
	return exitOK
}

// runCheck answers the operator's question and says nothing else.
//
// It takes no lock, so it is answerable while the companion is mid-drain — which
// is when somebody is most likely to ask.
func runCheck(ctx context.Context, root string, stdout, stderr io.Writer) int {
	status, err := vaultsyncd.Read(ctx, root, vaultsyncd.ReadOptions{Build: buildID()})
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	if err := writeJSON(stdout, status); err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	if status.BuildMismatch {
		fmt.Fprintf(stderr, "bn-vault-sync: warning: the companion last ran as build %s but this binary is build %s; restart the LaunchAgent so the installed build runs\n",
			buildinfo.Short(status.CompanionBuild), buildinfo.Short(buildID()))
	}
	if status.NeedsPerson() {
		return exitNeedsPerson
	}
	return exitOK
}

// runOnce is the mode for a machine where the companion is not installed, and
// for a person who wants the exchange to happen now.
//
// A worker that failed is reported as a failure of the run, because somebody is
// watching this one: the whole point of running it by hand is to find out.
//
// authorizeDeletion is the deprecated -authorize-deletion value. Committed
// regular-file deletions now publish automatically, so it approves nothing; it
// is still validated and passed through only so older scripts that supply it
// keep working, and it is never persisted.
func runOnce(ctx context.Context, root, authorizeDeletion string, stdout, stderr io.Writer) int {
	opts := vaultsyncd.Options{Build: buildID()}
	if authorizeDeletion != "" {
		opts.AuthorizeDeletions = []string{authorizeDeletion}
	}
	pass, err := vaultsyncd.Once(ctx, root, opts)
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		if errors.Is(err, vaultsyncd.ErrAlreadyRunning) {
			return exitUnreadable
		}
		return exitNeedsPerson
	}
	if err := writeJSON(stdout, pass); err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	if pass.Failed() {
		return exitNeedsPerson
	}
	return exitOK
}

// runLoop is the installed mode.
//
// A companion already holding the vault is exitUnreadable here exactly as it is
// for -once; see the package documentation for why that is non-zero.
func runLoop(ctx context.Context, root string, stdout, stderr io.Writer) int {
	// Structured for bn-vault-guard's reasons, and content-free for the same one:
	// this log lives outside the vault's privacy boundary, so every field written
	// here is a count, a state, a package identity, or a sanitized diagnostic.
	// The exception is Event.Paths, which carries note paths and carries them
	// deliberately — a conflict nobody can locate is a conflict nobody resolves.
	logger := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	build := buildID()
	err := vaultsyncd.Run(ctx, root, vaultsyncd.Options{
		Build:   build,
		Observe: func(event vaultsyncd.Event) { logEvent(logger, event, build) },
	})
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		if errors.Is(err, vaultsyncd.ErrAlreadyRunning) {
			return exitUnreadable
		}
		return exitNeedsPerson
	}
	return exitOK
}

// writeJSON renders one value as an indented object with a trailing newline,
// which is what a person piping this into `jq` or reading it in a terminal
// expects.
func writeJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("render the report: %w", err)
	}
	return nil
}

// logEvent renders one event as a structured record.
//
// The attribute set is fixed rather than derived from which fields happen to be
// populated, minus the empty ones: a log whose shape changes with its content is
// one nobody can write a query against.
//
// The build identifier rides on the "started" record only, for bn-vault-guard's
// reason: it is constant for the process.
func logEvent(logger *slog.Logger, event vaultsyncd.Event, build string) {
	attrs := []any{
		slog.String("event", string(event.Kind)),
		slog.String("vault", event.Vault),
	}
	if event.Kind == vaultsyncd.EventStarted {
		attrs = append(attrs, slog.String("build", build))
	}
	attrs = appendString(attrs, "worker", string(event.Worker))
	attrs = appendString(attrs, "state", string(event.State))
	attrs = appendCount(attrs, "packages", event.Packages)
	attrs = appendCount(attrs, "changed", event.Changed)
	attrs = appendCount(attrs, "held", event.Held)
	attrs = appendCount(attrs, "conflicts", event.Conflicts)
	attrs = appendCount(attrs, "awaiting", event.Awaiting)
	attrs = appendCount(attrs, "failures", event.Failures)
	if len(event.Paths) > 0 {
		attrs = append(attrs, slog.Any("paths", event.Paths))
	}
	attrs = appendString(attrs, "package_id", event.PackageID)
	attrs = appendString(attrs, "subject", event.Subject)
	attrs = appendString(attrs, "detail", event.Detail)
	if !event.NextAttemptAfter.IsZero() {
		attrs = append(attrs, slog.Time("next_attempt_after", event.NextAttemptAfter))
	}
	logger.LogAttrs(context.Background(), levelFor(event.Kind), event.Message, toAttrs(attrs)...)
}

// levelFor decides how loud one event is.
//
// The two that need a person are errors, a held package is a warning because it
// will not clear on its own either, and a deferral is debug because a healthy
// vault emits one every time it has nothing to do.
func levelFor(kind vaultsyncd.EventKind) slog.Level {
	switch kind {
	case vaultsyncd.EventFailed, vaultsyncd.EventProblem, vaultsyncd.EventRejected:
		return slog.LevelError
	case vaultsyncd.EventHeld:
		return slog.LevelWarn
	case vaultsyncd.EventDeferred:
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

func appendString(attrs []any, key, value string) []any {
	if value == "" {
		return attrs
	}
	return append(attrs, slog.String(key, value))
}

func appendCount(attrs []any, key string, value int) []any {
	if value == 0 {
		return attrs
	}
	return append(attrs, slog.Int(key, value))
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

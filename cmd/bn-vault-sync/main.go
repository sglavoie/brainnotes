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
// Three modes, and the same work under all of them:
//
//	bn-vault-sync           run until stopped, one bounded pass every interval
//	bn-vault-sync -once     run one bounded pass, print it as JSON, exit
//	bn-vault-sync -check    print where synchronization has got to, exit
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
	"syscall"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
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
	// vault, or another companion already holding this one.
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

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("bn-vault-sync", flag.ContinueOnError)
	flags.SetOutput(stderr)
	check := flags.Bool("check", false, "report where synchronization has got to and exit")
	once := flags.Bool("once", false, "run one bounded pass and exit")
	if err := flags.Parse(args); err != nil {
		// `-h` is a question this command can answer, and the flag set has already
		// answered it — on stderr, because SetOutput above sends every refusal
		// there and the usage is one, which keeps stdout carrying only the JSON a
		// `-check` or `-once` produces. Reporting it as "no answer could be
		// produced" would put a 2 in a health script's log for somebody who asked
		// for the usage.
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUnreadable
	}
	if *check && *once {
		fmt.Fprintln(stderr, "bn-vault-sync: -check and -once ask different questions; pass one of them")
		return exitUnreadable
	}

	cfg, err := config.Load(config.LoadOptions{})
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}

	switch {
	case *check:
		return runCheck(ctx, cfg.Vault, stdout, stderr)
	case *once:
		return runOnce(ctx, cfg.Vault, stdout, stderr)
	}
	return runLoop(ctx, cfg.Vault, stdout, stderr)
}

// runCheck answers the operator's question and says nothing else.
//
// It takes no lock, so it is answerable while the companion is mid-drain — which
// is when somebody is most likely to ask.
func runCheck(ctx context.Context, root string, stdout, stderr io.Writer) int {
	status, err := vaultsyncd.Read(ctx, root, vaultsyncd.ReadOptions{})
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
	}
	if err := writeJSON(stdout, status); err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
		return exitUnreadable
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
func runOnce(ctx context.Context, root string, stdout, stderr io.Writer) int {
	pass, err := vaultsyncd.Once(ctx, root, vaultsyncd.Options{})
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
func runLoop(ctx context.Context, root string, stdout, stderr io.Writer) int {
	// Structured for bn-vault-guard's reasons, and content-free for the same one:
	// this log lives outside the vault's privacy boundary, so every field written
	// here is a count, a state, a package identity, or a sanitized diagnostic.
	// The exception is Event.Paths, which carries note paths and carries them
	// deliberately — a conflict nobody can locate is a conflict nobody resolves.
	logger := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	err := vaultsyncd.Run(ctx, root, vaultsyncd.Options{
		Observe: func(event vaultsyncd.Event) { logEvent(logger, event) },
	})
	if err != nil {
		fmt.Fprintf(stderr, "bn-vault-sync: %v\n", err)
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
func logEvent(logger *slog.Logger, event vaultsyncd.Event) {
	attrs := []any{
		slog.String("event", string(event.Kind)),
		slog.String("vault", event.Vault),
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

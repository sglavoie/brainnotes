package main

import (
	"context"
	"log/slog"

	"github.com/sglavoie/brainnotes/internal/vaultsyncd"
)

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

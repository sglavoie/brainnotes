package cli

import "errors"

// bn's exit status follows grep's convention: 0 is success, 1 is a run that
// completed with a negative answer, and 2 (cmd/bn's default) is an error. A
// script can therefore tell "lint found violations" or "the picker came back
// empty" from "the vault did not load" or "that flag does not exist".
//
// The negative answers are these sentinels and LintError. Each is still
// printed, so the terminal says why nothing happened.
var (
	// ErrNothingSelected is a picker or prompt that produced nothing to act
	// on: an empty vault, or an fzf query that matched nothing.
	ErrNothingSelected = errors.New("nothing selected")
	// ErrCanceled is a confirmation the user declined, or could not give
	// because stdin ended first.
	ErrCanceled = errors.New("canceled")
	// ErrNoMatch is a search that completed and found nothing, which is
	// grep's exit 1.
	ErrNoMatch = errors.New("no matches")
	// ErrCheckFailed is a health gate that ran to completion and found
	// something wrong, like lint finding violations: `bn views check` with a
	// saved view that no longer compiles.
	ErrCheckFailed = errors.New("check failed")
)

// negativeOutcome is a negative answer with a message naming the command.
type negativeOutcome struct {
	msg  string
	kind error
}

func (e *negativeOutcome) Error() string { return e.msg }
func (e *negativeOutcome) Unwrap() error { return e.kind }

// nothingSelected reports that name had nothing to act on, and why.
func nothingSelected(name, why string) error {
	return &negativeOutcome{msg: prefixed(name, why), kind: ErrNothingSelected}
}

// noMatch reports that name searched and found nothing.
func noMatch(name string) error {
	return &negativeOutcome{msg: prefixed(name, "no matches"), kind: ErrNoMatch}
}

// canceled reports that name stopped at a declined confirmation.
func canceled(name string) error {
	return &negativeOutcome{msg: prefixed(name, "canceled"), kind: ErrCanceled}
}

// checkFailed reports that name's health gate completed and failed, and why.
func checkFailed(name, why string) error {
	return &negativeOutcome{msg: prefixed(name, why), kind: ErrCheckFailed}
}

func prefixed(name, msg string) string {
	if name == "" {
		return msg
	}
	return name + ": " + msg
}

// IsNegativeOutcome reports whether err is a completed run with a negative
// answer, which exits 1 rather than 2.
func IsNegativeOutcome(err error) bool {
	var lint *LintError
	return errors.As(err, &lint) || errors.Is(err, ErrNothingSelected) ||
		errors.Is(err, ErrCanceled) || errors.Is(err, ErrNoMatch) ||
		errors.Is(err, ErrCheckFailed)
}

package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	editutil "github.com/sglavoie/sglavoie_brainnotes/internal/editor"
)

// enumValueError is the one wording for a flag value outside a fixed set:
//
//	<cmd>: --<flag> must be a, b, or c (got "x")
//
// Every enumerated flag reports through it, so a script or a reader sees the
// same sentence whichever command refused.
func enumValueError(name, flag, got string, allowed ...string) error {
	return fmt.Errorf("%s: --%s must be %s (got %q)", name, flag, joinAlternatives(allowed), got)
}

// rangeValueError is enumValueError's counterpart for a numeric bound:
//
//	<cmd>: --<flag> must be <requirement> (got N)
func rangeValueError(name, flag, requirement string, got any) error {
	return fmt.Errorf("%s: --%s must be %s (got %v)", name, flag, requirement, got)
}

// joinAlternatives renders a, b and c as "a, b, or c" and a and b as "a or b".
func joinAlternatives(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " or " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + ", or " + items[len(items)-1]
	}
}

// errNoEditorText is the shared sentence for a command that needs an editor
// and finds none. [open].editor is named first because it wins.
const errNoEditorText = "no editor configured; set [open].editor or $EDITOR"

// resolveEditorSpec is the one place a command turns [open].editor and $EDITOR
// into an editor spec. Every failure is reported under name, and the
// no-editor case is the same sentence for every command.
func resolveEditorSpec(name, configured string) (string, error) {
	spec, err := editutil.Spec(configured, os.Getenv("EDITOR"))
	if errors.Is(err, editutil.ErrNoEditorConfigured) {
		return "", fmt.Errorf("%s: %s", name, errNoEditorText)
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return spec, nil
}

package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/schema"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// lintOptions is the flag state of one `bn lint` invocation. `ff` is the shared
// filter surface rather than a copy of it, so `lint` keeps routing through
// findflags.go's single execution path.
//
// There is no validate(): --property narrows which definitions are checked and
// the find flags narrow which notes are checked, so the two compose and every
// combination of them is meaningful.
type lintOptions struct {
	property string
	ff       *findFlags
}

// runLintCommand validates the selected notes against the configured property
// definitions and reports every violation.
//
// Order is behaviour and is preserved from the original closure: the vault is
// opened first, then a vault with no declared properties short-circuits with
// the schema hint, and only then is an undefined --property refused. `bn lint
// --property nosuch` in a vault that declares nothing therefore prints the hint
// and succeeds rather than reporting the unknown property.
//
// `lint` takes cobra.NoArgs, so the runner has no args parameter.
func runLintCommand(cmd *cobra.Command, opts lintOptions) error {
	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("lint: no config in context")
	}
	v, err := vault.Open(cfg)
	if err != nil {
		return err
	}

	if len(cfg.Frontmatter.Properties) == 0 {
		writeSchemaHint(cmd.ErrOrStderr(), "lint", cfg.SourcePath)
		return nil
	}
	props, err := lintProperties(cfg, opts.property)
	if err != nil {
		return err
	}

	notes, skipped, err := v.NotesLenient()
	if err != nil {
		return err
	}
	sel, err := opts.ff.selectNotes(cmd.Context(), cfg, v.Root, notes, nil)
	if err != nil {
		return err
	}

	violations, err := collectLintViolations(sel.notes, v.Root, props)
	if err != nil {
		return err
	}
	violations = append(violations, unparsableViolations(skipped, v.Root)...)
	if len(violations) == 0 {
		return nil
	}

	formatViolations(cmd, violations)
	// LintError is what turns a report into exit code 1; main.go prints it.
	return &LintError{Count: len(violations)}
}

// lintProperties narrows the configured definitions to the one --property names,
// or returns them all. An undefined property is an error rather than an empty
// result, so a typo cannot read as a clean vault.
func lintProperties(cfg *config.Config, property string) (map[string]config.PropertyDefinition, error) {
	if property == "" {
		return cfg.Frontmatter.Properties, nil
	}
	def, ok := cfg.Frontmatter.Definition(property)
	if !ok {
		return nil, fmt.Errorf("lint: property %q not defined in [frontmatter.properties]", property)
	}
	return map[string]config.PropertyDefinition{property: def}, nil
}

// unparsableViolations turns every note whose frontmatter could not be parsed
// into a "parse" violation, so one malformed note is reported alongside the
// schema findings instead of aborting the run. They are reported whatever the
// find flags select: a note that cannot be parsed cannot be matched against a
// filter either, and staying silent would read as a clean vault.
func unparsableViolations(skipped []vault.SkippedNote, root string) []schema.Violation {
	out := make([]schema.Violation, 0, len(skipped))
	for _, s := range skipped {
		out = append(out, schema.Violation{
			Property: "frontmatter",
			Kind:     schema.KindParse,
			Detail:   s.Err.Error(),
			NotePath: displayPath(s.Path, root),
		})
	}
	return out
}

// collectLintViolations validates every selected note in selection order. The
// notes come from NotesLenient, so an unparsable one has already been diverted
// to unparsableViolations; an error here is a genuine load failure.
func collectLintViolations(notes []*vault.Note, root string, props map[string]config.PropertyDefinition) ([]schema.Violation, error) {
	var all []schema.Violation
	for _, n := range notes {
		front, err := n.Front()
		if err != nil {
			return nil, err
		}
		all = append(all, schema.ValidateNote(front, displayPath(n.Path, root), props)...)
	}
	return all, nil
}

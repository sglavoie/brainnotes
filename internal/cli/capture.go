package cli

import (
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

// clockForCapture stamps a capture. Tests swap it for a fixed time so the
// rendered bullet is deterministic.
var clockForCapture = time.Now

// NewCaptureCmd returns the `capture` subcommand. Exported for tests.
func NewCaptureCmd() *cobra.Command {
	var (
		dryRun bool
		note   string
	)

	cmd := &cobra.Command{
		Use:   "capture [text...]",
		Short: "Append a timestamped line to the inbox note",
		Long: `Capture appends one line to the configured inbox note (capture.inbox_note,
default Inbox.md) under the configured heading (capture.heading, default
'## Captures'). The note and the heading are created when absent; everything
else in the note stays byte-identical.

Positional arguments are joined with a space and their whitespace is collapsed.
With no positional arguments the text is read from stdin, so 'pbpaste | bn
capture' works.

The line format is fixed and not configurable:

  - YYYY-MM-DD HH:MM <text>

Wikilinks typed in the text pass through untouched.

Undo caveat: the capture is journaled as a whole-note snapshot, so a restore
replaces the entire inbox note. Ordinary 'bn undo' is fail-fast: if the note
changed after the capture, it refuses and writes nothing. 'bn undo --force'
restores the whole snapshot and overwrites those later edits.

Use --dry-run to preview the append without writing.`,
		Example: `  bn capture "call the dentist"
  pbpaste | bn capture
  bn capture --dry-run "read [[Dune]]"
  bn capture --note "Scratch.md" "one-off idea"`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("capture: no config in context")
			}

			text, err := captureInputText(cmd.InOrStdin(), cmd.ErrOrStderr(), args)
			if err != nil {
				return err
			}

			dest := cfg.Capture.InboxNote
			if strings.TrimSpace(note) != "" {
				dest = captureNoteOverride(note)
			}

			req := noteops.CaptureRequest{
				Heading: cfg.Capture.Heading,
				Text:    text,
				At:      clockForCapture(),
			}
			out := cmd.OutOrStdout()

			if dryRun {
				result, err := noteops.PreviewCapture(cfg.Vault, dest, req)
				if err != nil {
					return captureError(err)
				}
				return noteops.RenderNotePreview(out, result.Preview)
			}

			result, err := noteops.ApplyCapture(cfg.Vault, dest, req, &cfg.Frontmatter,
				noteops.WithCaptureCommand(commandInvocation(cmd, args)),
				noteops.WithCaptureContext(commandContext(cmd)))
			if err != nil {
				return captureError(err)
			}
			return printCaptureResult(out, result)
		},
	}

	f := cmd.Flags()
	f.BoolVar(&dryRun, "dry-run", false, "preview the append without writing")
	f.StringVar(&note, "note", "", "vault-relative note to append to (overrides capture.inbox_note)")
	return cmd
}

// captureInputText joins the positional text, or reads it from in when there is
// none. Normalization and validation belong to noteops; this only rejects the
// case where no text was supplied at all. Reading a terminal says so first:
// otherwise `bn capture` with no arguments just sits there until Ctrl-D.
func captureInputText(in io.Reader, errOut io.Writer, args []string) (string, error) {
	if len(args) > 0 {
		text := strings.Join(args, " ")
		if strings.TrimSpace(text) == "" {
			return "", errors.New("capture: empty text")
		}
		return text, nil
	}
	if in == nil {
		return "", errors.New("capture: no text given and no stdin to read")
	}
	if isTerminal(in) {
		_, _ = fmt.Fprintln(errOut, "capture: reading text from stdin; press Ctrl-D to finish (or pass the text as arguments)")
	}
	data, err := io.ReadAll(in)
	if err != nil {
		return "", fmt.Errorf("capture: read stdin: %w", err)
	}
	text := string(data)
	if strings.TrimSpace(text) == "" {
		return "", errors.New("capture: empty text (pass text as arguments or on stdin)")
	}
	return text, nil
}

// captureNoteOverride mirrors the normalization config applies to
// capture.inbox_note so '--note Scratch' targets a Markdown note rather than
// creating an extension-less file.
func captureNoteOverride(note string) string {
	note = strings.TrimSpace(note)
	if !strings.EqualFold(path.Ext(note), ".md") {
		note += ".md"
	}
	return note
}

// captureError labels an error with the command name unless noteops already
// did, so a message never reads "capture: capture: ...".
func captureError(err error) error {
	if err == nil {
		return nil
	}
	if strings.HasPrefix(err.Error(), "capture") {
		return err
	}
	return fmt.Errorf("capture: %w", err)
}

func printCaptureResult(out io.Writer, result *noteops.CaptureResult) error {
	rel := result.Destination.RelPath
	created := ""
	if result.CreatedNote {
		created = " (created)"
	}
	if _, err := fmt.Fprintf(out, "captured to %s%s\n%s\n", rel, created, result.Line); err != nil {
		return err
	}
	if result.Journal != nil {
		if _, err := fmt.Fprintf(out, "journal_id: %s\n", result.Journal.ID); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(out, "note: undo restores all of %s; after later edits to it 'bn undo' refuses, and 'bn undo --force' overwrites those edits\n", rel)
	return err
}

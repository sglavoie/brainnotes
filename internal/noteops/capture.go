package noteops

import (
	"context"
	"os"
	"path"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// captureTimestampLayout is the fixed prefix format of a capture bullet. It is
// deliberately not configurable.
const captureTimestampLayout = "2006-01-02 15:04"

// captureAttempts caps how many times a capture re-reads and replans after a
// transaction conflict. Replanning is safe here — and only here — because an
// append is commutative, so fresh bytes cannot lose the user's intent. The cap
// exists so a note an editor holds open can never spin the process.
const captureAttempts = 3

// CaptureRequest is one quick capture: the text to append, the heading it goes
// under, and the moment it was captured. The caller owns the clock.
type CaptureRequest struct {
	// ID identifies a browser submission across retries; empty for unkeyed CLI calls.
	ID      string
	Heading string
	Text    string
	At      time.Time
}

// CaptureResult reports where a capture landed. Line is the rendered bullet and
// LineNumber is its 1-based position in the note body.
type CaptureResult struct {
	Replayed     bool
	Destination  PathResolution
	Line         string
	LineNumber   int
	CreatedNote  bool
	AddedHeading bool
	Preview      NotePreview
	Journal      *journal.Entry
}

type captureOptions struct {
	command        string
	journalOptions []journal.RecorderOption
	ctx            context.Context
}

// CaptureOption customizes ApplyCapture.
type CaptureOption func(*captureOptions)

// WithCaptureCommand sets the command stored in the undo journal entry.
func WithCaptureCommand(command string) CaptureOption {
	return func(o *captureOptions) {
		o.command = strings.TrimSpace(command)
	}
}

// WithCaptureContext makes the operation honour ctx, so a CLI interrupt
// cancels it at the same boundaries as every other journaled write.
func WithCaptureContext(ctx context.Context) CaptureOption {
	return func(o *captureOptions) {
		o.ctx = ctx
	}
}

// WithCaptureJournalOptions passes recorder options through to the undo journal.
func WithCaptureJournalOptions(opts ...journal.RecorderOption) CaptureOption {
	return func(o *captureOptions) {
		o.journalOptions = append(o.journalOptions, opts...)
	}
}

// PreviewCapture resolves what a capture would append without writing anything.
// It returns the same validation, containment, and parse errors ApplyCapture
// would, so a dry run cannot promise an append that a real one refuses.
func PreviewCapture(vaultRoot, relPath string, req CaptureRequest) (*CaptureResult, error) {
	plan, err := planCapture(vaultRoot, relPath, req, nil)
	if err != nil {
		return nil, err
	}
	return plan.result(), nil
}

// ApplyCapture appends one capture bullet to relPath under the requested
// heading, creating the note and the heading when they are absent. The write
// goes through an in-vault transaction pinned to the exact revision that was
// planned against, so a concurrent edit is a conflict rather than a silent
// overwrite; the capture then re-reads and replans, up to captureAttempts times.
func ApplyCapture(vaultRoot, relPath string, req CaptureRequest, fmCfg *config.FrontmatterConfig, opts ...CaptureOption) (*CaptureResult, error) {
	req.ID = strings.TrimSpace(req.ID)
	cfg := defaultCaptureOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	var (
		plan capturePlan
		err  error
	)
	for attempt := 0; attempt < captureAttempts; attempt++ {
		if replay, err := replayCapture(vaultRoot, req); replay != nil || err != nil {
			return replay, err
		}
		plan, err = planCapture(vaultRoot, relPath, req, fmCfg)
		if err != nil {
			return nil, err
		}
		result, commitErr := commitCapture(vaultRoot, plan, cfg)
		if commitErr == nil {
			return result, nil
		}
		if !captureShouldReplan(commitErr) {
			return result, commitErr
		}
		err = commitErr
	}
	return nil, WrapError(
		CodeConflict,
		"capture: "+plan.destination.RelPath+" kept changing on disk; nothing was appended",
		err)
}

func defaultCaptureOptions() captureOptions {
	return captureOptions{command: "bn capture"}
}

// capturePlan is one planned capture: the exact bytes to write plus everything
// the result and the preview are derived from. PreviewCapture and ApplyCapture
// share it so the two can never describe different appends.
type capturePlan struct {
	request      CaptureRequest
	destination  PathResolution
	content      []byte
	expect       vault.FileExpectation
	line         string
	lineNumber   int
	createdNote  bool
	addedHeading bool
}

func (p capturePlan) result() *CaptureResult {
	return &CaptureResult{
		Destination:  p.destination,
		Line:         p.line,
		LineNumber:   p.lineNumber,
		CreatedNote:  p.createdNote,
		AddedHeading: p.addedHeading,
		Preview:      p.preview(),
	}
}

// preview renders the plan as the canonical note preview: the appended bullet,
// and the note itself when the capture bootstraps it.
func (p capturePlan) preview() NotePreview {
	changes := make([]PreviewChange, 0, 2)
	if p.createdNote {
		changes = append(changes, PreviewChange{
			Kind: PreviewPath,
			Old:  AbsentText(),
			New:  PresentText(p.destination.RelPath),
		})
	}
	changes = append(changes, PreviewChange{
		Kind: PreviewBody,
		Line: p.lineNumber,
		Old:  AbsentText(),
		New:  PresentText(p.line),
	})
	return NotePreview{RelPath: p.destination.RelPath, Changes: changes}
}

func planCapture(vaultRoot, relPath string, req CaptureRequest, fmCfg *config.FrontmatterConfig) (capturePlan, error) {
	dest, err := resolveCaptureDestination(vaultRoot, relPath)
	if err != nil {
		return capturePlan{}, err
	}
	line, err := captureBullet(req)
	if err != nil {
		return capturePlan{}, err
	}
	source, err := readCaptureSource(dest, fmCfg)
	if err != nil {
		return capturePlan{}, err
	}
	body, lineNumber, addedHeading, err := planCaptureInsert(source.body, req.Heading, line)
	if err != nil {
		return capturePlan{}, err
	}
	return capturePlan{
		request:      req,
		destination:  dest,
		content:      []byte(source.prefix + body),
		expect:       source.expect,
		line:         line,
		lineNumber:   lineNumber,
		createdNote:  source.createdNote,
		addedHeading: addedHeading,
	}, nil
}

func commitCapture(vaultRoot string, plan capturePlan, cfg captureOptions) (*CaptureResult, error) {
	txn, err := vault.NewFileTxn(vaultRoot)
	if err != nil {
		return nil, WrapError(CodeInternal, "capture: open transaction", err)
	}
	if err := txn.Write(plan.destination.AbsPath, plan.content, 0, plan.expect); err != nil {
		return nil, ClassifyTxnError("capture: plan append", err)
	}
	if err := stageCaptureReceipt(txn, vaultRoot, plan); err != nil {
		return nil, err
	}
	// A bootstrapping capture journals a file that does not exist yet, so `bn
	// undo` deletes the note it created.
	j, err := BeginOperationContext(contextOrBackground(cfg.ctx), vaultRoot, "capture", cfg.command,
		[]string{plan.destination.AbsPath}, cfg.journalOptions...)
	if err != nil {
		return nil, err
	}
	defer j.Release()
	if err := txn.Commit(j); err != nil {
		return nil, ClassifyTxnError("capture: append to note", err)
	}
	result := plan.result()
	entry, err := j.Finalize()
	if err != nil {
		return result, err
	}
	result.Journal = entry
	return result, nil
}

// captureShouldReplan reports whether err is the plain lost race a re-read can
// resolve. A conflict reported alongside displaced content or a failed rollback
// carries recovery instructions and must reach the caller instead of being
// retried over an inconsistent vault. A lock timeout is excluded for a different
// reason: the plan is still valid, so re-reading buys nothing, and the commit
// already waited out its own budget.
func captureShouldReplan(err error) bool {
	if vault.IsTxnRecoveryNeeded(err) || vault.IsTxnRollbackFailure(err) {
		return false
	}
	return vault.IsTxnConflict(err)
}

// captureSource is the state a capture is planned against: the bytes ahead of
// the body, the body itself, and the precondition pinning that revision.
type captureSource struct {
	prefix      string
	body        string
	expect      vault.FileExpectation
	createdNote bool
}

// readCaptureSource reads the note, or renders the frontmatter a bootstrapped
// note starts from. The missing case is pinned with ExpectMissing so two racing
// first captures cannot both create the note.
func readCaptureSource(dest PathResolution, fmCfg *config.FrontmatterConfig) (captureSource, error) {
	data, err := os.ReadFile(dest.AbsPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return captureSource{}, WrapError(CodeConflict, "capture: read note", err)
		}
		stub, err := captureStubContent(captureNoteTitle(dest.RelPath), fmCfg)
		if err != nil {
			return captureSource{}, err
		}
		prefix, body, err := splitCaptureContent(stub)
		if err != nil {
			return captureSource{}, err
		}
		return captureSource{
			prefix:      prefix,
			body:        body,
			expect:      vault.ExpectMissing(),
			createdNote: true,
		}, nil
	}
	prefix, body, err := splitCaptureContent(data)
	if err != nil {
		return captureSource{}, err
	}
	return captureSource{prefix: prefix, body: body, expect: vault.ExpectHash(vault.HashBytes(data))}, nil
}

// splitCaptureContent separates the frontmatter region from the body. A capture
// only ever rewrites the body, so the frontmatter bytes are carried through
// untouched rather than re-marshaled: an append must not reformat properties the
// user never asked it to touch.
func splitCaptureContent(data []byte) (prefix, body string, err error) {
	doc, err := frontmatter.Parse(data)
	if err != nil {
		return "", "", WrapError(CodeConflict, "capture: parse note", err)
	}
	raw := string(data)
	if !strings.HasSuffix(raw, doc.Body) {
		return "", "", Errorf(CodeInternal, "capture: note body is not a suffix of the file")
	}
	return raw[:len(raw)-len(doc.Body)], doc.Body, nil
}

// captureStubContent renders the frontmatter a bootstrapped note starts from. It
// mirrors stubFrontmatterContent but honors the vault's property declarations,
// so the created note is written exactly as a property write to it would be.
func captureStubContent(title string, fmCfg *config.FrontmatterConfig) ([]byte, error) {
	doc := frontmatter.Document{Front: frontmatter.NewMap()}
	doc.Front.Set("title", title)
	out, err := frontmatter.Marshal(doc, fmCfg)
	if err != nil {
		return nil, WrapError(CodeInternal, "capture: render note frontmatter", err)
	}
	return out, nil
}

func captureNoteTitle(relPath string) string {
	base := path.Base(relPath)
	return strings.TrimSuffix(base, path.Ext(base))
}

func captureBullet(req CaptureRequest) (string, error) {
	if req.At.IsZero() {
		return "", Errorf(CodeInvalidRequest, "capture: timestamp is required")
	}
	text, err := normalizeCaptureText(req.Text)
	if err != nil {
		return "", err
	}
	return "- " + req.At.Format(captureTimestampLayout) + " " + text, nil
}

func resolveCaptureDestination(vaultRoot, relPath string) (PathResolution, error) {
	clean, err := CleanRelPath(relPath)
	if err != nil {
		return PathResolution{}, err
	}
	dest, err := PathForRel(vaultRoot, clean)
	if err != nil {
		return PathResolution{}, err
	}
	if dest.RelPath != clean {
		return PathResolution{}, Errorf(CodeForbidden, "capture: note must not resolve through a symlink")
	}
	return dest, nil
}

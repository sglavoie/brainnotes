package noteops

import (
	"errors"
	"os"
	"path"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// MaxSourceBytes caps the raw Markdown one source read or save may carry.
//
// It bounds the decoded value rather than the request, so the same limit holds
// for a CLI caller and for a web form whose encoding tripled the bytes on the
// wire. The transport bound is a separate, larger number in the web layer.
const MaxSourceBytes = 4 << 20

// sourceRevisionLength is the length of a revision token: sha256 as lowercase
// hex, which is what vault.HashBytes produces.
const sourceRevisionLength = 64

// SourceDocument is one note's Markdown exactly as it is stored, together with
// the revision those bytes hash to.
//
// The revision is the sha256 of the file's exact content — the same digest
// vault.ExpectHash pins a transaction to — rather than a timestamp or a
// generation counter. That is what makes a revision a client echoed back
// checkable against the file at the commit boundary: a modification time can be
// shared by two different versions of a note, and a counter cannot be derived
// from bytes an external editor wrote.
//
// A note that does not exist has no revision, and the empty string is how a
// create says so.
type SourceDocument struct {
	Destination PathResolution
	Content     string
	Revision    string
	Exists      bool
}

// SourceRevisionError reports a save that was planned against a revision the
// note no longer holds. Nothing was written.
//
// It exists as a type rather than a plain conflict because the caller owes the
// user more than a message for this one failure: the editor holds text that is
// not on disk anywhere, and it needs to offer a merge rather than tell someone
// to copy their draft out and reload. Expected and Actual are digests, never
// note bytes, so the error stays as safe to log as a transaction intent is; the
// current content is re-read by the layer that is going to display it.
//
// Actual is empty when the conflict was detected at the commit boundary instead
// of before it. The file has moved again by then, and any digest read here could
// be stale before it reached the client.
type SourceRevisionError struct {
	RelPath  string
	Expected string
	Actual   string
	Err      error
}

func (e *SourceRevisionError) Error() string {
	if e == nil || e.Err == nil {
		return "source: note changed since it was loaded"
	}
	return e.Err.Error()
}

func (e *SourceRevisionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsSourceRevisionConflict reports whether err is a stale-revision refusal.
func IsSourceRevisionConflict(err error) bool {
	var conflict *SourceRevisionError
	return errors.As(err, &conflict)
}

func newSourceRevisionError(relPath, expected, actual string) error {
	return &SourceRevisionError{
		RelPath:  relPath,
		Expected: expected,
		Actual:   actual,
		Err: Errorf(CodeConflict,
			"source: %s changed since it was loaded, so nothing was written", relPath),
	}
}

// newSourceExistsError refuses a create whose destination is already taken by
// different content.
//
// It is a SourceRevisionError rather than a plain conflict because the caller
// owes the user the same thing here as on a stale save: the editor holds text
// that is on disk nowhere, and "it already exists" without the bytes that are in
// the way leaves nothing to merge against. Expected is empty — a create was
// planned against no revision at all, which is precisely what turned out to be
// wrong.
func newSourceExistsError(relPath, actual string) error {
	return &SourceRevisionError{
		RelPath:  relPath,
		Expected: "",
		Actual:   actual,
		Err: Errorf(CodeConflict,
			"source: %s already exists and holds different content, so nothing was written", relPath),
	}
}

// ReadSource reads relPath's bytes verbatim.
//
// Nothing is normalized: not the line endings, not a missing final newline, not
// the frontmatter key order, not the indentation of a nested property. That is
// the whole point of a source API — a read that quietly rewrote the bytes would
// make every save planned against it either a spurious conflict or a silent
// reformat of a note somebody only opened, and `frontmatter.Marshal` sorts keys,
// so a round trip through the parsed document is exactly that reformat.
//
// A missing note is not an error. It comes back with Exists false and no
// revision, which is what a create is planned against; the callers that need a
// note to be there say so themselves.
func ReadSource(vaultRoot string, cfg *config.Config, relPath string) (*SourceDocument, error) {
	dest, err := resolveSourceDestination(vaultRoot, cfg, relPath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(dest.AbsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &SourceDocument{Destination: dest}, nil
		}
		return nil, WrapError(CodeConflict, "source: read "+dest.RelPath, err)
	}
	if len(data) > MaxSourceBytes {
		return nil, Errorf(CodeInvalidRequest,
			"source: %s is larger than the %d byte maximum this API serves", dest.RelPath, MaxSourceBytes)
	}
	return &SourceDocument{
		Destination: dest,
		Content:     string(data),
		Revision:    vault.HashBytes(data),
		Exists:      true,
	}, nil
}

// ValidateSource refuses submitted bytes that could not be stored as a note,
// before anything is written and before any revision is consulted.
//
// Exactly three things are checked. The size bound is the decoded one. A NUL
// byte means the submission is not a text note at all, and storing it would put
// bytes in the vault that every reader downstream treats as binary. And a
// properties block that is opened and closed must hold YAML that parses: that is
// the one syntax error which changes what the rest of the file *means* rather
// than merely how it reads, because a note whose frontmatter does not parse has
// no properties anywhere in the vault, so it is refused at submission rather
// than stored and discovered by a later query that quietly omits it.
//
// Two things are deliberately not validated, for the same reason: refusing them
// would be this API inventing a rule the vault does not have. Markdown has no
// invalid document, so the body is not checked at all. And a `---` line with no
// closing delimiter is **not** an error here, because it is not one for
// frontmatter.Parse either — it is a note whose body begins with a horizontal
// rule, a reading that package chose deliberately so such a note stays editable.
// Refusing it would make this surface the only one in the vault that cannot save
// a note the vault itself accepts, and an editor that can open a note but never
// save it is a worse failure than the one that rule would prevent.
// TestSaveSourceStoresAnUnterminatedPropertiesBlockVerbatim pins that.
func ValidateSource(content string) error {
	if len(content) > MaxSourceBytes {
		return Errorf(CodeInvalidRequest, "source: content exceeds the %d byte maximum", MaxSourceBytes)
	}
	if strings.IndexByte(content, 0) >= 0 {
		return Errorf(CodeInvalidRequest, "source: content holds a NUL byte, so it is not a text note")
	}
	if _, err := frontmatter.Parse([]byte(content)); err != nil {
		return WrapError(CodeInvalidRequest,
			"source: the note's frontmatter is not valid YAML, so nothing was written: "+err.Error(), err)
	}
	return nil
}

// resolveSourceDestination resolves relPath for reading or writing as a note.
//
// It mirrors resolveSynthesisDestination, which is the only other surface where
// a client names a brand-new path outright, and it owes the same four refusals.
// Containment and the extension check are the obvious two. The symlink check is
// the third: a destination that resolves somewhere other than where it was asked
// for went through a link, and the location policy approved the link's path
// rather than its target.
//
// vault.WalkExclusion is the fourth and the least obvious, and leaving it out is
// a silent-loss bug rather than a missing nicety. Walk prunes hidden components,
// the configured ignore list and the templates directory, so a note written into
// one of those exists on disk and is in no index, no graph, no search and no
// query — and `.bnotes/`, `.git/` and the ignore list are largely outside
// vaultgit.InScope too, where a journaled write commits *nothing* while the
// response reports a revision and a success. Both failures are invisible from
// the client's side, which is exactly the class of loss this surface exists to
// rule out. A nil cfg still refuses every hidden path; it only loses the
// configured ignores and the templates directory, so callers pass the published
// configuration they planned the save against.
func resolveSourceDestination(vaultRoot string, cfg *config.Config, relPath string) (PathResolution, error) {
	clean, err := CleanRelPath(relPath)
	if err != nil {
		return PathResolution{}, err
	}
	if !strings.EqualFold(path.Ext(clean), ".md") {
		return PathResolution{}, Errorf(CodeInvalidRequest, "source: %s is not a Markdown note", clean)
	}
	if reason, excluded := vault.WalkExclusion(cfg, clean); excluded {
		return PathResolution{}, Errorf(CodeForbidden,
			"source: %s is not a note location (%s), so nothing was written", clean, reason)
	}
	dest, err := PathForRel(vaultRoot, clean)
	if err != nil {
		return PathResolution{}, err
	}
	if dest.RelPath != clean {
		return PathResolution{}, Errorf(CodeForbidden, "source: note must not resolve through a symlink")
	}
	return dest, nil
}

// validSourceRevision reports whether token has the shape vault.HashBytes
// produces. A malformed token would simply never match and come back as a
// conflict, which would send a person looking for a concurrent editor that does
// not exist; this makes it the client bug it is.
func validSourceRevision(token string) bool {
	if len(token) != sourceRevisionLength {
		return false
	}
	for i := 0; i < len(token); i++ {
		c := token[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// sourceOptions is the shared configuration of a source save.
type sourceOptions struct {
	command        string
	journalOptions []journal.RecorderOption
	requires       []fileRequirement
}

// SourceOption customizes SaveSource.
type SourceOption func(*sourceOptions)

// WithSourceCommand sets the command stored in the undo journal entry.
func WithSourceCommand(command string) SourceOption {
	return func(o *sourceOptions) {
		o.command = strings.TrimSpace(command)
	}
}

// WithSourceJournalOptions passes recorder options through to the undo journal.
func WithSourceJournalOptions(opts ...journal.RecorderOption) SourceOption {
	return func(o *sourceOptions) {
		o.journalOptions = append(o.journalOptions, opts...)
	}
}

// WithSourceRequire pins a file the save depends on but never writes.
//
// The transaction verifies it at the commit boundary, so a save validated
// against one revision of the vault configuration is refused if another revision
// replaced it in the meantime. That matters here even though a source save never
// re-renders frontmatter: whether the submitted properties block is acceptable
// at all is a question about the vault's declarations, and answering it against
// a configuration that is no longer in force is answering the wrong question.
func WithSourceRequire(path string, expect vault.FileExpectation) SourceOption {
	return func(o *sourceOptions) {
		if strings.TrimSpace(path) == "" {
			return
		}
		o.requires = append(o.requires, fileRequirement{path: path, expect: expect})
	}
}

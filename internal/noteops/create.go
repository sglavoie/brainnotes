package noteops

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

// CreateResult describes a stub note created from an unresolved wikilink.
type CreateResult struct {
	Source      PathResolution
	Destination PathResolution
	Target      string
	Title       string
	Journal     *journal.Entry
}

type createOptions struct {
	command        string
	journalOptions []journal.RecorderOption
}

// CreateOption customizes CreateStubFromBrokenTarget.
type CreateOption func(*createOptions)

// WithCreateCommand sets the command stored in the undo journal entry.
func WithCreateCommand(command string) CreateOption {
	return func(o *createOptions) {
		o.command = strings.TrimSpace(command)
	}
}

// WithCreateJournalOptions passes recorder options through to the undo journal.
func WithCreateJournalOptions(opts ...journal.RecorderOption) CreateOption {
	return func(o *createOptions) {
		o.journalOptions = append(o.journalOptions, opts...)
	}
}

// StubPreview is the read-only plan for a stub note that would be created from a
// broken wikilink target. It resolves and validates the destination without
// writing anything, so callers (CLI dry-run, web preview, cockpit fix planning)
// can show exactly what CreateStubFromBrokenTarget would produce.
type StubPreview struct {
	Source      PathResolution
	Destination PathResolution
	Target      string
	Title       string
}

// stubPlan is the resolved, validated destination shared by the preview and the
// create paths so the two can never diverge.
type stubPlan struct {
	source      PathResolution
	destination PathResolution
	target      string
	title       string
}

// planStubFromBrokenTarget resolves and validates where a stub note for
// rawTarget would land, without touching the filesystem beyond stat checks. It
// is the single source of truth for both PreviewStubFromBrokenTarget and
// CreateStubFromBrokenTarget.
func planStubFromBrokenTarget(vaultRoot, sourceRel, rawTarget string) (stubPlan, error) {
	source, err := resolveSourceNote(vaultRoot, sourceRel, "create:")
	if err != nil {
		return stubPlan{}, err
	}
	target, err := normalizeBrokenCreateTarget(rawTarget)
	if err != nil {
		return stubPlan{}, err
	}
	destRel, title, err := createDestinationRel(source.RelPath, target)
	if err != nil {
		return stubPlan{}, err
	}
	dest, err := PathForRel(vaultRoot, destRel)
	if err != nil {
		return stubPlan{}, err
	}
	if dest.RelPath != destRel {
		return stubPlan{}, Errorf(CodeForbidden, "create: destination must not resolve through a symlink")
	}
	if exists, err := pathExists(dest.AbsPath); err != nil {
		return stubPlan{}, WrapError(CodeConflict, "create: stat destination", err)
	} else if exists {
		return stubPlan{}, Errorf(CodeConflict, "create: destination %s already exists", dest.RelPath)
	}
	return stubPlan{source: source, destination: dest, target: target, title: title}, nil
}

// PreviewStubFromBrokenTarget resolves the note a stub would create for a broken
// wikilink target without writing anything. It returns the same conflict and
// containment errors CreateStubFromBrokenTarget would.
func PreviewStubFromBrokenTarget(vaultRoot, sourceRel, rawTarget string) (*StubPreview, error) {
	plan, err := planStubFromBrokenTarget(vaultRoot, sourceRel, rawTarget)
	if err != nil {
		return nil, err
	}
	return &StubPreview{
		Source:      plan.source,
		Destination: plan.destination,
		Target:      plan.target,
		Title:       plan.title,
	}, nil
}

// CreateStubFromBrokenTarget creates a blank markdown note for a broken
// wikilink target. Folder-qualified targets create in that folder; bare
// targets create beside the source note.
func CreateStubFromBrokenTarget(vaultRoot, sourceRel, rawTarget string, opts ...CreateOption) (*CreateResult, error) {
	cfg := defaultCreateOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	plan, err := planStubFromBrokenTarget(vaultRoot, sourceRel, rawTarget)
	if err != nil {
		return nil, err
	}
	dest := plan.destination

	body, err := stubFrontmatterContent(plan.title)
	if err != nil {
		return nil, err
	}
	// Ahead of the folder creation, which is the first thing here that touches
	// disk and happens before any journaling or locking would consult the policy.
	if err := guardMutation(vaultRoot, dest.AbsPath); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dest.AbsPath), 0o755); err != nil {
		return nil, WrapError(CodeInternal, "create: create destination folder", err)
	}

	j := NewJournal(vaultRoot, cfg.command, cfg.journalOptions...)
	if err := j.Capture(dest.AbsPath); err != nil {
		return nil, err
	}
	// Without the lock this exclusive create is exactly the writer that wins the
	// vacancy a concurrent transaction opens while holding the destination in
	// custody, stranding that transaction's content in a recovery file.
	release, err := vault.LockPaths(vaultRoot, dest.AbsPath)
	if err != nil {
		return nil, classifyTxnError("create: write stub note", err)
	}
	defer release()
	if err := writeNewFileExclusive(dest.AbsPath, body); err != nil {
		if os.IsExist(err) {
			return nil, Errorf(CodeConflict, "create: destination %s already exists", dest.RelPath)
		}
		return nil, WrapError(CodeInternal, "create: write stub note", err)
	}
	entry, err := j.Finalize()
	if err != nil {
		return nil, err
	}

	return &CreateResult{
		Source:      plan.source,
		Destination: dest,
		Target:      plan.target,
		Title:       plan.title,
		Journal:     entry,
	}, nil
}

func defaultCreateOptions() createOptions {
	return createOptions{command: "bn web create"}
}

// resolveSourceNote resolves a vault-relative note that must already exist as a
// regular file reachable without traversing a symlink. op prefixes the error
// messages so each caller keeps its own wording.
func resolveSourceNote(vaultRoot, sourceRel, op string) (PathResolution, error) {
	cleanRel, err := CleanRelPath(sourceRel)
	if err != nil {
		return PathResolution{}, err
	}
	source, err := ResolveExistingPath(vaultRoot, filepath.FromSlash(cleanRel))
	if err != nil {
		return PathResolution{}, err
	}
	if source.RelPath != cleanRel {
		return PathResolution{}, Errorf(CodeForbidden, "%s source path must not resolve through a symlink", op)
	}
	if err := ensureRegularSource(source.AbsPath, op); err != nil {
		return PathResolution{}, err
	}
	return source, nil
}

func normalizeBrokenCreateTarget(raw string) (string, error) {
	target := strings.TrimSpace(raw)
	if target == "" {
		return "", Errorf(CodeInvalidRequest, "create: empty target")
	}
	if strings.HasPrefix(target, "[[") || strings.HasSuffix(target, "]]") {
		links := wikilink.Parse(target)
		if len(links) != 1 || links[0].Raw != target {
			return "", Errorf(CodeInvalidRequest, "create: invalid wikilink target")
		}
		target = links[0].Target
	} else {
		if idx := strings.IndexByte(target, '|'); idx >= 0 {
			target = target[:idx]
		}
		if idx := strings.IndexByte(target, '#'); idx >= 0 {
			target = target[:idx]
		}
	}

	target = strings.TrimSpace(strings.ReplaceAll(target, `\`, "/"))
	if target == "" {
		return "", Errorf(CodeInvalidRequest, "create: empty target")
	}
	if targetLooksAbsolute(target) {
		return "", Errorf(CodeForbidden, "create: target path must be vault-relative")
	}
	if containsControlRune(target) {
		return "", Errorf(CodeInvalidRequest, "create: target contains a control character")
	}
	if _, ok := wikilink.FirstReservedTargetRune(target); ok {
		return "", Errorf(CodeInvalidRequest, "create: target contains wikilink syntax characters")
	}
	if strings.HasSuffix(target, "/") {
		return "", Errorf(CodeInvalidRequest, "create: target has an empty title segment")
	}
	parts := strings.Split(target, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", Errorf(CodeInvalidRequest, "create: invalid target path segment")
		}
	}
	return strings.Join(parts, "/"), nil
}

func createDestinationRel(sourceRel, target string) (string, string, error) {
	sourceFolder := path.Dir(sourceRel)
	if sourceFolder == "." {
		sourceFolder = ""
	}

	targetFolder := path.Dir(target)
	if targetFolder == "." {
		targetFolder = ""
	}
	rawTitle := path.Base(target)
	title := stripMarkdownExtension(rawTitle)
	if err := validateNoteTitle("create:", title); err != nil {
		return "", "", err
	}

	folder := targetFolder
	if folder == "" {
		folder = sourceFolder
	}
	destRel := title + ".md"
	if folder != "" {
		destRel = path.Join(folder, destRel)
	}
	clean, err := CleanRelPath(destRel)
	if err != nil {
		return "", "", err
	}
	if clean != destRel {
		return "", "", Errorf(CodeInvalidRequest, "create: invalid destination path")
	}
	return destRel, title, nil
}

func stripMarkdownExtension(title string) string {
	ext := path.Ext(title)
	if !strings.EqualFold(ext, ".md") {
		return title
	}
	stem := strings.TrimSuffix(title, ext)
	if stem == "" {
		return title
	}
	return stem
}

// validateNoteTitle enforces the shared rules for a title that becomes a file
// name and a wikilink target. op prefixes the error messages so each caller
// keeps its own wording.
func validateNoteTitle(op, title string) error {
	if title == "" {
		return Errorf(CodeInvalidRequest, "%s empty title", op)
	}
	if strings.TrimSpace(title) == "" {
		return Errorf(CodeInvalidRequest, "%s title may not be whitespace-only", op)
	}
	if title != strings.TrimSpace(title) {
		return Errorf(CodeInvalidRequest, "%s title may not have leading or trailing whitespace", op)
	}
	if strings.ContainsRune(title, '/') {
		return Errorf(CodeInvalidRequest, "%s title may not contain '/'", op)
	}
	if containsControlRune(title) {
		return Errorf(CodeInvalidRequest, "%s title contains a control character", op)
	}
	if strings.HasPrefix(title, ".") {
		return Errorf(CodeInvalidRequest, "%s title may not start with '.'", op)
	}
	if r, ok := wikilink.FirstReservedTargetRune(title); ok {
		return Errorf(CodeInvalidRequest, "%s title may not contain %q", op, r)
	}
	return nil
}

func stubFrontmatterContent(title string) ([]byte, error) {
	doc := frontmatter.Document{Front: frontmatter.NewMap()}
	doc.Front.Set("title", title)
	out, err := frontmatter.Marshal(doc, nil)
	if err != nil {
		return nil, WrapError(CodeInternal, "create: render stub frontmatter", err)
	}
	return out, nil
}

func writeNewFileExclusive(dest string, data []byte) error {
	file, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(dest)
		}
	}()

	n, err := file.Write(data)
	if err == nil && n < len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("write %q: %w", dest, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync %q: %w", dest, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %q: %w", dest, err)
	}
	cleanup = false
	return nil
}

func targetLooksAbsolute(target string) bool {
	if path.IsAbs(target) || filepath.IsAbs(target) {
		return true
	}
	return len(target) >= 2 && isASCIIAlpha(target[0]) && target[1] == ':' && (len(target) == 2 || target[2] == '/')
}

package noteops

import (
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/frontmatter"
	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/wikilink"
)

// MaxSynthesisDraftBytes caps the editable draft a synthesis note may carry.
const MaxSynthesisDraftBytes = 1 << 20

// synthesisSourcesHeading and synthesisSourcesLevel name the section the writer
// appends. The draft may not contain that heading itself: the assembled note
// would then hold two Sources sections and the generated one would no longer be
// identifiable as the authoritative list.
const (
	synthesisSourcesHeading = "Sources"
	synthesisSourcesLevel   = 2
)

// SynthesisSource identifies one note that a synthesis draws on.
type SynthesisSource struct {
	// RelPath is the vault-relative path of the note.
	RelPath string
	// Title becomes the wikilink alias. An empty or unsafe title falls back to
	// a path-only wikilink.
	Title string
	// SHA256 pins the revision the draft was written against. Empty requires
	// only that the note still exists.
	SHA256 string
}

// SynthesisRequirement is a file the synthesis depends on but never writes,
// such as brainnotes.toml.
type SynthesisRequirement struct {
	// Path is vault-relative when supplied to a request and absolute once the
	// plan has resolved it.
	Path   string
	Expect vault.FileExpectation
}

// SynthesisRequest is the user-controlled input for a new synthesis note.
type SynthesisRequest struct {
	Title string
	// Folder is vault-relative. Empty means the vault root.
	Folder string
	Draft  string
	// Sources are ordered: the anchor first, then supporters in user order.
	Sources []SynthesisSource
	// Requires pins read-only dependencies beyond the sources themselves.
	Requires []SynthesisRequirement
}

// SynthesisPlan is the resolved, validated result of planning a synthesis note.
// It is the preview and the apply input at once, so what a user approves is
// byte-for-byte what gets written.
type SynthesisPlan struct {
	Destination PathResolution
	// Content is the exact file bytes.
	Content []byte
	// Expect is always a missing-destination expectation: a synthesis never
	// edits or overwrites an existing note.
	Expect vault.FileExpectation
	// Sources are the resolved sources in render order.
	Sources []SynthesisSource
	// Requirements hold absolute paths and cover both the sources and the
	// request's own requirements.
	Requirements []SynthesisRequirement
}

// SynthesisResult reports what ApplySynthesisNote created.
type SynthesisResult struct {
	Destination PathResolution
	Content     []byte
	Journal     *journal.Entry
}

type synthesisOptions struct {
	command        string
	journalOptions []journal.RecorderOption
}

// SynthesisOption customizes ApplySynthesisNote.
type SynthesisOption func(*synthesisOptions)

// WithSynthesisJournalOptions passes recorder options through to the undo journal.
func WithSynthesisJournalOptions(opts ...journal.RecorderOption) SynthesisOption {
	return func(o *synthesisOptions) {
		o.journalOptions = append(o.journalOptions, opts...)
	}
}

// newSynthesisOptions folds the variadic options onto the defaults, skipping the
// nil ones a conditional caller passes.
func newSynthesisOptions(opts []SynthesisOption) synthesisOptions {
	cfg := defaultSynthesisOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}

func defaultSynthesisOptions() synthesisOptions {
	return synthesisOptions{command: "bn web synthesize"}
}

// PlanSynthesisNote resolves and validates everything a synthesis note needs and
// returns the exact bytes it would write, without touching the vault. Every
// dependency is verified against its current state here so a doomed plan is
// refused before a user is shown it; the same expectations are re-pinned at the
// commit boundary, which is what actually makes the write safe.
func PlanSynthesisNote(vaultRoot string, cfg *config.Config, req SynthesisRequest) (*SynthesisPlan, error) {
	draft, err := normalizeSynthesisDraft(req.Draft)
	if err != nil {
		return nil, err
	}
	sources, requirements, err := resolveSynthesisSources(vaultRoot, req.Sources)
	if err != nil {
		return nil, err
	}
	extra, err := resolveSynthesisRequires(vaultRoot, req.Requires)
	if err != nil {
		return nil, err
	}
	requirements = append(requirements, extra...)

	destination, err := resolveSynthesisDestination(vaultRoot, cfg, req.Title, req.Folder)
	if err != nil {
		return nil, err
	}
	content, err := synthesisContent(req.Title, draft, sources)
	if err != nil {
		return nil, err
	}
	if err := verifySynthesisRequirements(requirements); err != nil {
		return nil, err
	}

	return &SynthesisPlan{
		Destination:  destination,
		Content:      content,
		Expect:       vault.ExpectMissing(),
		Sources:      sources,
		Requirements: requirements,
	}, nil
}

// ApplySynthesisNote writes the exact bytes the plan carries. It never rebuilds
// user-controlled content, so the note that lands is the note that was
// previewed.
func ApplySynthesisNote(vaultRoot string, plan *SynthesisPlan, opts ...SynthesisOption) (*SynthesisResult, error) {
	if plan == nil {
		return nil, Errorf(CodeInvalidRequest, "synthesize: nil plan")
	}
	if plan.Expect.Exists {
		return nil, Errorf(CodeInvalidRequest, "synthesize: plan must expect a missing destination")
	}
	cfg := newSynthesisOptions(opts)

	txn, err := vault.NewFileTxn(vaultRoot)
	if err != nil {
		return nil, WrapError(CodeInternal, "synthesize: open transaction", err)
	}
	for _, requirement := range plan.Requirements {
		if err := txn.Require(requirement.Path, requirement.Expect); err != nil {
			return nil, ClassifyTxnError("synthesize: pin dependency", err)
		}
	}
	if err := txn.Write(plan.Destination.AbsPath, plan.Content, 0, plan.Expect); err != nil {
		return nil, ClassifyTxnError("synthesize: plan note", err)
	}

	j, err := BeginOperation(vaultRoot, "synthesize", cfg.command, []string{plan.Destination.AbsPath}, cfg.journalOptions...)
	if err != nil {
		return nil, err
	}
	defer j.Release()
	if err := txn.Commit(j); err != nil {
		return nil, ClassifyTxnError("synthesize: create note", err)
	}
	entry, err := j.Finalize()
	if err != nil {
		return nil, err
	}

	return &SynthesisResult{
		Destination: plan.Destination,
		Content:     plan.Content,
		Journal:     entry,
	}, nil
}

// normalizeSynthesisDraft validates the editable draft and reduces it to the
// body form the note stores. Browsers submit CRLF, so line endings are
// normalized before anything else compares or measures the text.
func normalizeSynthesisDraft(draft string) (string, error) {
	if len(draft) > MaxSynthesisDraftBytes {
		return "", Errorf(CodeInvalidRequest, "synthesize: draft exceeds the %d byte maximum", MaxSynthesisDraftBytes)
	}
	if !utf8.ValidString(draft) {
		return "", Errorf(CodeInvalidRequest, "synthesize: draft is not valid UTF-8")
	}
	normalized := strings.ReplaceAll(draft, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")

	lines := strings.Split(normalized, "\n")
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	trimmed := strings.Join(lines[start:end], "\n")
	if trimmed == "" {
		return "", Errorf(CodeInvalidRequest, "synthesize: draft is empty")
	}
	if containsATXHeading(trimmed, synthesisSourcesLevel, synthesisSourcesHeading) {
		return "", Errorf(CodeInvalidRequest, "synthesize: draft may not contain a %q heading", strings.Repeat("#", synthesisSourcesLevel)+" "+synthesisSourcesHeading)
	}
	return trimmed, nil
}

// resolveSynthesisSources validates the ordered source list and derives the
// read-only expectation that pins each one.
func resolveSynthesisSources(vaultRoot string, sources []SynthesisSource) ([]SynthesisSource, []SynthesisRequirement, error) {
	if len(sources) == 0 {
		return nil, nil, Errorf(CodeInvalidRequest, "synthesize: no sources")
	}
	resolved := make([]SynthesisSource, 0, len(sources))
	requirements := make([]SynthesisRequirement, 0, len(sources))
	seen := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		note, err := resolveSourceNote(vaultRoot, source.RelPath, "synthesize:")
		if err != nil {
			return nil, nil, err
		}
		if _, dup := seen[note.RelPath]; dup {
			return nil, nil, Errorf(CodeInvalidRequest, "synthesize: source %s is listed more than once", note.RelPath)
		}
		seen[note.RelPath] = struct{}{}
		resolved = append(resolved, SynthesisSource{
			RelPath: note.RelPath,
			Title:   source.Title,
			SHA256:  source.SHA256,
		})
		requirements = append(requirements, SynthesisRequirement{
			Path:   note.AbsPath,
			Expect: sourceExpectation(source.SHA256),
		})
	}
	return resolved, requirements, nil
}

func sourceExpectation(sha string) vault.FileExpectation {
	if strings.TrimSpace(sha) == "" {
		return vault.ExpectAnyContent()
	}
	return vault.ExpectHash(sha)
}

// resolveSynthesisRequires resolves the request's extra dependencies to
// contained absolute paths.
func resolveSynthesisRequires(vaultRoot string, requires []SynthesisRequirement) ([]SynthesisRequirement, error) {
	if len(requires) == 0 {
		return nil, nil
	}
	resolved := make([]SynthesisRequirement, 0, len(requires))
	for _, requirement := range requires {
		target, err := PathForRel(vaultRoot, requirement.Path)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, SynthesisRequirement{Path: target.AbsPath, Expect: requirement.Expect})
	}
	return resolved, nil
}

func verifySynthesisRequirements(requirements []SynthesisRequirement) error {
	for _, requirement := range requirements {
		if err := vault.VerifyState(requirement.Path, requirement.Expect); err != nil {
			return ClassifyTxnError("synthesize: verify dependency", err)
		}
	}
	return nil
}

// resolveSynthesisDestination places <folder>/<title>.md, refusing anywhere the
// vault walk would never look: a note written into an ignored, hidden, or
// template path exists on disk but is invisible to every query.
func resolveSynthesisDestination(vaultRoot string, cfg *config.Config, title, folder string) (PathResolution, error) {
	if err := validateNoteTitle("synthesize:", title); err != nil {
		return PathResolution{}, err
	}
	folderRel, err := synthesisFolderRel(vaultRoot, folder)
	if err != nil {
		return PathResolution{}, err
	}
	destRel := title + ".md"
	if folderRel != "" {
		destRel = path.Join(folderRel, destRel)
	}
	clean, err := CleanRelPath(destRel)
	if err != nil {
		return PathResolution{}, err
	}
	if clean != destRel {
		return PathResolution{}, Errorf(CodeInvalidRequest, "synthesize: invalid destination path")
	}
	if reason, excluded := vault.WalkExclusion(cfg, destRel); excluded {
		return PathResolution{}, Errorf(CodeForbidden, "synthesize: destination %s is not a note location (%s)", destRel, reason)
	}
	dest, err := PathForRel(vaultRoot, destRel)
	if err != nil {
		return PathResolution{}, err
	}
	if dest.RelPath != destRel {
		return PathResolution{}, Errorf(CodeForbidden, "synthesize: destination must not resolve through a symlink")
	}
	if exists, err := pathExists(dest.AbsPath); err != nil {
		return PathResolution{}, WrapError(CodeConflict, "synthesize: stat destination", err)
	} else if exists {
		return PathResolution{}, Errorf(CodeConflict, "synthesize: destination %s already exists", dest.RelPath)
	}
	return dest, nil
}

// synthesisFolderRel normalizes a vault-relative folder, treating an empty value
// as the vault root.
func synthesisFolderRel(vaultRoot, folder string) (string, error) {
	if strings.TrimSpace(folder) == "" {
		return "", nil
	}
	abs, err := resolveNewDestFolder(vaultRoot, folder, "synthesize: folder")
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(vaultRoot, abs)
	if err != nil {
		return "", WrapError(CodeInternal, "synthesize: resolve folder", err)
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return "", nil
	}
	return CleanRelPath(rel)
}

// renderSynthesisBody assembles the draft and the reserved Sources section.
func renderSynthesisBody(draft string, sources []SynthesisSource) (string, error) {
	var b strings.Builder
	b.WriteString(draft)
	b.WriteString("\n\n")
	b.WriteString(strings.Repeat("#", synthesisSourcesLevel) + " " + synthesisSourcesHeading + "\n")
	for _, source := range sources {
		link, err := synthesisSourceLink(source)
		if err != nil {
			return "", err
		}
		b.WriteString("- " + link + "\n")
	}
	return b.String(), nil
}

// synthesisSourceLink renders one path-qualified wikilink. An alias that would
// change how the link parses is dropped rather than escaped, leaving a
// path-only link that still resolves.
func synthesisSourceLink(source SynthesisSource) (string, error) {
	target := stripMarkdownExtension(source.RelPath)
	if target == "" {
		return "", Errorf(CodeInvalidRequest, "synthesize: source has an empty path")
	}
	if r, ok := wikilink.FirstReservedTargetRune(target); ok {
		return "", Errorf(CodeInvalidRequest, "synthesize: source path %s may not contain %q", source.RelPath, r)
	}
	alias := source.Title
	if !safeWikilinkAlias(alias) {
		alias = ""
	}
	return wikilink.FormatLink(target, "", alias), nil
}

// safeWikilinkAlias reports whether alias survives a wikilink round trip
// unchanged. Parse trims ASCII spaces and tabs around the alias and treats
// brackets, pipes, and newlines as syntax, so any of those would make the
// written link parse back as something other than what was intended.
func safeWikilinkAlias(alias string) bool {
	if alias == "" || alias != strings.Trim(alias, " \t") {
		return false
	}
	if strings.ContainsAny(alias, "[]|\r\n") {
		return false
	}
	return !containsControlRune(alias)
}

// synthesisContent serializes the note. The frontmatter config is deliberately
// not passed: a vault that declares `title` as a wikilink property would
// otherwise wrap the title in brackets, and the created-note contract fixes the
// title as plain text.
func synthesisContent(title, draft string, sources []SynthesisSource) ([]byte, error) {
	body, err := renderSynthesisBody(draft, sources)
	if err != nil {
		return nil, err
	}
	doc := frontmatter.Document{Front: frontmatter.NewMap(), Body: body}
	doc.Front.Set("title", title)
	out, err := frontmatter.Marshal(doc, nil)
	if err != nil {
		return nil, WrapError(CodeInternal, "synthesize: render frontmatter", err)
	}
	return out, nil
}

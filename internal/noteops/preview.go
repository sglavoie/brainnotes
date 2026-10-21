package noteops

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sglavoie/brainnotes/internal/frontmatter"
)

// PreviewChangeKind identifies which locus of a note a PreviewChange describes.
type PreviewChangeKind string

const (
	// PreviewPath is a change to a note's on-disk path (rename/move).
	PreviewPath PreviewChangeKind = "path"
	// PreviewFrontmatter is a change to a single frontmatter property.
	PreviewFrontmatter PreviewChangeKind = "frontmatter"
	// PreviewBody is a change to a single (1-based) body line.
	PreviewBody PreviewChangeKind = "body"
)

// PreviewText is a JSON-safe before/after rendering of a single value. Present
// distinguishes an absent value (a frontmatter key that does not exist) from a
// present value whose rendered text happens to be empty.
type PreviewText struct {
	Present bool   `json:"present"`
	Text    string `json:"text"`
}

// PreviewChange is one before/after edit within a NotePreview. Key is set for
// frontmatter changes; Line is set for body changes; neither is set for a path
// change.
type PreviewChange struct {
	Kind PreviewChangeKind `json:"kind"`
	Key  string            `json:"key,omitempty"`
	Line int               `json:"line,omitempty"`
	Old  PreviewText       `json:"old"`
	New  PreviewText       `json:"new"`
}

// NotePreview collects the planned changes to one note under its vault-relative
// path. It is the shared, JSON-safe contract used by both CLI --dry-run output
// and the web preview APIs so the two views can never drift.
type NotePreview struct {
	RelPath string          `json:"rel_path"`
	Changes []PreviewChange `json:"changes,omitempty"`
}

// Changed reports whether the preview carries any changes.
func (p NotePreview) Changed() bool { return len(p.Changes) > 0 }

// PresentText builds a present PreviewText from an already-rendered string.
func PresentText(text string) PreviewText {
	return PreviewText{Present: true, Text: text}
}

// AbsentText is the zero PreviewText: a value that is not present.
func AbsentText() PreviewText { return PreviewText{} }

// previewValueText renders a Go-native frontmatter value into a PreviewText,
// preserving the present/absent distinction.
func previewValueText(present bool, v any) PreviewText {
	if !present {
		return AbsentText()
	}
	return PresentText(FormatPreviewValue(v))
}

// FrontmatterChange builds a frontmatter PreviewChange for key from its
// before/after presence and Go-native values.
func FrontmatterChange(key string, beforePresent bool, beforeVal any, afterPresent bool, afterVal any) PreviewChange {
	return PreviewChange{
		Kind: PreviewFrontmatter,
		Key:  key,
		Old:  previewValueText(beforePresent, beforeVal),
		New:  previewValueText(afterPresent, afterVal),
	}
}

// RenderNotePreview writes the canonical text rendering of a single note's
// preview to out. Nothing is written when the note has no changes, so callers
// can pass unchanged notes through without emitting a stray `--- <relpath>`
// header. The canonical line forms are:
//
//	--- <relpath>
//	path: <old> -> <new>
//	frontmatter[<key>]: <old> -> <new>
//	body:<line>: <old> -> <new>
func RenderNotePreview(out io.Writer, p NotePreview) error {
	if !p.Changed() {
		return nil
	}
	if _, err := fmt.Fprintf(out, "--- %s\n", p.RelPath); err != nil {
		return err
	}
	for _, c := range p.Changes {
		if _, err := io.WriteString(out, previewChangeLine(c)); err != nil {
			return err
		}
	}
	return nil
}

// RenderNotePreviews renders every preview in order via RenderNotePreview.
func RenderNotePreviews(out io.Writer, previews []NotePreview) error {
	for _, p := range previews {
		if err := RenderNotePreview(out, p); err != nil {
			return err
		}
	}
	return nil
}

func previewChangeLine(c PreviewChange) string {
	switch c.Kind {
	case PreviewPath:
		return fmt.Sprintf("path: %s -> %s\n", c.Old.Text, c.New.Text)
	case PreviewFrontmatter:
		return fmt.Sprintf("frontmatter[%s]: %s -> %s\n", c.Key, c.Old.Text, c.New.Text)
	case PreviewBody:
		return fmt.Sprintf("body:%d: %s -> %s\n", c.Line, c.Old.Text, c.New.Text)
	default:
		return ""
	}
}

// FormatPreviewValue renders v as a single-line representation for previews.
// Lists use flow style `[a, b, c]` so before/after comparisons fit on one line
// each. Whole-number floats keep a `.0` suffix and timestamps use the same
// rendering as a real save, so the preview always matches the apply path.
func FormatPreviewValue(v any) string {
	switch vv := v.(type) {
	case nil:
		return "null"
	case string:
		return vv
	case bool:
		if vv {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", vv)
	case int64:
		return fmt.Sprintf("%d", vv)
	case float64:
		// Whole-number floats must render with a `.0` suffix so the preview
		// matches what yaml.v3 actually writes (`rating: 3.0`). Using `%v`
		// (or `%g`) would emit `3`, which the parser would then read back as
		// int — a real apply-vs-preview mismatch.
		if vv == float64(int64(vv)) {
			return fmt.Sprintf("%d.0", int64(vv))
		}
		return fmt.Sprintf("%v", vv)
	case time.Time:
		return frontmatter.FormatTimestamp(vv)
	case []any:
		parts := make([]string, len(vv))
		for i, item := range vv {
			parts[i] = FormatPreviewValue(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return fmt.Sprintf("%v", vv)
	}
}

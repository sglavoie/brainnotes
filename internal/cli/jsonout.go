package cli

import (
	"encoding/json"
	"io"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// writeJSON is the one encoder every `--json` output uses: indented, and
// without HTML escaping, so a title such as "Q&A <draft>" reads the same in
// JSON as it does on disk instead of as & and <.
func writeJSON(w io.Writer, payload any) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(payload)
}

// noteJSON is the note record every path-listing `--json` output shares. `path`
// is vault-relative with forward slashes — the convention `related`, `lint`
// and `history` already follow — and `abs_path` is the absolute path the
// plain-text form of the same command prints, so a script can switch from
// lines to JSON without resolving paths itself.
type noteJSON struct {
	Path    string `json:"path"`
	AbsPath string `json:"abs_path"`
	Title   string `json:"title"`
}

// newNoteJSON builds the record for the note at absPath under root.
func newNoteJSON(root, absPath string) noteJSON {
	return noteJSON{
		Path:    relOrAbsPath(root, absPath),
		AbsPath: absPath,
		Title:   (&vault.Note{Path: absPath}).Title(),
	}
}

// writeNotesJSON writes paths, in order, as a JSON array of note records. An
// empty list is `[]`, never `null`.
func writeNotesJSON(w io.Writer, root string, paths []string) error {
	notes := make([]noteJSON, 0, len(paths))
	for _, p := range paths {
		notes = append(notes, newNoteJSON(root, p))
	}
	return writeJSON(w, notes)
}

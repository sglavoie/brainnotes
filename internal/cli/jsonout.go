package cli

import (
	"encoding/json"
	"io"
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

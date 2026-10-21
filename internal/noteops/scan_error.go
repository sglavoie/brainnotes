package noteops

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sglavoie/brainnotes/internal/vault"
)

// ScanError is a command's walk over the whole vault that stopped, most often
// on one note whose frontmatter does not parse. Its message names the command
// and the note's vault-relative path — `replace: scan vault: note: parse
// "Bad.md": frontmatter: ...` — rather than `vault: walk:` and an absolute path
// the user never typed. It unwraps to the walk's own error, so a
// *vault.ParseError is still found with errors.As.
type ScanError struct {
	msg string
	err error
}

func (e *ScanError) Error() string { return e.msg }
func (e *ScanError) Unwrap() error { return e.err }

// WrapScanError wraps a failed vault walk for command op over the vault at
// root. A nil err stays nil.
func WrapScanError(op, root string, err error) error {
	if err == nil {
		return nil
	}
	return &ScanError{msg: op + ": scan vault: " + describeScanCause(root, err), err: err}
}

func describeScanCause(root string, err error) string {
	var parseErr *vault.ParseError
	if errors.As(err, &parseErr) {
		path := parseErr.Path
		if rel, relErr := filepath.Rel(root, path); relErr == nil && !strings.HasPrefix(rel, "..") {
			path = filepath.ToSlash(rel)
		}
		return fmt.Sprintf("note: parse %q: %v", path, parseErr.Err)
	}
	return strings.TrimPrefix(err.Error(), "vault: walk: ")
}

package web

import (
	"net/http"
	"path/filepath"
)

// vaultRootRedactor takes this vault's absolute location out of a message
// before it reaches a reader on another machine. Relative note paths are served
// by every page; the root is served by none, and a remote reader can act on the
// vault-relative remainder without learning where the vault lives on the
// serving machine's disk.
//
// It names the root both as opened and with symlinks resolved, because a
// filesystem error can carry either spelling (on macOS a temp vault under /var
// is reported as /private/var). A zero redactor changes nothing, which is what
// the local profile gets: the person reading is the person whose disk it is.
type vaultRootRedactor struct {
	roots []string
}

// vaultRootRedactorFor builds the redactor the mode asks for. The decision is
// the capability table's (absolutePaths), not an option flag's.
func vaultRootRedactorFor(caps capabilities, root string) vaultRootRedactor {
	if caps.absolutePaths || root == "" {
		return vaultRootRedactor{}
	}
	roots := []string{root}
	if resolved, err := filepath.EvalSymlinks(root); err == nil && resolved != root {
		// The longer spelling goes first so a resolved path is never half
		// rewritten by the shorter one it happens to contain.
		if len(resolved) > len(root) {
			roots = []string{resolved, root}
		} else {
			roots = append(roots, resolved)
		}
	}
	return vaultRootRedactor{roots: roots}
}

func (r vaultRootRedactor) active() bool { return len(r.roots) > 0 }

func (r vaultRootRedactor) redact(message string) string {
	for _, root := range r.roots {
		message = withoutVaultRoot(message, root)
	}
	return message
}

// redactingResponseWriter marks a response as bound for a remote reader, so the
// shared management error writer can redact without every handler threading a
// server through to it. requestGuard installs it only when the mode redacts.
type redactingResponseWriter struct {
	http.ResponseWriter
	redactor vaultRootRedactor
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *redactingResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// redactForReader applies the response's redaction, if it carries one.
func redactForReader(w http.ResponseWriter, message string) string {
	if rw, ok := w.(*redactingResponseWriter); ok {
		return rw.redactor.redact(message)
	}
	return message
}

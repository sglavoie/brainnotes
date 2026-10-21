package web

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/sglavoie/brainnotes/internal/config"
)

// openMediaFile opens a resolved media path for serving and reports ok only
// when the descriptor it opened is a regular file.
//
// resolveContainedRegularFile already checked the path, but by name: between
// that check and this open the entry can be swapped for a FIFO or a device.
// Judging the opened descriptor closes that window, and O_NONBLOCK keeps the
// open itself from waiting forever on a FIFO nobody writes to. A regular file
// ignores the flag.
func openMediaFile(path string) (*os.File, os.FileInfo, bool, error) {
	//bn:direct-write O_RDONLY: this opens a media file for reading only and creates, truncates or writes nothing
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, false, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, false, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, false, nil
	}
	return file, info, true, nil
}

func mediaPathForTarget(root, target string) (string, bool, error) {
	rel, err := normalizeVaultRelativePath(target)
	if err != nil {
		return "", false, nil
	}

	_, ok, err := resolveContainedRegularFile(root, rel)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}

	route, err := mediaPathForRel(rel)
	if err != nil {
		return "", false, err
	}
	return route, true, nil
}

func mediaFilePathFromRequest(root, requestPath string) (string, bool, error) {
	rel, err := mediaRelFromPath(requestPath)
	if err != nil {
		return "", false, nil
	}
	rel, err = normalizeVaultRelativePath(rel)
	if err != nil {
		return "", false, nil
	}
	return resolveContainedRegularFile(root, rel)
}

func normalizeVaultRelativePath(raw string) (string, error) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if raw == "" {
		return "", errorsf("empty vault-relative path")
	}

	parts := strings.Split(raw, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", errorsf("invalid vault-relative path %q", raw)
		}
	}
	return strings.Join(parts, "/"), nil
}

func resolveMediaFile(root, rel string) (string, bool, error) {
	if root == "" || rel == "" {
		return "", false, nil
	}

	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false, errorsf("resolve vault root %q: %v", root, err)
	}

	candidate := filepath.Join(root, filepath.FromSlash(rel))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, errorsf("resolve media path %q: %v", candidate, err)
	}

	contained, err := pathWithinRoot(realRoot, resolved)
	if err != nil {
		return "", false, errorsf("resolve media containment %q: %v", resolved, err)
	}
	if !contained {
		return "", false, nil
	}

	return regularMediaFile(resolved)
}

func regularMediaFile(resolved string) (string, bool, error) {
	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, errorsf("stat media path %q: %v", resolved, err)
	}
	if !info.Mode().IsRegular() {
		return "", false, nil
	}

	return resolved, true, nil
}

func pathWithinRoot(root, path string) (bool, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false, err
	}
	if filepath.IsAbs(rel) {
		return false, nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, nil
	}
	return true, nil
}

// privateVaultFiles names the configuration files that are never attachments.
// Both layers are listed: brainnotes.local.toml carries this machine's editor,
// listen address, and agent command, so it is at least as private as the
// portable file beside it and is classified from the moment it can exist rather
// than once something starts writing one.
var privateVaultFiles = []string{config.FileName, config.LocalFileName}

// Configuration and runtime internals are never attachments, including symlink aliases.
func privateMediaPath(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if strings.HasPrefix(part, ".") {
			return true
		}
		for _, private := range privateVaultFiles {
			if strings.EqualFold(part, private) {
				return true
			}
		}
	}
	return false
}

// activeMediaTypes are the media types a browser executes rather than merely
// renders. An attachment in a vault is arbitrary bytes a person dropped in a
// folder, and /media serves it from the same origin as the note pages — so an
// HTML or SVG attachment that a browser executed would be running script with
// the session's full reach over every other route.
//
// The list is matched on the type alone, with parameters stripped, so
// `text/html; charset=utf-8` and `text/html` are the same answer. The `+xml`
// suffix is handled separately: it is an open set (`image/svg+xml`,
// `application/xhtml+xml`, and any future member) and naming members one by one
// would leave the next one executable.
var activeMediaTypes = map[string]bool{
	"text/html":                     true,
	"text/xml":                      true,
	"text/xsl":                      true,
	"application/xml":               true,
	"application/xhtml+xml":         true,
	"application/javascript":        true,
	"application/ecmascript":        true,
	"application/x-javascript":      true,
	"text/javascript":               true,
	"text/ecmascript":               true,
	"application/x-shockwave-flash": true,
	"application/xslt+xml":          true,
}

// activeMediaType reports whether a browser would execute this content type.
// An unparsable type is treated as active: the safe answer for bytes nobody can
// classify is the restricted one.
func activeMediaType(contentType string) bool {
	base, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return true
	}
	base = strings.ToLower(strings.TrimSpace(base))
	if activeMediaTypes[base] {
		return true
	}
	return strings.HasSuffix(base, "+xml")
}

// applyMediaSafetyHeaders decides how an attachment is allowed to reach the
// browser, and is the only place that decision is made.
//
// A passive type (an image, an audio file, a PDF) is served as declared. An
// active one is served under `Content-Security-Policy: sandbox`, which puts it
// in an opaque origin with scripting disabled, so navigating straight to a
// booby-trapped .html in the vault cannot read any other route. The sandbox is
// applied only to active types rather than to every response, because a
// restrictive policy on a directly navigated image breaks the synthetic image
// document browsers wrap it in.
//
// `declared` distinguishes a type that came from the file extension from one
// http.DetectContentType guessed at. A guessed active type is a file whose name
// claims nothing and whose bytes look executable; it is downgraded to
// application/octet-stream so the browser cannot be talked into running it.
// Either way an active type is also marked as an attachment, so following a
// link to one downloads it instead of executing it in this origin.
func applyMediaSafetyHeaders(header http.Header, filename, contentType string, declared bool) {
	if contentType == "" {
		return
	}
	if !activeMediaType(contentType) {
		header.Set("Content-Type", contentType)
		return
	}
	if !declared {
		contentType = "application/octet-stream"
	}
	header.Set("Content-Type", contentType)
	header.Set("Content-Security-Policy", "sandbox")
	header.Set("Content-Disposition", attachmentDisposition(filename))
}

// attachmentDisposition builds the Content-Disposition header for a filename
// that came out of the vault, so a name is never concatenated into a header by
// hand. mime.FormatMediaType quotes and escapes it — and RFC 2231-encodes a
// non-ASCII one — so a name holding a quote, a semicolon or a newline cannot
// forge a header of its own. It answers "" for a name it cannot encode at all
// (invalid UTF-8); a bare `attachment` still downloads the file, which is the
// property being defended, so the name is dropped rather than the header.
func attachmentDisposition(filename string) string {
	if formatted := mime.FormatMediaType("attachment", map[string]string{"filename": filename}); formatted != "" {
		return formatted
	}
	return "attachment"
}

func resolveContainedRegularFile(root, rel string) (string, bool, error) {
	if privateMediaPath(rel) {
		return "", false, nil
	}
	resolved, ok, err := resolveMediaFile(root, rel)
	if err != nil || !ok {
		return resolved, ok, err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false, err
	}
	resolvedRel, err := filepath.Rel(realRoot, resolved)
	if err != nil || privateMediaPath(filepath.ToSlash(resolvedRel)) {
		return "", false, err
	}
	return resolved, true, nil
}

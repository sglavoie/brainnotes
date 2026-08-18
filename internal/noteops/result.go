package noteops

// Result is the success payload shape for web management operations.
type Result struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	// Detail carries the exact bytes an operation wrote when the client must not
	// reconstruct them itself — a capture bullet's timestamp comes from the
	// server's clock, so echoing it is the only way the browser can show what
	// actually landed in the note.
	Detail    string `json:"detail,omitempty"`
	Key       string `json:"key,omitempty"`
	Path      string `json:"path,omitempty"`
	RelPath   string `json:"rel_path,omitempty"`
	JournalID string `json:"journal_id,omitempty"`
}

// ErrorResult is the error payload shape for web management operations.
type ErrorResult struct {
	OK      bool      `json:"ok"`
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

// Success builds a management success response.
func Success(message string) Result {
	return Result{OK: true, Message: message}
}

// ResultForPath builds a success response for a note path operation.
func ResultForPath(message, key, path, relPath, journalID string) Result {
	return Result{
		OK:        true,
		Message:   message,
		Key:       key,
		Path:      path,
		RelPath:   relPath,
		JournalID: journalID,
	}
}

// ErrorResponse builds a management error response from err.
func ErrorResponse(err error) ErrorResult {
	return ErrorResult{
		OK:      false,
		Code:    CodeOf(err),
		Message: MessageOf(err),
	}
}

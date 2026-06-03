package noteops

import (
	"errors"
	"fmt"
	"net/http"
)

// ErrorCode is a stable machine-readable operation error class.
type ErrorCode string

const (
	CodeInvalidRequest  ErrorCode = "invalid_request"
	CodeInvalidKey      ErrorCode = "invalid_key"
	CodeNotFound        ErrorCode = "not_found"
	CodeConflict        ErrorCode = "conflict"
	CodePreviewRequired ErrorCode = "preview_required"
	CodePreviewStale    ErrorCode = "preview_stale"
	CodeForbidden       ErrorCode = "forbidden"
	CodeJournal         ErrorCode = "journal_error"
	CodeInternal        ErrorCode = "internal_error"
)

// Error carries a stable code for web JSON responses while preserving causes.
type Error struct {
	Code    ErrorCode
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	if e.Code != "" {
		return string(e.Code)
	}
	return "note operation error"
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Errorf creates a classified operation error.
func Errorf(code ErrorCode, format string, args ...any) error {
	return &Error{
		Code:    code,
		Message: fmt.Sprintf(format, args...),
	}
}

// WrapError classifies err with a stable operation code and public message.
func WrapError(code ErrorCode, message string, err error) error {
	if err == nil {
		return nil
	}
	return &Error{
		Code:    code,
		Message: message,
		Err:     err,
	}
}

// CodeOf returns the classified code for err, defaulting to internal_error.
func CodeOf(err error) ErrorCode {
	if err == nil {
		return ""
	}
	var opErr *Error
	if errors.As(err, &opErr) && opErr.Code != "" {
		return opErr.Code
	}
	return CodeInternal
}

// MessageOf returns the public message for err.
func MessageOf(err error) string {
	if err == nil {
		return ""
	}
	var opErr *Error
	if errors.As(err, &opErr) && opErr.Message != "" {
		return opErr.Message
	}
	return err.Error()
}

// HTTPStatusForError maps operation error classes to management API statuses.
func HTTPStatusForError(err error) int {
	switch CodeOf(err) {
	case CodeInvalidRequest, CodeInvalidKey:
		return http.StatusBadRequest
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeConflict, CodePreviewRequired, CodePreviewStale:
		return http.StatusConflict
	case CodeJournal, CodeInternal:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

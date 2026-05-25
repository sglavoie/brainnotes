package template

import (
	"strings"
	"testing"
)

func TestRenderParseError(t *testing.T) {
	t.Parallel()
	ctx := NewContext("x", "", "/v", nil, nil)
	err := mustRenderError(t, "{{ .Unclosed", ctx)
	if !strings.Contains(err.Error(), "parse") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestRenderExecuteError(t *testing.T) {
	t.Parallel()
	ctx := NewContext("x", "", "/v", nil, nil)
	// Calling a known function with the wrong number of args triggers an
	// execute error (dateFormat expects two args).
	err := mustRenderError(t, `{{ dateFormat "2006" }}`, ctx)
	if !strings.Contains(err.Error(), "execute") && !strings.Contains(err.Error(), "parse") {
		t.Fatalf("expected execute or parse error, got %v", err)
	}
}

func TestRenderUndefinedField(t *testing.T) {
	t.Parallel()
	// Referencing a non-existent Context field must surface a clear execute
	// error rather than panic or silently render the literal Go zero form.
	ctx := NewContext("x", "", "/v", nil, nil)
	err := mustRenderError(t, `{{ .NoSuchField }}`, ctx)
	if !strings.Contains(err.Error(), "execute") {
		t.Fatalf("expected execute error, got %v", err)
	}
}

func TestRenderUnknownFunction(t *testing.T) {
	t.Parallel()
	// Referencing a non-existent helper must produce a parse error.
	ctx := NewContext("x", "", "/v", nil, nil)
	err := mustRenderError(t, `{{ nosuchfunction "x" }}`, ctx)
	if !strings.Contains(err.Error(), "parse") {
		t.Fatalf("expected parse error, got %v", err)
	}
}

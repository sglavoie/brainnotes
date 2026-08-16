package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteSchemaHint(t *testing.T) {
	var buf bytes.Buffer
	writeSchemaHint(&buf, "lint", "/home/user/.config/bnotes/config.toml")
	got := buf.String()

	if !strings.HasPrefix(got, "lint: no frontmatter properties defined in config.\n") {
		t.Errorf("unexpected prefix: %q", got)
	}
	if !strings.Contains(got, "Config: /home/user/.config/bnotes/config.toml") {
		t.Errorf("expected config path in output, got:\n%s", got)
	}
	if !strings.Contains(got, "[frontmatter.properties.status]") {
		t.Errorf("expected example schema block, got:\n%s", got)
	}
	if !strings.Contains(got, "bn config edit") {
		t.Errorf("expected config edit hint, got:\n%s", got)
	}
}

func TestWriteSchemaHint_EmptyPath(t *testing.T) {
	var buf bytes.Buffer
	writeSchemaHint(&buf, "repair", "")
	got := buf.String()

	if !strings.HasPrefix(got, "repair: no frontmatter properties defined in config.\n") {
		t.Errorf("unexpected prefix: %q", got)
	}
	if strings.Contains(got, "Config:") {
		t.Errorf("should not print Config line when path is empty, got:\n%s", got)
	}
}

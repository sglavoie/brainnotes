package cli

import (
	"reflect"
	"testing"
)

func TestAppendEditorLineTarget_TerminalEditor(t *testing.T) {
	got := appendEditorLineTarget([]string{"nvim", "--clean"}, "/vault/Note.md", 12)
	want := []string{"nvim", "--clean", "+12", "/vault/Note.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
}

func TestAppendEditorLineTarget_VSCodeAddsGoto(t *testing.T) {
	got := appendEditorLineTarget([]string{"code", "--wait"}, "/vault/Note.md", 12)
	want := []string{"code", "--wait", "-g", "/vault/Note.md:12"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
}

func TestAppendEditorLineTarget_VSCodeKeepsGoto(t *testing.T) {
	got := appendEditorLineTarget([]string{"code", "--goto"}, "/vault/Note.md", 12)
	want := []string{"code", "--goto", "/vault/Note.md:12"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
}

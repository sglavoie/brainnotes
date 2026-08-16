package web

import (
	"os"
	"reflect"
	"testing"
)

func TestEditorProcessCommandInheritsServerTerminal(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	argv := []string{"configured-editor", "--wait", "/vault/Note.md"}

	cmd := editorProcessCommand(bin, argv)

	if !reflect.DeepEqual(cmd.Args, argv) {
		t.Fatalf("editor argv = %#v, want %#v", cmd.Args, argv)
	}
	if cmd.Stdin != os.Stdin {
		t.Fatal("editor stdin does not inherit the server stdin")
	}
	if cmd.Stdout != os.Stdout {
		t.Fatal("editor stdout does not inherit the server stdout")
	}
	if cmd.Stderr != os.Stderr {
		t.Fatal("editor stderr does not inherit the server stderr")
	}
}

package vault

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRenameNoReplace(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("atomic no-replace rename is unsupported on this platform")
	}

	t.Run("vacant destination", func(t *testing.T) {
		root := t.TempDir()
		source := filepath.Join(root, "source")
		destination := filepath.Join(root, "destination")
		if err := os.WriteFile(source, []byte("source"), 0o644); err != nil {
			t.Fatalf("write source: %v", err)
		}
		if err := renameNoReplace(source, destination); err != nil {
			t.Fatalf("renameNoReplace: %v", err)
		}
		if _, err := os.Stat(source); !os.IsNotExist(err) {
			t.Fatalf("source should have moved, stat err = %v", err)
		}
		if got := readFileString(t, destination); got != "source" {
			t.Fatalf("destination = %q", got)
		}
	})

	t.Run("occupied destination", func(t *testing.T) {
		root := t.TempDir()
		source := filepath.Join(root, "source")
		destination := filepath.Join(root, "destination")
		if err := os.WriteFile(source, []byte("source"), 0o644); err != nil {
			t.Fatalf("write source: %v", err)
		}
		if err := os.WriteFile(destination, []byte("destination"), 0o644); err != nil {
			t.Fatalf("write destination: %v", err)
		}
		if err := renameNoReplace(source, destination); !os.IsExist(err) {
			t.Fatalf("renameNoReplace error = %v, want destination-exists", err)
		}
		if got := readFileString(t, source); got != "source" {
			t.Fatalf("source = %q", got)
		}
		if got := readFileString(t, destination); got != "destination" {
			t.Fatalf("destination = %q", got)
		}
	})
}

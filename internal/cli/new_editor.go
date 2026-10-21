package cli

import (
	"os"

	"github.com/sglavoie/brainnotes/internal/editor"
)

// runEditor execs the editor on file. Its failures — an editor that is not
// installed, cannot be executed, or fails to start — are a user-visible
// child's, so cmd/bn maps them to 127/126.
func runEditor(editorSpec, file string) error {
	return runEditorAtLine(editorSpec, file, 0)
}

func runEditorAtLine(editorSpec, file string, line int) error {
	bin, argv, err := editor.Resolve(editorSpec, file, line)
	if err != nil {
		return childProcessError(err)
	}
	return execEditor(bin, argv)
}

func execEditor(bin string, argv []string) error {
	return childProcessError(execReplace(bin, argv, os.Environ()))
}

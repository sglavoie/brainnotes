package cli

import (
	"os"

	"github.com/sglavoie/sglavoie_brainnotes/internal/editor"
)

func runEditor(editorSpec, file string) error {
	bin, argv, err := editor.Resolve(editorSpec, file, 0)
	if err != nil {
		return err
	}
	return execEditor(bin, argv)
}

func runEditorAtLine(editorSpec, file string, line int) error {
	bin, argv, err := editor.Resolve(editorSpec, file, line)
	if err != nil {
		return err
	}
	return execEditor(bin, argv)
}

func execEditor(bin string, argv []string) error {
	return execReplace(bin, argv, os.Environ())
}

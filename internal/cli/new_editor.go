package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unicode"
)

func runEditor(editor, file string) error {
	bin, argv, err := editorInvocation(editor, file)
	if err != nil {
		return err
	}
	return execEditor(bin, argv)
}

func execEditor(bin string, argv []string) error {
	return execReplace(bin, argv, os.Environ())
}

func editorInvocation(editor, file string) (string, []string, error) {
	argv, err := splitEditorCommand(editor)
	if err != nil {
		return "", nil, err
	}
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return "", nil, fmt.Errorf("locate editor %q: %w", argv[0], err)
	}
	argv = append(argv, file)
	return bin, argv, nil
}

func splitEditorCommand(editor string) ([]string, error) {
	var (
		args    []string
		current strings.Builder
		quote   rune
	)
	runes := []rune(editor)
	flush := func() {
		if current.Len() == 0 {
			return
		}
		args = append(args, current.String())
		current.Reset()
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote != 0 {
			switch {
			case r == quote:
				quote = 0
			case r == '\\' && quote != '\'' && i+1 < len(runes):
				next := runes[i+1]
				if next == quote || next == '\\' || unicode.IsSpace(next) {
					current.WriteRune(next)
					i++
				} else {
					current.WriteRune(r)
				}
			default:
				current.WriteRune(r)
			}
			continue
		}
		switch {
		case unicode.IsSpace(r):
			flush()
		case r == '\'' || r == '"':
			quote = r
		case r == '\\' && i+1 < len(runes):
			next := runes[i+1]
			if next == '\\' || next == '\'' || next == '"' || unicode.IsSpace(next) {
				current.WriteRune(next)
				i++
			} else {
				current.WriteRune(r)
			}
		default:
			current.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("parse editor %q: unterminated quote", editor)
	}
	flush()
	if len(args) == 0 {
		return nil, errors.New("editor command is empty")
	}
	return args, nil
}

package editor

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
)

// ErrNoEditorConfigured is returned when neither configured nor env editor is available.
var ErrNoEditorConfigured = errors.New("no editor configured")

var lookPath = exec.LookPath

// Spec chooses the editor command using configured first, then env fallback.
func Spec(configured, env string) (string, error) {
	if configured != "" {
		return configured, nil
	}
	if env != "" {
		return env, nil
	}
	return "", ErrNoEditorConfigured
}

// Resolve resolves an editor invocation for file, optionally targeting line.
// A line value <= 0 opens the file without a line hint.
func Resolve(spec, file string, line int) (bin string, argv []string, err error) {
	argv, err = splitCommand(spec)
	if err != nil {
		return "", nil, err
	}
	bin, err = lookPath(argv[0])
	if err != nil {
		return "", nil, fmt.Errorf("locate editor %q: %w", argv[0], err)
	}
	if line <= 0 {
		argv = append(argv, file)
		return bin, argv, nil
	}
	argv = appendLineTarget(argv, file, line)
	return bin, argv, nil
}

func appendLineTarget(argv []string, file string, line int) []string {
	name := strings.ToLower(filepath.Base(argv[0]))
	name = strings.TrimSuffix(name, ".exe")
	switch name {
	case "code", "code-insiders", "codium", "vscodium", "cursor", "windsurf":
		if !hasGotoArg(argv[1:]) {
			argv = append(argv, "-g")
		}
		return append(argv, fmt.Sprintf("%s:%d", file, line))
	case "subl", "sublime_text", "mate", "zed", "hx", "helix":
		return append(argv, fmt.Sprintf("%s:%d", file, line))
	default:
		return append(argv, fmt.Sprintf("+%d", line), file)
	}
}

func hasGotoArg(args []string) bool {
	for _, arg := range args {
		if arg == "-g" || arg == "--goto" {
			return true
		}
	}
	return false
}

func splitCommand(spec string) ([]string, error) {
	var (
		args    []string
		current strings.Builder
		quote   rune
	)
	runes := []rune(spec)
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
		return nil, fmt.Errorf("parse editor %q: unterminated quote", spec)
	}
	flush()
	if len(args) == 0 {
		return nil, errors.New("editor command is empty")
	}
	return args, nil
}

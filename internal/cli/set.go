package cli

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// NewSetCmd returns the `set` subcommand. Exported for tests.
func NewSetCmd() *cobra.Command {
	var (
		typeFlag string
		nullSep  bool
	)

	cmd := &cobra.Command{
		Use:   "set <note...> <key=value...>",
		Short: "Set frontmatter properties on one or more notes",
		Long: `Set writes or updates frontmatter properties on one or more notes.

Positional arguments are split into notes and key=value pairs: any arg containing
'=' is treated as a pair, and all preceding args are note paths. Use '-' as the
sole note arg to read paths from stdin (composes with 'bn find').

Type coercion (auto): true/false -> bool, integer -> int, YYYY-MM-DD -> date,
else string. Use --type to force date|int|bool|raw. Properties listed in
list_properties are comma-split before coercion. Wikilink-typed properties are
auto-wrapped when the note is written.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("set: no config in context")
			}
			v, err := vault.Open(cfg)
			if err != nil {
				return err
			}

			noteArgs, pairs, err := splitSetArgs(args)
			if err != nil {
				return err
			}
			if len(noteArgs) == 0 {
				return errors.New("set: at least one note path is required")
			}
			if len(pairs) == 0 {
				return errors.New("set: at least one key=value pair is required")
			}

			parsedPairs, err := parsePairs(pairs, typeFlag, cfg)
			if err != nil {
				return err
			}

			paths, err := resolveNoteInputs(v.Root, noteArgs, cmd.InOrStdin(), nullSep)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			for _, p := range paths {
				if err := applySet(p, parsedPairs, &cfg.Frontmatter); err != nil {
					return err
				}
				if _, err := fmt.Fprintf(out, "%s: updated\n", p); err != nil {
					return err
				}
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&typeFlag, "type", "", "force value type: date|int|bool|raw (default: auto)")
	f.BoolVarP(&nullSep, "null", "0", false, "read NUL-separated paths from stdin")
	return cmd
}

// splitSetArgs partitions args. Any arg containing '=' is a key=value pair,
// and all args before the first pair are note paths. A non-pair argument
// appearing after a pair is rejected so users cannot accidentally interleave.
func splitSetArgs(args []string) (notes, pairs []string, err error) {
	seenPair := false
	for _, a := range args {
		if strings.Contains(a, "=") {
			pairs = append(pairs, a)
			seenPair = true
			continue
		}
		if seenPair {
			return nil, nil, fmt.Errorf("set: note path %q must come before key=value pairs", a)
		}
		notes = append(notes, a)
	}
	return notes, pairs, nil
}

type setPair struct {
	Key   string
	Value any
}

// parsePairs converts raw "key=value" strings into typed setPair entries,
// applying list splitting for keys configured in list_properties.
func parsePairs(pairs []string, typeFlag string, cfg *config.Config) ([]setPair, error) {
	out := make([]setPair, 0, len(pairs))
	for _, p := range pairs {
		idx := strings.IndexByte(p, '=')
		if idx <= 0 {
			return nil, fmt.Errorf("set: invalid pair %q (expected key=value)", p)
		}
		key := p[:idx]
		rawVal := p[idx+1:]

		if containsString(cfg.Frontmatter.ListProperties, key) {
			parts := strings.Split(rawVal, ",")
			vals := make([]any, 0, len(parts))
			for _, part := range parts {
				trimmed := strings.TrimSpace(part)
				if trimmed == "" {
					continue
				}
				v, err := coerceValue(trimmed, typeFlag)
				if err != nil {
					return nil, err
				}
				vals = append(vals, v)
			}
			out = append(out, setPair{Key: key, Value: vals})
			continue
		}

		v, err := coerceValue(rawVal, typeFlag)
		if err != nil {
			return nil, err
		}
		out = append(out, setPair{Key: key, Value: v})
	}
	return out, nil
}

var (
	intValueRe  = regexp.MustCompile(`^\d+$`)
	dateValueRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// coerceValue converts a raw string to a Go-native value. When typeFlag is
// empty, the order of auto-detection matches the spec: bool > int > date >
// string. "raw" forces the literal string with no coercion.
func coerceValue(s, typeFlag string) (any, error) {
	switch typeFlag {
	case "date":
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return nil, fmt.Errorf("set: invalid date %q: %w", s, err)
		}
		return t, nil
	case "int":
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil, fmt.Errorf("set: invalid int %q: %w", s, err)
		}
		return n, nil
	case "bool":
		switch strings.ToLower(s) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return nil, fmt.Errorf("set: invalid bool %q (expected true or false)", s)
	case "raw":
		return s, nil
	case "", "auto":
		// fall through to auto-detect
	default:
		return nil, fmt.Errorf("set: unknown --type %q (want date|int|bool|raw)", typeFlag)
	}

	switch strings.ToLower(s) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	if intValueRe.MatchString(s) {
		if n, err := strconv.Atoi(s); err == nil {
			return n, nil
		}
	}
	if dateValueRe.MatchString(s) {
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return t, nil
		}
	}
	return s, nil
}

// applySet loads the note, applies every pair in a single read-mutate-write
// cycle, and saves atomically. Wikilink wrapping is handled by frontmatter
// Marshal via the supplied config.
func applySet(path string, pairs []setPair, fm *config.FrontmatterConfig) error {
	n := &vault.Note{Path: path}
	if err := n.Load(); err != nil {
		return err
	}
	front, err := n.Front()
	if err != nil {
		return err
	}
	for _, p := range pairs {
		front.Set(p.Key, p.Value)
	}
	return n.Save(fm)
}

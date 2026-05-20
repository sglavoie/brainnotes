package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// NewSetCmd returns the `set` subcommand. Exported for tests.
func NewSetCmd() *cobra.Command {
	var (
		typeFlag string
		nullSep  bool
		dryRun   bool
	)

	cmd := &cobra.Command{
		Use:   "set [note...] <key[=|+=|-=]value...>",
		Short: "Set frontmatter properties on one or more notes",
		Long: `Set writes or updates frontmatter properties on one or more notes.

Positional arguments are split into notes and pairs: any arg containing an
operator ('=', '+=', '-=') is treated as a pair, and all preceding args are
note paths. Use '-' as the sole note arg to read paths from stdin (composes
with 'bn find'). When no note args are given, an fzf multi-select picker
launches to choose notes interactively.

Operators:
  key=value    Set the property (overwriting any prior value).
  key+=value   Append to a list property (no-op if value already present).
  key-=value   Remove a value from a list property (no-op if absent).

'+=' and '-=' require the key to be in [frontmatter].list_properties. Values
are comma-split for list properties exactly as with '='. Mixing operators in
one invocation is allowed and applied left-to-right.

Type coercion (auto): true/false -> bool, integer -> int, YYYY-MM-DD -> date,
else string. Use --type to force date|int|bool|raw. Wikilink-typed properties
are auto-wrapped before mutation.

Each file is rewritten atomically, but there is no cross-file transaction: if
an error stops the run mid-way, prior writes stand. Use 'git diff' to recover.

Use --dry-run to preview changes without writing. Each affected file is
printed as a unified diff block; unchanged files are omitted.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MinimumNArgs(1),
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
			if len(pairs) == 0 {
				return errors.New("set: at least one key=value pair is required")
			}

			parsedPairs, err := parsePairs(pairs, typeFlag, cfg)
			if err != nil {
				return err
			}

			var paths []string
			if len(noteArgs) == 0 {
				selected, err := pickNotes(v, cfg, true)
				if err != nil {
					return err
				}
				if len(selected) == 0 {
					return nil
				}
				paths = selected
			} else {
				paths, err = resolveNoteInputs(v.Root, noteArgs, cmd.InOrStdin(), nullSep)
				if err != nil {
					return err
				}
			}
			sort.Strings(paths)

			out := cmd.OutOrStdout()
			var dry *DryRun
			if dryRun {
				dry = &DryRun{Out: out}
			}
			for _, p := range paths {
				changed, err := applySet(p, parsedPairs, &cfg.Frontmatter, v.Root, dry)
				if err != nil {
					return err
				}
				if changed && dry == nil {
					if _, err := fmt.Fprintf(out, "%s: updated\n", p); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&typeFlag, "type", "", "force value type: date|int|bool|raw (default: auto)")
	f.BoolVarP(&nullSep, "null", "0", false, "read NUL-separated paths from stdin")
	f.BoolVar(&dryRun, "dry-run", false, "preview changes without writing")
	return cmd
}

// splitSetArgs partitions args. Any arg containing '=' (which covers '=',
// '+=', and '-=') is a pair, and all args before the first pair are note
// paths. A non-pair argument appearing after a pair is rejected so users
// cannot accidentally interleave.
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

// setOp identifies the mutation requested for a single pair.
type setOp int

const (
	opSet setOp = iota
	opAdd
	opRemove
)

type setPair struct {
	Key   string
	Op    setOp
	Value any
}

var pairKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// splitPairOp finds the leftmost operator ('=', '+=', or '-=') in p and
// returns the key, the operator literal, and the raw value. Two-character
// operators take precedence when they share a starting position with '='.
func splitPairOp(p string) (key, op, val string, ok bool) {
	for i := 0; i < len(p); i++ {
		if i+1 < len(p) && p[i+1] == '=' && (p[i] == '+' || p[i] == '-') {
			return p[:i], p[i : i+2], p[i+2:], true
		}
		if p[i] == '=' {
			return p[:i], "=", p[i+1:], true
		}
	}
	return "", "", "", false
}

// parsePairs converts raw pair strings into typed setPair entries, applying
// list splitting for keys configured in list_properties and wikilink wrapping
// for keys configured in wikilink_properties (for '+=' and '-=').
func parsePairs(pairs []string, typeFlag string, cfg *config.Config) ([]setPair, error) {
	out := make([]setPair, 0, len(pairs))
	for _, p := range pairs {
		key, opStr, rawVal, ok := splitPairOp(p)
		if !ok || key == "" {
			return nil, fmt.Errorf("set: invalid pair %q (expected key=value, key+=value, or key-=value)", p)
		}
		if !pairKeyRe.MatchString(key) {
			return nil, fmt.Errorf("set: invalid key %q (allowed: letters, digits, '_', '-')", key)
		}

		isList := containsString(cfg.Frontmatter.ListProperties, key)
		isWikilink := containsString(cfg.Frontmatter.WikilinkProperties, key)

		switch opStr {
		case "=":
			if isList {
				vals, err := splitListValues(rawVal, typeFlag)
				if err != nil {
					return nil, err
				}
				out = append(out, setPair{Key: key, Op: opSet, Value: vals})
				continue
			}
			v, err := coerceValue(rawVal, typeFlag)
			if err != nil {
				return nil, err
			}
			out = append(out, setPair{Key: key, Op: opSet, Value: v})

		case "+=", "-=":
			if !isList {
				return nil, fmt.Errorf("set: operator %q requires %q to be in [frontmatter].list_properties", opStr, key)
			}
			if strings.TrimSpace(rawVal) == "" {
				return nil, fmt.Errorf("set: empty value for %q on key %q", opStr, key)
			}
			vals, err := splitListValues(rawVal, typeFlag)
			if err != nil {
				return nil, err
			}
			if len(vals) == 0 {
				return nil, fmt.Errorf("set: empty value for %q on key %q", opStr, key)
			}
			if isWikilink {
				for i, v := range vals {
					if s, ok := v.(string); ok {
						vals[i] = wrapWikilinkValue(s)
					}
				}
			}
			op := opAdd
			if opStr == "-=" {
				op = opRemove
			}
			out = append(out, setPair{Key: key, Op: op, Value: vals})
		}
	}
	return out, nil
}

// splitListValues splits a raw comma-separated string into coerced Go-native
// values, dropping empty entries (matching the legacy '=' behavior).
func splitListValues(rawVal, typeFlag string) ([]any, error) {
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
	return vals, nil
}

// wikilinkNormalized returns v with each string element wrapped as `[[X]]`
// when key is a wikilink-typed property. It mirrors the wrap step performed
// by frontmatter.Marshal so an `opSet` comparison can detect that the new
// value would round-trip to the same on-disk bytes as the current value.
// Without this, `bn set parent=Foo` would always rewrite a note whose stored
// value is `[[Foo]]` because the raw user input differs byte-for-byte.
func wikilinkNormalized(v any, key string, fm *config.FrontmatterConfig) any {
	if fm == nil || !containsString(fm.WikilinkProperties, key) {
		return v
	}
	switch vv := v.(type) {
	case string:
		return wrapWikilinkValue(vv)
	case []any:
		out := make([]any, len(vv))
		for i, item := range vv {
			if s, ok := item.(string); ok {
				out[i] = wrapWikilinkValue(s)
			} else {
				out[i] = item
			}
		}
		return out
	}
	return v
}

// wrapWikilinkValue wraps a bare string in `[[...]]` unless it is empty or
// already wrapped. Mirrors the unexported helper in internal/frontmatter so
// the CLI can normalize input before delegating to ListAdd/ListRemove, which
// compare values byte-for-byte.
func wrapWikilinkValue(s string) string {
	if s == "" {
		return s
	}
	if strings.HasPrefix(s, "[[") && strings.HasSuffix(s, "]]") {
		return s
	}
	return "[[" + s + "]]"
}

var (
	// intValueRe accepts an optional leading `-` so negative integers
	// auto-coerce. Before the fix `rating=-3` was stored as the string "-3".
	intValueRe  = regexp.MustCompile(`^-?\d+$`)
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
// cycle, and saves atomically. Returns changed=false (and skips Save) when no
// pair caused a real mutation, so '+=' / '-=' no-ops do not bump file mtime.
// Wikilink wrapping for the '=' case is handled by frontmatter Marshal via
// the supplied config; '+=' / '-=' wrap their inputs at parse time so the
// list-mutation primitives can compare byte-for-byte.
//
// When dry is non-nil, the mutation is computed in memory, a per-file diff is
// emitted to dry.Out, and the file is not written. vaultRoot is used to
// shorten paths in the diff header; pass "" to fall back to the absolute path.
func applySet(path string, pairs []setPair, fm *config.FrontmatterConfig, vaultRoot string, dry *DryRun) (bool, error) {
	n := &vault.Note{Path: path}
	if err := n.Load(); err != nil {
		return false, err
	}
	front, err := n.Front()
	if err != nil {
		return false, err
	}

	var before map[string]snapshot
	if dry != nil {
		before = snapshotKeys(front, pairs)
	}

	changed := false
	for _, p := range pairs {
		switch p.Op {
		case opSet:
			// Normalize wikilink-typed values before storing so the in-memory
			// state matches what Marshal will write to disk. Without this,
			// dry-run output for keys like `categories` reports the raw user
			// input (e.g. `Books`) even though the saved file would contain
			// the wrapped form (`[[Books]]`), producing a misleading preview.
			newVal := wikilinkNormalized(p.Value, p.Key, fm)
			existing, present := front.Get(p.Key)
			if present && dryRunValuesEqual(existing, newVal) {
				continue
			}
			front.Set(p.Key, newVal)
			changed = true
		case opAdd:
			vals, ok := p.Value.([]any)
			if !ok {
				return false, fmt.Errorf("set: internal error: += value for %q is not a list", p.Key)
			}
			c, err := front.ListAdd(p.Key, vals...)
			if err != nil {
				if errors.Is(err, frontmatter.ErrNotAList) {
					return false, fmt.Errorf("set: cannot apply '+=' to %q: existing value is not a list", p.Key)
				}
				return false, err
			}
			if c {
				changed = true
			}
		case opRemove:
			vals, ok := p.Value.([]any)
			if !ok {
				return false, fmt.Errorf("set: internal error: -= value for %q is not a list", p.Key)
			}
			c, err := front.ListRemove(p.Key, vals...)
			if err != nil {
				if errors.Is(err, frontmatter.ErrNotAList) {
					return false, fmt.Errorf("set: cannot apply '-=' to %q: existing value is not a list", p.Key)
				}
				return false, err
			}
			if c {
				changed = true
			}
		}
	}
	if !changed {
		return false, nil
	}
	if dry != nil {
		block := newFileBlock(displayPath(path, vaultRoot))
		for _, key := range orderedKeys(pairs) {
			snap := before[key]
			afterVal, afterOK := front.Get(key)
			if !snap.present && !afterOK {
				continue
			}
			if snap.present && afterOK && dryRunValuesEqual(snap.value, afterVal) {
				continue
			}
			if snap.present {
				block.removed(key, snap.value)
			}
			if afterOK {
				block.added(key, afterVal)
			}
		}
		if err := block.flush(dry.Out); err != nil {
			return false, err
		}
		return !block.empty(), nil
	}
	if err := n.Save(fm); err != nil {
		return false, err
	}
	return true, nil
}

// snapshot captures a key's value (or absence) for later diffing.
type snapshot struct {
	value   any
	present bool
}

// snapshotKeys records the current value of every distinct key in pairs so we
// can compare against the post-mutation state.
func snapshotKeys(front *frontmatter.Map, pairs []setPair) map[string]snapshot {
	out := make(map[string]snapshot, len(pairs))
	for _, p := range pairs {
		if _, seen := out[p.Key]; seen {
			continue
		}
		v, ok := front.Get(p.Key)
		out[p.Key] = snapshot{value: v, present: ok}
	}
	return out
}

// orderedKeys returns the distinct keys from pairs in first-appearance order
// so dry-run output is deterministic regardless of map iteration.
func orderedKeys(pairs []setPair) []string {
	seen := make(map[string]bool, len(pairs))
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		if seen[p.Key] {
			continue
		}
		seen[p.Key] = true
		out = append(out, p.Key)
	}
	return out
}

// displayPath returns a vault-relative path for dry-run headers. Falls back
// to the absolute path when the relative form cannot be computed.
func displayPath(absPath, vaultRoot string) string {
	if vaultRoot == "" {
		return absPath
	}
	rel, err := filepath.Rel(vaultRoot, absPath)
	if err != nil {
		return absPath
	}
	return filepath.ToSlash(rel)
}

package cli

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// splitSetArgs partitions args. Any arg containing '=' (which covers '=',
// '+=', and '-=') is a pair, and all args before the first pair are note
// paths. A non-pair argument appearing after a pair is rejected so users
// cannot accidentally interleave.
func splitSetArgs(args []string) (notes, pairs []string, err error) {
	seenPair := false
	for _, arg := range args {
		if strings.Contains(arg, "=") {
			pairs = append(pairs, arg)
			seenPair = true
			continue
		}
		if seenPair {
			return nil, nil, fmt.Errorf("set: note path %q must come before key=value pairs", arg)
		}
		notes = append(notes, arg)
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
	for _, rawPair := range pairs {
		pair, err := parsePair(rawPair, typeFlag, &cfg.Frontmatter)
		if err != nil {
			return nil, err
		}
		out = append(out, pair)
	}
	return out, nil
}

func parsePair(rawPair, typeFlag string, fm *config.FrontmatterConfig) (setPair, error) {
	key, opStr, rawVal, ok := splitPairOp(rawPair)
	if !ok || key == "" {
		return setPair{}, fmt.Errorf("set: invalid pair %q (expected key=value, key+=value, or key-=value)", rawPair)
	}
	if !pairKeyRe.MatchString(key) {
		return setPair{}, fmt.Errorf("set: invalid key %q (allowed: letters, digits, '_', '-')", key)
	}

	isList := slices.Contains(fm.ListProperties, key)
	isWikilink := slices.Contains(fm.WikilinkProperties, key)
	effectiveType := effectiveSetType(typeFlag, isWikilink)

	switch opStr {
	case "=":
		return parseSetAssignment(key, rawVal, effectiveType, isList)
	case "+=", "-=":
		return parseSetListMutation(key, opStr, rawVal, effectiveType, isList, isWikilink)
	default:
		return setPair{}, fmt.Errorf("set: invalid pair %q (expected key=value, key+=value, or key-=value)", rawPair)
	}
}

func effectiveSetType(typeFlag string, isWikilink bool) string {
	if isWikilink && (typeFlag == "" || typeFlag == "auto") {
		return "raw"
	}
	return typeFlag
}

func parseSetAssignment(key, rawVal, typeFlag string, isList bool) (setPair, error) {
	if isList {
		vals, err := splitListValues(rawVal, typeFlag)
		if err != nil {
			return setPair{}, err
		}
		return setPair{Key: key, Op: opSet, Value: vals}, nil
	}

	value, err := coerceValue(rawVal, typeFlag)
	if err != nil {
		return setPair{}, err
	}
	return setPair{Key: key, Op: opSet, Value: value}, nil
}

func parseSetListMutation(key, opStr, rawVal, typeFlag string, isList, isWikilink bool) (setPair, error) {
	if !isList {
		return setPair{}, fmt.Errorf("set: operator %q requires %q to be in [frontmatter].list_properties", opStr, key)
	}
	if strings.TrimSpace(rawVal) == "" {
		return setPair{}, fmt.Errorf("set: empty value for %q on key %q", opStr, key)
	}

	vals, err := splitListValues(rawVal, typeFlag)
	if err != nil {
		return setPair{}, err
	}
	if len(vals) == 0 {
		return setPair{}, fmt.Errorf("set: empty value for %q on key %q", opStr, key)
	}
	if isWikilink {
		vals = wrapWikilinkValues(vals)
	}

	op := opAdd
	if opStr == "-=" {
		op = opRemove
	}
	return setPair{Key: key, Op: op, Value: vals}, nil
}

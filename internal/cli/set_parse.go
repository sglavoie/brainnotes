package cli

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
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
type setOp = noteops.FrontmatterOp

const (
	opSet    = noteops.FrontmatterSet
	opAdd    = noteops.FrontmatterListAdd
	opRemove = noteops.FrontmatterListRemove
)

type setPair = noteops.FrontmatterMutation

var pairKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// splitPairOp finds the leftmost operator ('=', '+=', or '-=') in p and
// returns the key, the operator literal, and the raw value. Two-character
// operators take precedence when they share a starting position with '='.
func splitPairOp(p string) (key, op, val string, ok bool) {
	return noteops.SplitFrontmatterPairOp(p)
}

// parsePairs converts raw pair strings into typed setPair entries, applying
// list splitting for keys configured in list_properties and wikilink wrapping
// for keys configured in wikilink_properties (for '+=' and '-=').
func parsePairs(pairs []string, typeFlag string, cfg *config.Config) ([]setPair, error) {
	return noteops.ParseFrontmatterMutations(pairs, typeFlag, cfg)
}

func parsePair(rawPair, typeFlag string, fm *config.FrontmatterConfig) (setPair, error) {
	return noteops.ParseFrontmatterMutation(rawPair, typeFlag, fm)
}

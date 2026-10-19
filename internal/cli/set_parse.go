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

type setPair = noteops.FrontmatterMutation

var pairKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// parsePairs converts raw pair strings into typed setPair entries, applying
// list splitting for keys defined as lists and wikilink wrapping for keys
// defined as wikilinks in [frontmatter.properties].
func parsePairs(pairs []string, typeFlag string, cfg *config.Config) ([]setPair, error) {
	return noteops.ParseFrontmatterMutations(pairs, typeFlag, cfg)
}

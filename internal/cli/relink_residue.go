package cli

import (
	"fmt"
	"io"
	"sort"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
)

// relinkResidue is one unresolved link relink deliberately left alone because
// no deterministic target exists.
type relinkResidue struct {
	relPath        string
	line           int
	frontmatterKey string
	raw            string
}

func relinkResiduesForNote(relPath string, broken []query.BrokenLink) []relinkResidue {
	out := make([]relinkResidue, 0, len(broken))
	for _, bl := range broken {
		out = append(out, relinkResidue{
			relPath:        relPath,
			line:           bl.Line,
			frontmatterKey: bl.FrontmatterKey,
			raw:            bl.Link.Raw,
		})
	}
	return out
}

func printRelinkResidues(out io.Writer, residues []relinkResidue) error {
	if len(residues) == 0 {
		return nil
	}
	sort.Slice(residues, func(i, j int) bool {
		if residues[i].relPath != residues[j].relPath {
			return residues[i].relPath < residues[j].relPath
		}
		if residues[i].line != residues[j].line {
			return residues[i].line < residues[j].line
		}
		if residues[i].frontmatterKey != residues[j].frontmatterKey {
			return residues[i].frontmatterKey < residues[j].frontmatterKey
		}
		return residues[i].raw < residues[j].raw
	})
	for _, residue := range residues {
		if residue.frontmatterKey != "" {
			if _, err := fmt.Fprintf(out, "SKIP: %s:frontmatter[%s]: %s (no deterministic target)\n", residue.relPath, residue.frontmatterKey, residue.raw); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(out, "SKIP: %s:%d: %s (no deterministic target)\n", residue.relPath, residue.line, residue.raw); err != nil {
			return err
		}
	}
	return nil
}

func sortRelinkChanges(changes []relinkChange) {
	sort.SliceStable(changes, func(i, j int) bool {
		iBody := changes[i].frontmatterKey == ""
		jBody := changes[j].frontmatterKey == ""
		if iBody != jBody {
			return iBody
		}
		if iBody && changes[i].line != changes[j].line {
			return changes[i].line < changes[j].line
		}
		if changes[i].frontmatterKey != changes[j].frontmatterKey {
			return changes[i].frontmatterKey < changes[j].frontmatterKey
		}
		if changes[i].oldRaw != changes[j].oldRaw {
			return changes[i].oldRaw < changes[j].oldRaw
		}
		return changes[i].newRaw < changes[j].newRaw
	})
}

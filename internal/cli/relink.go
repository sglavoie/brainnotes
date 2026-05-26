package cli

import (
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/wikilink"
)

var relinkMediaExts = map[string]struct{}{
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".svg": {}, ".webp": {},
	".pdf": {}, ".mp4": {}, ".mov": {}, ".mp3": {}, ".wav": {}, ".ogg": {}, ".heic": {},
}

type relinkTarget struct {
	key      string
	relNoExt string
	title    string
}

type relinkIndex struct {
	byPathKey  map[string]relinkTarget
	byTitleKey map[string][]relinkTarget
}

type relinkChange struct {
	line           int
	frontmatterKey string
	oldRaw         string
	newRaw         string
}

type relinkResidue struct {
	relPath        string
	line           int
	frontmatterKey string
	raw            string
}

func NewRelinkCmd() *cobra.Command {
	var dryRun bool

	ff := new(findFlags)

	cmd := &cobra.Command{
		Use:   "relink [notes...]",
		Short: "Repair unresolved wikilinks with safe deterministic rewrites",
		Long: `Relink scans unresolved wikilinks and applies only deterministic rewrites.

Safe rewrites in v1:
  - exact path/key match after path normalization (e.g. backslashes -> '/')
  - exact title match (case-insensitive, optional .md) when unique

Ambiguous targets are skipped and reported for manual follow-up.

Scope:
  - No notes and no find flags: every note in the vault.
  - One or more find flags (--tag, --folder, ...): matching notes only.
  - Positional notes: only those notes (mutually exclusive with find flags).

Atomicity: each file is rewritten via a temp-file + rename. There is no
transaction across files: if a mid-apply error occurs, prior writes stand.
Use 'git diff' to inspect or recover.

Use --dry-run to preview changes without writing.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("relink: no config in context")
			}
			v, err := vault.Open(cfg)
			if err != nil {
				return err
			}

			anyFlagSet := false
			for _, name := range queryFlagNames {
				if f := cmd.Flag(name); f != nil && f.Changed {
					anyFlagSet = true
					break
				}
			}
			if len(args) > 0 && anyFlagSet {
				return errors.New("relink: cannot combine positional notes with find filter flags")
			}

			candidates, err := collectReplaceCandidates(v, cmd, args, ff.toQueryFlags(v.Root, nil))
			if err != nil {
				return err
			}
			allNotes, err := v.Notes()
			if err != nil {
				return err
			}

			g := query.BuildGraphForRoot(v.Root, allNotes)
			idx := newRelinkIndex(v.Root, g)

			out := cmd.OutOrStdout()
			stderr := cmd.ErrOrStderr()
			var dry *DryRun
			if dryRun {
				dry = &DryRun{Out: out}
			}
			var journaler *mutationJournal
			if dry == nil {
				journaler = newMutationJournal(v.Root, commandInvocation(cmd, args))
			}

			relinked := 0
			var residues []relinkResidue
			for _, n := range candidates {
				changed, noteResidues, err := applyRelink(n, idx, g, &cfg.Frontmatter, v.Root, dry, journaler)
				if err != nil {
					_, _ = fmt.Fprintln(stderr, "WARN: relink interrupted; run 'git diff' to inspect partial state")
					return err
				}
				residues = append(residues, noteResidues...)
				if !changed {
					continue
				}
				relinked++
				if dry == nil {
					fmt.Fprintf(out, "relinked: %s\n", displayPath(n.Path, v.Root))
				}
			}
			if err := journaler.Finalize(); err != nil {
				return err
			}

			if err := printRelinkResidues(stderr, residues); err != nil {
				return err
			}
			if len(residues) > 0 {
				fmt.Fprintln(stderr, "\nhint: run 'bn find --broken-links --show-broken' for manual follow-up")
			}
			if dry == nil {
				summary := fmt.Sprintf("relinked %d file(s)", relinked)
				if len(residues) > 0 {
					summary += fmt.Sprintf(", %d unresolved skipped", len(residues))
				}
				fmt.Fprintln(out, summary)
			}

			return nil
		},
	}

	ff.register(cmd)
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview changes without writing")
	return cmd
}

func newRelinkIndex(vaultRoot string, g *query.Graph) *relinkIndex {
	idx := &relinkIndex{
		byPathKey:  make(map[string]relinkTarget),
		byTitleKey: make(map[string][]relinkTarget),
	}
	if g == nil {
		return idx
	}
	keys := make([]string, 0, len(g.Titles))
	for key := range g.Titles {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		absPath, ok := g.PathsByTitle[key]
		if !ok || absPath == "" {
			continue
		}
		relNoExt := relinkRelativeNoExt(vaultRoot, absPath)
		if relNoExt == "" {
			continue
		}
		title := g.OriginalTitles[key]
		if title == "" {
			title = path.Base(relNoExt)
		}
		target := relinkTarget{
			key:      key,
			relNoExt: relNoExt,
			title:    title,
		}
		idx.byPathKey[key] = target
		titleKey := relinkNormalizeTitle(title)
		if titleKey == "" {
			continue
		}
		idx.byTitleKey[titleKey] = append(idx.byTitleKey[titleKey], target)
	}
	for titleKey := range idx.byTitleKey {
		sort.Slice(idx.byTitleKey[titleKey], func(i, j int) bool {
			return idx.byTitleKey[titleKey][i].relNoExt < idx.byTitleKey[titleKey][j].relNoExt
		})
	}
	return idx
}

func relinkRelativeNoExt(vaultRoot, absPath string) string {
	rel, err := filepath.Rel(vaultRoot, absPath)
	if err != nil {
		rel = absPath
	}
	rel = filepath.ToSlash(rel)
	ext := path.Ext(rel)
	return strings.TrimSuffix(rel, ext)
}

func (idx *relinkIndex) resolves(link wikilink.Link) bool {
	return len(idx.resolve(link)) > 0
}

func (idx *relinkIndex) resolve(link wikilink.Link) []relinkTarget {
	if idx == nil {
		return nil
	}
	if link.Folder != "" {
		pathKey := relinkNormalizeTarget(link.Target)
		if pathKey == "" {
			return nil
		}
		if target, ok := idx.byPathKey[pathKey]; ok {
			return []relinkTarget{target}
		}
		titleKey := relinkNormalizeTitle(link.Title())
		if titleKey == "" {
			return nil
		}
		if targets := idx.byTitleKey[titleKey]; len(targets) == 1 {
			return append([]relinkTarget(nil), targets...)
		}
		return nil
	}

	titleKey := relinkNormalizeTitle(link.Title())
	if titleKey == "" {
		return nil
	}
	return append([]relinkTarget(nil), idx.byTitleKey[titleKey]...)
}

func (idx *relinkIndex) deterministicTarget(link wikilink.Link) (string, bool) {
	if idx.resolves(link) {
		return "", false
	}

	title := relinkTargetTitle(link.Target)
	if title == "" || relinkIsMediaEmbed(title) {
		return "", false
	}

	if target, ok := idx.byPathKey[relinkNormalizeTarget(link.Target)]; ok {
		return relinkRenderTarget(link, target), true
	}

	titleKey := relinkNormalizeTitle(title)
	targets := idx.byTitleKey[titleKey]
	if len(targets) != 1 {
		return "", false
	}
	return relinkRenderTarget(link, targets[0]), true
}

func relinkRenderTarget(link wikilink.Link, target relinkTarget) string {
	out := target.title
	if strings.ContainsAny(link.Target, `/\`) {
		out = target.relNoExt
	}
	_, ext := relinkSplitMDExt(relinkTargetTitle(link.Target))
	if ext != "" {
		out += ext
	}
	return out
}

func applyRelink(n *vault.Note, idx *relinkIndex, g *query.Graph, fmCfg *config.FrontmatterConfig, vaultRoot string, dry *DryRun, journaler *mutationJournal) (bool, []relinkResidue, error) {
	preBroken := query.UnresolvedLinks(n, g)
	if len(preBroken) == 0 {
		return false, nil, nil
	}
	bodyLineByStart := make(map[int]int, len(preBroken))
	for _, bl := range preBroken {
		if bl.FrontmatterKey == "" {
			bodyLineByStart[bl.Link.Start] = bl.Line
		}
	}

	var changes []relinkChange

	body, err := n.Body()
	if err != nil {
		return false, nil, err
	}
	newBody, bodyChanges := rewriteRelinkText(body, idx, "", bodyLineByStart)
	if newBody != body {
		if err := n.SetBody(newBody); err != nil {
			return false, nil, err
		}
		changes = append(changes, bodyChanges...)
	}

	front, err := n.Front()
	if err != nil {
		return false, nil, err
	}
	for _, key := range front.Keys() {
		value, ok := front.Get(key)
		if !ok {
			continue
		}
		newValue, fmChanges, changed := rewriteRelinkValue(value, key, idx)
		if !changed {
			continue
		}
		front.Set(key, newValue)
		changes = append(changes, fmChanges...)
	}

	relPath := displayPath(n.Path, vaultRoot)
	if len(changes) == 0 {
		return false, relinkResiduesForNote(relPath, preBroken), nil
	}
	sortRelinkChanges(changes)

	if dry != nil {
		block := newFileBlock(relPath)
		for _, change := range changes {
			label := change.frontmatterKey
			if label == "" {
				label = "body"
				if change.line > 0 {
					label = fmt.Sprintf("body[%d]", change.line)
				}
			}
			block.removed(label, change.oldRaw)
			block.added(label, change.newRaw)
		}
		if err := block.flush(dry.Out); err != nil {
			return false, nil, err
		}
	} else {
		if err := journaler.Capture(n.Path); err != nil {
			return false, nil, err
		}
		if err := n.Save(fmCfg); err != nil {
			return false, nil, err
		}
	}

	postBroken := query.UnresolvedLinks(n, g)
	return true, relinkResiduesForNote(relPath, postBroken), nil
}

func rewriteRelinkValue(v any, topKey string, idx *relinkIndex) (any, []relinkChange, bool) {
	switch vv := v.(type) {
	case string:
		out, changes := rewriteRelinkText(vv, idx, topKey, nil)
		if out == vv {
			return v, nil, false
		}
		return out, changes, true
	case []any:
		out := make([]any, len(vv))
		changed := false
		var changes []relinkChange
		for i, item := range vv {
			newItem, itemChanges, itemChanged := rewriteRelinkValue(item, topKey, idx)
			out[i] = newItem
			if itemChanged {
				changed = true
			}
			changes = append(changes, itemChanges...)
		}
		if !changed {
			return v, nil, false
		}
		return out, changes, true
	case map[string]any:
		keys := make([]string, 0, len(vv))
		for key := range vv {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(vv))
		changed := false
		var changes []relinkChange
		for _, key := range keys {
			newItem, itemChanges, itemChanged := rewriteRelinkValue(vv[key], topKey, idx)
			out[key] = newItem
			if itemChanged {
				changed = true
			}
			changes = append(changes, itemChanges...)
		}
		if !changed {
			return v, nil, false
		}
		return out, changes, true
	default:
		return v, nil, false
	}
}

func rewriteRelinkText(text string, idx *relinkIndex, frontmatterKey string, bodyLineByStart map[int]int) (string, []relinkChange) {
	var changes []relinkChange
	out := wikilink.Rewrite(text, func(link wikilink.Link) (string, bool) {
		newTarget, ok := idx.deterministicTarget(link)
		if !ok {
			return "", false
		}
		replacement := wikilink.FormatLink(newTarget, link.Heading, link.Alias)
		if replacement == link.Raw {
			return "", false
		}
		change := relinkChange{
			frontmatterKey: frontmatterKey,
			oldRaw:         link.Raw,
			newRaw:         replacement,
		}
		if frontmatterKey == "" {
			change.line = bodyLineByStart[link.Start]
		}
		changes = append(changes, change)
		return replacement, true
	})
	return out, changes
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

func relinkNormalizeTarget(target string) string {
	target = strings.TrimSpace(strings.ReplaceAll(target, "\\", "/"))
	target = strings.TrimPrefix(path.Clean("/"+target), "/")
	target = relinkStripMDExt(target)
	if target == "" || target == "." {
		return ""
	}
	return strings.ToLower(target)
}

func relinkNormalizeTitle(title string) string {
	title = strings.TrimSpace(title)
	title = relinkStripMDExt(title)
	if title == "" {
		return ""
	}
	return strings.ToLower(title)
}

func relinkStripMDExt(s string) string {
	if len(s) >= 3 && strings.EqualFold(s[len(s)-3:], ".md") {
		return s[:len(s)-3]
	}
	return s
}

func relinkTargetTitle(target string) string {
	target = strings.TrimSpace(target)
	if idx := strings.LastIndexAny(target, `/\`); idx >= 0 {
		return target[idx+1:]
	}
	return target
}

func relinkSplitMDExt(s string) (string, string) {
	if len(s) >= 3 && strings.EqualFold(s[len(s)-3:], ".md") {
		return s[:len(s)-3], s[len(s)-3:]
	}
	return s, ""
}

func relinkIsMediaEmbed(title string) bool {
	ext := strings.ToLower(filepath.Ext(title))
	_, ok := relinkMediaExts[ext]
	return ok
}

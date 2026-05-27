package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
)

// rewriteFunc is the signature shared by wikilink.RewriteTitle and
// wikilink.RewriteMove: given a text body and two path/title strings, return
// the rewritten body.
type rewriteFunc func(text, oldVal, newVal string) string

// scanBodyWithRewrite returns one renameChange per body line whose content
// shifts after applying rewrite(body, oldVal, newVal). Line numbers are
// 1-based. The rewrite runs on the whole body first so that code-fence masking
// in wikilink.Parse sees the multi-line context — a per-line rewrite would miss
// fence openers on a preceding line.
func scanBodyWithRewrite(file, body, oldVal, newVal string, rewrite rewriteFunc) []renameChange {
	if body == "" {
		return nil
	}
	newBody := rewrite(body, oldVal, newVal)
	if newBody == body {
		return nil
	}
	return diffBodyLines(file, body, newBody)
}

// scanFrontmatterWithRewrite inspects every wikilink-typed property and records
// a change when any string value (scalar or list element) is modified by
// rewrite(s, oldVal, newVal).
func scanFrontmatterWithRewrite(file string, front *frontmatter.Map, oldVal, newVal string, wikilinkProps []string, rewrite rewriteFunc) []renameChange {
	var changes []renameChange
	for _, key := range wikilinkProps {
		v, ok := front.Get(key)
		if !ok {
			continue
		}
		switch vv := v.(type) {
		case string:
			newV := rewrite(vv, oldVal, newVal)
			if newV != vv {
				changes = append(changes, renameChange{
					File:    file,
					Kind:    changeFrontmatter,
					Key:     key,
					OldText: vv,
					NewText: newV,
				})
			}
		case []any:
			for _, item := range vv {
				s, ok := item.(string)
				if !ok {
					continue
				}
				ns := rewrite(s, oldVal, newVal)
				if ns != s {
					changes = append(changes, renameChange{
						File:    file,
						Kind:    changeFrontmatter,
						Key:     key,
						OldText: s,
						NewText: ns,
					})
				}
			}
		}
	}
	return changes
}

// rewriteFrontmatterProps mutates front in place: every string value (scalar or
// list element) under a wikilink-typed key is passed through
// rewrite(s, oldVal, newVal). Values that do not change are left untouched.
func rewriteFrontmatterProps(front *frontmatter.Map, oldVal, newVal string, fmCfg *config.FrontmatterConfig, rewrite rewriteFunc) {
	if fmCfg == nil {
		return
	}
	for _, key := range fmCfg.WikilinkProperties {
		v, ok := front.Get(key)
		if !ok {
			continue
		}
		switch vv := v.(type) {
		case string:
			ns := rewrite(vv, oldVal, newVal)
			if ns != vv {
				front.Set(key, ns)
			}
		case []any:
			changed := false
			newList := make([]any, len(vv))
			for i, item := range vv {
				if s, ok := item.(string); ok {
					ns := rewrite(s, oldVal, newVal)
					if ns != s {
						changed = true
					}
					newList[i] = ns
				} else {
					newList[i] = item
				}
			}
			if changed {
				front.Set(key, newList)
			}
		}
	}
}

// printChanges writes sorted change lines (body and frontmatter) to out using
// the `<relpath>:<line>: old -> new` and `<relpath>:frontmatter[key]: old ->
// new` formats. Used by both printPlan and printMovePlan.
func printChanges(out io.Writer, vaultRoot string, changes []renameChange) error {
	sorted := make([]renameChange, len(changes))
	copy(sorted, changes)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].File != sorted[j].File {
			return sorted[i].File < sorted[j].File
		}
		if sorted[i].Kind != sorted[j].Kind {
			return sorted[i].Kind < sorted[j].Kind
		}
		if sorted[i].Line != sorted[j].Line {
			return sorted[i].Line < sorted[j].Line
		}
		return sorted[i].Key < sorted[j].Key
	})
	for _, c := range sorted {
		rel, err := filepath.Rel(vaultRoot, c.File)
		if err != nil {
			rel = c.File
		}
		switch c.Kind {
		case changeBody:
			if _, err := fmt.Fprintf(out, "%s:%d: %s -> %s\n", rel, c.Line, c.OldText, c.NewText); err != nil {
				return err
			}
		case changeFrontmatter:
			if _, err := fmt.Fprintf(out, "%s:frontmatter[%s]: %s -> %s\n", rel, c.Key, c.OldText, c.NewText); err != nil {
				return err
			}
		}
	}
	return nil
}

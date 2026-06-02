package noteops

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
)

// ChangeKind identifies the locus rewritten by a rename or move operation.
type ChangeKind int

const (
	ChangeBody ChangeKind = iota
	ChangeFrontmatter
)

// Change is one line-level or frontmatter-property edit produced by a plan.
type Change struct {
	File    string
	Kind    ChangeKind
	Line    int
	Key     string
	OldText string
	NewText string
}

type rewriteFunc func(text, oldVal, newVal string) string

func scanBodyWithRewrite(file, body, oldVal, newVal string, rewrite rewriteFunc) []Change {
	if body == "" {
		return nil
	}
	newBody := rewrite(body, oldVal, newVal)
	if newBody == body {
		return nil
	}
	return diffBodyLines(file, body, newBody)
}

func diffBodyLines(file, oldBody, newBody string) []Change {
	var changes []Change
	line := 1
	oStart, nStart := 0, 0
	for oStart <= len(oldBody) && nStart <= len(newBody) {
		oEnd := strings.IndexByte(oldBody[oStart:], '\n')
		nEnd := strings.IndexByte(newBody[nStart:], '\n')
		var oldLine, newLine string
		if oEnd < 0 {
			oldLine = oldBody[oStart:]
		} else {
			oldLine = oldBody[oStart : oStart+oEnd]
		}
		if nEnd < 0 {
			newLine = newBody[nStart:]
		} else {
			newLine = newBody[nStart : nStart+nEnd]
		}
		if oldLine != newLine {
			changes = append(changes, Change{
				File:    file,
				Kind:    ChangeBody,
				Line:    line,
				OldText: oldLine,
				NewText: newLine,
			})
		}
		if oEnd < 0 || nEnd < 0 {
			break
		}
		oStart += oEnd + 1
		nStart += nEnd + 1
		line++
	}
	return changes
}

func scanFrontmatterWithRewrite(file string, front *frontmatter.Map, oldVal, newVal string, wikilinkProps []string, rewrite rewriteFunc) []Change {
	var changes []Change
	for _, key := range wikilinkProps {
		v, ok := front.Get(key)
		if !ok {
			continue
		}
		switch vv := v.(type) {
		case string:
			newV := rewrite(vv, oldVal, newVal)
			if newV != vv {
				changes = append(changes, Change{
					File:    file,
					Kind:    ChangeFrontmatter,
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
					changes = append(changes, Change{
						File:    file,
						Kind:    ChangeFrontmatter,
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

func printChanges(out io.Writer, vaultRoot string, changes []Change) error {
	sorted := make([]Change, len(changes))
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
		case ChangeBody:
			if _, err := fmt.Fprintf(out, "%s:%d: %s -> %s\n", rel, c.Line, c.OldText, c.NewText); err != nil {
				return err
			}
		case ChangeFrontmatter:
			if _, err := fmt.Fprintf(out, "%s:frontmatter[%s]: %s -> %s\n", rel, c.Key, c.OldText, c.NewText); err != nil {
				return err
			}
		}
	}
	return nil
}

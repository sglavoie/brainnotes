package config

import "strings"

// SavedView is one named, persisted query, stored as an entry in the [[views]]
// array of tables:
//
//	[[views]]
//	name = "Active work"
//	query = "status:wip and not folder:Archive"
//	sort = "-modified"
//	limit = 20
//	description = "What I am actually working on"
//
// Query, Sort, and RelativeTo are opaque text at this layer. internal/config is
// the bottom of the package layering: importing internal/queryexpr to check
// that Query parses would close a cycle (queryexpr already imports config),
// exactly like the documented internal/pins → internal/noteops edge. Validation
// here is therefore shape-only — a name, a non-empty query, a non-negative
// limit — and never semantic.
//
// The consequence is load-bearing rather than incidental: a view whose
// expression stopped parsing (a property it named was renamed, say) still loads
// fine, so every layer above must render it as a *disabled* entry carrying its
// parse error. It must never be dropped silently, and it must never fail the
// whole config load — one stale view would otherwise lock the user out of the
// vault. `bn views check` is the user-facing answer to config's inability to
// validate the expression.
type SavedView struct {
	// Name identifies the view to `--view` and to `bn views`. It is matched
	// case-insensitively and is unique under that folding.
	Name string `toml:"name"`
	// Query is the saved query expression.
	Query string `toml:"query"`
	// Sort is the saved ordering, in the spelling `--sort` accepts
	// (`modified`, `-modified`, `title:desc`, …). Empty means the default.
	Sort string `toml:"sort"`
	// RelativeTo names the anchor note a `relevance` sort ranks against. It is
	// its own field rather than part of Sort because a sort spec's text form
	// deliberately does not carry it.
	RelativeTo string `toml:"relative_to"`
	// Limit caps the result count. Zero means no cap.
	Limit int `toml:"limit"`
	// Description is the one-line summary shown by `bn views` and the web nav.
	Description string `toml:"description"`
}

// Clone returns a deep copy of one saved view. SavedView holds no reference
// types today; the method exists so growing one cannot silently make Clone
// shallow.
func (v SavedView) Clone() SavedView { return v }

// View returns the saved view named name, matched case-insensitively so a
// `--view` argument does not have to reproduce the stored capitalization.
func (c *Config) View(name string) (SavedView, bool) {
	if c == nil {
		return SavedView{}, false
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return SavedView{}, false
	}
	for _, view := range c.Views {
		if strings.EqualFold(view.Name, name) {
			return view, true
		}
	}
	return SavedView{}, false
}

// ViewNames lists the saved view names in file order, which is the order
// `bn views` and the web nav present them in.
func (c *Config) ViewNames() []string {
	if c == nil || len(c.Views) == 0 {
		return nil
	}
	names := make([]string, 0, len(c.Views))
	for _, view := range c.Views {
		names = append(names, view.Name)
	}
	return names
}

package cli

import (
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

type findFlags struct {
	tags         []string
	categories   []string
	status       string
	typeProp     string
	hasProp      string
	missingProp  string
	age          string
	created      string
	folder       string
	orphan       bool
	brokenLinks  bool
	minBacklinks int
	maxBacklinks int
	minLinks     int
	maxLinks     int
	deadEnd      bool
}

func (ff *findFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringSliceVar(&ff.tags, "tag", nil, "filter by tag (repeatable; AND semantics)")
	f.StringSliceVar(&ff.categories, "category", nil, "filter by category target (repeatable; AND semantics)")
	f.StringVar(&ff.status, "status", "", "filter by status property")
	f.StringVar(&ff.typeProp, "type", "", "filter by type/kind property")
	f.StringVar(&ff.hasProp, "has", "", "require property present")
	f.StringVar(&ff.missingProp, "missing", "", "require property absent")
	f.StringVar(&ff.age, "age", "", "filter by 'last' property age, e.g. >30d, <7d, =0d")
	f.StringVar(&ff.created, "created", "", "filter by 'created' property age, e.g. >30d, <7d, =0d")
	f.StringVar(&ff.folder, "folder", "", "limit to a vault-relative folder (matches descendants)")
	f.BoolVar(&ff.orphan, "orphan", false, "only notes nothing in the vault links to")
	f.BoolVar(&ff.brokenLinks, "broken-links", false, "only notes with unresolved wikilinks")
	f.IntVar(&ff.minBacklinks, "min-backlinks", -1, "minimum inbound links from other notes")
	f.IntVar(&ff.maxBacklinks, "max-backlinks", -1, "maximum inbound links from other notes")
	f.IntVar(&ff.minLinks, "min-links", -1, "minimum outgoing links to other notes")
	f.IntVar(&ff.maxLinks, "max-links", -1, "maximum outgoing links to other notes")
	f.BoolVar(&ff.deadEnd, "dead-end", false, "notes with zero outgoing links (except dead_end: false)")
}

func (ff *findFlags) needsGraph() bool {
	return ff.orphan || ff.brokenLinks || ff.deadEnd ||
		ff.minBacklinks >= 0 || ff.maxBacklinks >= 0 ||
		ff.minLinks >= 0 || ff.maxLinks >= 0
}

func (ff *findFlags) toQueryFlags(root string, g *query.Graph) query.Flags {
	return query.Flags{
		Tags:         ff.tags,
		Categories:   ff.categories,
		Status:       ff.status,
		Type:         ff.typeProp,
		Has:          ff.hasProp,
		Missing:      ff.missingProp,
		Age:          ff.age,
		Created:      ff.created,
		Folder:       ff.folder,
		Orphan:       ff.orphan,
		BrokenLinks:  ff.brokenLinks,
		MinBacklinks: ff.minBacklinks,
		MaxBacklinks: ff.maxBacklinks,
		MinLinks:     ff.minLinks,
		MaxLinks:     ff.maxLinks,
		DeadEnd:      ff.deadEnd,
		Root:         root,
		Graph:        g,
	}
}

func noteKey(n *vault.Note) string {
	base := filepath.Base(n.Path)
	return strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))
}

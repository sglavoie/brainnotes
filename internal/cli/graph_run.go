package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// graphOptions is the flag state of one `bn graph` invocation. `ff` is the
// shared filter surface rather than a copy of it, so the subgraph selection
// routes through findflags.go's single execution path.
type graphOptions struct {
	format string
	ff     *findFlags
}

// validate refuses an unknown --format. There is no mutual-exclusion matrix
// here: --format composes with every find flag, so the only rule is that the
// format names an exporter that exists.
func (o graphOptions) validate() error {
	switch o.format {
	case "dot", "mermaid", "code":
		return nil
	default:
		return fmt.Errorf("graph: unknown format %q (must be dot, mermaid, or code)", o.format)
	}
}

// runGraphCommand exports the wikilink graph, restricted to the notes the find
// flags select.
//
// Order is behaviour: the format is refused before the config is read, so
// `bn graph --format nonsense` outside a vault is a format error rather than a
// vault error.
//
// The graph is built over *every* note and the selection only narrows the node
// set handed to the exporter. That is what keeps an edge between two selected
// notes intact when the path between them runs through an unselected one, and
// it is why selectNotes is given the full graph: the graph predicates rank
// against the whole vault, not against their own result.
func runGraphCommand(cmd *cobra.Command, opts graphOptions) error {
	if err := opts.validate(); err != nil {
		return err
	}

	cfg := ConfigFromContext(cmd.Context())
	if cfg == nil {
		return errors.New("graph: no config in context")
	}
	v, err := vault.Open(cfg)
	if err != nil {
		return err
	}
	notes, err := loadReadableNotes(cmd, v)
	if err != nil {
		return err
	}

	g := query.BuildGraphForRoot(v.Root, notes)

	sel, err := opts.ff.selectNotes(cmd.Context(), cfg, v.Root, notes, g)
	if err != nil {
		return err
	}

	nodeSet := make(map[string]struct{})
	for _, n := range sel.notes {
		nodeSet[g.KeyForNote(n)] = struct{}{}
	}

	out := cmd.OutOrStdout()
	switch opts.format {
	case "mermaid":
		return g.ExportMermaid(out, nodeSet)
	case "code":
		return g.ExportCode(out, notes, nodeSet)
	default:
		return g.ExportDot(out, nodeSet)
	}
}

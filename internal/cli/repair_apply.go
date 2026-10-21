package cli

import (
	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/schema"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// applyRepairs applies the planned repair actions to a single note. On dry-run
// it emits a diff block and skips the write; otherwise the file is rewritten
// atomically. Returns false when result contains no actions.
func applyRepairs(n *vault.Note, result schema.RepairResult, fmCfg *config.FrontmatterConfig, vaultRoot string, dry *DryRun) (bool, error) {
	if len(result.Actions) == 0 {
		return false, nil
	}

	front, err := n.Front()
	if err != nil {
		return false, err
	}

	if dry != nil {
		preview := noteops.NotePreview{RelPath: displayPath(n.Path, vaultRoot)}
		for _, act := range result.Actions {
			preview.Changes = append(preview.Changes,
				noteops.FrontmatterChange(act.Property, true, act.OldValue, true, act.NewValue))
		}
		return true, noteops.RenderNotePreview(dry.Out, preview)
	}

	var definitions map[string]config.PropertyDefinition
	if fmCfg != nil {
		definitions = fmCfg.Properties
	}
	for _, act := range result.Actions {
		schema.ApplyRepairAction(front, act, definitions)
	}
	return true, n.Save(fmCfg)
}

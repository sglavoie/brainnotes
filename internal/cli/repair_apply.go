package cli

import (
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/schema"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
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
		block := newFileBlock(displayPath(n.Path, vaultRoot))
		for _, act := range result.Actions {
			block.removed(act.Property, act.OldValue)
			block.added(act.Property, act.NewValue)
		}
		return true, block.flush(dry.Out)
	}

	for _, act := range result.Actions {
		front.Set(act.Property, act.NewValue)
	}
	return true, n.Save(fmCfg)
}

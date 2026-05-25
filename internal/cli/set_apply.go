package cli

import (
	"errors"
	"fmt"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// applySet loads the note, applies every pair in a single read-mutate-write
// cycle, and saves atomically. Returns changed=false (and skips Save) when no
// pair caused a real mutation, so '+=' / '-=' no-ops do not bump file mtime.
// Wikilink wrapping for the '=' case is handled by frontmatter Marshal via
// the supplied config; '+=' / '-=' wrap their inputs at parse time so the
// list-mutation primitives can compare byte-for-byte.
//
// When dry is non-nil, the mutation is computed in memory, a per-file diff is
// emitted to dry.Out, and the file is not written. vaultRoot is used to
// shorten paths in the diff header; pass "" to fall back to the absolute path.
func applySet(path string, pairs []setPair, fm *config.FrontmatterConfig, vaultRoot string, dry *DryRun) (bool, error) {
	note, front, err := loadSetNote(path)
	if err != nil {
		return false, err
	}

	before := snapshotKeys(front, pairs)
	changed, err := applySetPairs(front, pairs, fm)
	if err != nil || !changed {
		return false, err
	}
	if !setPairsNetChanged(front, before, pairs) {
		return false, nil
	}
	if dry != nil {
		return writeSetDryRun(path, vaultRoot, front, before, pairs, dry)
	}
	if err := note.Save(fm); err != nil {
		return false, err
	}
	return true, nil
}

func loadSetNote(path string) (*vault.Note, *frontmatter.Map, error) {
	note := &vault.Note{Path: path}
	if err := note.Load(); err != nil {
		return nil, nil, err
	}
	front, err := note.Front()
	if err != nil {
		return nil, nil, err
	}
	return note, front, nil
}

func applySetPairs(front *frontmatter.Map, pairs []setPair, fm *config.FrontmatterConfig) (bool, error) {
	changed := false
	for _, pair := range pairs {
		pairChanged, err := applySetPair(front, pair, fm)
		if err != nil {
			return false, err
		}
		if pairChanged {
			changed = true
		}
	}
	return changed, nil
}

func applySetPair(front *frontmatter.Map, pair setPair, fm *config.FrontmatterConfig) (bool, error) {
	switch pair.Op {
	case opSet:
		newVal := wikilinkNormalized(pair.Value, pair.Key, fm)
		existing, present := front.Get(pair.Key)
		if present && dryRunValuesEqual(existing, newVal) {
			return false, nil
		}
		front.Set(pair.Key, newVal)
		return true, nil
	case opAdd:
		return applySetListMutation(front, pair.Key, pair.Value, "+=")
	case opRemove:
		return applySetListMutation(front, pair.Key, pair.Value, "-=")
	default:
		return false, fmt.Errorf("set: internal error: unsupported operation for %q", pair.Key)
	}
}

func applySetListMutation(front *frontmatter.Map, key string, value any, op string) (bool, error) {
	vals, ok := value.([]any)
	if !ok {
		return false, fmt.Errorf("set: internal error: %s value for %q is not a list", op, key)
	}

	var (
		changed bool
		err     error
	)
	if op == "+=" {
		changed, err = front.ListAdd(key, vals...)
	} else {
		changed, err = front.ListRemove(key, vals...)
	}
	if err != nil {
		if errors.Is(err, frontmatter.ErrNotAList) {
			return false, fmt.Errorf("set: cannot apply %q to %q: existing value is not a list", op, key)
		}
		return false, err
	}
	return changed, nil
}

func setPairsNetChanged(front *frontmatter.Map, before map[string]snapshot, pairs []setPair) bool {
	for _, key := range orderedKeys(pairs) {
		snap := before[key]
		afterVal, afterOK := front.Get(key)
		if !snap.present && !afterOK {
			continue
		}
		if snap.present && afterOK && dryRunValuesEqual(snap.value, afterVal) {
			continue
		}
		return true
	}
	return false
}

func writeSetDryRun(path, vaultRoot string, front *frontmatter.Map, before map[string]snapshot, pairs []setPair, dry *DryRun) (bool, error) {
	block := newFileBlock(displayPath(path, vaultRoot))
	for _, key := range orderedKeys(pairs) {
		snap := before[key]
		afterVal, afterOK := front.Get(key)
		if !snap.present && !afterOK {
			continue
		}
		if snap.present && afterOK && dryRunValuesEqual(snap.value, afterVal) {
			continue
		}
		if snap.present {
			block.removed(key, snap.value)
		}
		if afterOK {
			block.added(key, afterVal)
		}
	}
	if err := block.flush(dry.Out); err != nil {
		return false, err
	}
	return !block.empty(), nil
}

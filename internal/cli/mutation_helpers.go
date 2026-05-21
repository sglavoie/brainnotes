package cli

import (
	"path/filepath"

	"github.com/sglavoie/sglavoie_brainnotes/internal/frontmatter"
)

// snapshot captures a key's value (or absence) for later diffing.
type snapshot struct {
	value   any
	present bool
}

// snapshotKeys records the current value of every distinct key in pairs so we
// can compare against the post-mutation state.
func snapshotKeys(front *frontmatter.Map, pairs []setPair) map[string]snapshot {
	out := make(map[string]snapshot, len(pairs))
	for _, p := range pairs {
		if _, seen := out[p.Key]; seen {
			continue
		}
		v, ok := front.Get(p.Key)
		out[p.Key] = snapshot{value: v, present: ok}
	}
	return out
}

// orderedKeys returns the distinct keys from pairs in first-appearance order
// so dry-run output is deterministic regardless of map iteration.
func orderedKeys(pairs []setPair) []string {
	seen := make(map[string]bool, len(pairs))
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		if seen[p.Key] {
			continue
		}
		seen[p.Key] = true
		out = append(out, p.Key)
	}
	return out
}

// displayPath returns a vault-relative path for dry-run headers. Falls back
// to the absolute path when the relative form cannot be computed.
func displayPath(absPath, vaultRoot string) string {
	if vaultRoot == "" {
		return absPath
	}
	rel, err := filepath.Rel(vaultRoot, absPath)
	if err != nil {
		return absPath
	}
	return filepath.ToSlash(rel)
}

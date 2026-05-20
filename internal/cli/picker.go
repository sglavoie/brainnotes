package cli

import (
	"sort"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

type pickerFunc = func(items []string, extraArgs []string) ([]string, error)

var notePicker pickerFunc = RunFZF

var propertyPicker pickerFunc = RunFZF

func collectNotePaths(v *vault.Vault) ([]string, error) {
	var paths []string
	if err := v.Walk(vault.WalkFilter{}, func(n *vault.Note) error {
		paths = append(paths, n.Path)
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

func collectPropertyKeys(v *vault.Vault) ([]string, error) {
	notes, err := v.Notes()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	for _, n := range notes {
		if n == nil {
			continue
		}
		fm, err := n.Front()
		if err != nil {
			continue
		}
		for _, k := range fm.Keys() {
			seen[k] = struct{}{}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

func pickNotes(v *vault.Vault, cfg *config.Config, multi bool) ([]string, error) {
	paths, err := collectNotePaths(v)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, nil
	}
	extraArgs := append([]string(nil), cfg.Fzf.Args...)
	if multi {
		extraArgs = append(extraArgs, "--multi")
	}
	return notePicker(paths, extraArgs)
}

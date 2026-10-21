package cli

import (
	"sort"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/vault"
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
	// Completion candidates: a note with unparsable frontmatter has no keys
	// to offer, and a warning here would corrupt the completion output.
	notes, _, err := v.NotesLenient()
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

// pickNotes runs the note picker for the command name, which prefixes the
// message when the vault is empty or nothing is selected.
func pickNotes(name string, v *vault.Vault, cfg *config.Config, multi bool) ([]string, error) {
	paths, err := collectNotePaths(v)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, nothingSelected(name, "no notes in vault")
	}
	extraArgs := append([]string(nil), cfg.Fzf.Args...)
	if multi {
		extraArgs = append(extraArgs, "--multi")
	}
	selected, err := notePicker(paths, extraArgs)
	return requireSelection(name, selected, err)
}

// requireSelection turns a picker that came back empty into ErrNothingSelected
// under the command name, so a script sees exit 1 instead of a success with
// nothing to act on, and the terminal says which command stopped.
func requireSelection(name string, selected []string, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		return nil, nothingSelected(name, "nothing selected")
	}
	return selected, nil
}

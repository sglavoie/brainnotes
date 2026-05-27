package journal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func entryDir(journalRoot, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("empty entry id")
	}
	if strings.Contains(id, "/") || strings.Contains(id, `\`) {
		return "", fmt.Errorf("entry id %q contains path separator", id)
	}
	root, err := filepath.Abs(journalRoot)
	if err != nil {
		return "", fmt.Errorf("resolve journal root %q: %w", journalRoot, err)
	}
	return filepath.Join(root, id), nil
}

func loadEntryFromDir(dir string) (*Entry, error) {
	manifest := filepath.Join(dir, ManifestFileName)
	var e Entry
	if err := readJSON(manifest, &e); err != nil {
		return nil, fmt.Errorf("read %q: %w", manifest, err)
	}
	if err := e.validate(); err != nil {
		return nil, fmt.Errorf("validate %q: %w", manifest, err)
	}
	return &e, nil
}

// LoadEntry reads one entry manifest by ID.
func LoadEntry(journalRoot, id string) (*Entry, error) {
	dir, err := entryDir(journalRoot, id)
	if err != nil {
		return nil, fmt.Errorf("journal: %w", err)
	}
	e, err := loadEntryFromDir(dir)
	if err != nil {
		return nil, fmt.Errorf("journal: %w", err)
	}
	if e.ID != id {
		return nil, fmt.Errorf("journal: manifest id %q does not match directory %q", e.ID, id)
	}
	return e, nil
}

type listedEntry struct {
	entry Entry
	dir   string
}

func listEntries(journalRoot string) ([]listedEntry, error) {
	root, err := filepath.Abs(journalRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve journal root %q: %w", journalRoot, err)
	}
	entries, err := os.ReadDir(root)
	switch {
	case err == nil:
	case os.IsNotExist(err):
		return nil, nil
	default:
		return nil, fmt.Errorf("readdir %q: %w", root, err)
	}
	out := make([]listedEntry, 0, len(entries))
	for _, de := range entries {
		if !de.IsDir() {
			continue
		}
		name := de.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		dir := filepath.Join(root, name)
		e, err := loadEntryFromDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// Skip incomplete entry directories left behind by interrupted
				// recordings. They have no committed manifest yet.
				continue
			}
			return nil, err
		}
		if e.ID != name {
			return nil, fmt.Errorf("journal: manifest id %q does not match directory %q", e.ID, name)
		}
		out = append(out, listedEntry{entry: *e, dir: dir})
	}
	sort.Slice(out, func(i, j int) bool {
		ti := out[i].entry.Timestamp.UTC()
		tj := out[j].entry.Timestamp.UTC()
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return out[i].entry.ID > out[j].entry.ID
	})
	return out, nil
}

// ListEntries returns entries newest-first.
func ListEntries(journalRoot string) ([]Entry, error) {
	listed, err := listEntries(journalRoot)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, len(listed))
	for i := range listed {
		out[i] = listed[i].entry
	}
	return out, nil
}

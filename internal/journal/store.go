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
//
// A specifically requested entry stays a direct error when its manifest is
// unreadable: the caller named this entry, and reporting the failure as a
// warning would leave `bn undo <id>` claiming success for an entry it could not
// read. Enumeration is the opposite case and degrades to a warning, so one
// corrupt entry never hides the rest of the history.
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

// Warning reports one journal entry that could not be listed. It names the
// entry directory so a person can inspect or delete exactly that entry, and
// carries the underlying failure without any of the entry's content.
//
// A warning is never a listing failure: the entries around it are returned
// normally and remain undoable. Only the unreadable entry is unavailable.
type Warning struct {
	// ID is the entry directory name, which is the ID a user would pass to
	// `bn undo`.
	ID string
	// Dir is the absolute path of the entry directory.
	Dir string
	// Err is the failure that made the entry unlistable.
	Err error
}

func (w Warning) Error() string {
	if w.Err == nil {
		return fmt.Sprintf("journal entry %s is unreadable", w.ID)
	}
	return fmt.Sprintf("journal entry %s is unreadable and was skipped: %v", w.ID, w.Err)
}

func (w Warning) Unwrap() error { return w.Err }

type listedEntry struct {
	entry Entry
	dir   string
}

// listEntries enumerates committed entries newest-first.
//
// A journal root that cannot be read is still fatal: nothing about the history
// is known, so returning an empty list would claim there is none. An individual
// entry is different — a manifest that is corrupt says nothing about its
// neighbours — so it becomes a Warning naming its directory and the remaining
// entries are returned intact. That is the difference between one bad manifest
// costing one undo record and it costing every undo record.
//
// A directory with no manifest at all is skipped without a warning. It is not
// corruption: NewRecorder creates the entry directory and its snapshots before
// any manifest exists, so this is what an in-flight recording looks like from
// another process, and what an interrupted one leaves behind. Warning on it
// would report a live mutation as damage, and would keep reporting an
// interrupted one forever, since Prune only ever removes entries it can list.
func listEntries(journalRoot string) ([]listedEntry, []Warning, error) {
	root, err := filepath.Abs(journalRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve journal root %q: %w", journalRoot, err)
	}
	entries, err := os.ReadDir(root)
	switch {
	case err == nil:
	case os.IsNotExist(err):
		return nil, nil, nil
	default:
		return nil, nil, fmt.Errorf("readdir %q: %w", root, err)
	}
	out := make([]listedEntry, 0, len(entries))
	var warnings []Warning
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
				continue
			}
			warnings = append(warnings, Warning{ID: name, Dir: dir, Err: err})
			continue
		}
		if e.ID != name {
			warnings = append(warnings, Warning{
				ID:  name,
				Dir: dir,
				Err: fmt.Errorf("manifest id %q does not match directory %q", e.ID, name),
			})
			continue
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
	// Warnings follow directory order, which is not guaranteed to be stable
	// across filesystems, so sort them the way the entries are sorted: by the
	// name a person would act on.
	sort.Slice(warnings, func(i, j int) bool { return warnings[i].ID < warnings[j].ID })
	return out, warnings, nil
}

// ListEntries returns entries newest-first together with one warning per entry
// that could not be read. An error means the journal itself is unreadable; a
// warning means one entry is, and the returned entries are complete apart from
// it.
func ListEntries(journalRoot string) ([]Entry, []Warning, error) {
	listed, warnings, err := listEntries(journalRoot)
	if err != nil {
		return nil, nil, err
	}
	out := make([]Entry, len(listed))
	for i := range listed {
		out[i] = listed[i].entry
	}
	return out, warnings, nil
}

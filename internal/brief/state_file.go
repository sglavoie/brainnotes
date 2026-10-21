package brief

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/brainnotes/internal/checkpoint"
	"github.com/sglavoie/brainnotes/internal/textnorm"
	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/vaultstate"
)

type briefStateFile struct {
	Version         int                          `json:"version"`
	DailySelections map[string]DailySelection    `json:"daily_selections"`
	NoteExposures   map[string]time.Time         `json:"note_exposures"`
	PairExposures   map[string]briefPairExposure `json:"pair_exposures"`
	Visits          map[string]time.Time         `json:"visits"`
	Opens           []briefOpenRecord            `json:"opens"`
}

type briefPairExposure struct {
	SourceRelPath    string    `json:"source_rel_path"`
	CandidateRelPath string    `json:"candidate_rel_path"`
	ExposedAt        time.Time `json:"exposed_at"`
}

type briefOpenRecord struct {
	CardID   CardID    `json:"card_id"`
	OpenedAt time.Time `json:"opened_at"`
}

func (store *FileStateStore) prepareWriteLocked(at time.Time) (briefStateFile, map[string]struct{}, error) {
	store.state = nil
	store.stateExists = false
	state, _, err := store.loadLocked()
	if err != nil {
		return briefStateFile{}, nil, err
	}
	paths, err := store.notePathsLocked(true)
	if err != nil {
		return briefStateFile{}, nil, err
	}
	state = cloneBriefState(state)
	pruneBriefState(&state, paths, at)
	return state, paths, nil
}

func (store *FileStateStore) loadLocked() (briefStateFile, bool, error) {
	if store == nil {
		return briefStateFile{}, false, errors.New("brief: nil state store")
	}
	if store.state != nil {
		return *store.state, store.stateExists, nil
	}
	statePath, err := store.statePath()
	if err != nil {
		return briefStateFile{}, false, err
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			state := newBriefState()
			store.state = &state
			return state, false, nil
		}
		return briefStateFile{}, false, fmt.Errorf("brief: read state %q: %w", statePath, err)
	}

	var state briefStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return briefStateFile{}, false, fmt.Errorf("%w: parse %q: %v", ErrCorruptState, statePath, err)
	}
	if state.Version != briefStateVersion {
		return briefStateFile{}, false, fmt.Errorf(
			"%w: got %d want %d",
			ErrUnsupportedStateVersion,
			state.Version,
			briefStateVersion,
		)
	}
	if state.DailySelections == nil {
		state.DailySelections = map[string]DailySelection{}
	}
	if state.NoteExposures == nil {
		state.NoteExposures = map[string]time.Time{}
	}
	if state.PairExposures == nil {
		state.PairExposures = map[string]briefPairExposure{}
	}
	if state.Visits == nil {
		state.Visits = map[string]time.Time{}
	}
	if state.Opens == nil {
		state.Opens = []briefOpenRecord{}
	}
	if err := validateBriefState(state); err != nil {
		return briefStateFile{}, false, err
	}
	store.state = &state
	store.stateExists = true
	return state, true, nil
}

func newBriefState() briefStateFile {
	return briefStateFile{
		Version:         briefStateVersion,
		DailySelections: map[string]DailySelection{},
		NoteExposures:   map[string]time.Time{},
		PairExposures:   map[string]briefPairExposure{},
		Visits:          map[string]time.Time{},
		Opens:           []briefOpenRecord{},
	}
}

func cloneBriefState(state briefStateFile) briefStateFile {
	clone := briefStateFile{
		Version:         state.Version,
		DailySelections: make(map[string]DailySelection, len(state.DailySelections)),
		NoteExposures:   make(map[string]time.Time, len(state.NoteExposures)),
		PairExposures:   make(map[string]briefPairExposure, len(state.PairExposures)),
		Visits:          make(map[string]time.Time, len(state.Visits)),
		Opens:           make([]briefOpenRecord, len(state.Opens)),
	}
	for date, selection := range state.DailySelections {
		clone.DailySelections[date] = cloneDailySelection(selection)
	}
	for relPath, exposedAt := range state.NoteExposures {
		clone.NoteExposures[relPath] = exposedAt
	}
	for pairID, record := range state.PairExposures {
		clone.PairExposures[pairID] = record
	}
	for date, visitedAt := range state.Visits {
		clone.Visits[date] = visitedAt
	}
	copy(clone.Opens, state.Opens)
	return clone
}

func cloneDailySelection(selection DailySelection) DailySelection {
	cloned := DailySelection{
		Date:  selection.Date,
		Cards: make([]Card, len(selection.Cards)),
	}
	for i, card := range selection.Cards {
		cloned.Cards[i] = cloneCard(card)
	}
	return cloned
}

func cloneCard(card Card) Card {
	cloned := card
	cloned.Metadata.Reasons = append([]string(nil), card.Metadata.Reasons...)
	if card.Candidate != nil {
		candidate := *card.Candidate
		cloned.Candidate = &candidate
	}
	return cloned
}

func (store *FileStateStore) saveLocked(state briefStateFile) error {
	state.Version = briefStateVersion
	vaultRoot, err := store.rootPath()
	if err != nil {
		return err
	}
	statePath, err := store.statePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("brief: encode state: %w", err)
	}
	data = append(data, '\n')
	if err := store.writeState(vaultRoot, statePath, data); err != nil {
		return err
	}
	snapshot := cloneBriefState(state)
	store.state = &snapshot
	store.stateExists = true
	return nil
}

func (store *FileStateStore) notePathsLocked(refresh bool) (map[string]struct{}, error) {
	if !refresh && store.notePaths != nil {
		return store.notePaths, nil
	}
	root, err := store.rootPath()
	if err != nil {
		return nil, err
	}
	paths := make(map[string]struct{})
	err = filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filePath == root {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || strings.HasPrefix(entry.Name(), ".") || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return nil
		}
		relative, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		normalized, err := normalizeBriefPath(textnorm.NFC(filepath.ToSlash(relative)))
		if err != nil {
			return fmt.Errorf("brief: inspect note path %q: %w", relative, err)
		}
		paths[normalized] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("brief: scan vault notes: %w", err)
	}
	store.notePaths = paths
	return paths, nil
}

func (store *FileStateStore) persistenceRoot() (string, error) {
	if store.storageRoot == "" {
		return store.rootPath()
	}
	// Reuse the same directory validation without changing the note root.
	storage := &FileStateStore{root: store.storageRoot}
	return storage.rootPath()
}

func (store *FileStateStore) statePath() (string, error) {
	root, err := store.persistenceRoot()
	if err != nil {
		return "", err
	}
	directory := filepath.Join(root, ".bnotes")
	if info, err := os.Lstat(directory); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("brief: state directory %q must be a real directory", directory)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("brief: inspect state directory %q: %w", directory, err)
	}
	statePath := filepath.Join(root, filepath.FromSlash(briefStateRelPath))
	if info, err := os.Lstat(statePath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", fmt.Errorf("brief: state path %q must be a regular file", statePath)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("brief: inspect state path %q: %w", statePath, err)
	}
	return statePath, nil
}

func (store *FileStateStore) rootPath() (string, error) {
	if store == nil {
		return "", errors.New("brief: nil state store")
	}
	if strings.TrimSpace(store.root) == "" {
		return "", errors.New("brief: empty state root")
	}
	root, err := filepath.Abs(store.root)
	if err != nil {
		return "", fmt.Errorf("brief: resolve state root %q: %w", store.root, err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("brief: resolve state root %q: %w", store.root, err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("brief: stat state root %q: %w", root, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("brief: state root %q is not a directory", root)
	}
	return root, nil
}

func (store *FileStateStore) currentTime() time.Time {
	if store == nil || store.now == nil {
		return time.Now()
	}
	return store.now()
}

// writeBriefStateAtomic hands the state file to the shared durable-state writer,
// which owns the location refusal, the path-continuous replacement, and the
// exact-path snapshot. Rendering a brief is a read and stays available on a
// vault that cannot be written; only recording one here is refused.
func writeBriefStateAtomic(vaultRoot, statePath string, data []byte, permissions os.FileMode) error {
	return vaultstate.Save(vaultRoot, "brief-state", statePath, data, permissions)
}

// External replica activity is operational state, with no vault Git lifecycle.
//
// It still joins the vault's checkpoint barrier. The file is outside the vault,
// so a copy of the vault alone never holds it — but nothing copies the vault
// alone: the deployment snapshot takes the vault and this activity directory
// together, and the pair is only consistent if a checkpoint excludes writes to
// both. The in-vault route below reaches the same barrier through
// vaultstate.Save; this one has to say so itself.
func (store *FileStateStore) writeState(vaultRoot, path string, data []byte) error {
	if store.storageRoot == "" {
		return writeBriefStateAtomic(vaultRoot, path, data, 0o644)
	}
	barrier, err := checkpoint.Join(vaultRoot)
	if err != nil {
		// A read-only, unmanaged replica has nowhere to keep a checkpoint
		// lock. Its external activity is still safe to write: no checkpoint can
		// be running against a vault whose lock directory cannot be created.
		// Keep every other checkpoint refusal, including a live checkpoint,
		// because those are evidence that the vault does have a working barrier.
		if !errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("brief: %w", err)
		}
		barrier = func() {}
	}
	defer barrier()
	if err := vault.GuardMutation(vaultRoot, path); err != nil {
		return err
	}
	//bn:direct-write external activity directory after location preflight; server validates it is outside the vault.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Recheck after creating the directory to preserve symlink refusal.
	if _, err := store.statePath(); err != nil {
		return err
	}
	return vault.WriteStateFile(path, data, 0o600)
}

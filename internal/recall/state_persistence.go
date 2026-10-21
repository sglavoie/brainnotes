package recall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/brainnotes/internal/checkpoint"
	"github.com/sglavoie/brainnotes/internal/textnorm"
	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/vaultstate"
)

// mutateLocked is the single write boundary for recall.json. The caller holds
// store.mu; this method adds the process-wide/cross-process vault path lock and
// keeps it through the fresh read, mutation, and atomic replacement.
func (store *StateStore) mutateLocked(
	now time.Time,
	mutate func(*recallStateFile, map[string]struct{}) error,
) error {
	root, err := store.rootPath()
	if err != nil {
		return err
	}
	// Before the path lock, never after. vaultstate.Save joins the barrier itself
	// and saveLocked below reaches it from inside this lock, so a join taken here
	// is the nested one that costs nothing — and a join taken only down there
	// would be a writer asking for the barrier while holding a vault lock, which
	// is the one ordering internal/checkpoint says closes a cycle.
	joinedCtx, barrier, err := checkpoint.JoinContext(context.Background(), root)
	if err != nil {
		return fmt.Errorf("recall: %w", err)
	}
	defer barrier()
	release, err := vault.LockPaths(root, recallStateRelPath)
	if err != nil {
		return fmt.Errorf("recall: lock state: %w", err)
	}
	defer release()

	state, paths, err := store.prepareWriteLocked(now)
	if err != nil {
		return err
	}
	if err := mutate(&state, paths); err != nil {
		return err
	}
	return store.saveLocked(joinedCtx, state)
}

func (store *StateStore) prepareWriteLocked(now time.Time) (recallStateFile, map[string]struct{}, error) {
	// Explicit writes always reread the current file. The cached snapshot is a
	// read optimization, not permission to overwrite corruption or concurrent
	// state changes that appeared after an earlier query.
	store.state = nil
	store.stateExists = false
	state, _, err := store.loadLocked()
	if err != nil {
		return recallStateFile{}, nil, err
	}
	paths, err := store.notePathsLocked(true)
	if err != nil {
		return recallStateFile{}, nil, err
	}
	state = cloneRecallState(state)
	pruneRecallState(&state, paths, now)
	return state, paths, nil
}

func pruneRecallState(state *recallStateFile, paths map[string]struct{}, now time.Time) {
	for pairID, record := range state.Records {
		if !containsRecallPath(paths, record.SourceRelPath) || !containsRecallPath(paths, record.CandidateRelPath) {
			delete(state.Records, pairID)
			continue
		}
		if record.SnoozedUntil != nil && !record.SnoozedUntil.After(now) {
			record.SnoozedUntil = nil
			record.UpdatedAt = now
			if recallRecordEmpty(record) {
				delete(state.Records, pairID)
			} else {
				state.Records[pairID] = record
			}
		}
	}
}

func recallRecordEmpty(record recallStateRecord) bool {
	return record.Rating == "" && record.SnoozedUntil == nil && record.LastOpenedAt == nil
}

func requireRecallPairExists(paths map[string]struct{}, sourcePath, candidatePath string) error {
	if !containsRecallPath(paths, sourcePath) {
		return fmt.Errorf("recall: source note %q does not exist in the vault", sourcePath)
	}
	if !containsRecallPath(paths, candidatePath) {
		return fmt.Errorf("recall: candidate note %q does not exist in the vault", candidatePath)
	}
	return nil
}

func containsRecallPath(paths map[string]struct{}, wanted string) bool {
	_, ok := paths[wanted]
	return ok
}

func (store *StateStore) loadLocked() (recallStateFile, bool, error) {
	if store == nil {
		return recallStateFile{}, false, errors.New("recall: nil state store")
	}
	if store.state != nil {
		return *store.state, store.stateExists, nil
	}
	statePath, err := store.statePath()
	if err != nil {
		return recallStateFile{}, false, err
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			state := newRecallState()
			store.state = &state
			return state, false, nil
		}
		return recallStateFile{}, false, fmt.Errorf("recall: read state %q: %w", statePath, err)
	}

	var state recallStateFile
	if err := json.Unmarshal(data, &state); err != nil {
		return recallStateFile{}, false, fmt.Errorf("%w: parse %q: %v", ErrCorruptState, statePath, err)
	}
	if state.Version != recallStateVersion {
		return recallStateFile{}, false, fmt.Errorf(
			"%w: got %d want %d",
			ErrUnsupportedStateVersion,
			state.Version,
			recallStateVersion,
		)
	}
	if state.Records == nil {
		state.Records = map[string]recallStateRecord{}
	}
	if err := validateRecallState(state); err != nil {
		return recallStateFile{}, false, err
	}
	store.state = &state
	store.stateExists = true
	return state, true, nil
}

func validateRecallState(state recallStateFile) error {
	ids := make([]string, 0, len(state.Records))
	for pairID := range state.Records {
		ids = append(ids, pairID)
	}
	sort.Strings(ids)
	for _, pairID := range ids {
		record := state.Records[pairID]
		sourcePath, candidatePath, expectedID, err := normalizeRecallPair(record.SourceRelPath, record.CandidateRelPath)
		if err != nil {
			return fmt.Errorf("%w: record %q: %v", ErrCorruptState, pairID, err)
		}
		if pairID != expectedID {
			return fmt.Errorf("%w: record %q does not match its source/candidate paths", ErrCorruptState, pairID)
		}
		if sourcePath != record.SourceRelPath || candidatePath != record.CandidateRelPath {
			return fmt.Errorf("%w: record %q contains non-normalized paths", ErrCorruptState, pairID)
		}
		switch record.Rating {
		case "", string(FeedbackUseful), string(FeedbackDismissed):
		default:
			return fmt.Errorf("%w: record %q has unknown rating %q", ErrCorruptState, pairID, record.Rating)
		}
		if record.Rating != "" && record.SnoozedUntil != nil {
			return fmt.Errorf("%w: record %q has both a rating and snooze", ErrCorruptState, pairID)
		}
		if record.UpdatedAt.IsZero() {
			return fmt.Errorf("%w: record %q has no updated_at", ErrCorruptState, pairID)
		}
		if recallRecordEmpty(record) {
			return fmt.Errorf("%w: record %q has no feedback or open history", ErrCorruptState, pairID)
		}
	}
	return nil
}

func newRecallState() recallStateFile {
	return recallStateFile{
		Version: recallStateVersion,
		Records: map[string]recallStateRecord{},
	}
}

func cloneRecallState(state recallStateFile) recallStateFile {
	clone := recallStateFile{
		Version: state.Version,
		Records: make(map[string]recallStateRecord, len(state.Records)),
	}
	for pairID, record := range state.Records {
		if record.SnoozedUntil != nil {
			deadline := *record.SnoozedUntil
			record.SnoozedUntil = &deadline
		}
		if record.LastOpenedAt != nil {
			opened := *record.LastOpenedAt
			record.LastOpenedAt = &opened
		}
		clone.Records[pairID] = record
	}
	return clone
}

func (store *StateStore) saveLocked(ctx context.Context, state recallStateFile) error {
	state.Version = recallStateVersion
	if state.Records == nil {
		state.Records = map[string]recallStateRecord{}
	}
	root, err := store.rootPath()
	if err != nil {
		return err
	}
	statePath, err := store.statePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("recall: encode state: %w", err)
	}
	data = append(data, '\n')
	if err := writeRecallStateAtomic(ctx, root, statePath, data, 0o644); err != nil {
		return err
	}
	snapshot := cloneRecallState(state)
	store.state = &snapshot
	store.stateExists = true
	return nil
}

func (store *StateStore) notePathsLocked(refresh bool) (map[string]struct{}, error) {
	if store.notePathsErr != nil {
		return nil, store.notePathsErr
	}
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
		normalized, err := normalizeRecallPath(textnorm.NFC(filepath.ToSlash(relative)))
		if err != nil {
			return fmt.Errorf("recall: inspect note path %q: %w", relative, err)
		}
		paths[normalized] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("recall: scan vault notes: %w", err)
	}
	// A caller-provided snapshot remains authoritative for read-only queries,
	// but an explicit write must return the freshly scanned existence set. This
	// keeps resilient web visibility separate from the pruning domain shared by
	// every CLI and web mutation.
	if !store.notePathsFixed {
		store.notePaths = paths
	}
	return paths, nil
}

func (store *StateStore) statePath() (string, error) {
	root, err := store.rootPath()
	if err != nil {
		return "", err
	}
	directory := filepath.Join(root, ".bnotes")
	if info, err := os.Lstat(directory); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("recall: state directory %q must be a real directory", directory)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("recall: inspect state directory %q: %w", directory, err)
	}
	statePath := filepath.Join(root, filepath.FromSlash(recallStateRelPath))
	if info, err := os.Lstat(statePath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", fmt.Errorf("recall: state path %q must be a regular file", statePath)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("recall: inspect state path %q: %w", statePath, err)
	}
	return statePath, nil
}

func (store *StateStore) rootPath() (string, error) {
	if store == nil {
		return "", errors.New("recall: nil state store")
	}
	if strings.TrimSpace(store.root) == "" {
		return "", errors.New("recall: empty state root")
	}
	root, err := filepath.Abs(store.root)
	if err != nil {
		return "", fmt.Errorf("recall: resolve state root %q: %w", store.root, err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("recall: resolve state root %q: %w", store.root, err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("recall: stat state root %q: %w", root, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("recall: state root %q is not a directory", root)
	}
	return root, nil
}

func (store *StateStore) currentTime() time.Time {
	if store == nil || store.now == nil {
		return time.Now()
	}
	return store.now()
}

// writeRecallStateAtomic hands the sidecar to the shared durable-state writer,
// which owns the location refusal, the path-continuous replacement, and the
// exact-path snapshot. Reads are untouched: recall still ranks and returns
// context under a refused root, it just cannot record there.
func writeRecallStateAtomic(ctx context.Context, vaultRoot, statePath string, data []byte, permissions os.FileMode) error {
	return vaultstate.SaveContext(ctx, vaultRoot, "recall-state", statePath, data, permissions)
}

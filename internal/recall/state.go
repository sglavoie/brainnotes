package recall

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/textnorm"
)

const (
	recallStateVersion = 1
	recallStateRelPath = ".bnotes/recall.json"
)

var (
	// ErrCorruptState identifies state that cannot be decoded or whose records
	// violate the versioned recall-state contract.
	ErrCorruptState = errors.New("recall: corrupt state")
	// ErrUnsupportedStateVersion identifies a readable state file written with
	// a schema version this binary does not understand.
	ErrUnsupportedStateVersion = errors.New("recall: unsupported state version")
)

// FeedbackState is an explicit disposition for a source/candidate pair.
type FeedbackState string

const (
	FeedbackUseful    FeedbackState = "useful"
	FeedbackDismissed FeedbackState = "dismissed"
	FeedbackLater     FeedbackState = "later"
	FeedbackClear     FeedbackState = "clear"
)

// Valid reports whether state is accepted by SetFeedback.
func (state FeedbackState) Valid() bool {
	switch state {
	case FeedbackUseful, FeedbackDismissed, FeedbackLater, FeedbackClear:
		return true
	default:
		return false
	}
}

// StateStoreOption configures a recall StateStore.
type StateStoreOption func(*StateStore)

// WithClock injects the clock used for feedback, snooze, and open timestamps.
func WithClock(now func() time.Time) StateStoreOption {
	return func(store *StateStore) {
		store.now = now
	}
}

// StateStore persists recall feedback for one vault. It loads state lazily and
// keeps one validated snapshot for the lifetime of the store, which lets a
// ranker request many pair signals without rereading recall.json per candidate.
type StateStore struct {
	root string
	now  func() time.Time

	mu          sync.Mutex
	state       *recallStateFile
	stateExists bool
	notePaths   map[string]struct{}
}

type recallStateFile struct {
	Version int                          `json:"version"`
	Records map[string]recallStateRecord `json:"records"`
}

type recallStateRecord struct {
	SourceRelPath    string     `json:"source_rel_path"`
	CandidateRelPath string     `json:"candidate_rel_path"`
	Rating           string     `json:"rating"`
	SnoozedUntil     *time.Time `json:"snoozed_until,omitempty"`
	LastOpenedAt     *time.Time `json:"last_opened_at,omitempty"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// NewStateStore returns a recall state store rooted at root. The .bnotes
// directory and recall.json are created lazily on the first explicit write.
func NewStateStore(root string, options ...StateStoreOption) *StateStore {
	store := &StateStore{root: root, now: time.Now}
	for _, option := range options {
		if option != nil {
			option(store)
		}
	}
	return store
}

// Validate loads and validates the current state without creating or changing
// any file. Callers can use it to surface corruption even when a query has no
// candidate pairs and therefore never calls Signal.
func (store *StateStore) Validate() error {
	if store == nil {
		return errors.New("recall: nil state store")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	_, _, err := store.loadLocked()
	return err
}

// Signal implements RecallSignalSource. Missing notes and expired snoozes are
// ignored in the returned signal, but this read-only method never prunes or
// persists the loaded state.
func (store *StateStore) Signal(sourcePath, candidatePath string) (RecallSignal, error) {
	if store == nil {
		return RecallSignal{}, errors.New("recall: nil state store")
	}
	sourcePath, candidatePath, pairID, err := normalizeRecallPair(sourcePath, candidatePath)
	if err != nil {
		return RecallSignal{}, err
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	state, _, err := store.loadLocked()
	if err != nil {
		return RecallSignal{}, err
	}
	record, ok := state.Records[pairID]
	if !ok {
		return RecallSignal{}, nil
	}
	paths, err := store.notePathsLocked(false)
	if err != nil {
		return RecallSignal{}, err
	}
	if !containsRecallPath(paths, sourcePath) || !containsRecallPath(paths, candidatePath) {
		return RecallSignal{}, nil
	}

	signal := RecallSignal{
		Useful:    record.Rating == string(FeedbackUseful),
		Dismissed: record.Rating == string(FeedbackDismissed),
	}
	if record.SnoozedUntil != nil && record.SnoozedUntil.After(store.currentTime()) {
		signal.SnoozedUntil = *record.SnoozedUntil
	}
	if record.LastOpenedAt != nil {
		signal.LastOpenedAt = *record.LastOpenedAt
	}
	return signal, nil
}

// SetFeedback writes an explicit useful, dismissed, later, or clear state for
// a pair. Later requires a future deadline; every other state rejects one.
func (store *StateStore) SetFeedback(
	sourcePath string,
	candidatePath string,
	feedback FeedbackState,
	until time.Time,
) error {
	if store == nil {
		return errors.New("recall: nil state store")
	}
	if !feedback.Valid() {
		return fmt.Errorf("recall: unknown feedback state %q", feedback)
	}
	sourcePath, candidatePath, pairID, err := normalizeRecallPair(sourcePath, candidatePath)
	if err != nil {
		return err
	}
	now := store.currentTime()
	if feedback == FeedbackLater {
		if until.IsZero() {
			return errors.New("recall: later feedback requires a snooze deadline")
		}
		if !until.After(now) {
			return fmt.Errorf("recall: snooze deadline %s must be in the future", until.Format(time.RFC3339))
		}
	} else if !until.IsZero() {
		return fmt.Errorf("recall: feedback state %q does not accept a snooze deadline", feedback)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	state, paths, err := store.prepareWriteLocked(now)
	if err != nil {
		return err
	}
	if err := requireRecallPairExists(paths, sourcePath, candidatePath); err != nil {
		return err
	}

	record := state.Records[pairID]
	record.SourceRelPath = sourcePath
	record.CandidateRelPath = candidatePath
	switch feedback {
	case FeedbackUseful, FeedbackDismissed:
		record.Rating = string(feedback)
		record.SnoozedUntil = nil
	case FeedbackLater:
		record.Rating = ""
		deadline := until
		record.SnoozedUntil = &deadline
	case FeedbackClear:
		record.Rating = ""
		record.SnoozedUntil = nil
	}
	record.UpdatedAt = now
	if recallRecordEmpty(record) {
		delete(state.Records, pairID)
	} else {
		state.Records[pairID] = record
	}
	return store.saveLocked(state)
}

// RecordOpened records that the candidate was selected interactively from the
// source note while preserving any explicit feedback for the same pair.
func (store *StateStore) RecordOpened(sourcePath, candidatePath string) error {
	if store == nil {
		return errors.New("recall: nil state store")
	}
	sourcePath, candidatePath, pairID, err := normalizeRecallPair(sourcePath, candidatePath)
	if err != nil {
		return err
	}
	now := store.currentTime()

	store.mu.Lock()
	defer store.mu.Unlock()

	state, paths, err := store.prepareWriteLocked(now)
	if err != nil {
		return err
	}
	if err := requireRecallPairExists(paths, sourcePath, candidatePath); err != nil {
		return err
	}
	record := state.Records[pairID]
	record.SourceRelPath = sourcePath
	record.CandidateRelPath = candidatePath
	record.LastOpenedAt = &now
	record.UpdatedAt = now
	state.Records[pairID] = record
	return store.saveLocked(state)
}

// PairID returns the deterministic identifier for a normalized, directed
// source/candidate pair.
func PairID(sourcePath, candidatePath string) (string, error) {
	_, _, pairID, err := normalizeRecallPair(sourcePath, candidatePath)
	return pairID, err
}

func normalizeRecallPair(sourcePath, candidatePath string) (string, string, string, error) {
	sourcePath, err := normalizeRecallPath(sourcePath)
	if err != nil {
		return "", "", "", fmt.Errorf("recall: source path: %w", err)
	}
	candidatePath, err = normalizeRecallPath(candidatePath)
	if err != nil {
		return "", "", "", fmt.Errorf("recall: candidate path: %w", err)
	}
	if sourcePath == candidatePath {
		return "", "", "", errors.New("recall: source and candidate paths must differ")
	}
	sum := sha256.Sum256([]byte(sourcePath + "\x00" + candidatePath))
	return sourcePath, candidatePath, hex.EncodeToString(sum[:]), nil
}

func normalizeRecallPath(value string) (string, error) {
	if value == "" {
		return "", errors.New("empty vault-relative path")
	}
	if strings.ContainsRune(value, '\x00') {
		return "", errors.New("vault-relative path contains NUL")
	}
	if strings.Contains(value, `\`) {
		return "", fmt.Errorf("vault-relative path %q must use forward slashes", value)
	}
	value = textnorm.NFC(filepath.ToSlash(value))
	if filepath.IsAbs(value) || path.IsAbs(value) || filepath.VolumeName(value) != "" || looksLikeWindowsDrivePath(value) {
		return "", fmt.Errorf("path %q must be vault-relative", value)
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("path %q escapes the vault root", value)
	}
	if cleaned != value {
		return "", fmt.Errorf("path %q is not a normalized vault-relative path", value)
	}
	return value, nil
}

func looksLikeWindowsDrivePath(value string) bool {
	if len(value) < 2 || value[1] != ':' {
		return false
	}
	first := value[0]
	return (first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z')
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

func (store *StateStore) saveLocked(state recallStateFile) error {
	state.Version = recallStateVersion
	if state.Records == nil {
		state.Records = map[string]recallStateRecord{}
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
	if err := writeRecallStateAtomic(statePath, data, 0o644); err != nil {
		return err
	}
	snapshot := cloneRecallState(state)
	store.state = &snapshot
	store.stateExists = true
	return nil
}

func (store *StateStore) notePathsLocked(refresh bool) (map[string]struct{}, error) {
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
	store.notePaths = paths
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

func writeRecallStateAtomic(statePath string, data []byte, permissions os.FileMode) error {
	directory := filepath.Dir(statePath)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("recall: mkdir state directory %q: %w", directory, err)
	}
	resolvedDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return fmt.Errorf("recall: resolve state directory %q: %w", directory, err)
	}
	if filepath.Clean(resolvedDirectory) != filepath.Clean(directory) {
		return fmt.Errorf("recall: state directory %q must not resolve through a symlink", directory)
	}

	temporary, err := os.CreateTemp(directory, ".recall-*")
	if err != nil {
		return fmt.Errorf("recall: create state temp in %q: %w", directory, err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() { _ = os.Remove(temporaryPath) }
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		cleanup()
		return fmt.Errorf("recall: write state temp %q: %w", temporaryPath, err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		cleanup()
		return fmt.Errorf("recall: sync state temp %q: %w", temporaryPath, err)
	}
	if err := temporary.Close(); err != nil {
		cleanup()
		return fmt.Errorf("recall: close state temp %q: %w", temporaryPath, err)
	}
	if permissions == 0 {
		permissions = 0o644
	}
	if err := os.Chmod(temporaryPath, permissions); err != nil {
		cleanup()
		return fmt.Errorf("recall: chmod state temp %q: %w", temporaryPath, err)
	}
	if err := os.Rename(temporaryPath, statePath); err != nil {
		cleanup()
		return fmt.Errorf("recall: rename state temp %q -> %q: %w", temporaryPath, statePath, err)
	}
	return nil
}

package journal

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

type recorderOptions struct {
	now   func() time.Time
	idGen func(time.Time) (string, error)
}

// RecorderOption customizes recorder construction.
type RecorderOption func(*recorderOptions)

// WithClock overrides the timestamp source.
func WithClock(now func() time.Time) RecorderOption {
	return func(o *recorderOptions) {
		if now != nil {
			o.now = now
		}
	}
}

// WithIDGenerator overrides entry ID generation.
func WithIDGenerator(gen func(time.Time) (string, error)) RecorderOption {
	return func(o *recorderOptions) {
		if gen != nil {
			o.idGen = gen
		}
	}
}

// Recorder snapshots pre-change states and writes one manifest on finalize.
type Recorder struct {
	vaultRoot   string
	journalRoot string
	entryDir    string
	entry       Entry
	files       []FileRecord
	index       map[string]int
	finalized   bool
}

// NewRecorder creates a journal entry directory and an in-memory recorder.
func NewRecorder(vaultRoot, journalRoot, command string, opts ...RecorderOption) (*Recorder, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, fmt.Errorf("journal: empty command")
	}
	if strings.TrimSpace(vaultRoot) == "" {
		return nil, fmt.Errorf("journal: empty vault root")
	}
	if strings.TrimSpace(journalRoot) == "" {
		return nil, fmt.Errorf("journal: empty journal root")
	}
	vaultAbs, err := filepath.Abs(vaultRoot)
	if err != nil {
		return nil, fmt.Errorf("journal: resolve vault root %q: %w", vaultRoot, err)
	}
	journalAbs, err := filepath.Abs(journalRoot)
	if err != nil {
		return nil, fmt.Errorf("journal: resolve journal root %q: %w", journalRoot, err)
	}
	// The recorder is usually the first thing a mutation creates on disk — a
	// high-level caller captures pre-change state before it takes any lock — so
	// the location policy has to be consulted here rather than left to the write
	// that follows. Otherwise a refused mutation still leaves an entry directory
	// and its snapshots behind.
	if err := vault.GuardMutation(vaultAbs, journalAbs); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(journalAbs, 0o755); err != nil {
		return nil, fmt.Errorf("journal: mkdir %q: %w", journalAbs, err)
	}

	cfg := recorderOptions{
		now: func() time.Time { return time.Now().UTC() },
		idGen: func(ts time.Time) (string, error) {
			suffix, err := defaultEntryID()
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%s-%s", ts.UTC().Format("20060102T150405.000000000Z"), suffix), nil
		},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	ts := cfg.now().UTC()
	if ts.IsZero() {
		return nil, fmt.Errorf("journal: zero timestamp")
	}

	var (
		id       string
		entryDir string
	)
	for i := 0; i < 16; i++ {
		id, err = cfg.idGen(ts)
		if err != nil {
			return nil, fmt.Errorf("journal: generate id: %w", err)
		}
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, fmt.Errorf("journal: generated empty id")
		}
		if strings.Contains(id, "/") || strings.Contains(id, `\`) {
			return nil, fmt.Errorf("journal: generated id %q contains path separator", id)
		}
		entryDir = filepath.Join(journalAbs, id)
		err = os.Mkdir(entryDir, 0o755)
		if err == nil {
			break
		}
		if os.IsExist(err) {
			continue
		}
		return nil, fmt.Errorf("journal: mkdir %q: %w", entryDir, err)
	}
	if err != nil {
		return nil, fmt.Errorf("journal: could not allocate unique entry id")
	}
	if err := os.MkdirAll(filepath.Join(entryDir, SnapshotsDirName), 0o755); err != nil {
		return nil, fmt.Errorf("journal: mkdir snapshots: %w", err)
	}

	return &Recorder{
		vaultRoot:   vaultAbs,
		journalRoot: journalAbs,
		entryDir:    entryDir,
		entry: Entry{
			ID:        id,
			Timestamp: ts,
			Command:   command,
		},
		index: make(map[string]int),
	}, nil
}

// EntryID is the identity of the entry this recorder is building.
//
// It is minted in NewRecorder, together with the directory that carries it, so
// it is knowable long before Finalize writes the manifest. That is what a caller
// needs when something else has to be made durable *before* the mutation the
// entry describes is committed — a record of where an applied change came from,
// which is worthless if it is written after the commit it explains. The entry
// may still fail to finalize, so the identity is a promise about naming and not
// about existence; journal.LoadEntry is how a caller asks the second question.
func (r *Recorder) EntryID() string {
	if r == nil {
		return ""
	}
	return r.entry.ID
}

// Capture records the pre-change state for path, once per unique path.
func (r *Recorder) Capture(path string) error {
	if r == nil {
		return fmt.Errorf("journal: nil recorder")
	}
	if r.finalized {
		return fmt.Errorf("journal: recorder already finalized")
	}
	abs, relSlash, err := normalizeWithinRoot(r.vaultRoot, path)
	if err != nil {
		return fmt.Errorf("journal: capture %q: %w", path, err)
	}
	if _, ok := r.index[relSlash]; ok {
		return nil
	}
	state, data, err := fileStateFromDisk(abs, true)
	if err != nil {
		return fmt.Errorf("journal: capture %q: %w", relSlash, err)
	}
	if state.Kind == KindFile {
		snapshotRel := filepath.ToSlash(filepath.Join(SnapshotsDirName, snapshotFileName(relSlash)))
		snapshotPath := filepath.Join(r.entryDir, filepath.FromSlash(snapshotRel))
		if err := writeAtomic(snapshotPath, data, 0o600); err != nil {
			return fmt.Errorf("journal: capture %q: write snapshot: %w", relSlash, err)
		}
		state.Snapshot = snapshotRel
	}
	r.index[relSlash] = len(r.files)
	r.files = append(r.files, FileRecord{Path: relSlash, Before: state})
	return nil
}

// Finalize captures post-change states and persists manifest.json.
func (r *Recorder) Finalize() (*Entry, error) {
	if r == nil {
		return nil, fmt.Errorf("journal: nil recorder")
	}
	if r.finalized {
		return nil, fmt.Errorf("journal: recorder already finalized")
	}
	for i := range r.files {
		abs := filepath.Join(r.vaultRoot, filepath.FromSlash(r.files[i].Path))
		state, _, err := fileStateFromDisk(abs, false)
		if err != nil {
			return nil, fmt.Errorf("journal: finalize %q: %w", r.files[i].Path, err)
		}
		r.files[i].After = state
	}
	sort.Slice(r.files, func(i, j int) bool { return r.files[i].Path < r.files[j].Path })
	r.entry.Files = append([]FileRecord(nil), r.files...)
	if err := r.entry.validate(); err != nil {
		return nil, fmt.Errorf("journal: finalize invalid manifest: %w", err)
	}
	manifestPath := filepath.Join(r.entryDir, ManifestFileName)
	if err := writeJSONAtomic(manifestPath, r.entry); err != nil {
		return nil, fmt.Errorf("journal: write manifest: %w", err)
	}
	r.finalized = true
	clone := r.entry
	clone.Files = append([]FileRecord(nil), r.entry.Files...)
	return &clone, nil
}

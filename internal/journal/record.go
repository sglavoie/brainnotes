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
	now         func() time.Time
	idGen       func(time.Time) (string, error)
	operationID string
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

// WithOperationID records an opaque browser attempt ID beside the journal
// entry so a lost HTTP response can be checked without guessing from filenames
// or timestamps. The web layer validates its format before a write starts.
func WithOperationID(id string) RecorderOption {
	return func(o *recorderOptions) { o.operationID = id }
}

// NewEntryID mints the identity an entry recorded at ts would be created under.
// It is what NewRecorder uses when no generator is supplied.
//
// It is exported for the one caller that cannot wait for the recorder to exist:
// an operation whose identity has to be durable *before* the mutation it
// describes is committed, so that a process death in between leaves a record
// naming the entry rather than an entry nothing names. Such a caller mints the
// identity here and hands it back through WithIDGenerator, which is what makes
// the intent it wrote and the entry the recorder creates the same entry.
//
// A minted identity is a name, not a reservation: nothing exists on disk until
// NewRecorder creates the directory, and two mints in the same nanosecond are
// kept apart by the random suffix rather than by a check. The retry in NewRecorder
// is what makes that safe, and a caller pinning one identity gives that retry up
// — which is correct for a caller that needs the name it already wrote down.
func NewEntryID(ts time.Time) (string, error) {
	suffix, err := defaultEntryID()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s", ts.UTC().Format("20060102T150405.000000000Z"), suffix), nil
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
	command, vaultAbs, journalAbs, err := resolveRecorder(vaultRoot, journalRoot, command)
	if err != nil {
		return nil, err
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
		now:   func() time.Time { return time.Now().UTC() },
		idGen: NewEntryID,
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

	id, entryDir, err := allocateEntry(journalAbs, ts, cfg.idGen)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(entryDir, SnapshotsDirName), 0o755); err != nil {
		return nil, fmt.Errorf("journal: mkdir snapshots: %w", err)
	}

	return &Recorder{
		vaultRoot:   vaultAbs,
		journalRoot: journalAbs,
		entryDir:    entryDir,
		entry: Entry{
			ID:          id,
			Timestamp:   ts,
			Command:     command,
			OperationID: cfg.operationID,
		},
		index: make(map[string]int),
	}, nil
}

// resolveRecorder checks what the caller asked for and turns it into the two
// absolute roots an entry is written under.
//
// It runs before anything is created, because every argument it rejects would
// otherwise be rejected later by a call that had already made a directory: an
// entry recording a mutation nobody can name is worse than no entry, and the
// only moment it costs nothing to say so is this one.
func resolveRecorder(vaultRoot, journalRoot, command string) (string, string, string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", "", "", fmt.Errorf("journal: empty command")
	}
	if strings.TrimSpace(vaultRoot) == "" {
		return "", "", "", fmt.Errorf("journal: empty vault root")
	}
	if strings.TrimSpace(journalRoot) == "" {
		return "", "", "", fmt.Errorf("journal: empty journal root")
	}
	vaultAbs, err := filepath.Abs(vaultRoot)
	if err != nil {
		return "", "", "", fmt.Errorf("journal: resolve vault root %q: %w", vaultRoot, err)
	}
	journalAbs, err := filepath.Abs(journalRoot)
	if err != nil {
		return "", "", "", fmt.Errorf("journal: resolve journal root %q: %w", journalRoot, err)
	}
	return command, vaultAbs, journalAbs, nil
}

// allocateEntry mints an identity and creates the directory that carries it,
// retrying until it gets one nobody else holds.
//
// The exclusive os.Mkdir is what makes the identity real: two recorders that
// mint the same name in the same nanosecond both try to create it and exactly
// one succeeds, so the directory is the allocation rather than a check followed
// by a write. A caller that pinned an identity through WithIDGenerator gets the
// same generator answer every time round, so for it the loop is one attempt and
// a collision is a genuine failure — which is correct, since a pinned identity
// was written down somewhere before this call and cannot be traded for another.
func allocateEntry(journalAbs string, ts time.Time, idGen func(time.Time) (string, error)) (string, string, error) {
	var err error
	for range 16 {
		var id string
		id, err = idGen(ts)
		if err != nil {
			return "", "", fmt.Errorf("journal: generate id: %w", err)
		}
		id = strings.TrimSpace(id)
		if id == "" {
			return "", "", fmt.Errorf("journal: generated empty id")
		}
		if strings.Contains(id, "/") || strings.Contains(id, `\`) {
			return "", "", fmt.Errorf("journal: generated id %q contains path separator", id)
		}
		entryDir := filepath.Join(journalAbs, id)
		switch err = os.Mkdir(entryDir, 0o755); {
		case err == nil:
			return id, entryDir, nil
		case os.IsExist(err):
			continue
		default:
			return "", "", fmt.Errorf("journal: mkdir %q: %w", entryDir, err)
		}
	}
	return "", "", fmt.Errorf("journal: could not allocate unique entry id")
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
	state, data, err := fileStateFromDisk(abs)
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
		state, err := fileStateOnly(abs)
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

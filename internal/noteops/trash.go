package noteops

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

const trashTimestampFormat = "20060102T150405Z"

// TrashDestinationPlan captures the deterministic destination chosen for a
// Trash move so a later apply can reuse the exact previewed path.
type TrashDestinationPlan struct {
	SourceRelPath      string    `json:"source_rel_path"`
	BaseRelPath        string    `json:"base_rel_path"`
	DestinationRelPath string    `json:"destination_rel_path"`
	Timestamp          time.Time `json:"timestamp"`
	CollisionIDs       []string  `json:"collision_ids,omitempty"`
}

// TrashResult describes a note moved into the vault-local Trash folder.
type TrashResult struct {
	Source      PathResolution
	Destination PathResolution
	Journal     *journal.Entry
}

type trashOptions struct {
	command        string
	now            func() time.Time
	shortID        func() (string, error)
	journalOptions []journal.RecorderOption
}

// TrashOption customizes TrashNote.
type TrashOption func(*trashOptions)

// WithTrashCommand sets the command stored in the undo journal entry.
func WithTrashCommand(command string) TrashOption {
	return func(o *trashOptions) {
		o.command = strings.TrimSpace(command)
	}
}

// WithTrashClock sets the timestamp source for collision suffixes and journaling.
func WithTrashClock(now func() time.Time) TrashOption {
	return func(o *trashOptions) {
		if now == nil {
			return
		}
		o.now = now
		o.journalOptions = append(o.journalOptions, journal.WithClock(now))
	}
}

// WithTrashShortIDGenerator sets the short ID source used in collision suffixes.
func WithTrashShortIDGenerator(gen func() (string, error)) TrashOption {
	return func(o *trashOptions) {
		if gen != nil {
			o.shortID = gen
		}
	}
}

// WithTrashJournalOptions passes recorder options through to the undo journal.
func WithTrashJournalOptions(opts ...journal.RecorderOption) TrashOption {
	return func(o *trashOptions) {
		o.journalOptions = append(o.journalOptions, opts...)
	}
}

// TrashNote moves sourceRel to Trash/<sourceRel> without rewriting backlinks.
func TrashNote(vaultRoot, sourceRel string, opts ...TrashOption) (*TrashResult, error) {
	cfg := defaultTrashOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	source, err := resolveTrashSource(vaultRoot, sourceRel)
	if err != nil {
		return nil, err
	}
	dest, err := resolveTrashDestination(vaultRoot, path.Join("Trash", source.RelPath), cfg)
	if err != nil {
		return nil, err
	}

	j := NewJournal(vaultRoot, cfg.command, cfg.journalOptions...)
	if err := j.Capture(source.AbsPath); err != nil {
		return nil, err
	}
	if err := j.Capture(dest.AbsPath); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(dest.AbsPath), 0o755); err != nil {
		return nil, WrapError(CodeInternal, "create trash folder", err)
	}
	if err := moveToTrash(vaultRoot, source.AbsPath, dest.AbsPath); err != nil {
		return nil, err
	}

	entry, err := j.Finalize()
	if err != nil {
		return nil, err
	}
	return &TrashResult{
		Source:      source,
		Destination: dest,
		Journal:     entry,
	}, nil
}

// moveToTrash performs the guarded move shared by TrashNote and BulkTrashNotes.
// It holds the path lock on both ends across the existence check and the rename,
// so the check cannot go stale before the rename acts on it and the source
// vacancy the move opens is never visible to another bn writer. It is a function
// rather than inline code so the locks release per note instead of accumulating
// across a bulk batch.
func moveToTrash(vaultRoot, source, dest string) error {
	release, err := vault.LockPaths(vaultRoot, source, dest)
	if err != nil {
		return classifyTxnError("move note to trash", err)
	}
	defer release()
	if exists, err := pathExists(dest); err != nil {
		return WrapError(CodeConflict, "check trash destination", err)
	} else if exists {
		return Errorf(CodeConflict, "trash destination already exists")
	}
	if err := os.Rename(source, dest); err != nil {
		return WrapError(CodeInternal, "move note to trash", err)
	}
	return nil
}

func defaultTrashOptions() trashOptions {
	return trashOptions{
		command: "bn web trash",
		now:     func() time.Time { return time.Now().UTC() },
		shortID: defaultTrashShortID,
	}
}

func resolveTrashSource(vaultRoot, sourceRel string) (PathResolution, error) {
	cleanRel, err := CleanRelPath(sourceRel)
	if err != nil {
		return PathResolution{}, err
	}
	source, err := ResolveExistingPath(vaultRoot, filepath.FromSlash(cleanRel))
	if err != nil {
		return PathResolution{}, err
	}
	if source.RelPath != cleanRel {
		return PathResolution{}, Errorf(CodeForbidden, "source path must not resolve through a symlink")
	}
	info, err := os.Lstat(source.AbsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return PathResolution{}, Errorf(CodeNotFound, "note not found")
		}
		return PathResolution{}, WrapError(CodeConflict, "stat source note", err)
	}
	if !info.Mode().IsRegular() {
		return PathResolution{}, Errorf(CodeConflict, "source is not a regular file")
	}
	return source, nil
}

func resolveTrashDestination(vaultRoot, baseRel string, cfg trashOptions) (PathResolution, error) {
	dest, _, err := resolveTrashDestinationPlan(vaultRoot, baseRel, cfg)
	return dest, err
}

func resolveTrashDestinationPlan(vaultRoot, baseRel string, cfg trashOptions) (PathResolution, TrashDestinationPlan, error) {
	baseRel, err := CleanRelPath(baseRel)
	if err != nil {
		return PathResolution{}, TrashDestinationPlan{}, err
	}
	now := cfg.now().UTC()
	if now.IsZero() {
		return PathResolution{}, TrashDestinationPlan{}, Errorf(CodeInternal, "trash timestamp is zero")
	}
	plan := TrashDestinationPlan{
		BaseRelPath:  baseRel,
		Timestamp:    now,
		CollisionIDs: []string{},
	}

	candidateRel := baseRel
	for attempt := 0; attempt < 16; attempt++ {
		dest, err := PathForRel(vaultRoot, candidateRel)
		if err != nil {
			return PathResolution{}, TrashDestinationPlan{}, err
		}
		if dest.RelPath != candidateRel {
			return PathResolution{}, TrashDestinationPlan{}, Errorf(CodeForbidden, "trash destination must not resolve through a symlink")
		}
		exists, err := pathExists(dest.AbsPath)
		if err != nil {
			return PathResolution{}, TrashDestinationPlan{}, WrapError(CodeConflict, "stat trash destination", err)
		}
		if !exists {
			plan.DestinationRelPath = dest.RelPath
			return dest, plan, nil
		}

		shortID, err := cfg.shortID()
		if err != nil {
			return PathResolution{}, TrashDestinationPlan{}, WrapError(CodeInternal, "generate trash collision id", err)
		}
		shortID = strings.TrimSpace(shortID)
		if shortID == "" || strings.ContainsAny(shortID, `/\`) || containsControlRune(shortID) {
			return PathResolution{}, TrashDestinationPlan{}, Errorf(CodeInternal, "invalid trash collision id")
		}
		plan.CollisionIDs = append(plan.CollisionIDs, shortID)
		candidateRel = trashCollisionRel(baseRel, now.Format(trashTimestampFormat), shortID)
	}
	return PathResolution{}, TrashDestinationPlan{}, Errorf(CodeConflict, "could not choose a unique trash destination")
}

func trashCollisionRel(baseRel, timestamp, shortID string) string {
	dir := path.Dir(baseRel)
	name := path.Base(baseRel)
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	next := fmt.Sprintf("%s.deleted-%s-%s%s", stem, timestamp, shortID, ext)
	if dir == "." || dir == "" {
		return next
	}
	return path.Join(dir, next)
}

func defaultTrashShortID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func pathExists(p string) (bool, error) {
	_, err := os.Lstat(p)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func containsControlRune(s string) bool {
	for _, r := range s {
		if r == 0 || r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

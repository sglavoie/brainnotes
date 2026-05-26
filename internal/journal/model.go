package journal

import (
	"fmt"
	"strings"
	"time"
)

const (
	// ManifestFileName is the JSON file name used for each journal entry.
	ManifestFileName = "manifest.json"
	// SnapshotsDirName is the subdirectory that stores pre-change snapshots.
	SnapshotsDirName = "snapshots"
)

// FileKind describes a path state in a journal entry.
type FileKind string

const (
	KindMissing   FileKind = "missing"
	KindFile      FileKind = "file"
	KindDirectory FileKind = "directory"
	KindOther     FileKind = "other"
)

// FileState stores metadata for one path state.
type FileState struct {
	Kind FileKind `json:"kind"`
	// SHA256 is set when Kind == KindFile.
	SHA256 string `json:"sha256,omitempty"`
	Size   int64  `json:"size,omitempty"`
	Mode   uint32 `json:"mode,omitempty"`
	// Snapshot is a journal-entry-relative path to a pre-change file snapshot.
	// It is only set for FileRecord.Before when Kind == KindFile.
	Snapshot string `json:"snapshot,omitempty"`
}

// Label renders a stable state label for CLI preview output.
func (s FileState) Label() string {
	switch s.Kind {
	case KindFile:
		return "file"
	case KindMissing:
		return "missing"
	case KindDirectory:
		return "directory"
	case KindOther:
		return "other"
	default:
		return "unknown"
	}
}

func (s FileState) equalsContent(other FileState) bool {
	if s.Kind != other.Kind {
		return false
	}
	if s.Kind != KindFile {
		return true
	}
	return s.SHA256 == other.SHA256 && s.Size == other.Size
}

func (s FileState) validateBefore() error {
	if err := s.validateCommon(); err != nil {
		return err
	}
	if s.Kind == KindFile && strings.TrimSpace(s.Snapshot) == "" {
		return fmt.Errorf("missing snapshot for file state")
	}
	return nil
}

func (s FileState) validateAfter() error {
	if err := s.validateCommon(); err != nil {
		return err
	}
	return nil
}

func (s FileState) validateCommon() error {
	switch s.Kind {
	case KindMissing, KindFile, KindDirectory, KindOther:
	default:
		return fmt.Errorf("unsupported kind %q", s.Kind)
	}
	if s.Kind == KindFile {
		if strings.TrimSpace(s.SHA256) == "" {
			return fmt.Errorf("missing SHA256 for file state")
		}
		if s.Size < 0 {
			return fmt.Errorf("invalid file size %d", s.Size)
		}
	}
	return nil
}

// FileRecord tracks one touched path's pre- and post-command states.
type FileRecord struct {
	// Path is vault-relative with forward slashes.
	Path string `json:"path"`
	// Before is the state captured before the first mutation for this path.
	Before FileState `json:"before"`
	// After is the state at command completion.
	After FileState `json:"after"`
}

func (r FileRecord) validate() error {
	if strings.TrimSpace(r.Path) == "" {
		return fmt.Errorf("empty path")
	}
	if isEscapingRel(r.Path) {
		return fmt.Errorf("path %q escapes vault root", r.Path)
	}
	if err := r.Before.validateBefore(); err != nil {
		return fmt.Errorf("before: %w", err)
	}
	if err := r.After.validateAfter(); err != nil {
		return fmt.Errorf("after: %w", err)
	}
	return nil
}

// Entry is one operation journal record.
type Entry struct {
	ID        string       `json:"id"`
	Timestamp time.Time    `json:"timestamp"`
	Command   string       `json:"command"`
	Files     []FileRecord `json:"files"`
}

func (e *Entry) validate() error {
	if e == nil {
		return fmt.Errorf("nil entry")
	}
	if strings.TrimSpace(e.ID) == "" {
		return fmt.Errorf("empty entry id")
	}
	if strings.TrimSpace(e.Command) == "" {
		return fmt.Errorf("empty command")
	}
	if e.Timestamp.IsZero() {
		return fmt.Errorf("zero timestamp")
	}
	for i := range e.Files {
		if err := e.Files[i].validate(); err != nil {
			return fmt.Errorf("file[%d]: %w", i, err)
		}
	}
	return nil
}

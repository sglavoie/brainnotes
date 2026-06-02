package noteops

import (
	"path/filepath"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
)

// JournalRoot returns the vault-local undo journal directory.
func JournalRoot(vaultRoot string) string {
	return filepath.Join(vaultRoot, filepath.FromSlash(config.DefaultJournalDir))
}

// NewRecorder creates a journal recorder using the vault's default journal root.
func NewRecorder(vaultRoot, command string, opts ...journal.RecorderOption) (*journal.Recorder, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		command = "bn"
	}
	recorder, err := journal.NewRecorder(vaultRoot, JournalRoot(vaultRoot), command, opts...)
	if err != nil {
		return nil, WrapError(CodeJournal, "create journal entry", err)
	}
	return recorder, nil
}

// Journal lazily creates one recorder for a mutating operation.
type Journal struct {
	vaultRoot string
	command   string
	opts      []journal.RecorderOption
	recorder  *journal.Recorder
}

// NewJournal creates a lazy operation journal. It writes nothing until Capture.
func NewJournal(vaultRoot, command string, opts ...journal.RecorderOption) *Journal {
	return &Journal{
		vaultRoot: vaultRoot,
		command:   strings.TrimSpace(command),
		opts:      append([]journal.RecorderOption(nil), opts...),
	}
}

// Capture snapshots path's pre-mutation state, creating the recorder on demand.
func (j *Journal) Capture(path string) error {
	if j == nil {
		return nil
	}
	if j.recorder == nil {
		recorder, err := NewRecorder(j.vaultRoot, j.command, j.opts...)
		if err != nil {
			return err
		}
		j.recorder = recorder
	}
	if err := j.recorder.Capture(path); err != nil {
		return WrapError(CodeJournal, "capture journal state", err)
	}
	return nil
}

// Finalize commits the journal entry. It returns nil when nothing was captured.
func (j *Journal) Finalize() (*journal.Entry, error) {
	if j == nil || j.recorder == nil {
		return nil, nil
	}
	entry, err := j.recorder.Finalize()
	if err != nil {
		return nil, WrapError(CodeJournal, "finalize journal entry", err)
	}
	return entry, nil
}

package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
)

type mutationJournal struct {
	vaultRoot string
	command   string
	recorder  *journal.Recorder
}

func newMutationJournal(vaultRoot, command string) *mutationJournal {
	return &mutationJournal{
		vaultRoot: vaultRoot,
		command:   strings.TrimSpace(command),
	}
}

func (m *mutationJournal) Capture(path string) error {
	if m == nil {
		return nil
	}
	if m.recorder == nil {
		command := m.command
		if command == "" {
			command = "bn"
		}
		recorder, err := journal.NewRecorder(
			m.vaultRoot,
			journalRootForVault(m.vaultRoot),
			command,
		)
		if err != nil {
			return err
		}
		m.recorder = recorder
	}
	return m.recorder.Capture(path)
}

func (m *mutationJournal) Finalize() error {
	if m == nil || m.recorder == nil {
		return nil
	}
	_, err := m.recorder.Finalize()
	return err
}

func commandInvocation(cmd *cobra.Command, args []string) string {
	parts := make([]string, 0, 1+len(args))
	if cmd != nil {
		if path := strings.TrimSpace(cmd.CommandPath()); path != "" {
			parts = append(parts, path)
		}
	}
	parts = append(parts, args...)
	if len(parts) == 0 {
		return "bn"
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

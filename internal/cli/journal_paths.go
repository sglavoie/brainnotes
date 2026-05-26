package cli

import (
	"path/filepath"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func journalRootForVault(vaultRoot string) string {
	return filepath.Join(vaultRoot, filepath.FromSlash(config.DefaultJournalDir))
}

package cli

import (
	"github.com/sglavoie/brainnotes/internal/vault"
)

// resolveMoveTarget resolves the note `bn move` acts on. It goes through the
// shared resolver under its own name, so its errors stay wrapped (errors.Is
// still sees vault.ErrNoteNotFound) instead of being re-spelled from text.
func resolveMoveTarget(v *vault.Vault, noteArg string) (*vault.Note, error) {
	return resolveNoteTarget("move", v, noteArg)
}

package cli

import (
	"fmt"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func resolveMoveTarget(v *vault.Vault, noteArg string) (*vault.Note, error) {
	target, err := resolveRenameTarget(v, noteArg)
	if err != nil {
		return nil, fmt.Errorf("move: %s", strings.TrimPrefix(err.Error(), "rename: "))
	}
	return target, nil
}

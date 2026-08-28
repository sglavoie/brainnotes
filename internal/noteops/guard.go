package noteops

import (
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// guardMutation refuses an operation whose vault root or targets resolve onto
// storage the location policy does not allow bn to write.
//
// The policy is enforced in vault and journal, underneath every write, so this
// is not what makes an operation safe — it is what makes the refusal arrive
// early. A high-level operation creates folders and journal snapshots on its way
// to the write that would be refused, and those artifacts are exactly what the
// policy exists to keep off replicated storage. Calling this first is what turns
// "refused after it left a folder and an entry directory behind" into "refused
// before anything existed".
//
// The refusal is reported as forbidden rather than internal: nothing failed and
// nothing is broken. It maps to 403 on the web and keeps its own message,
// which names the protected ancestor and the target and nothing else about the
// file. There is no override.
func guardMutation(vaultRoot string, targets ...string) error {
	if err := vault.GuardMutation(vaultRoot, targets...); err != nil {
		return WrapError(CodeForbidden, err.Error(), err)
	}
	return nil
}

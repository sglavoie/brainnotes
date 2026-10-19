package vaultrecovery

import (
	"errors"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultimport"
)

// RecoverImportCommand is the one invocation that finishes interrupted import
// work while synchronization is quarantined for review. The re-enrollment
// refusal names it, so it is spelled once.
const RecoverImportCommand = "bn-vault-sync -recover-import"

// ImportRecovery is what RecoverImport did, and what it could not do.
type ImportRecovery struct {
	// Outcome is what resolving the interrupted application did, "none" when
	// there was nothing pending.
	Outcome string `json:"outcome"`
	// PackageID is the package that attempt was applying.
	PackageID string `json:"package_id,omitempty"`
	// Revised are packages whose owed conflict revision was made.
	Revised []string `json:"revised,omitempty"`
	// Abandoned are packages whose promised resolution was dropped.
	Abandoned []string `json:"abandoned,omitempty"`
	// Remaining is the unresolved work recovery cannot finish on its own — a
	// package held on a conflict needs a person's decision — empty when the
	// import record is ready for re-enrollment.
	Remaining string `json:"remaining,omitempty"`
}

// RecoverImport runs import recovery and nothing else: the pass every import,
// drain, and conflict resolution runs before it starts, which rolls an
// interrupted application back or forward and makes an owed revision.
//
// It exists for the restore-review quarantine. A companion pass runs no worker
// under the marker, so nothing would otherwise run that pass, and re-enrollment
// refuses while its record is pending — a dead end without this. It applies no
// package and reads no channel, so it cannot import anything the review has not
// seen; what it can change is the content an interrupted application left
// half-landed, which is why both inventories must be rebuilt afterwards. The
// caller holds the companion lock, as for every other review mode.
func RecoverImport(root string) (*ImportRecovery, error) {
	recovery, err := vaultimport.Recover(root, vaultimport.Options{Command: RecoverImportCommand})
	if recovery == nil {
		return nil, err
	}
	report := &ImportRecovery{
		Outcome:   string(recovery.Outcome),
		PackageID: recovery.PackageID,
		Revised:   recovery.Revised,
		Abandoned: recovery.Abandoned,
	}
	if err != nil {
		return report, err
	}
	if remaining := vaultimport.RequireResolved(root); remaining != nil {
		if !errors.Is(remaining, vaultimport.ErrUnresolvedImport) {
			return report, remaining
		}
		report.Remaining = remaining.Error()
	}
	return report, nil
}

package noteops

import (
	"context"

	"github.com/sglavoie/sglavoie_brainnotes/internal/backup"
)

// RestoreBackup extracts an archive over the vault under the same safety
// contract every other mutation runs under.
//
// It is the one mutation that cannot prove itself the ordinary way. A restore —
// and especially --wipe, which empties the vault before writing anything back —
// removes files that no journal entry ever described, because the entries
// describing them are themselves part of what is being replaced. Requiring
// journal proof here would mean the most destructive operation bn has is the one
// operation it can never snapshot.
//
// So this is the documented exception: an operator running `bn backup restore`
// is the authorization, and the resulting snapshot is taken through
// CommitRestore, which commits the working tree's own pending state rather than
// paths a manifest named. Everything else still applies — the location policy
// and the safety preflight both run before the archive is opened, so a blocked
// vault is refused while it still has its contents, and the snapshot failing
// afterward is reported as the partial success it is.
func RestoreBackup(vaultRoot, archivePath string, opts backup.RestoreOptions) (*backup.RestoreResult, error) {
	safety, err := SafetyFor(vaultRoot)
	if err != nil {
		return nil, err
	}
	if safety != nil {
		if err := safety.Preflight(context.Background()); err != nil {
			return nil, classifySafetyError("git safety preflight", err)
		}
	}

	// backup.Restore runs the location guard itself, before it extracts, which is
	// where it belongs: it is the layer that knows the vault has not been touched
	// yet.
	result, err := backup.Restore(vaultRoot, archivePath, opts)
	if err != nil {
		return nil, err
	}
	if safety == nil {
		return result, nil
	}
	if _, err := safety.CommitRestore(context.Background(), "backup-restore"); err != nil {
		return nil, &SnapshotError{
			Operation: "backup restore",
			Blocked:   vaultgitBlocks(err),
			Err:       err,
		}
	}
	return result, nil
}

package vaultexport

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/vaultgit"
)

// journalPrefix is the vault-relative directory every undo manifest lives under.
// Derived from the configured journal location rather than restated, so a move
// cannot leave this parser matching a directory that no longer holds entries and
// silently authorizing nothing.
var journalPrefix = config.DefaultJournalDir + "/"

// commitAuthority identifies journal entries carried by this commit, used to
// recognize imported operations and avoid sending them back to their source.
type commitAuthority struct {
	entryIDs []string
}

// readCommitAuthority reads journal identities from the commit itself. Malformed
// manifests provide no identity; ordinary changes, including deletions, can
// still be exported without a journal.
//
// An entry identity is taken from the *path* the manifest was committed at and
// the record inside it must agree, or it counts for nothing. The directory name is
// what internal/journal looks an entry up by, so it is the identity the rest of
// this vault means; the `id` field is bytes in a file. Trusting the field alone
// would let a manifest committed at one entry's name claim another's — and the
// identity an import is recognized by is exactly that name, so a commit could
// name an imported entry it does not carry and be skipped as an echo, silently
// dropping a local change that was never sent anywhere.
func readCommitAuthority(ctx context.Context, history *vaultgit.History, changes []vaultgit.FileChange) (*commitAuthority, error) {
	authority := &commitAuthority{}

	type manifestBlob struct{ oid, entryID string }
	manifests := make([]manifestBlob, 0, 4)
	oids := make([]string, 0, 4)
	for _, change := range changes {
		if change.Kind == vaultgit.ChangeDeleted {
			continue
		}
		entryID, isManifest := journalManifestEntry(change.Path)
		if !isManifest {
			continue
		}
		manifests = append(manifests, manifestBlob{oid: change.NewOID, entryID: entryID})
		oids = append(oids, change.NewOID)
	}
	blobs, err := history.Blobs(ctx, oids)
	if err != nil {
		return nil, err
	}
	for _, m := range manifests {
		var entry journal.Entry
		if err := json.Unmarshal(blobs[m.oid], &entry); err != nil {
			continue
		}
		if entry.ID != m.entryID {
			continue
		}
		authority.entryIDs = append(authority.entryIDs, entry.ID)
	}
	sort.Strings(authority.entryIDs)
	return authority, nil
}

// journalManifestEntry reports the journal entry a vault-relative path belongs
// to, and whether the path is that entry's manifest rather than one of its
// snapshots.
//
// Only the manifest counts. A snapshot under the same entry is the *content* a
// deletion removed, and mistaking one for a record would index an entry twice
// and, worse, make an entry look present in a commit that carried only its
// snapshots.
func journalManifestEntry(rel string) (string, bool) {
	if !strings.HasPrefix(rel, journalPrefix) {
		return "", false
	}
	parts := strings.Split(rel[len(journalPrefix):], "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != journal.ManifestFileName {
		return "", false
	}
	return parts[0], true
}

// regularFileMode reports whether a Git file mode is content this exporter may
// carry. A symlink, a gitlink, or anything else is not: a peer applying one
// would be asked to create something the portable protocol has no way to
// describe.
func regularFileMode(mode string) bool {
	return mode == vaultgit.ModeRegular || mode == vaultgit.ModeExecutable
}

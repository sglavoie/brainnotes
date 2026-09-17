package vaultexport

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/journal"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// journalPrefix is the vault-relative directory every undo manifest lives under.
// Derived from the configured journal location rather than restated, so a move
// cannot leave this parser matching a directory that no longer holds entries and
// silently authorizing nothing.
var journalPrefix = config.DefaultJournalDir + "/"

// commitAuthority is what one commit says about itself: which journal entries it
// carries, and which deletions those entries attest to.
//
// It is built from the commit's *own* changes, never from the journal on disk,
// and that is the same restriction internal/vaultgit applies for the same
// reason. A manifest already in the history proves a transition that already
// happened; letting it authorize a different commit's deletion would mean a note
// deleted, recreated, and deleted again by hand passes as intended. Here the
// binding is even tighter, because the commit is immutable: the proof and the
// deletion are the same object, and nothing can separate them afterwards.
type commitAuthority struct {
	// entryIDs are the journal entries this commit added a manifest for, sorted.
	entryIDs []string
	// deletions maps a vault-relative path to the content states some entry in
	// this commit attested to removing.
	deletions map[string][]removalProof
}

// removalProof is one journal record's claim that removing a path drops exactly
// this content.
type removalProof struct {
	entryID string
	sha256  string
	size    int64
}

// readCommitAuthority loads the journal manifests a commit added and indexes
// what they authorize.
//
// A manifest that does not decode contributes no proof and is not an error. The
// deletion it would have explained stays unexplained and halts the export, which
// is the conservative outcome; treating the manifest itself as corruption would
// halt a vault whose only problem is one unreadable record among many.
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
	authority := &commitAuthority{deletions: map[string][]removalProof{}}

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
		authority.add(&entry)
	}
	sort.Strings(authority.entryIDs)
	return authority, nil
}

// add indexes every delete-side record in one entry. A record counts only when
// it moves a real file to nothing: `Before` must be a file with a hash and
// `After` must be missing. A rename's delete side has exactly that shape, which
// is why renames need no special case here either.
func (a *commitAuthority) add(entry *journal.Entry) {
	if strings.TrimSpace(entry.ID) == "" {
		return
	}
	a.entryIDs = append(a.entryIDs, entry.ID)
	for _, record := range entry.Files {
		if record.Before.Kind != journal.KindFile || record.After.Kind != journal.KindMissing {
			continue
		}
		if record.Before.SHA256 == "" {
			continue
		}
		a.deletions[record.Path] = append(a.deletions[record.Path], removalProof{
			entryID: entry.ID,
			sha256:  record.Before.SHA256,
			size:    record.Before.Size,
		})
	}
}

// authorizes reports whether some entry in this commit attested to removing
// exactly this content from this path.
//
// Both the hash and the size must match, which is internal/vaultgit's rule
// restated rather than loosened: the size alone is worthless and the hash alone
// is enough in practice, but a mismatch in either says the record does not
// describe the file that is actually being removed.
func (a *commitAuthority) authorizes(path, sha256 string, size int64) bool {
	for _, proof := range a.deletions[path] {
		if proof.sha256 == sha256 && proof.size == size {
			return true
		}
	}
	return false
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

// checkDeletionAuthority refuses a commit that removes portable content without
// carrying the journal record proving the removal was intended.
//
// It judges the *parent's* bytes for each deleted path rather than trusting the
// record to be about the right file: the journal states a content hash and a
// size, and both must describe what this deletion actually drops. blobs is the
// commit's change set already read, so this costs no additional process.
//
// authorized is the caller's explicit override, naming commits whose deletions a
// person has approved. It exists for exactly one shape of commit:
// vaultgit.CommitRestore deliberately commits deletions on a caller's authority
// rather than a journal's, so a restore leaves a commit no record can ever
// explain. Mirroring that exception here keeps the authorization the caller's
// and keeps it unpersisted — a person approves one commit, once.
func checkDeletionAuthority(
	commit string,
	portable []vaultgit.FileChange,
	authority *commitAuthority,
	blobs map[string][]byte,
	authorized map[string]struct{},
) error {
	if _, approved := authorized[commit]; approved {
		return nil
	}
	var unexplained []string
	for _, change := range portable {
		if change.Kind != vaultgit.ChangeDeleted {
			continue
		}
		if !regularFileMode(change.OldMode) {
			// Not a file whose bytes a journal could have snapshotted, so no proof
			// could ever match. It is refused as an unsupported entry rather than an
			// unauthorized deletion, which is the accurate complaint.
			return fmt.Errorf("%w: %s removed %q, which had mode %s", ErrUnsupportedEntry, commit, change.Path, change.OldMode)
		}
		content, read := blobs[change.OldOID]
		if !read {
			return fmt.Errorf("%w: %s removed %q and its prior content could not be read", ErrCorruptState, commit, change.Path)
		}
		if !authority.authorizes(change.Path, vaultsync.HashBytes(content), int64(len(content))) {
			unexplained = append(unexplained, change.Path)
		}
	}
	if len(unexplained) > 0 {
		sort.Strings(unexplained)
		return fmt.Errorf("%w: %s removes %s with no journal record in the same commit",
			ErrUnauthorizedDeletion, commit, strings.Join(unexplained, ", "))
	}
	return nil
}

// regularFileMode reports whether a Git file mode is content this exporter may
// carry. A symlink, a gitlink, or anything else is not: a peer applying one
// would be asked to create something the portable protocol has no way to
// describe.
func regularFileMode(mode string) bool {
	return mode == vaultgit.ModeRegular || mode == vaultgit.ModeExecutable
}

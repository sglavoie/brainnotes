package web

import (
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// vaultIdentityPrefix values keep the two identity spaces disjoint.
//
// They are prefixes rather than a flag beside the value because the identity is
// an opaque token to every consumer: a browser keying its local drafts on it
// must never be able to read an enrolled identity and a path digest as the same
// string, and a prefix makes that true without the consumer knowing there are
// two kinds.
const (
	enrolledVaultIdentityPrefix = "sync:"
	locatedVaultIdentityPrefix  = "root:"
)

// vaultIdentity is the stable, opaque name of the vault this server serves.
//
// It exists for one reason: a browser keeps unsaved text for a note in local
// storage, and `http://127.0.0.1:8765` is one storage origin that a person
// points at several vaults over the life of a machine. Without an identity in
// the key, a draft written against one vault would be offered back over a note
// of the same name in another — the one way this feature could put somebody's
// words into a file they were never written for.
//
// The identity has to survive a server restart, because surviving one is most
// of what a draft is for, so it is derived rather than minted per process. An
// enrolled vault already has exactly the right name: vaultsync.Enrollment's
// VaultID is shared by every replica of one vault and never changes (see
// internal/vaultsync/enrollment.go). A vault that synchronizes with nothing has
// none, so the fallback is a digest of the resolved root — stable for as long as
// the vault stays where it is, and distinct between two vaults on one machine,
// which is the case that matters.
//
// Every failure falls back rather than propagating. The consequence of a wrong
// answer here is that drafts written under the old identity stop being offered,
// which loses nobody's text — nothing is ever submitted from a draft without a
// person choosing it — while a page that refused to render over an unreadable
// enrollment record would take the editor away entirely.
func (s *Server) vaultIdentity() string {
	if record, err := vaultsync.Load(s.vault.Root); err == nil && record.VaultID != "" {
		return enrolledVaultIdentityPrefix + record.VaultID
	}
	return locatedVaultIdentityPrefix + vault.HashBytes([]byte(s.vault.Root))
}

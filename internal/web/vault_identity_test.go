package web

import (
	"os"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// TestVaultIdentity_LocatedVaultIsStableAndDistinct covers the fallback, which is
// what every vault that synchronizes with nothing gets.
//
// Both halves are the point. Stability across calls is what makes a draft
// survive the server restart it exists for; distinctness between two roots is
// the case the identity was introduced for at all, since `127.0.0.1:8765` is one
// browser storage origin that a person points at several vaults.
func TestVaultIdentity_LocatedVaultIsStableAndDistinct(t *testing.T) {
	_, first, _ := newSourceTestServer(t, Options{})
	_, second, _ := newSourceTestServer(t, Options{})

	identity := first.vaultIdentity()
	if !strings.HasPrefix(identity, locatedVaultIdentityPrefix) {
		t.Fatalf("vaultIdentity() = %q, want the %q space for an unenrolled vault", identity, locatedVaultIdentityPrefix)
	}
	if again := first.vaultIdentity(); again != identity {
		t.Fatalf("vaultIdentity() moved between calls: %q then %q", identity, again)
	}
	if other := second.vaultIdentity(); other == identity {
		t.Fatalf("two vaults share the identity %q", identity)
	}
}

// TestVaultIdentity_EnrolledVaultUsesTheSharedVaultID pins the branch that makes
// the identity mean the same thing on both replicas of one vault: the enrollment
// record's VaultID is shared by every replica and never changes, so a draft
// written on the laptop is keyed the same way on the Pi.
func TestVaultIdentity_EnrolledVaultUsesTheSharedVaultID(t *testing.T) {
	_, srv, root := newSourceTestServer(t, Options{})

	before := srv.vaultIdentity()
	record, err := vaultsync.Establish(root, vaultsync.Options{Label: "identity-test"})
	if err != nil {
		t.Fatalf("vaultsync.Establish: %v", err)
	}

	identity := srv.vaultIdentity()
	if identity != enrolledVaultIdentityPrefix+record.VaultID {
		t.Fatalf("vaultIdentity() = %q, want %q", identity, enrolledVaultIdentityPrefix+record.VaultID)
	}
	// The two spaces are disjoint by construction, and this is the assertion that
	// keeps them so: an identity is an opaque token to the browser, and a vault
	// that reads as its own former self would hand a draft from before enrollment
	// to a note that is now a replica's.
	if identity == before {
		t.Fatalf("enrolling a vault did not change its identity: %q", identity)
	}
}

// TestVaultIdentity_UnreadableEnrollmentFallsBack is the failure arm, and it is
// the reason vaultIdentity swallows its error. A page that refused to render
// over a damaged enrollment record would take the editor away; falling back
// costs the drafts written under the enrolled identity, which are offered, never
// submitted.
func TestVaultIdentity_UnreadableEnrollmentFallsBack(t *testing.T) {
	_, srv, root := newSourceTestServer(t, Options{})

	if _, err := vaultsync.Establish(root, vaultsync.Options{}); err != nil {
		t.Fatalf("vaultsync.Establish: %v", err)
	}
	if err := os.WriteFile(vaultsync.EnrollmentPath(root), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("damage the enrollment record: %v", err)
	}

	identity := srv.vaultIdentity()
	if !strings.HasPrefix(identity, locatedVaultIdentityPrefix) {
		t.Fatalf("vaultIdentity() = %q, want the %q space for an unreadable record", identity, locatedVaultIdentityPrefix)
	}
}

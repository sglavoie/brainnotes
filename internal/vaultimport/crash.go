package vaultimport

// An import is a sequence of durable state transitions that cannot be made one,
// and the only honest way to find out what an interruption leaves behind is to
// stop a real process at each of them. The seam is the one internal/vault already
// uses for the same question: a hook that is nil in production, set by a
// subprocess helper test that calls os.Exit from it — which skips every deferred
// cleanup exactly the way a kill does, so the parent inspects the same disk a
// restart would see.
//
// The points below are named for what is *durable* when they fire, not for the
// line they sit on. That is what the recovery pass reasons about, and a point
// named after a function call would stop meaning anything the moment the call
// moved.

// importCrashHook is nil in production.
var importCrashHook func(point string)

const (
	// crashDeclared fires once the attempt record is durable and before the
	// transaction is committed. Nothing of this import is on the disk yet: no
	// content, no journal entry, no provenance.
	crashDeclared = "declared"
	// crashApplied fires once the package's content is durably on this disk and
	// its pre-change state is captured, and before anything says so. The journal
	// entry exists and has no manifest, which is what makes this the last point
	// the import is still undoable rather than undone.
	crashApplied = "applied"
	// crashProvenance fires once the anti-echo ledger knows the entry came from a
	// peer, and before the entry is finalized. Still no manifest, so this is the
	// application boundary again with one more durable fact behind it — and the
	// fact is about an entry a rollback is about to remove.
	crashProvenance = "provenance"
	// crashFinalized fires once the undo manifest and the content-history commit
	// are both durable, and before the peer is told anything. The change has
	// stopped being something this vault is doing and become something it did.
	crashFinalized = "finalized"
	// crashRestored fires inside a recovery pass, once a rollback has put the
	// content back and said so, and before the journal entry and the attempt that
	// authorized it are removed. It is the one point that is not on the import's
	// own path: a recovery pass is itself a sequence of durable steps, and it is
	// interrupted by exactly the same things.
	crashRestored = "restored"
	// crashAcknowledged fires once the receipt is durably published in the outgoing
	// channel, and before the ledger remembers the application. The peer may act on
	// that receipt at any moment after this point, which is what makes the receipt
	// rather than the ledger the authoritative statement here.
	crashAcknowledged = "acknowledged"
	// crashResolved fires on the resolution path only, once the peer's package is
	// durably applied and acknowledged and before the chosen revision that answers
	// it exists. It is the widest window this vault is ever knowingly in a state
	// nobody asked for — the peer's version is on the disk and the person chose
	// something else — which is why the revision record is durable before the
	// application starts rather than after it.
	crashResolved = "resolved"
	// crashRevised fires on the resolution path only, once the revision's content
	// is durably committed and before the undo manifest that explains it exists.
	// It is the resolution's own crashApplied, and it is the point the roll-forward
	// has the most to take back: internal/vault rolls a prepared transaction
	// forward rather than back, so what is on the disk here can still be arriving
	// after the process is gone.
	crashRevised = "revised"
)

func importCrash(point string) {
	if importCrashHook != nil {
		importCrashHook(point)
	}
}

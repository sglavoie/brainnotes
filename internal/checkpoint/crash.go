package checkpoint

// A checkpoint is a short sequence of durable transitions, and the only honest
// way to find out what an interruption leaves behind is to stop a real process
// at each of them. The seam is the one internal/vault and internal/vaultimport
// already use for the same question: a hook that is nil in production, set by a
// subprocess helper test that calls os.Exit from it — which skips every deferred
// cleanup exactly the way a kill does, so the parent inspects the same disk a
// restart would see.
//
// The points below are named for what is durable when they fire. Every one of
// them must leave a vault that writers can use: a checkpoint's whole failure
// mode is refusing writes it will never stop refusing.

// checkpointCrashHook is nil in production.
var checkpointCrashHook func(point string)

const (
	// crashAnnounced fires once the record is durable and before the exclusive
	// acquisition starts. The barrier is not held, so writers are only waiting
	// because the announcement told them to — this is the point that proves a dead
	// client's announcement does not outlive it.
	crashAnnounced = "announced"
	// crashDrained fires once the barrier is held exclusively and before the
	// settle pass. The record still says draining, which is the one state where
	// what the record claims and what the locks do disagree.
	crashDrained = "drained"
	// crashHeld fires once the settle pass has run and the record says held. It is
	// the widest window: everything a checkpoint promises is true, and the process
	// that promised it is gone.
	crashHeld = "held"
	// crashReleased fires once the barrier and the record are both gone and before
	// the claim lock is dropped. It exists to prove the release order is safe from
	// either end — nothing here is left for anybody to clean up.
	crashReleased = "released"
)

func checkpointCrash(point string) {
	if checkpointCrashHook != nil {
		checkpointCrashHook(point)
	}
}

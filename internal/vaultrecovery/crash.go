package vaultrecovery

// Accepting a review is a sequence of durable steps that cannot be made one, so
// the only honest test of a retry is to stop a real process between two of
// them. The seam is the one internal/noteops and internal/vaultimport use: a
// hook that is nil in production, set by a subprocess helper test that calls
// os.Exit from it, which skips every deferred release exactly as a kill does.
//
// The points are named for what is durable when they fire.

// applyCrashHook is nil in production.
var applyCrashHook func(point string)

const (
	// crashArchivedPrefix, followed by a source's base name, fires once that
	// source has been moved into the plan's archive and before the next one is.
	crashArchivedPrefix = "archived/"
	// crashEnrolled fires once the reviewed enrollment is installed and before
	// the marker leaves phase "applying".
	crashEnrolled = "enrolled"
	// crashRebaselined fires once the producer ledger is rebaselined and before
	// the consumer ledger is reset.
	crashRebaselined = "rebaselined"
	// crashCompleted fires once the completion record is durable and before the
	// review marker is removed.
	crashCompleted = "completed"
)

func applyCrash(point string) {
	if applyCrashHook != nil {
		applyCrashHook(point)
	}
}

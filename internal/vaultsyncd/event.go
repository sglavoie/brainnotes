package vaultsyncd

import "time"

// EventKind names what the companion observed. It is a closed vocabulary so a
// log consumer can switch on it, and it is deliberately about *work* rather than
// about states: [State] answers "where is this vault", an event answers "what
// just happened", and conflating the two gives a log that repeats the same
// sentence every poll.
type EventKind string

const (
	// EventStarted and EventStopped bracket the loop.
	EventStarted EventKind = "started"
	EventStopped EventKind = "stopped"
	// EventUnenrolled reports a vault that synchronizes with nothing. The loop
	// keeps running, because enrolling is a thing that can happen underneath it.
	EventUnenrolled EventKind = "unenrolled"
	// EventImported reports a drain that answered at least one package.
	EventImported EventKind = "imported"
	// EventExported reports an export pass that published at least one package.
	EventExported EventKind = "exported"
	// EventRetired reports packages or receipts removed from the outgoing
	// channel.
	EventRetired EventKind = "retired"
	// EventDeferred reports a worker that did not run: either its backoff has not
	// elapsed, or a condition means there is nothing for it to do.
	EventDeferred EventKind = "deferred"
	// EventFailed reports a worker that failed. The failure streak and the next
	// attempt are on the event, because "export failed" and "export has failed
	// eleven times and will next try in fifteen minutes" call for different
	// reactions.
	EventFailed EventKind = "failed"
	// EventHeld reports packages the drain kept, conflicts among them. It is
	// separate from EventImported because a drain can do both in one pass and
	// they are read by different people.
	EventHeld EventKind = "held"
	// EventStatus reports the headline state at the end of a pass, and only when
	// it differs from the last one reported. A state that has not changed is not
	// news, and a line every poll is a log nobody reads.
	EventStatus EventKind = "status"
	// EventProblem reports something a status read could not determine. It is
	// distinct from EventFailed: no work failed, a fact is simply unavailable.
	EventProblem EventKind = "problem"
	// EventRejected reports a receipt that arrived for one of this device's
	// packages and did not verify. Nothing retries it and waiting will not fix
	// it, which is why it is its own kind rather than a detail on EventRetired.
	EventRejected EventKind = "rejected"
)

// Event is one thing the companion observed.
//
// Every field is a count, a timestamp, a package identity, a vault-relative
// path, or a sanitized diagnostic. Note *contents* never reach this struct; note
// paths do, in Paths, and only for a conflict, where naming the file is the
// whole purpose.
type Event struct {
	// Kind names the observation.
	Kind EventKind `json:"kind"`
	// At is when it happened, in UTC.
	At time.Time `json:"at"`
	// Vault is the vault root this companion is running against.
	Vault string `json:"vault"`
	// Worker is the unit the event is about, empty for loop-level events.
	Worker WorkerName `json:"worker,omitempty"`
	// Message is a one-clause human explanation with no note contents in it.
	Message string `json:"message"`
	// State is the headline state, on EventStatus.
	State State `json:"state,omitempty"`
	// Packages is how many packages the event is about: answered by a drain,
	// published by an export, or removed by a retirement.
	Packages int `json:"packages,omitempty"`
	// Changed is how many of an import's packages actually wrote something.
	Changed int `json:"changed,omitempty"`
	// Held and Conflicts are packages kept in the incoming channel, and how many
	// of those are waiting for a person.
	Held      int `json:"held,omitempty"`
	Conflicts int `json:"conflicts,omitempty"`
	// Awaiting is how many published packages have no verified receipt.
	Awaiting int `json:"awaiting,omitempty"`
	// Paths are vault-relative paths a person has to look at — conflicts, and
	// nothing else.
	Paths []string `json:"paths,omitempty"`
	// PackageID names one package, on the events that are about exactly one.
	PackageID string `json:"package_id,omitempty"`
	// Failures is the consecutive failure count, and NextAttemptAfter is when the
	// worker may run again.
	Failures         int       `json:"failures,omitempty"`
	NextAttemptAfter time.Time `json:"next_attempt_after"`
	// Subject names what a problem is about, in the words an operator would use.
	Subject string `json:"subject,omitempty"`
	// Detail is a sanitized single-line diagnostic.
	Detail string `json:"detail,omitempty"`
}

// eventPathCap bounds how many paths one event names, matching
// internal/vaultgit's WatchEvent. Conflicts carries the exact count, so a
// truncated list never misrepresents the size of a problem.
const eventPathCap = 32

func capPaths(paths []string) []string {
	if len(paths) <= eventPathCap {
		return paths
	}
	return paths[:eventPathCap]
}

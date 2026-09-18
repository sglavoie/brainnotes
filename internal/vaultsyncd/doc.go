// Package vaultsyncd is the companion that keeps two enrolled replicas moving,
// and the status model that says where anything has got to.
//
// Everything a synchronization needs already exists elsewhere: internal/vaultgit
// records the content history, internal/vaultexport turns commits into packages,
// internal/vaultimport applies the peer's, and internal/vaultsync owns the wire
// protocol and the channel layout. What was missing is the thing that runs them
// on a schedule without a person typing a command, survives being killed, and
// can be asked what is going on. That is all this package is.
//
// # Three units, one goroutine, in one order
//
// A pass runs import, then export, then retire, serialized, never concurrently.
// That is a lock-ordering decision rather than a stylistic one and it is the
// invariant most worth protecting here.
//
// internal/vaultimport holds its own import guard for a whole drain, and an
// application inside that drain calls vaultexport.RecordImport, which takes the
// export state lock. internal/vaultexport holds that same export state lock for
// a whole pass. Two goroutines running a drain and an export at once would
// therefore contend for the export state lock from opposite directions, and the
// loser does not merely wait — internal/vault's path locks have a bounded
// acquisition budget, so it fails, and a drain that fails at RecordImport fails
// *after* the peer's change is already committed. Running the two units in one
// goroutine in a fixed order means this process never holds two subsystem locks
// at once, which is the same rule internal/vault's lock.go states for path sets:
// a caller holding a lock must not nest another.
//
// Import runs before export because a commit produced by an application is
// already in the export ledger when the exporter reaches it, so the pass that
// imports a change cannot publish it straight back.
//
// Retire runs last and takes no subsystem lock at all. See retire for why that
// is safe rather than merely convenient.
//
// # A receipt is the only evidence of application
//
// The status model refuses to conflate two facts that look alike from here. A
// package this device published and copied into the channel has been *delivered*
// at most; only a receipt in the peer's channel says the peer applied it durably
// against a named local recovery entry. So [StateTransportPending] covers
// everything from "not copied yet" to "copied and being applied right now", and
// nothing but a verified receipt retires a package or advances anything.
//
// The same distinction holds in the other direction: a package sitting in this
// vault's incoming channel is delivered, and [StatePeerApplication] says it is
// waiting for this device to apply it. Packages counted there are not changes
// this vault holds.
//
// # A converged package is never acknowledged, and therefore never retired
//
// internal/vaultimport mints no receipt for a package whose every path this
// vault already held: an acknowledgment promises a local recovery entry that can
// undo the application, and an application that wrote nothing produced none. It
// records the package as applied-and-converged and expects the sender to keep
// its copy and redeliver.
//
// That is a deliberate decision there, and it has a consequence here that this
// package can report but not fix: such a package stays in the sender's outgoing
// channel forever, counted in [TransportStatus.AwaitingReceipt], and a pair that
// is quiet for long enough afterwards reaches [StateDisconnected] on a transport
// that is working. The status is not wrong about the facts it states — no
// receipt has arrived, and nothing has been heard — but the headline names the
// wrong cause. Fixing it needs the protocol to carry a "this changed nothing"
// acknowledgment, which is internal/vaultsync's decision and not this package's.
// It is stated here rather than left to be discovered.
//
// # Nothing here hides a failure
//
// Every worker's last failure is written to a durable record beside the export
// cursor, with a failure streak and the backoff it implies, and the status read
// reports them. A vault whose exports have been failing for an hour says so
// rather than looking idle, and so does one whose content history is blocked —
// which is the failure most likely to be silent, because a blocked vault takes
// no snapshots and therefore produces nothing to export and no error to see.
//
// A record the status read cannot parse is a [Problem] and therefore needs a
// person, even though a companion discards the same record and carries on: an
// empty worker list is indistinguishable from a vault whose workers have never
// failed, and reporting one for the other is the silence this section refuses.
// The streak is cleared by a success and by a worker that is due and finds
// nothing to do, and by nothing else: a worker that stops being asked must not
// keep reporting a failure nothing is retrying. "Finds nothing to do" is proof
// rather than an absence of news — the skip decided before the worker runs, or
// the worker's own refusal as unenrolled, unmanaged or unborn — so a vault
// somebody else is draining keeps its streak, having never got as far as
// looking.
//
// Clearing it costs no visibility because the halts it could otherwise hide are
// reported on their own. A producer refusing on a cursor from an earlier
// enrollment epoch, on one following another branch, or on a branch reset behind
// it has nothing left to publish and so reads exactly like an idle vault — the
// epoch case most of all, since internal/vaultexport resolves an interrupted
// publication before it checks the epoch and resolving one advances the cursor
// durably. The status read names all three as problems, and a status carrying a
// problem runs the export anyway, so each becomes a recorded worker failure with
// its evidence rather than a silence.
//
// # What restarts preserve
//
// Progress is durable in the packages this process does not own: the export
// cursor, the import ledger, the conflict records, and the published packages
// and receipts themselves. This package's own record carries only scheduling
// state — when each worker last ran, how many times in a row it has failed, and
// when it may be tried again — so losing it costs one early retry and never a
// change. That is why it is discarded rather than refused when it cannot be
// read; see [LoadRecord].
//
// # Reading status never blocks
//
// [Read] takes no lock of any kind. It opens the content history through
// internal/vaultgit's read-only door, reads the durable records with the
// read-only loaders their packages expose, and lists the two channels and their
// receipts. A status command must be answerable while a drain is running — most
// of all then — so anything here that needed a lock would be the wrong design.
//
// # Two limits of what can be seen from here, stated rather than discovered
//
// [StateLocalSaved] is reported from internal/vaultgit's watch record, whose
// only writer is the autosave watcher inside bn-vault-guard. On a machine
// running this companion *without* that one — a supported arrangement, since the
// two are deliberately separate processes — edits that are on disk and not yet
// in the content history are invisible here, and such a vault reads as idle
// rather than as local-saved. The alternative is scanning the working tree,
// which is the autosave watcher's job and would race it; the honest answer is
// that this state is observed, not inferred, and needs the observer installed.
//
// A pass is bounded in the direction that can run away on its own. The export
// takes [Options.MaxPackages], because a first baseline is as large as the vault.
// The import drains the whole incoming channel to a fixed point — that is
// vaultimport.Drain's contract, and a cap there would mean deciding which of the
// peer's changes to leave unapplied — so a channel holding thousands of
// delivered packages makes one pass long. It is still crash-consistent, and a
// cancellation is seen between units rather than inside one.
package vaultsyncd

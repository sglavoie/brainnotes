# Restore review and peer re-enrollment

A complete checkpoint contains synchronization state as well as notes: device
identity, enrollment epoch, producer sequence, import high-water marks,
packages, and receipts. Restoring those bytes is therefore a causal rollback.
The content can be valid while the acknowledgments and sequence are stale.

Every enrolled `bn restore` writes `.bnotes/sync/restore-review.json` after the
restored content and Git history are durable and before the restore intent is
cleared. Its marker is bound to the durable restore intent, so retrying one
interrupted restore is idempotent while restoring a checkpoint that contains an
older marker always remints the proposed identity, whatever phase that marker
records: a marker captured mid-acceptance belongs to a restore operation that no
longer exists, and obeying it would make every retry of the restore fail on
the same stuck bytes. Deployment restores run
`bn-vault-sync -restore-prepare` immediately after staging the complete
checkpoint; each explicit invocation mints a fresh restore operation and
invalidates any inventory printed by an earlier invocation. The marker is fail-closed:
`vaultsyncd.Read` reports `restore-review`, `NeedsPerson` is true, and a
companion pass runs no worker.

`bn-vault-sync -inventory` hashes every portable regular file and reports the
current enrollment, producer sequence, per-origin applied high-water marks, and
the effective post-review device identity. A restored device always proposes a
fresh identity. The survivor keeps its identity. `-review PEER.json` compares
the two inventories and writes a digest-bound plan containing path differences,
sequence regressions, and the next shared epoch.

`-inventory`, `-review`, and `-accept-review` all refuse while the import
record holds unresolved work — an interrupted application, a package held on a
conflict, or a conflict decision still owed its revision
(`vaultimport.RequireResolved`). Acceptance archives that record and starts an
empty one, which would strand the only copy of that work. The refusal comes
before the inventory is printed, so the operator learns it before carrying the
inventory to the peer, and before a survivor's acceptance writes its quarantine
marker. Resolve held conflicts and let import recovery finish (any import, drain
or conflict resolution runs it first), then rebuild both inventories.

`-accept-review PLAN.json` refuses while any portable path differs. The operator
must reconcile edits and deletions offline, regenerate both inventories, and
review again. This conservative rule is what prevents a restored baseline from
resurrecting a survivor's deletion or replacing a newer edit.

Acceptance is restartable under the vault checkpoint and companion locks. It
archives the old channels, export/import ledgers, and backup-coverage record
under `.bnotes/sync/restore-archive/<plan>/`; installs the reviewed peer set and
epoch; writes fresh causal ledgers; publishes a plan-named completion record;
then removes the review marker last. The completion record makes retry after a
lost success response a no-op. Each step is re-derived from the marker's phase
and the disk, so a kill between any two of them resumes: an archive interrupted
part-way moves only what is still active, and a source that reappeared beside
its archived copy — the transport refilling the incoming channel while the
acceptance was down — is left where it is rather than refused, since nothing in
the flow rewrites a source after archiving it. The consumer reset takes the
import guard like every other writer of `import.json`, and refuses a record that
again holds unresolved work. Apply uses an ordinary checkpoint, so a pending
content restore remains a hard refusal, and every resume re-hashes portable
content against the reviewed inventory. A surviving identity retains the larger
of its committed and pending producer sequences, spending any ambiguous
reservation before clearing it. A restored identity starts a new stream at
zero, which is safe only because that identity has never appeared in a package.
With both devices restored, both identities rotate. Old packages then fail by
unknown origin or stale epoch, and old receipts cannot be found in the active
channel or import ledger.

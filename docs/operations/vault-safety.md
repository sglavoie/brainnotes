# Vault Safety

How the vault protects itself from loss, what it does automatically, and what
you do by hand on the rare occasion it stops and asks.

There is one moving part you did not start: `bn-vault-guard`, a companion
process that watches the vault's working tree and keeps its Git history current.
It is installed alongside `bn` and supervised by a LaunchAgent. It is not a `bn`
subcommand and it does not appear in `bn --help`, because there is nothing about
it to operate on a good day.

### Installing a new build

`just install` replaces `bn`, `bn-vault-guard` and `bn-vault-sync` together, but
the LaunchAgents keep running the companion binaries they started until they are
restarted; the recipe ends by printing the two `launchctl kickstart -k` lines.
Every binary reports the same build identifier (the release tag for a prebuilt
binary, otherwise the Git revision it was built from, `-dirty` for an
uncommitted tree): `bn --version`,
`bn-vault-guard -version` and `bn-vault-sync -version`. The running companions
write theirs to `guard.json` (beside `blocked.json`) and `.bnotes/sync/companion.json`,
log it on their `started` record, and `bn doctor` warns on stderr when either
differs from the running `bn`. `bn-vault-sync -check` reports the same skew as
`build_mismatch` without changing its exit status.

The guard's exit statuses are chosen for the LaunchAgent, which restarts it on
any non-zero exit: `0` when it stops on a signal or the vault has no content
history, `1` when it cannot start or keep watching, and `2` for bad flags or when
another guard already holds the vault. The duplicate is non-zero on purpose, as it
is for `bn-vault-sync`: the copy that lost the lock is usually the LaunchAgent's
own while a guard runs by hand, and it must keep retrying so the vault is guarded
again once that copy stops.

## What is protected

The vault's tracked scope is fixed and is not configurable:

- Every note in the vault, at its real path.
- The durable sidecars `.bnotes/brief.json`, `.bnotes/cockpit.json`, and
  `.bnotes/recall.json`.
- The undo journal under `.bnotes/journal/`, which preserves pre-mutation bytes
  for `bn undo`.
- `brainnotes.toml` and `.gitignore`.

Deliberately outside it: the note cache under `.bnotes/index/`, the lock files
under `.bnotes/locks/`, and the transaction records under `.bnotes/txn/`. Those
are derived state and process bookkeeping. Committing them would put a commit on
every read-shaped operation and would record nothing a person could use.

## Immediate versus scheduled snapshots

Two different things write to the vault, and they are treated differently
because they know different amounts about their own intent.

**A BrainNotes mutation snapshots immediately.** Every write through `bn`, the
web UI, `bn undo`, and every durable sidecar update commits its own paths — and
only its own paths — before the command returns. It knows exactly what it
changed, so there is nothing to wait for. If that commit cannot be made, the
command fails loudly, says the bytes are already on disk, and refuses the next
mutation. It never claims a rollback it did not perform.

**An external editor is scheduled.** A change made by anything else — Obsidian,
`vim`, a sync client, a script — is noticed by the guard on its five-second poll
and then waits for two independent conditions, one about the edit and one about
the history:

| Policy | Value | Configurable |
| --- | --- | --- |
| Quiet window | 30 seconds after the last observed change | no |
| Readiness deadline | 2 minutes after the first observed change | no |
| Autosave floor | `[autosave] min_interval`, default 5 minutes | **yes** |
| Poll interval | 5 seconds | no |
| Push interval | at startup, and at least every 5 minutes | no |

The first two decide when the *edit* is ready to be committed; the third decides
when the *history* is willing to take another commit of the guard's own making:

```
ready_at = min(last_change + 30s, first_change + 2m)
floor_at = last_external_autosave + min_interval
due_at   = max(ready_at, floor_at)
```

So the quiet window still collapses a burst of saves into one commit, and two
minutes still bounds how long a burst that never goes quiet can stay
*unready* — but readiness is not the commit. The floor can only ever push the
due time later, never earlier, so a vault under continuous editing no longer
acquires one commit every two minutes forever; it acquires one every
`min_interval`.

Two worked examples, both at the default five-minute floor:

- **You edit a note in a vault the guard last autosaved an hour ago.** The floor
  has nothing recent to be measured from, so readiness alone decides: you stop
  typing, the quiet window expires, and the commit lands about half a minute
  later.
- **You keep editing.** Your next save is ready 30 seconds after it, but the
  autosave above was under a minute ago, so `due_at` is `that commit + 5m` and
  the snapshot lands roughly four minutes later than readiness alone would have
  put it. The bytes are on disk the whole time; what waits is the commit.

Nothing here is a knob on how much of the vault is recoverable. At *any* floor
value, including `off`, an explicit BrainNotes mutation still commits before the
command returns, and the history is still pushed on the same schedule. The floor governs one
thing: how often the guard adds a commit **of its own**. See
[Configuration](../configuration.md#the-autosave-floor) for the accepted values.

The clocks are persisted in `<git-dir>/brainnotes-safety/watch.json`, so killing
the guard — or logging out, or a crash — cannot postpone a pending snapshot: both
the readiness deadline and the floor are measured from what the guard recorded,
not from the last time it started. That record also publishes the two derived
fields the health check reads: `autosave_min_interval`, the policy actually in
force, and `next_commit_due_at`, the `due_at` above, absent when no burst is open
or when autosave is off.

The floor's anchor is bracketed on purpose. Before an autosave can produce a
commit the guard records `external_commit_started_at`. After Git advances the
branch and the guard observes that ref landing, the recovery intent records that
later bound; `last_commit_at` then publishes it to the watch state and both
temporary markers are cleared. If the final watch-state write fails or the guard
dies, the committed intent remains. Recovery proves its commit is reachable,
publishes the observed landing bound, and only then allows another external
snapshot. If the process died before it could record that bound, recovery uses
the later instant at which it proves the ref landed, which defers conservatively.

That distinction matters on a large or slow vault: staging, writing a tree, and
updating a ref take real time. The configured interval begins only after the
first ref was observed to land; none of the staging time is deducted. The
autosave commit retains the earlier write-ahead timestamp in its subject and Git
metadata, but scheduling never treats that authored timestamp as evidence of
when the ref became visible. If the initial marker cannot be written at all, no
autosave is attempted: the edits stay in the working tree, the burst stays open,
and the next poll retries.

### Changing the floor while the guard is running

The guard re-reads `brainnotes.toml` before every scan, so
`bn config set autosave.min_interval` and a hand edit both take effect within one
poll. The LaunchAgent never needs restarting for this.

A file that does not decode is refused rather than obeyed. The guard logs one
warning — deduplicated by the file's own bytes, so a broken file does not fill
the log — and **keeps enforcing the last valid policy** until the file parses
again. A half-typed edit therefore never becomes the policy, and the vault is
never left without one. The exception is startup: an invalid file when the guard
launches is fatal, because there is no last valid policy to fall back on.

Turning autosave `off` is a transition, not just a smaller schedule. On the next
poll the guard takes **one immediate full snapshot**, bypassing both the quiet
window and the floor, so whatever was pending when you disabled autosave is in
the history rather than stranded. `off` becomes effective only after that
snapshot succeeds — a vault that cannot take its final snapshot does not get to
stop taking snapshots. From then on external edits accumulate uncommitted until
you re-enable the floor or run an explicit `bn` command, and the health check
says so as a warning rather than a problem: it is a state you chose.

That promise survives a restart. If the guard is stopped — or crashes, or the
machine reboots — after the edit but before the final snapshot, the next launch
reads a file that already says `off` and would otherwise have no way to tell that
from a vault that was already off for weeks. The vault's own durable record
settles it: it names the policy last actually enforced, so a record still naming
a duration while the file says `off` is a transition that never finished. The
guard resumes it on its first poll, under the outgoing policy, and publishes
`off` only once the owed snapshot lands. A vault that was genuinely already off
carries no such record and is left alone — restarting the guard never adds a
commit to it.

The guard keeps watching either way. While autosave is off, deletions remain
pending alongside additions and edits; they do not block other operations.

The rest of this guide is split by task:

- [Operating the guard](vault-safety-operating.md) — deletions, logs and notifications, being offline, and checking on it.
- [Recovering from a refusal](vault-safety-recovery.md) — block markers, interrupted snapshots, and clearing them in order.
- [Restore drills](vault-safety-drills.md) — rehearsing restores, and what is deliberately absent.
- [Vault migration evidence](vault-migration-evidence.md) — the redacted record of moving the live vault off File Provider storage and verifying its three recovery layers.

# Vault Safety

How the vault protects itself from loss, what it does automatically, and what
you do by hand on the rare occasion it stops and asks.

There is one moving part you did not start: `bn-vault-guard`, a companion
process that watches the vault's working tree and keeps its Git history current.
It is installed alongside `bn` and supervised by a LaunchAgent. It is not a `bn`
subcommand and it does not appear in `bn --help`, because there is nothing about
it to operate on a good day.

## What is protected

The vault's tracked scope is fixed and is not configurable:

- Every note in the vault, at its real path.
- The durable sidecars `.bnotes/brief.json`, `.bnotes/cockpit.json`, and
  `.bnotes/recall.json`.
- The undo journal under `.bnotes/journal/`, which is what authorizes committing
  a deletion.
- `brainnotes.toml` and `.gitignore`.

Deliberately outside it: the note cache under `.bnotes/index/`, the lock files
under `.bnotes/locks/`, and the transaction records under `.bnotes/txn/`. Those
are derived state and process bookkeeping. Committing them would put a commit on
every read-shaped operation and would record nothing a person could use.

## Immediate versus debounced snapshots

Two different things write to the vault, and they are treated differently
because they know different amounts about their own intent.

**A BrainNotes mutation snapshots immediately.** Every write through `bn`, the
web UI, `bn undo`, and every durable sidecar update commits its own paths — and
only its own paths — before the command returns. It knows exactly what it
changed, so there is nothing to wait for. If that commit cannot be made, the
command fails loudly, says the bytes are already on disk, and refuses the next
mutation. It never claims a rollback it did not perform.

**An external editor is debounced.** A change made by anything else — Obsidian,
`vim`, a sync client, a script — is noticed by the guard on its five-second
poll and then waits:

| Policy | Value |
| --- | --- |
| Quiet window | 30 seconds after the last observed change |
| Maximum latency | 2 minutes after the first observed change |
| Poll interval | 5 seconds |
| Push interval | at startup, and at least every 5 minutes |

Whichever fires first wins, so a burst of saves becomes one commit, and a burst
that never goes quiet is still committed within two minutes. The debounce clock
is persisted, so killing the guard — or logging out, or a crash — cannot
postpone a pending snapshot: the maximum is measured from the first change the
guard ever saw, not from the last time the guard started.

## Deletions

The one thing the engine will not do on its own is commit a deletion it cannot
explain. A deletion is committed only when an *uncommitted* undo-journal record
proves BrainNotes made it. A tracked file that simply disappears is treated as
damage: the guard blocks immediately, on the same poll that noticed it, and
refuses every subsequent mutation until a person resolves it. Nothing is
committed and nothing is pushed, so the content is still in `HEAD` and still on
the remote.

That is the whole trade. A deletion you meant costs you one command. A deletion
you did not mean costs you nothing.

## Logs and notifications

The guard writes structured JSON lines to
`~/Library/Logs/brainnotes-vault-guard.log`:

```
$ tail -n 3 ~/Library/Logs/brainnotes-vault-guard.log
{"time":"…","level":"INFO","msg":"watching the vault working tree","event":"started","vault":"…"}
{"time":"…","level":"INFO","msg":"external changes were recorded in the content history","event":"committed","path_count":2,"paths":["…"],"commit":"…"}
{"time":"…","level":"INFO","msg":"the content history was published","event":"pushed","branch":"refs/heads/main"}
```

Logs, commit messages, block markers, and notifications carry paths, counts, and
object names. They never carry a note's contents. That is a hard rule, verified
by a gate rather than by inspection, and it is what makes it safe to keep this
log around and to paste it into a bug report.

You get a macOS notification for exactly two things: the vault entering blocked
state, and a push that has failed several times running. Everything else is in
the log, because everything else is normal.

## Being offline

Remote availability is never a correctness input. Offline, on a plane, or with
GitHub down, every local mutation still works and still commits. The guard
retries the push on a backoff that starts at 30 seconds and tops out at 5
minutes, notifies you once it has failed repeatedly, and pushes the backlog by
itself when the network returns. There is nothing to do and nothing to
remember.

## Checking on it

```
$ check-brainnotes-vault-health.sh
OK: …
```

It reports the guard's process state, whether the vault is blocked, whether the
working tree is clean or dirty (by counts, never by name), when the last local
snapshot was, and whether the local branch has diverged from the remote. It
exits 0 when healthy, 1 when something needs attention, and 2 when it could not
run at all. `--json` gives the same answers as data.

## Recovering from a block

Blocked state is a file:

```
$ cat "$(git -C ~/BrainNotesVault rev-parse --git-dir)/brainnotes-safety/blocked.json"
{
  "version": 1,
  "kind": "unexplained-deletion",
  "reason": "…",
  "operation": "…",
  "observed_at": "…",
  "paths": ["…"],
  "recovery": "…"
}
```

`reason` says what happened in one clause and `recovery` says what to do. There
is deliberately no `bn` command that clears it: a blocked vault means the engine
saw something it could not explain, and the only useful next step is for a
person to look.

The single-user procedure, in order:

1. **Read the marker.** `kind` and `paths` tell you which case you are in.

2. **Decide what the affected paths should be.** For an unexplained deletion
   there are exactly two right answers.

   To put the note back, restore it from the last commit:

   ```
   $ cd ~/BrainNotesVault
   $ git checkout HEAD -- "<path from the marker>"
   ```

   To confirm you did mean to delete it, review it first and then commit the
   deletion deliberately:

   ```
   $ git show "HEAD:<path from the marker>" | less
   $ git rm -- "<path from the marker>"
   $ git commit -m "remove <path from the marker> after review"
   ```

   For an interrupted snapshot (`intent.json` rather than `blocked.json`), the
   marker names the commit that may or may not have landed. `git log --oneline`
   and `git show <commit>` settle it; nothing is lost either way, because the
   commit object is already in the repository.

3. **Verify before you clear anything.** Run the health check. It must report a
   clean working tree.

4. **Remove the marker, and only then.**

   ```
   $ rm "$(git -C ~/BrainNotesVault rev-parse --git-dir)/brainnotes-safety/blocked.json"
   ```

   Removing it first and investigating afterwards is the one sequence that can
   lose something, because a cleared marker re-enables the writes that would
   commit the damage.

5. **Restart the guard**, so it re-reads state from a clean start:

   ```
   $ launchctl kickstart -k "gui/$(id -u)/com.sglavoie.brainnotes-vault-guard"
   $ check-brainnotes-vault-health.sh
   ```

## Restore drills

Three independent copies exist, and the point of a drill is to restore from one
into a scratch directory and compare, never to restore over the live vault.

**From the local Git history.** The vault is its own repository, so any past
state is one clone away:

```
$ git clone --no-hardlinks ~/BrainNotesVault /tmp/restore-local
$ git -C /tmp/restore-local checkout <commit>
```

**From the content remote.** The remote is a private GitHub repository holding
the same history:

```
$ git clone git@github.com:sglavoie/brainnotes-vault.git /tmp/restore-remote
```

Nothing about this procedure depends on GitHub specifically. The remote is an
ordinary Git remote; a self-hosted one, a second machine, or a bare repository
on an external disk restores the same way with a different URL. If GitHub is
unreachable or the account is gone, the local history is complete on its own,
and the remote can be re-created from it with a single `git push`.

**From the sealed archive.** The pre-migration archive under
`~/backups/old_backup/` is a flat copy with a `SHA256SUMS` manifest beside it.
It is destination-agnostic by design: it is verified by its manifest, not by
where it happens to live, so it restores the same way from an external disk, a
different machine, or a copy pulled out of a backup service.

```
$ mkdir /tmp/restore-archive
$ rsync -a ~/backups/old_backup/brainnotes-vault-pre-migration-20260826/ /tmp/restore-archive/
$ (cd /tmp/restore-archive && shasum -a 256 -c SHA256SUMS | grep -v ': OK$')
```

A drill passes when the manifests over the tracked paths of two restores agree
byte for byte. `scripts/verify_vault_safety_evidence.py` does exactly this,
computing the manifests itself rather than reading a recorded result.

`bn restore --wipe` is safe to use against a vault that has a history: the wipe
deliberately keeps `.git/` and the root `backups/` directory, because nothing
could put them back. Every version the wipe discards is still in the content
history afterwards.

## What is deliberately absent

There is no `bn recover`, no `bn unblock`, and no `bn vault-guard`. The recovery
path is Git plus one file to delete, both of which work when `bn` itself is the
thing that is broken. A recovery command that depends on the program you are
recovering from is a recovery command you cannot rely on.

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

It reports the guard's process state, whether the vault is refusing writes
(either marker below), whether the working tree is clean or dirty (by counts,
never by name), when the last local snapshot was, and whether the local branch
has diverged from the remote. It exits 0 when healthy, 1 when something needs
attention, and 2 when it could not run at all. `--json` gives the same answers
as data.

A vault that is refusing writes is never a healthy result. Either marker on its
own exits 1, including a bare `intent.json` with no `blocked.json` beside it.

## Recovering from a refusal

Two files under `<git-dir>/brainnotes-safety/` refuse mutations, and they say
different things:

- **`blocked.json`** — the engine saw something it could not explain and latched
  fail-closed state. It observed the failure, so it can name it.
- **`intent.json`** — an operation began and never finished. The engine observed
  nothing; the record is the only evidence the attempt happened at all.

Either one on its own refuses every later write, so both need clearing before
the vault accepts one. There is deliberately no `bn` command that clears either:
a refusing vault means the engine cannot account for something, and the only
useful next step is for a person to look.

### A block marker

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

`reason` says what happened in one clause and `recovery` says what to do.
`kind` and `paths` tell you which case you are in. For an unexplained deletion
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

### An interrupted snapshot

```
$ cat "$(git -C ~/BrainNotesVault rev-parse --git-dir)/brainnotes-safety/intent.json"
{
  "version": 1,
  "operation": "…",
  "id": "…",
  "phase": "planned",
  "observed_at": "…",
  "ref": "refs/heads/main",
  "old_oid": "…",
  "path_count": 3,
  "recovery": ["…"]
}
```

A `new_oid` appears beside `old_oid` in the `committed` phase and only there.
`recovery` is a list of steps written for the phase the record is in, so it says
one of the two things below rather than both.

`phase` is the whole story, because the record is written and fsynced before the
first index write and promoted to `committed` — carrying the new commit's object
name — and fsynced again *before* the ref update is attempted. The phase you
find is therefore a statement about how far the operation actually got.

**`"committed"`, with a `new_oid`.** The commit object was built and written
into the repository before anything asked the branch to move, so the commit
exists whether or not the ref advanced. Prove which:

```
$ cd ~/BrainNotesVault
$ git cat-file -t <new_oid>
$ git merge-base --is-ancestor <new_oid> HEAD && echo landed || echo "not on the branch"
```

`landed` means the snapshot is in the history and there is nothing to do but
clear the record — BrainNotes retires this case itself on the next operation, so
a record you still see means the reachability check could not be run. `not on
the branch` means a real commit holding real content that the branch never
reached; inspect it and move it forward deliberately:

```
$ git show --stat <new_oid>
$ git merge --ff-only <new_oid>
```

`--ff-only` is the safe form: it refuses rather than rewriting anything if the
branch has moved on since. If it refuses, `git cherry-pick <new_oid>` replays
the same content on top.

**`"planned"`, with no `new_oid`.** No commit was ever built. Promotion to
`committed` is fsynced before `update-ref` runs, so a planned record proves the
branch was never asked to move: nothing landed, nothing was deleted, nothing was
pushed, and there is no object to go looking for. The record is not recoverable
by proof — there is nothing to prove — so it is adjudicated by hand instead. The
changes it describes are still exactly where they were, in the working tree:

```
$ cd ~/BrainNotesVault
$ git status --short
$ git log --oneline -5
```

Commit what you recognize, or leave it for the next snapshot to pick up once the
record is cleared:

```
$ git add -A && git commit -m "commit work interrupted by <id from the record>"
```

Read that `git status` before running it. A deletion in the list is the same
question the block procedure above answers, and committing it by hand answers it
permanently.

**A record you cannot read.** A truncated or unknown-version record is treated
as present with nothing to prove, exactly like a planned one — the engine will
not guess at a field it cannot parse. Adjudicate it as a planned record: the
working tree is still the whole truth, and the file is JSON holding no note
contents, so it is safe to open and read.

### Clearing, in order

1. **Adjudicate first.** Work through whichever sections above apply. Clearing
   first and investigating afterwards is the one sequence that can lose
   something, because a cleared marker re-enables the writes that would commit
   the damage.

2. **Verify.** Run the health check. It must report a clean working tree, and
   the only remaining problems should be the markers themselves.

   ```
   $ check-brainnotes-vault-health.sh --json
   ```

3. **Remove both records together, and only then.**

   ```
   $ safety="$(git -C ~/BrainNotesVault rev-parse --git-dir)/brainnotes-safety"
   $ rm -f "$safety/blocked.json" "$safety/intent.json"
   ```

   Both, always, even when only one is present — that is what `bn`'s own
   `ClearBlock` does, and removing one while leaving the other keeps the vault
   refusing for a reason whose marker you just deleted. `-f` is there so the
   pair is one command whether or not both files exist.

4. **Restart the guard**, so it re-reads state from a clean start:

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

The same refusal to read a recorded result governs the drills that cannot be
re-run from this machine. `scripts/vault_safety_scenarios.py` is a versioned
catalogue naming every gate the record must contain and what each one's retained
narrative has to show — which observations, in which sections, holding which
relations to each other. The harness re-derives each verdict from the narrative
instead of believing the `outcome` beside it, and rejects a recorded exit status
unless the narrative itself records that number, so rewriting a drill's evidence
and re-hashing it does not produce a passing run. Changing what a gate has to
prove means changing the catalogue and bumping `CATALOGUE_VERSION`, which
invalidates the stored record and asks for the drills again;
`scripts/test_verify_vault_safety_evidence.py` covers the refusals.

`bn restore --wipe` is safe to use against a vault that has a history: the wipe
deliberately keeps `.git/` and the root `backups/` directory, because nothing
could put them back. Every version the wipe discards is still in the content
history afterwards.

## What is deliberately absent

There is no `bn recover`, no `bn unblock`, and no `bn vault-guard`. The recovery
path is Git plus two files to delete, both of which work when `bn` itself is the
thing that is broken. A recovery command that depends on the program you are
recovering from is a recovery command you cannot rely on.

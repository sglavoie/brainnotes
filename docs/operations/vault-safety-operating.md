# Vault Safety: operating the guard

Part of [Vault Safety](vault-safety.md).

## Deletions

Delete files directly from VS Code, a file manager, or another device whose
changes arrive in the vault. Missing tracked regular files are recorded with the
same autosave timing as external edits, then exported as sync tombstones. A
concurrent edit on a peer still requires conflict resolution.

`bn remove` continues to move a note to `Trash/` and record an undo entry.
`bn undo <id>` restores its pre-removal bytes, including edits newer than the
last Git snapshot. External deletions do not create Trash copies or undo entries.

Git retains previously committed versions after deletion. Find the deletion
commit and restore from its parent:

```sh
git -C <vault> log --diff-filter=D --format='%H %s' -- '<path>'
git -C <vault> restore --source='<deletion-commit>^' --worktree -- '<path>'
```

The restored file is recorded on the next autosave. Git cannot recover edits
that disappeared before any snapshot captured them.

Valid `unexplained_deletion` blocks written by older releases are cleared on the
next safety operation after repository and interrupted-snapshot checks pass.
Upgrade the CLI, guard and sync companion together and restart running services.
Corrupt markers, unsupported versions and other safety failures still refuse.

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
(either marker below), which autosave floor the guard is enforcing and when it
next intends to snapshot, whether the working tree is clean or dirty (by counts,
never by name), when the last local snapshot was, and whether the local branch
has diverged from the remote. It exits 0 when healthy, 1 when something needs
attention, and 2 when it could not run at all. `--json` gives the same answers
as data.

A vault that is refusing writes is never a healthy result. Either marker on its
own exits 1, including a bare `intent.json` with no `blocked.json` beside it.

**A dirty working tree is not, on its own, a problem.** A vault with a
five-minute floor is *supposed* to sit with uncommitted edits for most of five
minutes, so the check judges pending edits against the schedule the guard itself
published in `watch.json` rather than against the clock:

- **Pending.** Edits are waiting and `next_commit_due_at` is still ahead. This is
  the system working, and it exits 0.
- **Overdue.** The due time has passed by more than the five-second poll grace,
  so the guard is not snapshotting work it said it would. That is a problem and
  exits 1, quoting how late it is, the time it promised, and the floor in force.
- **Warning.** The floor is `off`, so the pending edits stay uncommitted until it
  is re-enabled or an explicit `bn` command commits them. Reported, not failed.

The unreadable cases are problems too, and unlike the three above they are judged
on the record alone, dirty tree or not. A record carrying no policy, a policy the
check cannot read, a due time that cannot be parsed, and a record that says `off`
while still naming a snapshot it intends to take all mean the same thing: nobody
can name the floor this vault is being held to. That is worth saying while the
vault is still clean, which is when it can still be fixed quietly. The one
verdict that does need a dirty tree is edits waiting under a readable floor with
no snapshot scheduled for them — a clean vault is *supposed* to have none.

A token is read as a floor only if the guard could have written it: `off`, or the
one canonical spelling of a duration inside the same inclusive 30s–24h bounds
`bn config set` enforces. `300s`, `5m0s`, `1ns` and `48h` are all duration-shaped
and none of them is a floor this vault could be under, so each is reported as
unreadable rather than obeyed.

A pending deletion follows the same floor as an external edit. With autosave
off, it remains pending until autosave resumes or an explicit snapshot records it.


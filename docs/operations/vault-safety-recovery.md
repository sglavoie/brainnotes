# Vault Safety: recovering from a refusal

Part of [Vault Safety](vault-safety.md).

## Recovering from a refusal

Two files under `<git-dir>/brainnotes-safety/` refuse mutations, and they say
different things:

- **`blocked.json`** — the engine saw something it could not explain and latched
  fail-closed state. It observed the failure, so it can name it.
- **`intent.json`** — an operation began and never finished. The engine observed
  nothing; the record is the only evidence the attempt happened at all.

Except for automatically migrated legacy deletion blocks, either one on its own
refuses every later write, so both need resolving before
the vault accepts one. There is deliberately no `bn` command that clears either:
a refusing vault means the engine cannot account for something, and the only
useful next step is for a person to look.

### A block marker

```
$ cat "$(git -C ~/BrainNotesVault rev-parse --git-dir)/brainnotes-safety/blocked.json"
{
  "version": 1,
  "kind": "ambiguous_change",
  "reason": "…",
  "operation": "…",
  "observed_at": "…",
  "paths": ["…"],
  "recovery": "…"
}
```

`reason` says what happened in one clause and `recovery` says what to do.
`kind` and `paths` identify the affected paths and failure type. Follow the
record's recovery instructions before clearing it. Ordinary file deletions no
longer create block markers; see [Deletions](vault-safety-operating.md#deletions) for recovery and upgrade
behavior.

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


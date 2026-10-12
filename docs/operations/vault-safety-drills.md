# Vault Safety: restore drills

Part of [Vault Safety](vault-safety.md).

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
`scripts/test_verify_vault_safety_*.py` cover the refusals.

`bn restore --wipe` is safe to use against a vault that has a history: the wipe
deliberately keeps `.git/` and the root `backups/` directory, because nothing
could put them back. Every version the wipe discards is still in the content
history afterwards.

## What is deliberately absent

There is no `bn recover`, no `bn unblock`, and no `bn vault-guard`. The recovery
path is Git plus two files to delete, both of which work when `bn` itself is the
thing that is broken. A recovery command that depends on the program you are
recovering from is a recovery command you cannot rely on.

There is also no setting for the quiet window, the readiness deadline, the poll
interval, or the push schedule. `[autosave] min_interval` is the one timing a
vault configures, and it is the only one that is a matter of taste: it bounds how
often the guard adds a commit nobody asked for. Every other number above bounds
how much of the vault is recoverable, and that is not a preference.

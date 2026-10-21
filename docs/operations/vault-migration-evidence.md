# Vault Migration Evidence — 2026-08-28

Operational record for session 1 of the vault loss-prevention plan: removing the
live vault from macOS File Provider control and establishing three independently
verified recovery layers before any code change.

This document is deliberately redacted. It contains no note bodies, no
credentials, and no note filenames other than the incident sentinel
`Contagious - Why Things Catch On.md`. Full per-file manifests live outside this
repository (see [Manifest locations](#manifest-locations)) so that vault
filenames are never published to the content remote or to this code remote.

## Outcome

| Layer | Location | State |
| --- | --- | --- |
| Old source (rollback) | `~/Documents/vault` | Intact, byte-for-byte unchanged, out of use |
| Offline archive | `~/backups/old_backup/brainnotes-vault-pre-migration-20260826` | Verified, sealed `uchg` |
| Canonical working vault | `~/BrainNotesVault` | Active, outside every File Provider root |
| Remote content history | a private GitHub repository | Private, pushed, fresh-clone verified |

Cutover completed 2026-08-28. Rollback status: **available and untested-by-need** —
the old source is unmodified and remains the authoritative fallback until session 6
retires it.

## Archive destination deviation

The plan specified `/Volumes/SanDisk/old_backup/brainnotes-vault-pre-migration-20260826`.
The first archive was written there and fully verified, but the SanDisk volume
unmounted mid-session and re-locked (FileVault). It could not be remounted without
an interactive passphrase:

```
$ diskutil mount disk5s1
Volume on disk5s1 failed to mount
This is an encrypted and locked APFS Volume; use "diskutil apfs unlockVolume"
$ diskutil info disk5s1 | grep -E 'Mounted|Locked'
   Mounted:                   No
   Locked:                    Yes
```

On supervisor instruction the destination was moved to
`~/backups/old_backup/` — an existing local backup location, outside
every File Provider root — and the session's completion contract was amended to
match. The archive was re-created and re-verified from scratch at the new path.

> **Operator decision, 2026-08-28.** Archive destination and physical medium are
> **not** retirement prerequisites. The archive is verified by its approved
> manifest and by a successful restore drill, not by where it lives; no SanDisk
> volume and no `/Volumes/...` path is required for session 6 to retire the old
> source. Re-making the archive on independent media remains worth doing for
> media-failure resilience, but it is a separate improvement and does not gate
> retirement.
>
> What that costs is stated plainly rather than waived: on this disk the archive
> is not an independent-media layer, so a whole-disk failure takes it out along
> with the live vault. The layer that survives that case is the content remote,
> which is off-machine and is itself gate-verified by a fresh clone and a
> manifest comparison.

## File Provider exposure

The ancestor `~/Documents` carries File Provider extended attributes;
this is the exposure the migration removes.

```
$ xattr ~/Documents
com.apple.file-provider-domain-id
com.apple.fileprovider.detached#B
com.apple.fileprovider.pinned#PX
...
```

File Provider roots on this machine: `Documents`, `Desktop`, `Dropbox`,
`Google Drive`, `OneDrive`. Every ancestor of the new vault root was checked and
carries **zero** File Provider attributes:

```
~/BrainNotesVault -> file-provider xattrs: 0
~                               -> file-provider xattrs: 0
/Users                          -> file-provider xattrs: 0
```

## Inventory of path references

Searched `~/.config`, `~/Library/LaunchAgents`, and `~/scripts` for the old vault
path. Exactly two files referenced it, both as predicted by the plan; no
LaunchAgent and no automation-repository reference existed.

| File | Change |
| --- | --- |
| `~/.config/bnotes/config.toml` | `vault = "…/Documents/vault"` → `"…/BrainNotesVault"` |
| `~/.config/karabiner/karabiner.json` | `BNOTES_VAULT=…/Documents/vault/` → `…/BrainNotesVault/` |

Post-change scan:

```
~/.config              -> 0 file(s) referencing old path
~/Library/LaunchAgents -> 0 file(s) referencing old path
~/scripts              -> 0 file(s) referencing old path
```

Karabiner reloaded the edited configuration automatically; `core_service.log`
records `Load ~/.config/karabiner/karabiner.json… core_configuration
is updated.` at the edit timestamp.

> **`~/.config/bnotes/config.toml` is inert.** This `bn` build has no user-level
> config file: `docs/configuration.md` states "There is no external or user-level
> config file", and the vault root is resolved by `--vault` > `$BNOTES_VAULT` >
> upward search for `brainnotes.toml`. The file was updated anyway for hygiene and
> because the completion contract asserts it, but the operative configuration is
> `<vault>/brainnotes.toml` plus the Karabiner `BNOTES_VAULT` assignment.

## Archive verification

Created with `ditto --rsrc --extattr <source> <archive>`, then compared against the
live source across content, structure, and metadata.

| Check | Result |
| --- | --- |
| Files hashed (SHA-256) | 1291 / 1291 identical, paths identical |
| Path set | Identical (1606 shared entries: files + directories) |
| File types | 0 differences |
| Mode / uid / gid / size | 0 differences |
| ACLs | 0 differences (source vault carries no ACLs) |
| Symlinks | None present in source |
| Extended-attribute **names** | 0 lost |

The only metadata delta is kernel-managed and unavoidable across a copy:

- `com.apple.provenance` added to 117 newly created entries and re-issued on 345;
  it is a per-file kernel token, not user data.
- `com.apple.quarantine` flag byte differs (`0081` → `0281`) on 142 **directories**
  and **zero files**; the remainder of the value (`;00000000;;`) is unchanged.

### Restore rehearsal

The completed archive was copied back into a temporary directory on the same
volume, compared, and then removed.

| Check | Result |
| --- | --- |
| Round-trip path/type/mode/owner/size/xattr **names and values** | 0 differences |
| `shasum -a 256 -c SHA256SUMS` inside the restored copy | 1291 checked, 0 failed |
| ACL comparison | Identical |

The verification copy was deleted and the archive was then sealed:

```
$ chflags -R uchg <archive>
$ touch <archive>/SHA256SUMS
touch: ... Operation not permitted
```

To modify or remove the archive later: `chflags -R nouchg <archive>`.

The archive contains three files that are **not** part of the source vault:
`SHA256SUMS`, `METADATA.tsv`, and `ARCHIVE-README.md`.

## New vault root

Built with `rsync -a` from the stopped source, carrying durable content only.

- Copied: 1278 files, 64 MiB.
- Every copied file is byte-identical to the source; **0** files present that are
  absent from the source; **0** hash mismatches.
- Dropped 13 files, all accounted for: `backups/` (2), `.codegraph/` (3),
  `.bnotes/index/` (2), `.bnotes/locks/` (5), `.DS_Store` (1).
- `.bnotes/` retained exactly `journal/` (142 entries, 871 files), `brief.json`,
  `cockpit.json`, `recall.json`.

### `.gitignore` and a silent-exclusion trap

The user's global `core.excludesFile` (`~/.gitignore`) contains `BRAIN[A-Z]*`, and
`core.ignoreCase` is `true`. With no mitigation this silently excluded
`brainnotes.toml` — which the plan requires to be tracked — and one note. The
global file also carries patterns (`PLAN.md`, `OVERVIEW.md`, `INSTRUCTIONS.md`,
`REFACTORING.md`, `CODE_REVIEW.md`, `mise.toml`, `tmp/`, `.project`) that are
plausible future note names.

Because a repository `.gitignore` outranks `core.excludesFile`, the vault
`.gitignore` opens with `!/**` to neutralise the global file, then re-states every
exclusion explicitly. Verified afterwards: 1278 working-tree files, 1278 tracked,
0 ignored, and every intended exclusion still matched by a rule.

Scratch and custody patterns were taken from the source rather than guessed —
`internal/vault` `openExclusiveTemp` produces `.note-<hex>.tmp`,
`.note-mode-<hex>.tmp`, `.txn-<hex>.tmp`, and `.txn-custody-<hex>.tmp`.

`.bnotes/` is excluded wholesale and re-included by allowlist, so generated state
added by future versions (for example `.bnotes/review.json`, which the code already
references) cannot dirty the working tree.

## Private remote and clone verification

| Property | Value |
| --- | --- |
| Repository | private GitHub repository |
| Visibility | `PRIVATE` |
| Default branch | `main` |
| Remote protocol | SSH |

Fresh clone into a temporary directory, compared against the canonical working copy:

| Check | Result |
| --- | --- |
| Commit | Identical |
| Tracked path set | Identical (1278 paths) |
| Working-tree file set | Identical |
| File sizes | 0 mismatches |
| SHA-256 of every file | 0 mismatches |

### Sentinel

`Contagious - Why Things Catch On.md` — the note lost in the original incident —
is present and identical in all four locations:

```
694e5cd85780a92fcef4845108242b22bc1d6bcd59f6c69aaca9210fd3e4b158  4540 bytes
  old source · offline archive · new vault root · fresh clone
```

## Cutover and smoke checks

`bn serve` was **not** running at session start and no LaunchAgent defines it, so
nothing had to be stopped; no process held a writable handle beneath the old vault.
It was started against the new root, detached:

```
nohup env BNOTES_VAULT=~/BrainNotesVault \
  ~/go/bin/bn serve --no-open \
  > ~/.local/state/bnotes/serve.log 2>&1 &
```

Serving at `http://127.0.0.1:8729`; the process holds no handle beneath the old
vault; served HTML contains zero references to the old path. Read-only checks
(`bn config`, `bn index`, `bn search`, `bn tags`, `bn doctor`, HTTP `GET /` → 200)
all resolved against `~/BrainNotesVault`.

### Reversible test mutation

A frontmatter property was set on one small stub note (filename redacted), then
reverted through the normal BrainNotes undo journal.

| Step | Result |
| --- | --- |
| `bn set … migration_smoke_test=20260828` | Note changed in the new root: 11 → 50 bytes |
| Old vault after mutation | **Unchanged** — all 1291 hashes still match session start |
| Journal entries | 142 → 143 |
| `bn undo <entry> --dry-run` then apply | `restored 1 path(s)` |
| Note after undo | Byte-identical to baseline (`a7b9df8a…4917c`, 11 bytes) |
| Working tree after revert | Clean |

Both operations are recorded in the content history as separate commits on top of
the baseline, and pushed.

## Old source disposition

`~/Documents/vault` was left byte-for-byte intact and out of use. It
was not renamed, trashed, or deleted. Re-manifested at the end of the session:

- Content manifest: 1291 / 1291 hashes match the session-start manifest.
- Metadata inventory: unchanged.

Its final manifest is identical to the archive's `SHA256SUMS`
(`sha256 90787662c30c0c9e09394813908eb6e84d006e332d1d43a4dc7d4d7819857928`), which is
the comparison baseline for the session 6 retirement check.

### Post-baseline drift, reconciled 2026-08-28

The old root did not stay frozen after the baseline was sealed, and the
retirement check had to stop assuming it did. `bn` refuses every write under
File Provider storage, but an editor does not, so therapy notes written directly
in `~/Documents/vault` after 2026-08-26 never reached the Git-backed vault.
Commit `c2a52d7` brought that content forward — four new notes, one pruned
rewrite whose superseded text remains in history at `d43131e`, and forward edits
to three existing notes — and removed the `bn new` probe the guard had
auto-committed.

So the retirement gate does **not** assert that the old root still equals the
sealed archive; that became false the moment the first post-baseline note was
written there, and asserting it would have been asserting something untrue. It
asserts the property that actually protects the content: every file in the old
root is byte-present either in the live vault's Git object database, at any
commit, or in the sealed archive. What remains after that is classified by path
as machinery — the derived note cache, lock files, the old root's own undo
journal, a `.codegraph/` index, the vault config, and `bn`'s own backup tarballs
of the same content — and the gate fails if a single file falls outside those
classes. Measured before the move: 1301 files, 0 unaccounted.

## Manifest locations

Stored outside both repositories so vault filenames are never pushed anywhere:

| File | Digest (sha256 of the manifest file) |
| --- | --- |
| `~/backups/old_backup/brainnotes-vault-pre-migration-20260826/SHA256SUMS` | `90787662c30c0c9e09394813908eb6e84d006e332d1d43a4dc7d4d7819857928` |
| `~/backups/old_backup/brainnotes-vault-pre-migration-20260826/METADATA.tsv` | metadata inventory, 1607 entries |
| `~/backups/old_backup/old-vault-final-manifest-20260828.sha256` | `90787662c30c0c9e09394813908eb6e84d006e332d1d43a4dc7d4d7819857928` |
| `~/backups/old_backup/old-vault-final-metadata-20260828.tsv` | `422157dca4313de47f9334f1be21cc04609187d55ed080c81727190ef552d361` |

New-root content manifest digest (1278 files, at baseline commit):
`a3ca44efebd94c5ff442f9cc8c144dc64b1ebe0f228974dffc37a26dc40d29ac`.

## Pre-existing defects found (not caused by the migration)

- **`bn history` is unusable.** Five of 142 journal manifests contain a raw newline
  inside a JSON string and fail to parse; `bn history` aborts on the first one. The
  affected files are byte-identical in the old source, the archive, and the new
  root, so this predates the migration. `bn undo <id>` still works when given an
  explicit entry ID, which is how the test mutation was reverted.
- **The old source remains a resolvable vault.** `~/Documents/vault`
  still contains a `brainnotes.toml`, so `bn` invoked from inside it resolves there
  by upward search. This is intended for now — the old source is the rollback
  layer — but session 6 must neutralise it when retiring the directory.

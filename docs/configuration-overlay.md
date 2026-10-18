# Machine-local configuration

Part of [Configuration](configuration.md).

### The machine-local overlay

`brainnotes.toml` describes the **vault**. `brainnotes.local.toml`, optional and
beside it, describes **this machine**: the editor to launch, the address to
bind, the command to execute. Neither file is required to be split — a single
`brainnotes.toml` carrying everything keeps working exactly as it always has,
and is what `bn init` writes.

The split earns its keep when the same vault is opened from more than one
machine. A laptop that wants `serve.addr = "127.0.0.1:8080"` and a server that
wants `"0.0.0.0:8080"` cannot both be right in a file that travels between them,
and an `open.editor` naming a binary that exists on one machine is a broken
command on the other.

These keys belong to `brainnotes.local.toml`:

| Key | What makes it machine-specific |
| --- | --- |
| `fzf.args` | Arguments to the `fzf` installed here |
| `open.editor` | A binary on this machine's `PATH` |
| `serve.addr` | An interface this machine has |
| `serve.open_browser` | Whether this machine has a browser |
| `agent.command` | A binary on this machine's `PATH` |
| `agent.timeout_seconds` | Paired with that command |
| `cockpit.agent_command` | A binary on this machine's `PATH` |
| `cockpit.agent_timeout_seconds` | Paired with that command |

Every other key is portable, including `autosave.min_interval`: that is a policy
about the vault's own safety history, expressed in the vault's own units, and
nothing in it names a machine.

How the two files combine:

- The overlay wins for exactly the keys it sets. A key it does not mention keeps
  the value `brainnotes.toml` gave it; a key neither file sets keeps its default.
- A table or an array of tables — `[frontmatter.properties]`, `[new.routes]`,
  `[[views]]` — is replaced whole rather than merged key by key, so the merged
  configuration always says precisely what one of the two files says.
- An empty table assigns nothing, in any spelling. `[frontmatter.properties]`
  with every block under it commented out — exactly what you get by copying the
  starter template — leaves the portable declarations alone rather than erasing
  them, and so does `frontmatter.properties = {}`. A table-valued key can be
  *replaced* from the overlay but never emptied. (An array is not a table:
  `views = []` does clear the portable views.)
- Both files are parsed under the same schema. An unknown key in the overlay is
  an error naming the overlay, not the portable file beside it. A value that is
  the wrong *shape* is reported after the two files are merged, so that message
  names the key but not which file supplied it.
- An absent overlay and an empty one are different states. Creating an empty
  `brainnotes.local.toml`, filling it in, and deleting it again are three
  distinct configurations.

Do not commit `brainnotes.local.toml` and do not synchronize it. `bn` treats it
as at least as private as `brainnotes.toml`: neither file is ever served as an
attachment, under its own name or through a symlink pointing at it, and the
vault's own Git safety history leaves the overlay out of every snapshot it takes
on its own initiative. (A file already tracked in that history stays tracked —
if you committed one before, remove it by hand.)

`bn config` reports the portable file, the active local overlay (or `none`), and
how the vault root was resolved. `bn config explain KEY` shows the effective
value after defaults and normalization, its source file (or built-in default),
and the file that owns the key. For example:

```sh
bn config explain open.editor
bn config explain serve.addr
bn config explain autosave.min_interval
```

This inspection is read-only and accepts schema field keys, including table
fields such as `frontmatter.properties`. Ownership describes where a setting
belongs; source describes where this loaded value was assigned. They can differ
in unenrolled vaults. `bn config get autosave.min_interval` keeps its existing
value-only output for scripts.

Once a vault is **enrolled in synchronization**, placement stops being advisory
and a misplaced key fails the load, naming the file it belongs in. The reason is
that either direction of misplacement is a real fault once a peer exists: a
portable key parked in the overlay never reaches the peer vault, and a local key
left in the portable file does reach it and overwrites the peer's own editor,
address, or agent command. A vault that is not enrolled synchronizes with
nothing, so neither failure exists there and any placement loads — which is also
what keeps every existing single-file configuration working untouched.

`bn config set` writes **the file that owns the key**, so it can never create a
misplacement. The only key it accepts today (`autosave.min_interval`) is
portable, so it patches `brainnotes.toml`. If you have put that key in the
overlay anyway — legal on an unenrolled vault — the write is **refused** rather
than applied to the portable file: the overlay still wins, so patching the file
underneath it would report success while leaving the value you just set out of
force. Move the key into one file (`bn config migrate` does it for you) and try
again.

### Repairing a misplaced configuration

```
bn config migrate [--dry-run]
```

Moves each assignment sitting in the wrong file into the one that owns it: the
keys naming this machine into `brainnotes.local.toml`, everything describing the
vault into `brainnotes.toml`. It is the repair for a legacy single-file vault
about to be enrolled, and the repair for a vault already enrolled whose
configuration the loader now refuses — so it is one of the few commands that
runs **without** a successful configuration load behind it.

- Assignments move as text. Values, quoting, and the comment on a moved line
  arrive unchanged; a comment sitting *above* a key stays where it is.
- Nothing about the vault's behaviour changes. The migration is refused rather
  than written if any key would end up with a different effective value.
- A key both files assign is a special case, because one of the two assignments
  is not in force and will disappear. It is named in the summary rather than
  reported as a plain move, so you can tell whether the value that was being
  shadowed mattered to you.
- Running it again does nothing, and says so rather than passing over it in
  silence.
- `--dry-run` prints the moves and both files' diffs and writes nothing.
- The originals are recoverable through `bn undo`, and an interrupted run leaves
  either both files migrated or neither: the pair is written as one transaction
  under one journal entry.

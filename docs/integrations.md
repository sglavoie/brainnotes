# Shell and editor integration

## Shell completion

Cobra ships completion scripts for free:

```sh
bn completion bash   > /etc/bash_completion.d/bn
bn completion zsh    > "${fpath[1]}/_bn"
bn completion fish   > ~/.config/fish/completions/bn.fish
```

For a one-shot zsh session:

```sh
source <(bn completion zsh) && compdef _bn bn
```

Beyond command and flag names, completion reads the vault the command would
act on (`--vault`, `$BNOTES_VAULT`, or the upward search): note titles for the
commands that take one note (`open`, `links`, `backlinks`, `related`,
`context`, `pin`, `unpin`, `remove`, `move`, `rename`), and the values of
`--view`, `--tag`, `--folder` and `--property`. Enumerated flags such as
`related --mode`, `context --format`, `graph --format`, `find --sort` and
`set --value-type` complete their fixed values. A vault that does not load completes
nothing.

## Scripting: exit status and JSON

`bn` follows grep's exit-status convention:

| Status | Meaning |
| --- | --- |
| `0` | Success. |
| `1` | The command ran and the answer was negative: `bn lint` found violations, `bn search` found no match, a picker came back empty (an empty vault, no related notes or properties to offer, or an fzf query that matched nothing), or a confirmation was declined. |
| `2` | An error: an unknown flag, a vault that does not load, a refused write, a confirmation refused for want of a terminal. |
| `126`/`127` | A program you asked bn to run (editor, fzf, the `checkpoint run` command) could not be run or was not found. |
| `130` | Interrupted: Ctrl-C, or a dismissed fzf picker. |

`bn checkpoint run` exits with its child's status. Only those user-visible
children pass their status through: when a Git command bn runs for its own
safety checks fails, bn exits 2, never with Git's code. Messages for statuses 1
and 2 go to stderr, prefixed with the command (`bn: search: no matches`,
`bn: open: nothing selected`), so `p=$(bn open --print)` fails rather than
succeeding with an empty value.

A command that asks before destroying something (`bn remove`,
`bn restore --wipe`) asks only on a terminal. Without one it refuses with
exit 2 and names the flag to pass (`--yes`, or `-y`); it never reads an answer
from a pipe.

Ctrl-C asks a running command to stop rather than killing it, so a journaled
write or `bn serve`'s shutdown can finish cleanly; press it a second time to
stop at once.

Machine-readable output is spelled `--json` on `find`, `search`, `links`,
`backlinks`, `values`/`tags`, `views`, `schema`, `lint`, `history`, `related`,
`doctor` and `checkpoint status`; `context --json` is shorthand for
`context --format json`. JSON output is indented and not HTML-escaped, and an
empty result is `[]` rather than nothing.

Paths in JSON are vault-relative with forward slashes, under the key `path`.
The commands that list notes — `find`, `search`, `links` and `backlinks` —
share one record, `{"path", "abs_path", "title"}`, where `abs_path` is the
absolute path their plain output prints; `search` adds `count` and `matches`.
`bn doctor --json` is the one exception: it predates the convention and keeps
`rel_path`.

## Editor integration

### Neovim

```lua
-- Pick a vault note and open it.
vim.keymap.set("n", "<leader>fb", function()
  local handle = io.popen("bn find -i")
  if handle == nil then return end
  local path = handle:read("*l"); handle:close()
  if path then vim.cmd.edit(vim.fn.fnameescape(path)) end
end)

-- Rename current buffer (also rewrites every wikilink).
vim.keymap.set("n", "<leader>nr", function()
  local current = vim.api.nvim_buf_get_name(0)
  local new = vim.fn.input("New title: ")
  if new == "" then return end
  vim.fn.system({ "bn", "rename", current, new })
  vim.cmd.edit()
end)
```

### VS Code

A `.vscode/tasks.json` snippet that can pick a related note, inspect a context
packet or optional local research brief, and rename the current note:

```jsonc
{
  "version": "2.0.0",
  "tasks": [
    {
      "label": "bn: related current",
      "type": "process",
      "command": "bn",
      "args": ["related", "${file}", "-i", "--open"],
      "presentation": { "reveal": "always", "focus": true }
    },
    {
      "label": "bn: context current",
      "type": "process",
      "command": "bn",
      "args": ["context", "${file}", "--purpose", "questions"],
      "presentation": { "reveal": "always", "focus": false }
    },
    {
      "label": "bn: research brief current",
      "type": "process",
      "command": "bn",
      "args": ["context", "${file}", "--purpose", "questions", "--agent"],
      "presentation": { "reveal": "always", "focus": false }
    },
    {
      "label": "bn: rename current",
      "type": "process",
      "command": "bn",
      "args": ["rename", "${file}", "${input:newTitle}"]
    }
  ],
  "inputs": [{ "id": "newTitle", "type": "promptString", "description": "New title" }]
}
```



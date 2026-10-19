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
| `1` | The command ran and the answer was negative: `bn lint` found violations, a picker came back empty (an empty vault, or an fzf query that matched nothing), or a confirmation was declined. |
| `2` | An error: an unknown flag, a vault that does not load, a refused write. |
| `126`/`127` | An external program (editor, fzf) could not be run or was not found. |
| `130` | Interrupted: Ctrl-C, or a dismissed fzf picker. |

`bn checkpoint run` exits with its child's status. Messages for statuses 1 and
2 go to stderr, so `p=$(bn open --print)` fails rather than succeeding with an
empty value.

Ctrl-C asks a running command to stop rather than killing it, so a journaled
write or `bn serve`'s shutdown can finish cleanly; press it a second time to
stop at once.

Machine-readable output is spelled `--json` on `find`, `lint`, `history`,
`related`, `doctor` and `checkpoint status`; `context --json` is shorthand for
`context --format json`. JSON output is indented and not HTML-escaped.

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



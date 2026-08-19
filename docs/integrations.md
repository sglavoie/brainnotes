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



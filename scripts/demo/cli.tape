# VHS script for docs/assets/demo-cli.gif. Run through `just demo`, which
# substitutes {{OUTPUT}} and starts VHS inside a throwaway copy of
# scripts/demo/vault with BNOTES_VAULT pointing at it.

Output "{{OUTPUT}}"

Require bn
Require jq

Set Shell "bash"
Set FontSize 22
Set Width 1280
Set Height 560
Set Padding 24
Set TypingSpeed 45ms
Set Theme "Catppuccin Mocha"

Hide
Type "export PS1='\[\e[1;36m\]vault\[\e[0m\] $ ' && clear"
Enter
Show

# Saved views are named queries from brainnotes.toml.
Type "bn views"
Sleep 300ms
Enter
Sleep 2s

# One query language: properties, booleans and comparisons.
Type "bn find -q 'category:Books and prop:rating>=5' --json | jq -r '.[].title'"
Sleep 300ms
Enter
Sleep 2.5s

# Link-graph operators the flags cannot spell.
Type "bn find -q 'near:[[Deep Work]]<=2 and status:wip' --json | jq -r '.[].title'"
Sleep 300ms
Enter
Sleep 2.5s

Type "clear"
Enter

# Related notes, with the reasons each one ranks.
Type "bn related 'Books/Deep Work.md' | head -9"
Sleep 300ms
Enter
Sleep 3.5s

Type "clear"
Enter

# Capture a thought without choosing a file.
Type "bn capture 'Try the Feynman technique on [[Spaced Repetition]]' | head -2"
Sleep 300ms
Enter
Sleep 2.5s

# Bulk edits preview first, then apply.
Type "bn find --view 'Reading list' | bn set - status=reading --dry-run"
Sleep 300ms
Enter
Sleep 3.5s

# CLI command shape

Companion to `CLAUDE.md` and `docs/internals/architecture.md`; read before adding a
subcommand to `internal/cli` or decomposing an existing one.

Every `New*Cmd` in `internal/cli` is a Cobra constructor, and the shape it is
written in decides whether the command is reviewable. Fifteen of thirty-seven
constructors grew the same way — the entire command body as an anonymous `RunE:`
closure inside the constructor, over a set of captured local variables — and that
one habit produced the worst function in the repository (`NewSearchCmd`,
cognitive complexity 231, cyclomatic 80, a 293-line closure inside a 368-line
file). The cost is not aesthetic. A closure over captured locals has no name to
call, no arguments to vary, and no seam to test through, so every flag
combination is reachable only by driving the whole Cobra tree, and each new flag
has to be checked against every other one by hand in a matrix nothing enforces.

`internal/cli/set.go` + `set_run.go` were already written the other way, and
`internal/cli/search.go` + `search_run.go` + `search_output.go` is the worked
example. This is the contract those two demonstrate.

## The shape

**One constructor file, `<cmd>.go`.** It holds `Use`/`Short`/`Long`, the Cobra
metadata (`Args`, `SilenceUsage`, `SilenceErrors`), the flag registrations, and
nothing else. `RunE` is one line:

```go
RunE: func(cmd *cobra.Command, args []string) error {
    return runSearchCommand(cmd, args, opts)
},
```

The constructor should score zero on every complexity linter. If it does not,
logic is still living in it.

**An options struct replacing the captured locals.** Declared as
`<cmd>Options` next to the runner, one field per flag, bound directly by the
registrations (`f.BoolVarP(&opts.regex, ...)`). Shared sub-surfaces are held as
the pointer they already are rather than copied — `searchOptions.ff` is the
`*findFlags` the command registers, so `search` keeps routing through
`findflags.go`'s single execution path instead of re-deriving the filter surface.

The struct is passed **by value** into the runner. The closure captures the
`opts` variable, the flag bindings point into that same storage, and Cobra has
finished parsing by the time `RunE` fires, so the runner receives a snapshot of
the fully parsed flags.

**A `validate()` method holding the flag mutual-exclusion rules.** All of them,
in one place, so a new flag is checked against the existing matrix by reading a
single method rather than by grepping a closure. Inputs that are properties of
the `*cobra.Command` rather than of the options — "was any find flag set", which
comes from `Flag(name).Changed` — are passed in as parameters instead of being
re-derived inside, which is what keeps `validate()` a pure function of its
inputs and therefore directly testable.

Split the method when the rules outgrow one threshold-clean function
(`validateContextCombos`, `validateCountCombos`), with the guard clause inside
the helper so the caller stays a flat list of `if err := ...; err != nil`.

**One named function per behaviour mode, in `<cmd>_run.go`.** A command with
several output modes gets one function per mode — `runSearchInteractive`,
`runSearchContext`, `runSearchCount`, `runSearchOpen`, `runSearchPaths` — plus a
small dispatcher whose `switch` **is** the mode-selection rule, stated once. The
order of the dispatcher's cases is part of the contract when modes can legally
combine: `--open` is legal with `-i`, so the interactive case comes first and
owns that combination.

Modes share resolved state through a small struct (`searchRun`) rather than
through a parameter list that grows with every mode. Build it once in the runner,
after selection, and hand it to the mode.

**Rendering stays in `<cmd>_output.go`.** Writers, formatters, and the
collectors that feed them belong beside the existing output helpers, not in a
third new file: `writeSearchPaths` and `writeSearchCounts` sit next to
`contextOutput` and `buildInteractiveLines`. A single writer per output form is
also how a format detail stops drifting — NUL separation lives in exactly one
function, so the default paths mode and interactive selection output cannot
disagree about the separator.

## Order is behaviour

Decomposition preserves the order in which a command refuses things, and that
order is observable. In `search`, an unparseable pattern is reported before the
flag matrix, and both before the vault is opened, so a bad regex is a regex error
rather than a vault error. `--open` resolves the editor **lazily**, only once a
match exists, so `--open` against a vault with no match succeeds on a machine
with no `$EDITOR`. Neither is incidental; both are pinned by tests.

When a rewrite would collapse two branches that look identical, check the empty
case first. `search --count` skips a note with an empty body *before* matching,
while the default paths mode does not — a regex like `^$` therefore matches an
empty note in one mode and not the other. That is preserved, not repaired.

## Test seams

Package-level `var` function seams stay at package scope and keep their names.
`searchOpenExec` is swapped by `search_open_test.go` to avoid exec-replacing the
test process; folding it into the options struct or into a mode function would
break every test that swaps it. The same applies to `fzfLookPath` / `fzfCommand`.

## Verifying a decomposition

No behaviour change is permitted in a decomposition, so the checks are:

- `go test ./internal/cli` — the goldens under `internal/cli/testdata` are the
  output contract.
- `just lint-new` — every line of a decomposition is inside the ratchet's diff
  range, so each new function must land under funlen 100/50, cyclop 10,
  gocognit 20, nestif 4. Moving an oversized function to a new file without
  decomposing it fails the gate, by design.
- `just lint-full` — the report that shows the score actually moved. Decomposing
  `search` dropped the `internal/cli` gocognit score total from 1326 to 1094 and
  the package's violation count from 80 to 75.

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
`find` splits at five rules, into `validateBrokenLinkRequirements` (which
reports the two asked for without `--broken-links`) and
`validateBrokenOutputCombos` (which rejects the path-shaped modes a broken-link
report cannot produce). Not every command needs the parameter: `find` reads
`--broken-links` off the shared `*findFlags` it already holds, so its
`validate()` is a pure function of the struct and takes nothing.

**A command with no mutual-exclusion rules gets no `validate()`.** `new` is the
case: `--folder`, `--var`, `--edit`, and `--force` all compose, so an empty
method would be ceremony rather than the place a rule goes. Give the options
struct a comment saying so, so the absence reads as a decision. Refusals that
are properties of a *positional argument* rather than of the flag matrix stay
out of it too, as package-level functions (`validateRenameKeys`,
`validateNewTitle`).

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
third new file: `writePathList` and `writeSearchCounts` sit next to
`contextOutput` and `buildInteractiveLines`.

The threshold for creating the file at all is **more than one output form**.
`find` has three — the path list, `--show-broken`, and `--list-broken-targets` —
so `find_output.go` exists and holds all three plus `pinnedOrder`. `rename-key`
has one seven-line preview renderer, so it stays in `rename_key_run.go`, and
`repair`'s preview already lives in `repair_apply.go` and was not moved. A file
per renderer is not the goal; one home per command's rendering is.

**A surface more than one command shares lives with the surface, not with the
first command that needed it.** `queryFlagNames` and its only reader
`anyQueryFlagSet` are in `findflags.go`, beside the flag registrations they
enumerate, because five commands (`search`, `repair`, `rename-key`, `replace`,
`relink`) read them and the list must name every registered scoping flag or the
note-args / find-flags exclusion silently stops firing. The same rule sends a
type its helper files share out of the constructor file: `relinkIndex` and
`relinkTarget` are declared in `relink_index.go`, `relinkChange` in
`relink_rewrite.go`, `relinkResidue` in `relink_residue.go`, so `relink.go` is
the constructor and nothing else.

A single writer per output form is also how a format detail stops drifting.
`-0` is defined in exactly one function, `writePathList`, and both `find` and
`search` write their paths through it, so the documented
`bn find -0 | bn set --null -` pipeline and `bn search -0` cannot end up with
different separators — and neither can a command's interactive selection output
and its default listing. `TestFindSetNullPipeline` runs that pipeline end to end
rather than testing each half on its own.

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

For a **mutating** command the order decides what a refusal leaves on disk, not
just which error is printed. `new` resolves the destination, reads the template,
refuses an occupied path, and renders — all of it in `planNewNote`, before
`writeNewNote` calls `noteops.BeginOperation` — so every refusal happens while
no artifact exists. `BeginOperation` then names the destination *folder* as one
of its targets ahead of creating it, which is what stops a note the location
policy goes on to refuse from leaving an empty folder behind. A decomposition
that hoisted `os.MkdirAll` above `BeginOperation`, or that validated after it,
would still pass every output test. The `//bn:direct-write` marker travels with
that `os.MkdirAll` for the same reason: `internal/audit` accepts it on the line
above the call or in the enclosing function's doc comment, so a moved write must
carry its comment group with it or the audit fails.

`rename` states the same rule as a sequence: `planRename` runs all seven
refusals — title shape, target resolution, identical-title, source-title
uniqueness, path computation, destination occupancy, destination-title
uniqueness — and returns a `*noteops.RenamePlan`, and only `applyRename` opens
the journal. Splitting on that line is what keeps every `rename` refusal
artifact-free, and it is why `--dry-run` can be a single `if` between the two.

**A resolver that can legally select nothing returns `(value, ok, err)`, not a
nil sentinel.** `unset` and `rename` both offer an fzf picker, and an empty
selection is a silent success — not an error, and *not* a run that continues
with zero paths, because `noteops.BeginOperation` would then open a journal for
an operation the user cancelled. Extracting that branch is also what clears the
`nestif` the picker's `if len(noteArgs) == 0 { ... } else { ... }` costs, so
`resolveUnsetPaths` and `resolveRenameArgs` both report the cancellation through
a third return value and the runner answers it with a bare `return nil`. Reusing
`set`'s `resolveSetPaths` here would be wrong for exactly that reason: it
returns `(nil, nil)` on an empty selection and `set` deliberately proceeds.

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
  decomposing it fails the gate, by design. The inverse is also true and is
  worth knowing before you tidy: a pre-existing violation that merely *shifts*
  line number stays outside the range, because the gate anchors on added lines
  rather than on final position. Deleting the 110-line `NewNewCmd` closure moved
  `routeForTemplate` (cyclop 11) from line 197 to line 80 of the same file and
  the gate stayed green; cutting and pasting it into `new_run.go` would have
  failed. So leave a neighbouring violation where it sits unless you intend to
  decompose it in the same session.
- `just lint-full` — the report that shows the score actually moved. Decomposing
  `search` dropped the `internal/cli` gocognit score total from 1326 to 1094 and
  the package's violation count from 80 to 75.

Expect a decomposition to remove **more** issues than it removes constructors.
`unset`/`replace`/`rename`/`relink` together took the whole-tree total from 541
to 535 and `internal/cli` from 69 to 63 — six, not four, because the `nestif`
violations at `unset.go:71` (the picker branch) and `rename.go:52` (the
one-arg-vs-two-arg branch) lived *inside* the closures and left with them.
`issues.uniq-by-line` still collapses each constructor's cyclop score into its
gocognit report, so the constructors themselves are one issue each; a nested
block reported on its own line is a second, separate issue in the same function.
Measure it the way `search` and `find` were measured — `git worktree add
--detach /tmp/<name> HEAD`, run `golangci-lint run ./...` there and again in the
working tree — rather than reading a delta off two remembered numbers.

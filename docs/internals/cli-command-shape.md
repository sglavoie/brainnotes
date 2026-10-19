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
inputs and therefore directly testable. A refusal is spelled
`<cmd>: --a cannot be combined with --b`; two flags that only exclude each other
use `refuseCombinedFlags` rather than cobra's `MarkFlagsMutuallyExclusive`,
whose message reads like nothing else bn prints. Diagnostics and prompts go to
stderr, and warnings are spelled `<cmd>: warning: ...`.

Split the method when the rules outgrow one threshold-clean function
(`validateContextCombos`, `validateCountCombos`), with the guard clause inside
the helper so the caller stays a flat list of `if err := ...; err != nil`.
`find` splits at five rules, into `validateBrokenLinkRequirements` (which
reports the two asked for without `--broken-links`) and
`validateBrokenOutputCombos` (which rejects the path-shaped modes a broken-link
report cannot produce). Not every command needs the parameter: `find` reads
`--broken-links` off the shared `*findFlags` it already holds, so its
`validate()` is a pure function of the struct and takes nothing.

**One rule is enough to earn a `validate()`.** Sessions asked repeatedly whether
a method holding a single `if` was ceremony. It is not, and the distribution
settles it: eight commands now have a one-rule `validate()` (`unset`, `replace`,
`relink`, `rename-key`, `repair`, `open`, `graph`, and the shared
`linkListOptions`), two have enough to split the method (`find`, `search`), and
three have none (`new`, `undo`, `lint`). `related`'s eight rules are the
exception that proves the shape rather than a counterexample: they live in the
package-level `validateRelatedOptions`, which predates this contract and carries
a cyclop violation the ratchet pins in place, so it stays where it is.

The method is where the *second* rule goes, and a rule that starts as a
package-level function has to be found and moved when that happens, so the cost
of the method is one name and the cost of not having it is a decision every
future flag re-litigates. Refusals that are not about the flag matrix still stay
out of it (below).

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

**When two commands share a whole prologue, the shared thing is a run builder,
not a copied closure.** `links` and `backlinks` are the same command up to one
lookup: both take an optional note argument, fall back to the fzf picker, refuse
an empty vault, open the vault, load the notes, and build the graph —
thirteen duplicate windows of it. `linklist.go` is that prologue once:
`linkListOptions` (the `--null`/root `--verbose` pair and their one rule),
`resolveLinkListTarget` returning `(target, ok, err)`, where an empty vault or
an empty pick is `ErrNothingSelected` (exit 1, see `outcome.go`), and `newLinkListRun` returning the `linkListRun{target, notes,
graph}` both runners then read. What is left in `links_run.go` and
`backlinks_run.go` is only what actually differs: which direction of the graph
is read, and how the verbose form is rendered. The verbose renderers were
deliberately **not** pulled into the shared file — they share no code, and
moving them would have put `writeVerboseBacklinks`'s existing cyclop violation
inside the ratchet's diff range (see *Verifying a decomposition*).

A single writer per output form is also how a format detail stops drifting.
`-0` is defined in exactly one function, `writePathList`, and five commands
write their paths through it — `find`, `search`, `related --paths`,
`open --print`, and the shared link listing behind `links`/`backlinks` — so the
documented `bn find -0 | bn set --null -` pipeline and `bn search -0` cannot end
up with different separators, and neither can a command's interactive selection
output and its default listing. `open --print -0` prints a single path and still
goes through it, because a one-element list is exactly the case where an inlined
`fmt.Fprintf(out, "%s%c", ...)` looks harmless and drifts.
`TestFindSetNullPipeline` runs that pipeline end to end rather than testing each
half on its own.

**A shared prologue is a function, not a context object.** Sessions 5 and 7
asked whether a `runContext{cmd, args, cfg, root}` should collapse the
five-to-seven-parameter `new<cmd>Run` constructors, and left the call to this
one. The answer is no, and the question is closed. `newLinkListRun` is the
strongest case that could have been made for it — two commands, an identical
prologue, four values to carry — and it came out better as a named function
returning a struct scoped to *those two commands* than it would have as a
vocabulary every command has to learn. A generic `runContext` bundles `cmd` and
`args` (which the runner already receives) with `cfg` and `root` (which most
runners derive in two lines) and buys nothing at the call site, while costing
every reader an indirection to find out which fields a given command actually
uses. Share the prologue that two commands genuinely have in common; do not
invent a container for prologues that merely rhyme.

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

`undo` is the case where the order that matters is on the *error* path. Its
restore is a single `noteops.Undo` call and was not touched; what was extracted
is `describeUndoError`, and its three cases are ordered: a conflict renders the
per-path report, a `noteops.IsSnapshotFailure` is wrapped bare because the
partial-failure guidance would be actively wrong for a change that already
landed on disk, and only the remaining errors get the guidance appended.
Reordering those cases silently changes what a user is told to do after a failed
restore.

`rename` states the same rule as a sequence: `planRename` runs all seven
refusals — title shape, target resolution, identical-title, source-title
uniqueness, path computation, destination occupancy, destination-title
uniqueness — and returns a `*noteops.RenamePlan`, and only `applyRename` opens
the journal. Splitting on that line is what keeps every `rename` refusal
artifact-free, and it is why `--dry-run` can be a single `if` between the two.

**A read/write boundary that lives in control flow has to be restated as
structure.** `bn related` is read-only except on one path: an accepted
interactive selection calls `setup.store.RecordOpened`, and that write is the
whole point of the mode, because it is what later rankings read back as the
recall signal (`docs/internals/recall-context.md`). Inside the old closure the
boundary was an `if options.interactive` two thirds of the way down a
seventy-line body. The decomposition makes it a function: `runRelatedInteractive`
is the only writer and is reachable only from that branch,
`runRelatedListing` holds all three read-only renderings, and the doc comment on
the writer says which line writes and why. A reviewer asking "can a
non-interactive `bn related` touch `.bnotes`" now reads one signature instead of
tracing a body. `bn context` ranks through the same corpus and writes nothing at
all, including on its `--agent` path.

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
  decomposing it fails the gate, by design. The unit of judgement is the
  **function**: change one line of a function's body and the whole function is
  measured, because a complexity score is reported on the declaration line that
  a body-only edit never touches. So a function you edit at all has to be under
  the thresholds — you cannot make an existing one worse and stay green.
  The inverse is also true and is worth knowing before you tidy: a pre-existing
  violation in a function you did *not* touch stays outside the gate, even when
  it merely *shifts* line number. Deleting the 110-line `NewNewCmd` closure moved
  `routeForTemplate` (cyclop 11) from line 197 to line 80 of the same file and
  the gate stayed green; cutting and pasting it into `new_run.go` would have
  failed. So leave a neighbouring violation where it sits unless you intend to
  decompose it in the same session.
- `just lint-full` — the report that shows the score actually moved. Decomposing
  `search` dropped the `internal/cli` gocognit score total from 1326 to 1094 and
  the package's violation count from 80 to 75.
- `just lint-phase-gate` — the verdict, as opposed to the score. It reads the
  whole-tree report and fails if **any non-test file under `internal/cli`** is
  named on an issue line, tolerating every violation outside that package, so it
  answers "is production CLI code under the thresholds" without requiring the
  tree-wide baseline to reach zero. It has to read `lint-full` rather than
  `lint-new`: this is old code and the ratchet cannot see it, so a gate built on
  `lint-new` would pass on day one.
  It selects by **path**, and that is a repair. The first version matched the
  function name in the message and only fired on a `New*Cmd`/`new*Cmd` name,
  which made it green at the end of Phase 1 while 21 production violations sat in
  the same package on ordinary helpers — `runValues` (gocognit 42), `applyRelink`
  (26), `runMove` (25), `validateRelatedOptions` (cyclop 18). A path match also
  sees `nestif`, whose message names an `if` condition and no function at all and
  so was invisible to any name match.
- `just lint-phase-gate-selftest` — the gate's own regression test
  (`scripts/lint_phase_gate_selftest.py`): a throwaway module with one
  over-threshold *non-constructor* function in production `internal/cli` code,
  proving the gate exits non-zero and names the file — then proving a violation
  outside `internal/cli` and one in an `internal/cli` `_test.go` are still
  tolerated. It copies this repository's `.golangci.yml` **and** its `justfile`,
  so it runs the thresholds and the recipe in force rather than a
  re-implementation. Like `lint-ratchet-selftest` it is outside `just ci` (only
  one golangci-lint may run at a time) and wired into `.github/workflows/ci.yml`
  as its own step, alongside the gate itself.

**A gate that checks less than it reports on will certify the difference.**
Phase 1 was closed with `lint-phase-gate` green and 21 complexity violations
still in production `internal/cli` code, because the gate looked only at command
constructors while the criterion it stood for was the package. Decomposing those
21 is what the section below records; widening the gate to the package, and
giving it a self-test that fails when it stops enforcing, is what makes the next
one impossible to close the same way. When a gate and the criterion it is named
after disagree, the gate is the thing that is wrong.

**The gate names constructors the phase list may not.** Finishing Phase 1 with
only the six commands it named would have left `lint-phase-gate` red:
`NewDoctorCmd` (cyclop 11), `NewGraphCmd` (cyclop 12), and `newContextCmd`
(gocognit 21) were also in the report, small enough to have escaped the
size-ranked survey the phase list was built from. All three were decomposed too,
which is the right response — narrowing the gate to the six named commands would
have made it pass by describing less than it checks. If a phase list and a gate
disagree, widen the work, not the exemption.

Expect a decomposition to remove **more** issues than it removes constructors.
`unset`/`replace`/`rename`/`relink` together took the whole-tree total from 541
to 535 and `internal/cli` from 69 to 63 — six, not four, because the `nestif`
violations at `unset.go:71` (the picker branch) and `rename.go:52` (the
one-arg-vs-two-arg branch) lived *inside* the closures and left with them.
`issues.uniq-by-line` still collapses each constructor's cyclop score into its
gocognit report, so the constructors themselves are one issue each; a nested
block reported on its own line is a second, separate issue in the same function.
The nine constructors of the final Phase 1 session repeated the pattern: 535 to
522 whole-tree and 35 to 22 in `internal/cli` source — thirteen, not nine,
because the `nestif` blocks at `links.go:41`, `backlinks.go:52`, `undo.go:64`
and `related.go:97` were all inside the closures and left with them.
Measure it the way `search` and `find` were measured — `git worktree add
--detach /tmp/<name> HEAD`, run `golangci-lint run ./...` there and again in the
working tree — rather than reading a delta off two remembered numbers.

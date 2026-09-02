# bn - Brain Notes CLI

binary_name := "bn"
pkg := "./cmd/bn"

# bn-vault-guard is the private companion process the LaunchAgent runs. It is
# installed alongside `bn` because a vault whose guard is a version behind its
# CLI is a vault whose safety engine and mutation paths disagree about what the
# on-disk state means. It is deliberately absent from `just --list`, from
# `just build`, and from the user documentation: it is not a supported command
# and has no user-tunable safety policy.
guard_binary_name := "bn-vault-guard"
guard_pkg := "./cmd/bn-vault-guard"

source_checkout_root := justfile_directory()
source_checkout_ldflags := "-X github.com/sglavoie/sglavoie_brainnotes/internal/cli.sourceCheckoutRoot=" + source_checkout_root

# The anchor for the new-code-only complexity gate: the commit
# docs/internals/refactor-baseline.md measured its 281/271 ratchet floor at.
# Everything after it is new code and must satisfy .golangci.yml.
#
# This is a commit and not the branch name `main`, and that is the whole point.
# `--new-from-merge-base=main` is what the audit called for, but work in this
# repo lands directly on `main`, so once a commit exists `git merge-base main
# HEAD` IS `HEAD`, the diff range is empty, and the gate passes on anything.
# That was measured, not assumed: a committed gocognit-28 function is reported
# as `0 issues.` when the anchor names the branch it was committed to.
#
# Naming the baseline commit fixes that without giving up the branch case —
# `merge-base` of an ancestor commit and any descendant is that commit, so this
# resolves to the baseline on `main` AND on a feature branch cut from it.
#
# Raise it only together with the floor recorded in the baseline doc; lowering
# it can only re-admit violations that are already excluded.
ratchet_base := "61f2296e7df02143c9542769debd45db93ebbb0e"

# The one place the golangci-lint version is written. `just install-lint` reads
# it and CI calls that recipe, so the version cannot drift between the workflow
# and the developer machine.
golangci_version := "v2.13.2"

# npm dependencies can ship Go source: `eslint` pulls in `flatted`, which
# vendors a Go port under `node_modules/flatted/golang`. Go's `./...` matcher
# skips `testdata`, `vendor`, and dot/underscore directories but NOT
# `node_modules`, so after `npm ci` the whole Go toolchain — build, vet, test,
# gofmt, golangci-lint — silently treats that package as ours. It moved the
# recorded complexity floor from 552 to 555 before this filter existed.
#
# `internal/audit`'s repository walk already skips `node_modules` by name; this
# is the same decision applied to the toolchain. The two substitutions below are
# the only way to express it: neither `go` nor `gofmt` has an exclude flag.
go_packages := "$(go list ./... | grep -v '/node_modules/')"
gofmt_check := "gofmt -l . | grep -v '^node_modules/' || true"

# Show available recipes
help:
    @just --list

# Build the binary in the current directory
build:
    @echo "Building {{ binary_name }}..."
    go build -ldflags '{{source_checkout_ldflags}}' -o {{ binary_name }} {{ pkg }}
    @echo "Built {{ binary_name }}"

# Install the binary into $GOPATH/bin via `go install`
install:
    @echo "Installing {{ binary_name }}..."
    go install -ldflags '{{source_checkout_ldflags}}' {{ pkg }}
    @echo "Installed {{ binary_name }}"
    @echo "Installing {{ guard_binary_name }}..."
    go install -ldflags '{{source_checkout_ldflags}}' {{ guard_pkg }}
    @echo "Installed {{ guard_binary_name }}"

# Run the full test suite
test:
    @echo "Running tests..."
    go test {{ go_packages }}

# Build the browser JavaScript bundle and the stylesheet
web-build:
    npm run build:web
    go run ./internal/web/cssbuild/gen

# Type-check, lint, and build the browser assets
web-check:
    npm run typecheck:web
    npm run lint:web
    npm run test:web
    npm run build:web
    go run ./internal/web/cssbuild/gen

# Run go vet across all packages
vet:
    go vet {{ go_packages }}

# Install the pinned golangci-lint into $GOPATH/bin
install-lint:
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@{{ golangci_version }}

# A missing binary must fail, not skip: a gate that quietly does nothing when a
# tool is absent reads as green and enforces nothing.
_require-golangci:
    #!/usr/bin/env bash
    set -euo pipefail
    if command -v golangci-lint >/dev/null 2>&1; then
        exit 0
    fi
    echo "golangci-lint not found on PATH; the complexity ratchet cannot run." >&2
    echo "Install the pinned version with:  just install-lint" >&2
    exit 1

# The complexity ratchet. Fails on any funlen/cyclop/gocognit/nestif violation
# in code added or changed since `ratchet_base`; the violations the baseline
# recorded are out of the diff range by construction, so they do not block.
#
# This is wired into both `lint` and `ci`. Those two recipes duplicate their
# gofmt and vet steps rather than sharing them, so a gate added to only one of
# them would be half-enforced.
#
# `just --list` shows only the last line of the comment block above a recipe,
# which is why the rationale sits above a blank line and a one-line summary sits
# directly on top of the recipe.

# Fail on complexity violations in code added since the ratchet baseline
lint-new: _require-golangci
    golangci-lint run --new-from-merge-base={{ ratchet_base }} ./...

# Report every complexity violation in the tree, not just new ones. This is a
# measurement, not a gate — it is deliberately absent from `lint` and `ci`, and
# exits 0 with a nonzero count so the count can be read from a passing run. The
# number to compare against is in docs/internals/refactor-baseline.md.

# Report the full complexity violation count across the whole tree
lint-full: _require-golangci
    golangci-lint run --issues-exit-code=0 ./...

# The Phase 1 completion gate: no Cobra command constructor may still carry a
# complexity violation. It reads the whole-tree report rather than the ratchet,
# because the constructors it is about are *old* code — `lint-new` cannot see
# them by construction, and a gate built on it would pass on day one.
#
# It is deliberately a whole-tree scan with a narrow verdict: every other
# violation in the report is tolerated, so the baseline recorded in
# docs/internals/refactor-baseline.md does not have to reach zero for this to
# pass. Only lines naming a `New*Cmd`/`new*Cmd` function fail it.
#
# Two limits are worth knowing before trusting a green run. It matches on the
# function name in the message, so it covers cyclop, gocognit, and funlen (which
# all name their function) but not nestif, whose message names an `if` condition
# and no function at all — a constructor deep enough to trip nestif alone would
# slip through, though in practice it trips gocognit first. And the match is
# textual, so a message that merely mentions such a name fails the gate; that
# error is in the safe direction.
#
# Not wired into `lint` or `ci`: `lint-new` is what stops a *new* violation, and
# only one golangci-lint may run at a time on a machine, so this stays a
# separately invoked gate like `lint-full`.

# Fail if any Cobra `New*Cmd` constructor still has a complexity violation
lint-phase-gate: _require-golangci
    #!/usr/bin/env bash
    set -euo pipefail
    report=$(golangci-lint run --issues-exit-code=0 ./... 2>&1)
    # Real issue lines are `path.go:LINE:COL: message (linter)` and start in
    # column 1; the echoed source context below each one is indented, so
    # anchoring on that shape keeps a constructor's own source out of the match.
    # The location prefix is consumed before the name is looked for, so a path
    # containing "cmd" cannot trigger it.
    hits=$(printf '%s\n' "$report" | grep -iE '^[^[:space:]]+\.go:[0-9]+:[0-9]+: .*(^|[^a-z0-9_])new[a-z0-9_]*cmd([^a-z0-9_]|$)' || true)
    if [ -n "$hits" ]; then
        count=$(printf '%s\n' "$hits" | wc -l | tr -d ' ')
        echo "phase gate: $count complexity violation(s) still on a command constructor:"
        printf '%s\n' "$hits"
        echo
        echo "Decompose each one per docs/internals/cli-command-shape.md: the constructor keeps"
        echo "Use/Short/Long/Args/flags and a one-line RunE, and the behaviour moves to <cmd>_run.go."
        exit 1
    fi
    echo "phase gate: no command constructor carries a complexity violation."

# Format Go code with gofmt
fmt:
    @echo "Formatting code..."
    gofmt -w .

# Check formatting, run vet, and gate new code on complexity
lint:
    #!/usr/bin/env bash
    set -euo pipefail
    out=$({{ gofmt_check }})
    if [ -n "$out" ]; then
        echo "gofmt issues:"
        echo "$out"
        exit 1
    fi
    go vet {{ go_packages }}
    just lint-new
    just web-check
    echo "Lint clean"

# Run the full CI validation envelope (fmt, vet, complexity ratchet, tests, web, bundle drift)
ci:
    #!/usr/bin/env bash
    set -euo pipefail
    out=$({{ gofmt_check }})
    if [ -n "$out" ]; then
        echo "gofmt issues:"
        echo "$out"
        exit 1
    fi
    go vet {{ go_packages }}
    just lint-new
    go test {{ go_packages }}
    npm run typecheck:web
    # The TypeScript half of the complexity ratchet. `ci` inlines the frontend
    # steps rather than calling `just web-check`, so — exactly like `lint-new`
    # above — a gate wired into only one of the two recipes is half-enforced.
    npm run lint:web
    npm run test:web
    bundle=internal/web/static/app.js
    if [ ! -f "$bundle" ]; then
        echo "generated bundle missing: $bundle"
        exit 1
    fi
    before=$(git hash-object "$bundle")
    npm run build:web
    after=$(git hash-object "$bundle")
    if [ "$before" != "$after" ]; then
        echo "generated bundle drift detected in $bundle: the on-disk bundle is not what the current TypeScript source produces"
        git --no-pager diff -- "$bundle"
        exit 1
    fi
    echo "CI clean"

# `metrics` is deliberately absent from `just ci` and from `just lint`: it is a
# report, not a gate. It reads the repository and writes only to stdout.
# Compare its output against docs/internals/refactor-baseline.md, which is the
# committed baseline every refactoring session measures against.
#
# `-B` is load-bearing, not a style choice: metrics.py imports sibling
# metrics_*.py modules, and without it Python would drop scripts/__pycache__/
# into the working tree. That directory is ignored only by a *user's global*
# gitignore, never by this repo's — the hazard the .gitignore comment already
# warns about — so on any other machine a read-only report would leave
# untracked files behind.

# Report the refactoring measurements (file size, complexity, duplication, dead code)
metrics:
    @python3 -B scripts/metrics.py

# Remove build artifacts
clean:
    @echo "Cleaning..."
    rm -f {{ binary_name }}
    @echo "Clean complete"

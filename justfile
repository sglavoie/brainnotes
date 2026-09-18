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

# bn-vault-sync is the second private companion, and it is installed on the same
# terms and for the same reason: it applies the peer's packages through the same
# import path `bn sync` uses, so a companion a version behind its CLI is two
# processes disagreeing about what a package means. It is a separate process from
# the guard rather than another loop inside it because a vault that synchronizes
# still needs its content history protected when this one is not installed.
sync_binary_name := "bn-vault-sync"
sync_pkg := "./cmd/bn-vault-sync"

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
    @echo "Installing {{ sync_binary_name }}..."
    go install -ldflags '{{source_checkout_ldflags}}' {{ sync_pkg }}
    @echo "Installed {{ sync_binary_name }}"

# Run the full test suite
test:
    @echo "Running tests..."
    go test {{ go_packages }}

# `$BNOTES_VAULT` is usually set in a developer's shell and it overrides a `cd`,
# so `cd $scratch && bn rename-key ...` runs against the real vault. That is not
# hypothetical: it renamed a frontmatter key in 214 real notes once, and the
# recovery left two commits and two journal entries to clean up by hand.
#
# So a mutating `bn` command must never be verified by hand against an ambient
# vault. This recipe is the sanctioned way: it scrubs `$BNOTES_VAULT`, creates a
# throwaway vault under the system temp directory, asks `bn config` which root it
# actually resolved, and refuses (exit 2) unless that root is the sandbox.
#
# The test half of the same rule is `internal/vaultfixture`, which states the
# three refusals once as `IsolationError` and hands out the disposable Git-backed
# vaults, pinned clock, and loopback ports that Go fixtures build on;
# `internal/cli/vault_isolation_test.go` delegates to it. `just test-e2e` is the
# third caller and invokes `scripts/vault_sandbox.py` directly, so the browser
# suite's server goes through the same guard rather than through a JavaScript
# re-implementation of it.

# Run one `bn` command against a throwaway sandbox vault
vault-sandbox *ARGS:
    python3 scripts/vault_sandbox.py {{ ARGS }}

# The real-browser gate. It builds a throwaway `bn` and a disposable vault pair
# under the system temp directory, launches `bn serve` through
# `scripts/vault_sandbox.py` on a reserved loopback port, and drives Chromium
# against it. What it proves is what Vitest/jsdom structurally cannot: that the
# committed `internal/web/static/app.js` the binary embeds actually executes,
# that server-rendered HTML parses and its wikilinks navigate, that a quick
# capture typed into the browser lands in the fixture vault and nowhere else, and
# that nothing the page loads leaves the fixture origin.
#
# Deliberately **not** in `just ci`, unlike every other frontend gate. It needs a
# browser binary that `npm ci` does not install (`npm run test:e2e:install`), and
# a CI recipe that silently skips when a dependency is missing is a gate that
# stops gating. Keeping it a named recipe means its absence is visible.
#
# Serving the *embedded* bundle rather than a freshly rebuilt one takes two
# separate measures, and neither is sufficient alone. The suite's `go build` is
# unstamped, which removes the checkout path a `just build` binary bakes in — but
# an unstamped binary then falls back to searching every ancestor of its working
# directory, so `playwright.config.js` also sets that directory to the fixture
# and `prepare.mjs` refuses a fixture root that lies under this checkout. Drop
# either one and `bn serve` regenerates `internal/web/static/app.js` into the
# working tree and serves that, with every spec still green.

# Run the real-browser end-to-end suite (needs `npm run test:e2e:install` once)
test-e2e:
    npm run test:e2e

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
# in a function added or changed since `ratchet_base`; a violation in a function
# nobody touched is tolerated, so the baseline recorded in
# docs/internals/refactor-baseline.md does not block.
#
# It runs through scripts/lintratchet rather than through
# `golangci-lint --new-from-merge-base` because that flag filters by changed
# *line*, and cyclop, gocognit and funlen all report a function's score on its
# *declaration* line — so editing only a function's body left its own violation
# outside the diff range and the gate passed while the function got worse. That
# was not hypothetical: `contextOutput` in internal/cli/search_output.go reached
# gocognit 21 that way while this recipe reported `0 issues.` The unit of
# judgement is now the enclosing function, mapped with go/parser.
#
# `just lint-ratchet-selftest` is the regression test for exactly that hole.
#
# This is wired into both `lint` and `ci`. Those two recipes duplicate their
# gofmt and vet steps rather than sharing them, so a gate added to only one of
# them would be half-enforced.
#
# `just --list` shows only the last line of the comment block above a recipe,
# which is why the rationale sits above a blank line and a one-line summary sits
# directly on top of the recipe.

# Fail on complexity violations in functions changed since the ratchet baseline
lint-new: _require-golangci
    go run ./scripts/lintratchet -base {{ ratchet_base }}

# The gate above is the only thing standing between the recorded floor and a
# function that quietly gets worse, and its first version enforced nothing
# against a body-only edit while reading green. So it gets a test that can fail:
# a throwaway repository, one function taken from under a threshold to over it
# by editing its body and nothing else, and the assertion that the ratchet exits
# non-zero — plus the complementary assertion that an untouched violation in the
# same edited file is still tolerated. Nothing in this repository is read except
# `.golangci.yml`, so the self-test cannot pass against thresholds that are not
# the ones in force.

# Prove the ratchet still catches a body-only complexity increase
lint-ratchet-selftest: _require-golangci
    @python3 -B scripts/lint_ratchet_selftest.py

# Report every complexity violation in the tree, not just new ones. This is a
# measurement, not a gate — it is deliberately absent from `lint` and `ci`, and
# exits 0 with a nonzero count so the count can be read from a passing run. The
# number to compare against is in docs/internals/refactor-baseline.md.

# Report the full complexity violation count across the whole tree
lint-full: _require-golangci
    golangci-lint run --issues-exit-code=0 ./...

# The Phase 1 completion gate: **no production file under internal/cli may carry
# a complexity violation** — not the Cobra constructors alone. It reads the
# whole-tree report rather than the ratchet, because the code it is about is
# *old* code: `lint-new` cannot see it by construction, and a gate built on it
# would pass on day one.
#
# It selects by *path*, not by function name. The earlier version matched lines
# naming a `New*Cmd`/`new*Cmd` function, which made the gate green while 21
# production violations sat in the same package on ordinary helpers — the
# constructors were the visible half of Phase 1, never the whole of it. Matching
# the path also picks up `nestif`, whose message names an `if` condition and no
# function at all, so it was invisible to any name match.
#
# The verdict stays narrow in the other direction: violations outside
# internal/cli are tolerated, so the tree-wide baseline recorded in
# docs/internals/refactor-baseline.md does not have to reach zero for this to
# pass. `_test.go` files are excluded — a test's table-driven bulk is not what
# this is about.
#
# The filter is byte-for-byte the one the Phase 1 completion contract runs, so a
# green gate and a green contract check cannot mean different things. Real issue
# lines are `path.go:LINE:COL: message (linter)` and start in column 1; the
# echoed source context below each one is indented, so anchoring on `^internal/`
# keeps a violating function's own source out of the match.
#
# Not wired into `lint` or `ci`: `lint-new` is what stops a *new* violation, and
# only one golangci-lint may run at a time on a machine, so this stays a
# separately invoked gate like `lint-full`.

# Fail if any non-test file under internal/cli still has a complexity violation
lint-phase-gate: _require-golangci
    #!/usr/bin/env bash
    set -euo pipefail
    report=$(golangci-lint run --issues-exit-code=0 ./... 2>&1)
    hits=$(printf '%s\n' "$report" | awk '/^internal\/cli\/.*\.go:/ && $1 !~ /_test\.go:/ {print}')
    if [ -n "$hits" ]; then
        count=$(printf '%s\n' "$hits" | wc -l | tr -d ' ')
        echo "phase gate: $count complexity violation(s) still in production internal/cli code:"
        printf '%s\n' "$hits"
        echo
        echo "Decompose each one per docs/internals/cli-command-shape.md: a constructor keeps"
        echo "Use/Short/Long/Args/flags and a one-line RunE, the flag matrix lives in one"
        echo "validate(), and each mode is one named function in <cmd>_run.go."
        exit 1
    fi
    echo "phase gate: no production file under internal/cli carries a complexity violation."

# The gate above certified Phase 1 while 21 production violations sat in
# internal/cli, because it only looked at command constructors. So it gets a test
# that can fail: a throwaway module carrying one over-threshold *non-constructor*
# function in production internal/cli code, and the assertion that the gate exits
# non-zero and names the file — plus the complementary assertions that a
# violation outside internal/cli and one in an internal/cli `_test.go` are still
# tolerated. It copies this repository's `.golangci.yml` *and* this justfile, so
# it runs the thresholds and the recipe actually in force rather than a
# re-implementation of them.
#
# Outside `just ci` for the same reason as lint-ratchet-selftest: it runs
# golangci-lint against a synthetic tree, and only one golangci-lint may run at a
# time on a machine. Wired into .github/workflows/ci.yml as its own step.

# Prove the phase gate catches a violating non-constructor function
lint-phase-gate-selftest: _require-golangci
    @python3 -B scripts/lint_phase_gate_selftest.py

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

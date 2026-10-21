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
# import path the web workspace's sync review uses, so a companion a version behind its CLI is two
# processes disagreeing about what a package means. It is a separate process from
# the guard rather than another loop inside it because a vault that synchronizes
# still needs its content history protected when this one is not installed.
sync_binary_name := "bn-vault-sync"
sync_pkg := "./cmd/bn-vault-sync"

source_checkout_root := justfile_directory()
source_checkout_ldflags := "-X github.com/sglavoie/brainnotes/internal/cli.sourceCheckoutRoot=" + source_checkout_root

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
race_packages := "$(go list ./internal/vault/... ./internal/vaultgit/... ./internal/noteops/... ./internal/web/... ./internal/vaultsyncd/... ./internal/checkpoint/... | grep -v '/node_modules/')"

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
    @echo ""
    @echo "Note: running LaunchAgents keep the previous companion binaries until restarted."
    @echo "Restart them so all three run build $(git rev-parse --short=12 HEAD 2>/dev/null || echo unknown):"
    @echo '  launchctl kickstart -k "gui/$(id -u)/com.sglavoie.brainnotes-vault-guard"'
    @echo '  launchctl kickstart -k "gui/$(id -u)/com.sglavoie.brainnotes.vault-sync"'
    @echo "Then check: bn-vault-sync -check (build_mismatch) and bn doctor (stderr warnings)."

# Run the full test suite
test:
    @echo "Running tests..."
    go test {{ go_packages }}

# The race detector over the packages that share state across goroutines: the
# lock and transaction layer, the Git safety engine and its watcher, note
# operations, the web server, the sync daemon and the checkpoint barrier. It is
# several times slower than `just test`, so it is not part of `just ci`; CI runs
# it as its own job. The package list goes through the same `node_modules`
# filter as `go_packages`.

# Run the concurrency-heavy packages under the race detector
test-race:
    go test -race {{ race_packages }}

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

# The README animations. Both drive a throwaway copy of `scripts/demo/vault`
# under the system temp directory: Playwright tours `bn serve` (started through
# `scripts/vault_sandbox.py`) and screenshots each step, and the terminal steps
# run for real and render through `scripts/demo/render_terminal_svg.py`. Needs
# uv and jq on PATH, and Chromium from `npm run test:e2e:install`.

# Re-record docs/assets/demo-web.svg and demo-cli.svg (TARGET: all, web or cli)
demo TARGET="all":
    node scripts/demo/record.mjs {{ TARGET }}

# The real-transport gate, and `just test-e2e`'s counterpart for synchronization.
# It provisions the pinned Syncthing release, builds two disposable vaults under
# the system temp directory, gives each its own identity, database, loopback
# listener and private API key, and points them at one send-only and one
# receive-only staging channel per device — never at a vault directory. What it
# proves is what `internal/vaultsyncd`'s in-process fixture structurally cannot:
# that packages produced by `internal/vaultexport` survive a transport nobody
# here wrote, arriving late, out of order, across a disconnect and across a
# restart; that an incomplete or altered package is carried faithfully and still
# never applied; and that removing a package is the sender's to do, because a
# receiver's deletion of its own copy is a local deviation the owner overrules.
#
# Deliberately **not** in `just ci`, on the same terms as `just test-e2e`: the
# scenarios spend their time waiting on two other processes, and the binary has
# to be downloaded once. The `synctransport` build tag is on the test files only,
# so gofmt, `go vet` and the complexity ratchet still cover `internal/syncthingbin`
# and `internal/synctransport` on every ordinary run.
#
# Provisioning happens here rather than inside the tests so that one resolution
# serves the whole run: `internal/syncthingbin` reads its cache directory at
# package initialization, before `internal/vaultfixture` rewrites HOME, so a test
# that resolves for itself still caches outside the fixture — but it pays for its
# own check every time. The resolved path crosses into the test binary in the
# environment, and a test that cannot get a binary at all fails rather than skips.

# Run the real two-process Syncthing transport suite
test-sync-transport:
    #!/usr/bin/env bash
    set -euo pipefail
    export BNOTES_SYNCTHING_BIN="$(go run ./scripts/syncthingbin)"
    echo "syncthing: ${BNOTES_SYNCTHING_BIN}"
    go test -tags synctransport -count=1 -timeout 1800s -run '^TestRealTransport' ./internal/vaultsyncd/

# The repeatable distributed recovery gate. The first run composes all focused
# synchronization package tests, including the seeded
# randomized offline schedule with exact capacity failure, dependent conflict,
# competing resolution, rewritten history, anti-echo, and real child-process
# death boundaries. The second run keeps the transport honest with two actual
# Syncthing processes, including disconnect, restart, corruption, and incomplete
# delivery scenarios. That transport half fails rather than skipping when the
# pinned Syncthing binary is unavailable.

# Run repeatable distributed synchronization and recovery fault scenarios
test-sync-recovery:
    #!/usr/bin/env bash
    set -euo pipefail
    go test -tags syncrecovery -count=1 -timeout 1800s ./internal/vaultsyncd/ ./internal/vaultimport/ ./internal/vaultexport/ ./internal/vaultsync/
    just test-sync-transport

# Build the browser JavaScript bundle and the stylesheet
web-build:
    npm run build:web
    go run ./internal/web/cssbuild/gen

# Type-check, lint, and build the browser assets
web-check: _web-static web-build

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
# This is wired into both `lint` and `ci`, as a dependency of each next to the
# shared `_go-static` gate.
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

# staticcheck and unused over the whole tree, every build tag included. Unlike
# the ratchet above it has no baseline: the tree is clean, so any finding fails.
# It reads its own config, .golangci-staticcheck.yml, rather than .golangci.yml,
# because the ratchet, `lint-full` and `lint-phase-gate` all count every issue
# that config produces as a complexity violation. It runs the same pinned
# golangci-lint, so there is no second tool version to keep in step.

# Fail on any staticcheck or unused finding across every build tag
lint-static: _require-golangci
    golangci-lint run -c .golangci-staticcheck.yml ./...

# Format Go code with gofmt (skipping node_modules, as `gofmt_check` does)
fmt:
    #!/usr/bin/env bash
    set -euo pipefail
    echo "Formatting code..."
    files=$({{ gofmt_check }})
    if [ -n "$files" ]; then
        printf '%s\n' "$files" | while IFS= read -r f; do gofmt -w "$f"; done
    fi

# `lint` and `ci` share their static gates through the two private recipes
# below rather than each inlining them, so a gate added to one cannot be
# half-enforced by being forgotten in the other. The one deliberate difference
# is the bundle step: `lint` goes through `web-check`, which *rebuilds* the
# committed bundles, while `ci` only *checks* them with `check:web-bundles`, so
# a stale bundle fails `ci` instead of being silently regenerated.

# gofmt (skipping node_modules) and go vet over the filtered package list
_go-static:
    #!/usr/bin/env bash
    set -euo pipefail
    out=$({{ gofmt_check }})
    if [ -n "$out" ]; then
        echo "gofmt issues:"
        echo "$out"
        exit 1
    fi
    go vet {{ go_packages }}

# Frontend typecheck, the TypeScript complexity ratchet, and Vitest
_web-static:
    npm run typecheck:web
    npm run lint:web
    npm run test:web

# Check formatting, run vet and staticcheck, and gate new code on complexity
lint: _go-static lint-new lint-static web-check
    @echo "Lint clean"

# Run the full CI validation envelope (fmt, vet, complexity ratchet, staticcheck, tests, web, bundle drift)
ci: _go-static lint-new lint-static
    #!/usr/bin/env bash
    set -euo pipefail
    go test {{ go_packages }}
    just _web-static
    npm run check:web-bundles
    # The browser suite's static gate: every spec assigned to exactly one suite
    # in internal/web/e2e/suites.js, the checker's own tests, and ESLint over
    # the specs and playwright.config.js. It needs no browser, unlike the suite.
    npm run lint:e2e
    # Every scripts/test_*.py, not a hand-kept list: the vault-safety evidence
    # verifier's own tests sat outside this recipe once, so a regression in the
    # verifier would have read as a green run.
    python3 -B -m unittest discover -s scripts -p 'test_*.py'
    # A go.mod or go.sum that `go mod tidy` would rewrite is drift a reviewer
    # cannot see in the diff that caused it.
    go mod tidy -diff
    echo "CI clean"

# The statement-coverage snapshot behind the test-volume ratio in
# docs/internals/refactor-baseline.md. Like `metrics`, a measurement and not a
# gate. It goes through `go_packages` because a bare `go test -cover ./...`
# also measures the Go package `node_modules` ships. The report is written to a
# temporary file first, so a failing package leaves the committed snapshot as
# it was.

# Regenerate docs/internals/coverage-baseline.txt (go test -cover per package)
coverage:
    #!/usr/bin/env bash
    set -euo pipefail
    out=docs/internals/coverage-baseline.txt
    tmp=$(mktemp)
    trap 'rm -f "$tmp"' EXIT
    report=$(go test -cover {{ go_packages }})
    {
        echo "# go test -cover over the justfile's go_packages — coverage baseline"
        echo "#"
        echo "# Captured at HEAD $(git rev-parse --short HEAD) on $(date -u +%Y-%m-%d). The test ratio in"
        echo "# docs/internals/refactor-baseline.md is a volume number; this is the"
        echo "# statement-coverage number behind it. A snapshot, not a gate."
        echo "#"
        echo "# A line with no \"ok\" prefix and 0.0% is a package with no test files of"
        echo "# its own. All packages passed."
        echo "#"
        echo "# Regenerate with: just coverage"
        echo
        printf '%s\n' "$report"
    } > "$tmp"
    mv "$tmp" "$out"
    trap - EXIT
    echo "wrote $out"

# `metrics` is deliberately absent from `just ci` and from `just lint`: it is a
# report, not a gate. It reads the repository and writes only to stdout.
# Compare its output against docs/internals/refactor-baseline.md, which is the
# committed baseline every refactoring session measures against.
#
# `-B` keeps a read-only report read-only: metrics.py imports sibling
# metrics_*.py modules, and without it Python would write scripts/__pycache__/
# into the working tree. The repo's .gitignore now covers that directory, but a
# report that writes nothing is still the better contract.

# Report the refactoring measurements (file size, complexity, duplication, dead code)
metrics:
    @python3 -B scripts/metrics.py

# Remove build artifacts
clean:
    @echo "Cleaning..."
    rm -f {{ binary_name }}
    @echo "Clean complete"

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
    go test ./...

# Build the browser JavaScript bundle and the stylesheet
web-build:
    npm run build:web
    go run ./internal/web/cssbuild/gen

# Type-check and build the browser assets
web-check:
    npm run typecheck:web
    npm run test:web
    npm run build:web
    go run ./internal/web/cssbuild/gen

# Run go vet across all packages
vet:
    go vet ./...

# Format Go code with gofmt
fmt:
    @echo "Formatting code..."
    gofmt -w .

# Check formatting and run vet (fails on gofmt drift)
lint:
    #!/usr/bin/env bash
    set -euo pipefail
    out=$(gofmt -l .)
    if [ -n "$out" ]; then
        echo "gofmt issues:"
        echo "$out"
        exit 1
    fi
    go vet ./...
    just web-check
    echo "Lint clean"

# Run the full CI validation envelope (fmt check, vet, tests, web checks, bundle drift)
ci:
    #!/usr/bin/env bash
    set -euo pipefail
    out=$(gofmt -l .)
    if [ -n "$out" ]; then
        echo "gofmt issues:"
        echo "$out"
        exit 1
    fi
    go vet ./...
    go test ./...
    npm run typecheck:web
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

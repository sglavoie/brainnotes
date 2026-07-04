# bn - Brain Notes CLI

binary_name := "bn"
pkg := "./cmd/bn"
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

# Run the full test suite
test:
    @echo "Running tests..."
    go test ./...

# Build the browser JavaScript bundle
web-build:
    npm run build:web

# Type-check and build the browser JavaScript bundle
web-check:
    npm run typecheck:web
    npm run test:web
    npm run build:web

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
    npm run build:web
    drift=$(git status --porcelain -- internal/web/static/app.js)
    if [ -n "$drift" ]; then
        echo "generated bundle drift detected in internal/web/static/app.js:"
        git --no-pager diff -- internal/web/static/app.js
        exit 1
    fi
    echo "CI clean"

# Remove build artifacts
clean:
    @echo "Cleaning..."
    rm -f {{ binary_name }}
    @echo "Clean complete"

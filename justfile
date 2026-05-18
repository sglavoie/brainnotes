# bn - Brain Notes CLI

binary_name := "bn"
pkg := "./cmd/bn"

# Show available recipes
help:
    @just --list

# Build the binary in the current directory
build:
    @echo "Building {{ binary_name }}..."
    go build -o {{ binary_name }} {{ pkg }}
    @echo "Built {{ binary_name }}"

# Install the binary into $GOPATH/bin via `go install`
install:
    @echo "Installing {{ binary_name }}..."
    go install {{ pkg }}
    @echo "Installed {{ binary_name }}"

# Run the full test suite
test:
    @echo "Running tests..."
    go test ./...

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
    echo "Lint clean"

# Remove build artifacts
clean:
    @echo "Cleaning..."
    rm -f {{ binary_name }}
    @echo "Clean complete"

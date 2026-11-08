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

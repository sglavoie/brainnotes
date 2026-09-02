// Command lintratchet is the Go half of the function-complexity ratchet.
//
// It exists because of one hole in the flag it replaces. `golangci-lint
// --new-from-merge-base` filters issues by changed *line*, and cyclop,
// gocognit and funlen all report a function's score on its *declaration* line.
// Editing only the body of an existing function therefore leaves that
// function's own violation outside the diff range: the function gets worse and
// the gate stays green. That was measured, not inferred — `contextOutput` in
// internal/cli/search_output.go took body changes after the ratchet base and
// sat at gocognit 21 while `just lint-new` reported `0 issues.`
//
// The fix is to widen the unit of judgement from the line to the function.
// This command asks golangci-lint for every violation in the tree, maps each
// one onto its enclosing top-level function with go/parser, and fails when any
// line of that function differs from the ratchet base. A violation in a
// function nobody touched is still tolerated, which is what keeps the floor
// recorded in docs/internals/refactor-baseline.md out of the way — this is
// deliberately not golangci-lint's `issues.whole-files`, which would make every
// untouched violation in a touched file the editor's problem.
//
//	go run ./scripts/lintratchet -base <commit>
//
// Run it from the repository root: golangci-lint reports paths relative to its
// working directory and they are compared against git's.
//
// `just lint-ratchet-selftest` (scripts/lint_ratchet_selftest.py) proves the
// body-only case in a throwaway repository, and is the regression test for the
// hole described above.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func main() {
	base := flag.String("base", "", "the commit the ratchet is anchored at (`ratchet_base` in the justfile)")
	flag.Parse()

	code, err := run(*base)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lint-ratchet: %v\n", err)
		os.Exit(1)
	}
	os.Exit(code)
}

// run returns the process exit code: 0 when no changed function carries a
// violation, 1 when one does. An error means the gate could not be evaluated,
// which is also fatal — a gate that cannot answer must never read as green.
func run(base string) (int, error) {
	if strings.TrimSpace(base) == "" {
		return 0, errors.New("-base is required; pass the `ratchet_base` commit from the justfile")
	}
	changed, err := changedGoLines(base)
	if err != nil {
		return 0, err
	}
	issues, err := reportedIssues()
	if err != nil {
		return 0, err
	}
	hits, err := hitsInChangedFunctions(issues, changed)
	if err != nil {
		return 0, err
	}
	if len(hits) == 0 {
		fmt.Printf("ratchet: %d Go file(s) changed since %s; no complexity violation in any changed function.\n",
			len(changed), shortSHA(base))
		return 0, nil
	}
	printReport(hits)
	return 1, nil
}

// changedFile is the set of lines of one file that differ from the ratchet
// base. `whole` marks an untracked file, every line of which is new.
type changedFile struct {
	whole bool
	lines map[int]bool
}

// firstChangedIn reports the first line inside fn that differs from the base.
func (c *changedFile) firstChangedIn(fn span) (int, bool) {
	if c.whole {
		return fn.start, true
	}
	for line := fn.start; line <= fn.end; line++ {
		if c.lines[line] {
			return line, true
		}
	}
	return 0, false
}

// changedGoLines collects the Go lines that differ from base, uncommitted work
// included: `git diff <commit>` compares the working tree against that commit,
// so a violation is caught while it is still unstaged. Untracked files are
// invisible to that diff and are added whole; deletions are dropped.
func changedGoLines(base string) (map[string]*changedFile, error) {
	if _, err := git("cat-file", "-e", base+"^{commit}"); err != nil {
		return nil, fmt.Errorf("ratchet base %s does not resolve to a commit in this checkout "+
			"(a shallow clone does not contain it; CI checks out with fetch-depth: 0)", base)
	}
	diff, err := git("diff", "--no-prefix", "--unified=0", "--diff-filter=d", base, "--", "*.go")
	if err != nil {
		return nil, err
	}
	files := parseDiff(diff)
	untracked, err := git("ls-files", "--others", "--exclude-standard", "--", "*.go")
	if err != nil {
		return nil, err
	}
	for _, path := range splitLines(untracked) {
		if path != "" {
			files[path] = &changedFile{whole: true}
		}
	}
	return files, nil
}

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// parseDiff reads `git diff --no-prefix --unified=0` output into per-file line
// sets. A file's path is taken from the first `+++` line after each `diff
// --git` header rather than from any `+++` line, because an *added* source line
// that itself begins with "++ " renders as "+++ " inside a hunk.
func parseDiff(diff string) map[string]*changedFile {
	files := map[string]*changedFile{}
	var current *changedFile
	awaitingPath := false
	for _, line := range splitLines(diff) {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			current, awaitingPath = nil, true
		case awaitingPath && strings.HasPrefix(line, "+++ "):
			current = &changedFile{lines: map[int]bool{}}
			files[strings.TrimPrefix(line, "+++ ")] = current
			awaitingPath = false
		case current != nil:
			addHunkLines(current, line)
		}
	}
	return files
}

// addHunkLines records the post-image lines a hunk header announces. A header
// with a zero count is a pure deletion and adds nothing.
func addHunkLines(file *changedFile, line string) {
	match := hunkHeader.FindStringSubmatch(line)
	if match == nil {
		return
	}
	start, err := strconv.Atoi(match[1])
	if err != nil {
		return
	}
	count := 1
	if match[2] != "" {
		count, _ = strconv.Atoi(match[2])
	}
	for i := start; i < start+count; i++ {
		file.lines[i] = true
	}
}

// span is the inclusive line range of one top-level function declaration.
type span struct {
	name  string
	start int
	end   int
}

// spanCache parses each offending file at most once.
type spanCache map[string][]span

// spans returns every top-level function declaration in path. The span starts
// at the `func` keyword rather than at the doc comment, so rewording a comment
// above a function does not put that function back inside the gate.
func (c spanCache) spans(path string) ([]span, error) {
	if cached, ok := c[path]; ok {
		return cached, nil
	}
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	spans := []span{}
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		spans = append(spans, span{
			name:  fn.Name.Name,
			start: fset.Position(fn.Pos()).Line,
			end:   fset.Position(fn.End()).Line,
		})
	}
	c[path] = spans
	return spans, nil
}

// enclosingSpan returns the function containing line, or a span covering only
// that line when the issue sits outside every function declaration — a score
// reported on a package-level function literal, where the old line-anchored
// behaviour is the honest answer.
func enclosingSpan(spans []span, line int) span {
	for _, fn := range spans {
		if fn.start <= line && line <= fn.end {
			return fn
		}
	}
	return span{start: line, end: line}
}

// hit is one reported violation whose enclosing function changed, together with
// the line that put it back inside the gate.
type hit struct {
	issue  lintIssue
	fn     span
	source int
}

func hitsInChangedFunctions(issues []lintIssue, changed map[string]*changedFile) ([]hit, error) {
	cache := spanCache{}
	hits := []hit{}
	for _, issue := range issues {
		path := filepath.ToSlash(issue.Pos.Filename)
		file, ok := changed[path]
		if !ok {
			continue
		}
		spans, err := cache.spans(path)
		if err != nil {
			return nil, err
		}
		fn := enclosingSpan(spans, issue.Pos.Line)
		line, inside := file.firstChangedIn(fn)
		if !inside {
			continue
		}
		hits = append(hits, hit{issue: issue, fn: fn, source: line})
	}
	sort.Slice(hits, func(i, j int) bool { return lessHit(hits[i], hits[j]) })
	return hits, nil
}

func lessHit(a, b hit) bool {
	if a.issue.Pos.Filename != b.issue.Pos.Filename {
		return a.issue.Pos.Filename < b.issue.Pos.Filename
	}
	return a.issue.Pos.Line < b.issue.Pos.Line
}

func printReport(hits []hit) {
	fmt.Printf("ratchet: %d complexity violation(s) in a function changed since the baseline:\n\n", len(hits))
	for _, h := range hits {
		pos := h.issue.Pos
		fmt.Printf("%s:%d:%d: %s (%s)\n", pos.Filename, pos.Line, pos.Column, h.issue.Text, h.issue.FromLinter)
		fmt.Printf("    %s\n", scopeLine(h))
	}
	fmt.Print(`
The whole enclosing function is judged, not only the lines that changed: a
complexity score is a property of the function, and cyclop, gocognit and funlen
all report it on the declaration line that a body-only edit never touches.

Bring the function under the .golangci.yml thresholds — docs/internals/
cli-command-shape.md is the decomposition this repository uses — or leave it
alone entirely, because an untouched violation is still tolerated.
`)
}

func scopeLine(h hit) string {
	if h.fn.name == "" {
		return fmt.Sprintf("line %d changed since the baseline", h.source)
	}
	return fmt.Sprintf("func %s spans lines %d-%d; line %d changed since the baseline",
		h.fn.name, h.fn.start, h.fn.end, h.source)
}

// lintIssue is the subset of golangci-lint's JSON report this gate reads.
type lintIssue struct {
	FromLinter string
	Text       string
	Pos        struct {
		Filename string
		Line     int
		Column   int
	}
}

// reportedIssues runs the whole-tree report. `--issues-exit-code=0` is what
// makes a violation data rather than an exit status: this command decides which
// of them are in scope, so golangci-lint must not decide for it. The text
// output and the per-linter statistics both default to stdout, where they would
// land in the middle of the JSON, so both are turned off.
func reportedIssues() ([]lintIssue, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("golangci-lint", "run",
		"--issues-exit-code=0",
		"--show-stats=false",
		"--output.json.path=stdout",
		"--output.text.path="+os.DevNull,
		"./...")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("golangci-lint run: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var report struct{ Issues []lintIssue }
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		return nil, fmt.Errorf("golangci-lint did not produce a JSON report: %w: %s",
			err, strings.TrimSpace(stderr.String()))
	}
	return report.Issues, nil
}

func git(args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("git", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func splitLines(s string) []string {
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

func shortSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

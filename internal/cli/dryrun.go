package cli

import (
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"
)

// DryRun captures the destination for dry-run output. A nil *DryRun means
// writes proceed normally. The unified-diff style emitted here is intentionally
// distinct from the format used by `rename --dry-run` / `move --dry-run`
// (`<relpath>:<line>: old -> new`). Harmonizing the two is deferred; do not
// re-converge them without a session that explicitly addresses both.
type DryRun struct {
	Out io.Writer
}

// fileBlock buffers diff lines for one file so the `--- <relpath>` header can
// be omitted when nothing actually changed.
type fileBlock struct {
	header string
	lines  []string
}

func newFileBlock(relPath string) *fileBlock {
	return &fileBlock{header: fmt.Sprintf("--- %s\n", relPath)}
}

// removed appends a `- key: value` line (key was deleted or replaced).
func (b *fileBlock) removed(key string, value any) {
	b.lines = append(b.lines, fmt.Sprintf("- %s: %s\n", key, formatDryRunValue(value)))
}

// added appends a `+ key: value` line (key was added or replaced).
func (b *fileBlock) added(key string, value any) {
	b.lines = append(b.lines, fmt.Sprintf("+ %s: %s\n", key, formatDryRunValue(value)))
}

// empty reports whether the block has no diff lines (so the caller can skip
// emitting the header for unchanged files).
func (b *fileBlock) empty() bool { return len(b.lines) == 0 }

func (b *fileBlock) flush(out io.Writer) error {
	if b.empty() {
		return nil
	}
	if _, err := io.WriteString(out, b.header); err != nil {
		return err
	}
	for _, l := range b.lines {
		if _, err := io.WriteString(out, l); err != nil {
			return err
		}
	}
	return nil
}

// formatDryRunValue renders v as a single-line representation. Lists use flow
// style `[a, b, c]` so before/after comparisons fit on one line each.
func formatDryRunValue(v any) string {
	switch vv := v.(type) {
	case nil:
		return "null"
	case string:
		return vv
	case bool:
		if vv {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", vv)
	case int64:
		return fmt.Sprintf("%d", vv)
	case float64:
		// Whole-number floats must render with a `.0` suffix so the dry-run
		// preview matches what yaml.v3 actually writes (`rating: 3.0`).
		// Using `%v` (or `%g`) would emit `3`, which the parser would then
		// read back as int — a real apply-vs-dry-run mismatch.
		if vv == float64(int64(vv)) {
			return fmt.Sprintf("%d.0", int64(vv))
		}
		return fmt.Sprintf("%v", vv)
	case time.Time:
		if vv.Hour() == 0 && vv.Minute() == 0 && vv.Second() == 0 && vv.Nanosecond() == 0 && vv.Location() == time.UTC {
			return vv.Format("2006-01-02")
		}
		return vv.Format(time.RFC3339)
	case []any:
		parts := make([]string, len(vv))
		for i, item := range vv {
			parts[i] = formatDryRunValue(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return fmt.Sprintf("%v", vv)
	}
}

// dryRunValuesEqual is a thin wrapper around reflect.DeepEqual used by the
// set/unset commands to detect whether a key's value actually changed.
func dryRunValuesEqual(a, b any) bool {
	return reflect.DeepEqual(a, b)
}

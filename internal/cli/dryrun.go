package cli

import (
	"io"
	"reflect"
)

// DryRun captures the destination for dry-run output. A nil *DryRun means
// writes proceed normally. Every mutating command renders through the shared
// noteops canonical preview contract (`--- <relpath>` headers, then
// `path:`/`frontmatter[<key>]:`/`body:<line>:` change lines), so CLI dry-run
// output and the web preview APIs can never drift.
type DryRun struct {
	Out io.Writer
}

// dryRunValuesEqual is a thin wrapper around reflect.DeepEqual used by the
// set/unset commands to detect whether a key's value actually changed.
func dryRunValuesEqual(a, b any) bool {
	return reflect.DeepEqual(a, b)
}

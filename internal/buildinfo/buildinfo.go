// Package buildinfo names the build a BrainNotes binary came from.
//
// `bn`, `bn-vault-guard` and `bn-vault-sync` are installed together and must run
// from the same source: the companions share on-disk records with `bn`, and a
// LaunchAgent keeps the previous companion binary running after `just install`
// has replaced the file on disk until somebody restarts it. Each binary reports
// the identifier below, the companions write it into their own records, and
// readers compare it with their own to say when the running processes disagree.
//
// The identifier is the Git revision Go stamps into every binary built inside a
// checkout (`debug.ReadBuildInfo`, setting `vcs.revision`), with `-dirty`
// appended when the working tree had uncommitted changes. A build made outside
// a checkout carries no revision; it may set one with
// `-ldflags "-X github.com/sglavoie/brainnotes/internal/buildinfo.revision=<id>"`,
// and otherwise reports Unknown. Two dirty builds of one revision compare equal
// even when their changes differ: the identifier detects a binary that was not
// rebuilt, not every possible difference.
package buildinfo

import (
	"runtime/debug"
	"strings"
	"sync"
)

// Unknown is what a binary with no recorded revision reports. It never compares
// as a mismatch: a test binary or a `go run` build cannot say what it is, and
// guessing would make every such run look skewed.
const Unknown = "unknown"

// revision is the optional linker override; see the package documentation.
var revision string

var (
	once    sync.Once
	current string
)

// ID reports this binary's build identifier, or Unknown.
func ID() string {
	once.Do(func() { current = resolve(revision, debug.ReadBuildInfo) })
	return current
}

func resolve(override string, read func() (*debug.BuildInfo, bool)) string {
	if override != "" {
		return override
	}
	info, ok := read()
	if !ok || info == nil {
		return Unknown
	}
	var rev string
	var dirty bool
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			rev = setting.Value
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}
	if rev == "" {
		return Unknown
	}
	if dirty {
		rev += "-dirty"
	}
	return rev
}

// Known reports whether id names an actual build.
func Known(id string) bool { return id != "" && id != Unknown }

// Mismatch reports whether two identifiers are both known and differ. An unknown
// side is not evidence of skew, so it never reports one.
func Mismatch(a, b string) bool { return Known(a) && Known(b) && a != b }

// Short abbreviates an identifier for a log line or a message. The full value is
// what records store.
func Short(id string) string {
	const width = 12
	if !Known(id) {
		return Unknown
	}
	suffix := ""
	if trimmed, dirty := strings.CutSuffix(id, "-dirty"); dirty {
		id, suffix = trimmed, "-dirty"
	}
	if len(id) > width {
		id = id[:width]
	}
	return id + suffix
}

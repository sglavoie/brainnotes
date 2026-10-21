package index

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Status describes the on-disk state file without loading the vault. It is what
// `bn index --status` reports, and it is deliberately read-only: inspecting the
// cache must never be the thing that builds it.
type Status struct {
	// Path is the absolute path of the state file, whether or not it exists.
	Path string
	// Exists reports that something is present at Path.
	Exists bool
	// Entries is the number of cached notes. It is meaningful only when the
	// file exists and Problem is empty, because a file that cannot be decoded
	// has no entry count to report.
	Entries int
	// Bytes is the state file's size on disk.
	Bytes int64
	// ModTime is when the state file was last written.
	ModTime time.Time
	// Problem names the failure class when the state file exists but cannot be
	// used, in the same vocabulary Load logs. It is empty when the file decoded
	// cleanly, and also empty when there is no file at all — a vault that has
	// never been indexed is the ordinary cold start, not a problem.
	Problem string
}

// Stat reports on the state file for the vault at root. It never rebuilds and
// never writes; an unusable file is reported through Status.Problem rather than
// returned as an error, because the index self-heals and a reader of the status
// wants to be told what the *next* load will do, not handed a failure.
//
// The only error is a caller mistake: an empty root.
func Stat(root string) (Status, error) {
	if root == "" {
		return Status{}, errors.New("index: empty vault root")
	}

	// A throwaway Index so the file is validated by exactly the code Load uses,
	// rather than by a second, driftable copy of the same checks.
	ix := &Index{root: root, dir: Dir(root)}
	status := Status{Path: filepath.Join(ix.dir, stateFileName)}

	info, err := os.Lstat(status.Path)
	if err != nil {
		if !os.IsNotExist(err) {
			status.Problem = "state file unreadable"
		}
		return status, nil
	}
	status.Exists = true
	status.Bytes = info.Size()
	status.ModTime = info.ModTime()

	entries, class, err := ix.read()
	if err != nil {
		if class == "" {
			class = "state file unreadable"
		}
		status.Problem = class
		return status, nil
	}
	status.Entries = len(entries)
	return status, nil
}

package vault

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// UnsafeReplacementError reports a refusal to overwrite an existing file. Every
// supported platform replaces a destination with a primitive that keeps the
// destination name continuously present; this package has no fallback that
// vacates it first, because that vacancy is what a synchronizing file provider
// reads as a deletion and later propagates over the replacement. A platform or
// volume that cannot prove the primitive therefore fails here, before the first
// user path changes.
type UnsafeReplacementError struct {
	// Path is the destination — or the directory holding it — that was refused.
	Path string
	// Reason says what could not be proved, in the terms a person can act on.
	Reason string
	// Err is the underlying syscall or probe failure, when there was one.
	Err error
}

func (e *UnsafeReplacementError) Error() string {
	if e == nil {
		return "vault: unsafe file replacement"
	}
	message := fmt.Sprintf(
		"vault: refusing to replace %s: %s; this vault only overwrites a file with a primitive that never leaves its path absent",
		e.Path, e.Reason)
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	return message
}

func (e *UnsafeReplacementError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsUnsafeReplacement reports whether err is a refusal to overwrite because the
// path-preserving primitive could not be proved on this platform or volume.
func IsUnsafeReplacement(err error) bool {
	var unsafe *UnsafeReplacementError
	return errors.As(err, &unsafe)
}

// PartialReplacementError reports a replacement that failed after the
// destination stopped holding its original content. Only Windows can produce
// it: ReplaceFileW has intermediate failure modes that can leave the original
// in the backup file with the destination not yet rewritten. Restored says
// whether the original was put straight back; when it is false, BackupPath
// names the only surviving copy.
type PartialReplacementError struct {
	Path       string
	Stage      string
	BackupPath string
	Restored   bool
	Err        error
}

func (e *PartialReplacementError) Error() string {
	if e == nil {
		return "vault: partial file replacement"
	}
	if e.Restored {
		return fmt.Sprintf(
			"vault: replacing %s failed part-way (%s: %v) and its original content was put back; nothing was lost, retry the operation",
			e.Path, e.Stage, e.Err)
	}
	return fmt.Sprintf(
		"vault: replacing %s failed part-way (%s: %v) and its original content could not be put back; that content is preserved at %s, move it back into place to recover",
		e.Path, e.Stage, e.Err, e.BackupPath)
}

func (e *PartialReplacementError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsPartialReplacement reports whether err left a destination mid-replacement.
func IsPartialReplacement(err error) bool {
	var partial *PartialReplacementError
	return errors.As(err, &partial)
}

// replaceExisting installs source at destination and returns the path now
// holding what destination held before the call.
//
// The destination name exists continuously across the call on every supported
// platform — that is the property the whole package is built on and the one the
// capability self-test below actually measures. The displaced copy is the
// caller's to judge and then delete: it is what turns an overwrite into a
// compare-and-swap, since the bytes it holds are the exact bytes that were
// replaced rather than the bytes some earlier read observed.
//
// destination must already exist and be a regular file; creating a new path is
// not this function's job and goes through the exclusive placement instead.
func replaceExisting(source, destination string) (string, error) {
	if err := requireSameVolume(source, destination); err != nil {
		return "", err
	}
	if err := requireProvenReplacement(filepath.Dir(destination)); err != nil {
		return "", err
	}
	displaced, err := nativeReplace(source, destination)
	if err != nil {
		if IsPartialReplacement(err) {
			return "", err
		}
		return "", fmt.Errorf("vault: replace %q with %q via %s: %w",
			destination, source, nativeReplacementName, err)
	}
	return displaced, nil
}

// volumeIdentity reports which storage volume dir lives on. It is a variable
// only so a test can simulate a two-volume vault on a machine that has one
// volume; nothing in production replaces it.
var volumeIdentity = volumeIdentityOf

// requireSameVolume refuses a staged file that does not live on the
// destination's storage. Every supported primitive is same-volume only, and a
// cross-volume call fails with an error that reads like a permission problem, so
// the condition is named here instead.
func requireSameVolume(source, destination string) error {
	sourceVolume, err := volumeIdentity(filepath.Dir(source))
	if err != nil {
		return &UnsafeReplacementError{
			Path:   destination,
			Reason: "the volume holding the staged replacement could not be identified",
			Err:    err,
		}
	}
	destinationVolume, err := volumeIdentity(filepath.Dir(destination))
	if err != nil {
		return &UnsafeReplacementError{
			Path:   destination,
			Reason: "the volume holding the destination could not be identified",
			Err:    err,
		}
	}
	if sourceVolume != destinationVolume {
		return &UnsafeReplacementError{
			Path: destination,
			Reason: fmt.Sprintf(
				"the staged replacement is on %s and the destination is on %s, and no atomic replacement crosses volumes",
				sourceVolume, destinationVolume),
		}
	}
	return nil
}

var (
	provenVolumesMu sync.Mutex
	provenVolumes   = map[string]struct{}{}
)

// requireProvenReplacement runs the capability self-test once per volume per
// process, before the first real overwrite on that volume. Only a success is
// cached: a volume that failed may be a different filesystem by the next call —
// a mount changing under the same path is exactly the case worth re-checking —
// and a cached failure would outlive the condition that caused it.
func requireProvenReplacement(dir string) error {
	if !nativeReplacementSupported {
		return &UnsafeReplacementError{
			Path:   dir,
			Reason: "this platform has no primitive that replaces a file without vacating its path",
		}
	}
	volume, err := volumeIdentity(dir)
	if err != nil {
		return &UnsafeReplacementError{
			Path:   dir,
			Reason: "the volume could not be identified, so its replacement semantics cannot be proved",
			Err:    err,
		}
	}
	provenVolumesMu.Lock()
	defer provenVolumesMu.Unlock()
	if _, proven := provenVolumes[volume]; proven {
		return nil
	}
	if err := proveVolume(dir); err != nil {
		return err
	}
	provenVolumes[volume] = struct{}{}
	return nil
}

// proveVolume is the self-test requireProvenReplacement runs. It is a variable
// so a test can count how often it runs and make it fail, which is the only way
// to observe that a success is cached and a failure is not.
var proveVolume = func(dir string) error {
	return proveReplacement(dir, nativeReplace)
}

// forgetProvenVolumesForTest drops the capability cache so a test can observe
// the self-test itself rather than a cached verdict from an earlier test.
func forgetProvenVolumesForTest() {
	provenVolumesMu.Lock()
	defer provenVolumesMu.Unlock()
	provenVolumes = map[string]struct{}{}
}

const (
	replacementProbeBody = "brainnotes replacement capability probe: replacement\n"
	destinationProbeBody = "brainnotes replacement capability probe: destination\n"
	replacementProbeMode = os.FileMode(0o600)
	destinationProbeMode = os.FileMode(0o640)
)

// proveReplacement runs the exact production primitive against two throwaway
// files in dir and refuses the volume unless it behaves the way an overwrite
// depends on. Compiling the syscall in is not evidence: a network mount, a FUSE
// filesystem, or an old kernel can accept the call and do something else, or
// reject it in a way that reads like an ordinary I/O error at the worst possible
// moment. Measuring it on a probe pair is what keeps that discovery away from a
// user's note.
//
// The measurement that matters is presence, not bytes. A watcher polls the names
// for the whole call, so a primitive that unlinks and recreates the destination —
// the behavior this package exists to avoid — is caught even though its final
// state looks correct.
//
// replace is the primitive under test. Production always passes nativeReplace,
// the exact function the overwrite path calls; it is a parameter only so a test
// can hand the probe a deliberately misbehaving primitive and check that it is
// refused.
func proveReplacement(dir string, replace func(source, destination string) (string, error)) error {
	refuse := func(reason string, err error) error {
		return &UnsafeReplacementError{Path: dir, Reason: reason, Err: err}
	}

	source, err := stageProbeFile(dir, replacementProbeBody, replacementProbeMode)
	if err != nil {
		return refuse("a replacement probe could not be staged on this volume", err)
	}
	defer func() { _ = os.Remove(source) }()
	destination, err := stageProbeFile(dir, destinationProbeBody, destinationProbeMode)
	if err != nil {
		return refuse("a destination probe could not be staged on this volume", err)
	}
	defer func() { _ = os.Remove(destination) }()

	watched := []string{destination}
	if replacementExchangesNames {
		watched = append(watched, source)
	}
	watch := watchPresence(watched)
	displaced, replaceErr := replace(source, destination)
	vanished := watch.stop()
	if replaceErr != nil {
		return refuse(fmt.Sprintf("%s failed on this volume", nativeReplacementName), replaceErr)
	}
	defer func() { _ = os.Remove(displaced) }()

	if len(vanished) > 0 {
		return refuse(fmt.Sprintf(
			"%s left %s absent part-way through, which is exactly the vacancy this vault must never produce",
			nativeReplacementName, strings.Join(vanished, " and ")), nil)
	}
	if err := probeHolds(destination, replacementProbeBody, replacementProbeMode); err != nil {
		return refuse("the destination did not take the replacement's content and metadata", err)
	}
	if err := probeHolds(displaced, destinationProbeBody, destinationProbeMode); err != nil {
		return refuse("the displaced content was not preserved with its metadata", err)
	}
	if err := syncDir(dir); err != nil {
		return refuse("the directory holding the destination could not be fsynced", err)
	}
	return nil
}

// stageProbeFile writes one throwaway probe with its final mode already
// applied. It carries its own name prefix so a probe a crash left behind is
// distinguishable from a staged replacement or a displaced note.
func stageProbeFile(dir, body string, mode os.FileMode) (string, error) {
	file, path, err := openExclusiveTemp(dir, ".txn-probe-", ".tmp", 0o600)
	if err != nil {
		return "", err
	}
	cleanup := func() { _ = os.Remove(path) }
	if _, err := file.WriteString(body); err != nil {
		_ = file.Close()
		cleanup()
		return "", err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		cleanup()
		return "", err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", err
	}
	if err := os.Chmod(path, mode); err != nil {
		cleanup()
		return "", err
	}
	return path, nil
}

// probeHolds checks one half of the exchange. Modes are only compared where the
// primitive is a true exchange of two names: ReplaceFileW deliberately carries
// the replaced file's attributes onto the result, and a Windows file mode is a
// read-only bit rather than a permission triple, so comparing it there would
// measure nothing.
func probeHolds(path, body string, mode os.FileMode) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, []byte(body)) {
		return fmt.Errorf("%q holds %q, want %q", path, string(data), body)
	}
	if !replacementExchangesNames {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if got := info.Mode().Perm(); got != mode {
		return fmt.Errorf("%q has mode %v, want %v", path, got, mode)
	}
	return nil
}

// presenceWatch records any moment a watched path reports as missing. It is a
// busy poll on purpose: the window it has to catch is a single syscall, and a
// sleeping poller would sample either side of it and see nothing.
type presenceWatch struct {
	done     chan struct{}
	stopped  chan struct{}
	mu       sync.Mutex
	vanished map[string]struct{}
}

// presenceVacancyObserved, when a test sets it, is called by a watcher each
// time it records a watched path as missing. It lets a test that fakes a
// vacancy hold the path away until the watcher has seen it, instead of sleeping
// and hoping the poller sampled the gap. It is nil in production, and each
// watcher reads it once when it starts, so the polling goroutine never races a
// test restoring it.
var presenceVacancyObserved func(path string)

func watchPresence(paths []string) *presenceWatch {
	observed := presenceVacancyObserved
	watch := &presenceWatch{
		done:     make(chan struct{}),
		stopped:  make(chan struct{}),
		vanished: make(map[string]struct{}, len(paths)),
	}
	ready := make(chan struct{})
	go func() {
		defer close(watch.stopped)
		first := true
		for {
			for _, path := range paths {
				if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
					watch.mu.Lock()
					watch.vanished[path] = struct{}{}
					watch.mu.Unlock()
					if observed != nil {
						observed(path)
					}
				}
			}
			if first {
				first = false
				close(ready)
			}
			select {
			case <-watch.done:
				return
			default:
			}
		}
	}()
	// Returning before the first pass would let a fast primitive finish against
	// a watcher that never ran, and report a clean sample it never took.
	<-ready
	return watch
}

// stop ends the poll and returns the watched paths that were ever observed
// missing, sorted so a failure message is stable.
func (w *presenceWatch) stop() []string {
	close(w.done)
	<-w.stopped
	w.mu.Lock()
	defer w.mu.Unlock()
	names := make([]string, 0, len(w.vanished))
	for path := range w.vanished {
		names = append(names, filepath.Base(path))
	}
	sort.Strings(names)
	return names
}

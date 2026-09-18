//go:build synctransport

package vaultsyncd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/synctransport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// live is the world from helpers_test.go with the fake transport replaced by two
// Syncthing processes.
//
// Everything else is deliberately identical: the same enrollment, the same
// editor, the same passes. The tests in realtransport_test.go are the tests in
// roundtrip_test.go asked again of a transport that can be slow, can be out of
// order, can be interrupted, and is not in this process — and the point of
// sharing the vocabulary is that the difference between the two files is exactly
// the transport and nothing else.
type live struct {
	*world
	st *synctransport.Transport
}

// live starts the real transport over this world's pair and stops it with the
// test.
func (w *world) live(t *testing.T) *live {
	t.Helper()
	return &live{world: w, st: synctransport.Start(t, w.pair)}
}

// settleTimeout, settlePoll and quietFor bound the three shapes of waiting here.
//
// settlePoll is slow on purpose: a pass runs Git, and hammering one at ten hertz
// would spend the test's time competing with the transport it is waiting for.
// It is also what a real deployment does — internal/vaultsyncd's companion runs
// a pass on an interval — so a scenario that needs several passes to converge is
// reproducing the daemon rather than working around it.
// quietFor is derived rather than chosen. A claim that something did not happen
// is only as good as the window it was observed over, and the slowest way this
// transport notices a new file is the folder rescan that backstops the watcher —
// so the window is two whole rescans plus a margin, and it moves if that interval
// does. A window equal to one rescan would let "the sender had not looked yet"
// pass as "the transport never delivered it", which is exactly the confusion the
// disconnect scenario must not be able to make.
const (
	settleTimeout = 120 * time.Second
	settlePoll    = 500 * time.Millisecond
	quietFor      = 2*synctransport.RescanInterval + 2*time.Second
)

// settle runs passes against a vault until want holds, and returns the pass that
// made it hold.
//
// This is the load-bearing helper. Over a real transport a package appears in a
// channel one file at a time, so a pass can legitimately find a package that is
// not all there yet and hold it; the design's answer to that is to look again
// next pass, and asserting through repeated passes is what tests that answer
// rather than tiptoeing around it.
func (l *live) settle(root, what string, want func() bool) *Pass {
	l.t.Helper()
	deadline := time.Now().Add(settleTimeout)
	var last *Pass
	for {
		last = l.once(root)
		if want() {
			return last
		}
		if time.Now().After(deadline) {
			l.t.Fatalf("waited %s for %s\n%s", settleTimeout, what, l.describe(last))
		}
		time.Sleep(settlePoll)
	}
}

// stays holds an assertion open for long enough that the transport had a fair
// chance to break it, and fails with failure the moment it stops holding.
//
// A claim that an asynchronous system will *not* do something is only worth
// making with a duration attached. quietFor covers two whole folder rescans and
// many watcher delays, so a predicate that survives it survived every opportunity
// the transport had, including on a platform where the watcher never started and
// the rescan is the only thing looking. Both directions are here on
// purpose: "the package never arrived" and "the package is still there" are the
// same kind of claim, and the second is what sender-owned cleanup asserts.
func (l *live) stays(failure string, ok func() bool) {
	l.t.Helper()
	deadline := time.Now().Add(quietFor)
	for time.Now().Before(deadline) {
		if !ok() {
			l.t.Fatalf("%s within %s of it being possible", failure, quietFor)
		}
		time.Sleep(settlePoll)
	}
}

// heldThroughout runs passes for quietFor and requires every one of them to hold
// exactly one package, change nothing, acknowledge nothing, and leave rel absent.
//
// stays on its own is weak evidence for a refusal: nothing is running during that
// window, so of course the vault did not apply anything. This asks the vault
// again and again while the transport keeps offering the package, which is the
// claim these scenarios actually make — the refusal is a decision the drain
// re-reaches every time, not a race the first pass happened to win before the
// bytes finished arriving.
func (l *live) heldThroughout(root, rel, what string) {
	l.t.Helper()
	deadline := time.Now().Add(quietFor)
	for {
		imported := l.once(root).imported(l.t)
		if imported.Changed != 0 || imported.Answered != 0 || imported.Held != 1 {
			l.t.Fatalf("%s: %+v", what, imported)
		}
		if !l.missing(root, rel) {
			l.t.Fatalf("%s: the vault wrote %s from a package it held", what, rel)
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(settlePoll)
	}
}

// describe is what a timeout prints: both channels, as the vaults see them.
func (l *live) describe(last *Pass) string {
	var b strings.Builder
	for _, root := range []string{l.root(), l.peerRoot()} {
		b.WriteString(filepath.Base(root) + " outgoing: ")
		b.WriteString(strings.Join(l.channel(vaultsync.OutgoingDir(root)), " ") + "\n")
		b.WriteString(filepath.Base(root) + " incoming: ")
		b.WriteString(strings.Join(l.channel(vaultsync.IncomingDir(root)), " ") + "\n")
	}
	if last != nil {
		for _, unit := range last.Units {
			fmt.Fprintf(&b, "last pass %s: %+v\n", unit.Worker, unit)
		}
	}
	return b.String()
}

// body reads a note without failing when it is absent, which is what every
// convergence predicate here needs: "not yet" is the expected answer for most of
// a scenario.
func (l *live) body(root, rel string) string {
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return ""
	}
	return string(data)
}

func (l *live) missing(root, rel string) bool {
	_, err := os.Stat(filepath.Join(root, rel))
	return os.IsNotExist(err)
}

// delivered reports whether a package name is in a vault's incoming channel.
func (l *live) delivered(root, name string) bool {
	return contains(l.channel(vaultsync.IncomingDir(root)), name)
}

// onlyPackage asserts a channel holds exactly one package and names it.
func onlyPackage(t *testing.T, names []string) string {
	t.Helper()
	if len(names) != 1 {
		t.Fatalf("the channel holds %v, want exactly one package", names)
	}
	return names[0]
}

// added names the package that appeared since a previous listing.
func added(t *testing.T, names []string, before string) string {
	t.Helper()
	var fresh []string
	for _, name := range names {
		if name != before {
			fresh = append(fresh, name)
		}
	}
	if len(fresh) != 1 {
		t.Fatalf("the channel holds %v, want exactly one package besides %s", names, before)
	}
	return fresh[0]
}

// withhold moves a published package out of the channel and returns a function
// that puts it back.
//
// It is how a test chooses a delivery order. A real transport will not be told
// what order to send in — that is the whole point — so the way to test the
// out-of-order case is to control what is *available* to send, which is a
// property of the sender's own directory and is therefore the test's to decide.
func withhold(t *testing.T, root, scratch, name string) func() {
	t.Helper()
	from := filepath.Join(vaultsync.OutgoingDir(root), name)
	aside := filepath.Join(scratch, "withheld", name)
	if err := os.MkdirAll(filepath.Dir(aside), 0o755); err != nil {
		t.Fatalf("stage a withheld package: %v", err)
	}
	if err := os.Rename(from, aside); err != nil {
		t.Fatalf("withhold %s: %v", name, err)
	}
	return func() {
		t.Helper()
		if err := os.Rename(aside, from); err != nil {
			t.Fatalf("release %s: %v", name, err)
		}
	}
}

// truncateManifest removes a package's manifest, which is the shape an
// interrupted copy has: the payloads are here and the thing that says what they
// are is not.
func truncateManifest(t *testing.T, root, name string) {
	t.Helper()
	manifest := filepath.Join(vaultsync.OutgoingDir(root), name, vaultsync.ManifestName)
	if err := os.Remove(manifest); err != nil {
		t.Fatalf("remove %s: %v", manifest, err)
	}
}

// alterPayloads changes every payload byte-for-byte while leaving the manifest
// describing what they used to be.
//
// Appending rather than substituting is deliberate: it breaks both the recorded
// length and the recorded digest, so the refusal does not depend on which of the
// two internal/vaultsync happens to check first.
func alterPayloads(t *testing.T, root, name string) {
	t.Helper()
	payloads := filepath.Join(vaultsync.OutgoingDir(root), name, vaultsync.PayloadsName)
	altered := 0
	err := filepath.WalkDir(payloads, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		if _, err := file.WriteString("tampered\n"); err != nil {
			return err
		}
		altered++
		return nil
	})
	if err != nil {
		t.Fatalf("alter payloads under %s: %v", payloads, err)
	}
	if altered == 0 {
		t.Fatalf("%s holds no payloads to alter", payloads)
	}
}

// discardDelivered removes a delivered package from a receiver's own incoming channel,
// which is the mistake TestRealTransportCleanupIsSenderOwned is about.
func discardDelivered(t *testing.T, root, name string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(vaultsync.IncomingDir(root), name)); err != nil {
		t.Fatalf("discard %s: %v", name, err)
	}
}

// folderTypes returns one device's folder types keyed by folder id, sorted for a
// stable failure message.
func folderTypes(views []synctransport.FolderView) []string {
	kinds := make([]string, 0, len(views))
	for _, view := range views {
		kinds = append(kinds, view.ID+"="+view.Type)
	}
	sort.Strings(kinds)
	return kinds
}

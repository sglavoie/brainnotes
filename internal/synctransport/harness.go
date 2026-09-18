package synctransport

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/syncthingbin"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultfixture"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// Transport is a running two-process Syncthing cluster over one fixture pair.
type Transport struct {
	// Local and Peer are the two devices, named for the two halves of
	// vaultfixture.Pair so a test reads the same way on both sides.
	Local *Device
	Peer  *Device

	t *testing.T
}

// Device is one Syncthing process, its identity, and the two directories it is
// allowed to touch.
type Device struct {
	// Label is what this device calls itself, in its own configuration, in its
	// peer's, and in failure messages.
	Label string
	// Root is the vault this device's channels live under. Syncthing is never
	// told this path; it is here so a test can say which vault it means.
	Root string
	// ID is the Syncthing device identity, generated once per fixture.
	ID string

	channel  string
	outgoing string
	incoming string

	home    string
	sync    string
	admin   string
	apiKey  string
	logPath string

	binary string
	peer   *Device
	t      *testing.T
	proc   *process
}

// Start brings up the real transport over a fixture pair and tears it down when
// the test ends.
//
// It is deliberately shaped as "give me a pair, get a running cluster": the
// argument is a vaultfixture.Pair rather than two paths, because a Pair is
// something this repository built under the system temporary directory, and that
// is the premise every isolation refusal below is checking rather than assuming.
func Start(t *testing.T, pair *vaultfixture.Pair) *Transport {
	t.Helper()
	binary := requireBinary(t)
	local := newDevice(t, binary, "local", pair.Local.Root, pair.Dir)
	peer := newDevice(t, binary, "peer", pair.Peer.Root, pair.Dir)
	local.peer, peer.peer = peer, local

	for _, device := range []*Device{local, peer} {
		device.prepare()
	}
	for _, device := range []*Device{local, peer} {
		device.writeConfig()
		device.start()
	}

	transport := &Transport{Local: local, Peer: peer, t: t}
	transport.AwaitConnected()
	return transport
}

// requireBinary resolves the pinned Syncthing and fails — never skips — when
// there is not one.
//
// `just test-sync-transport` provisions it and passes the path in the
// environment. Resolving here as a fallback is what makes the package usable
// from `go test -tags synctransport ./...` directly — syncthingbin's cache lives
// outside the fixture, so the fallback downloads once per machine rather than
// once per test — and the fallback is a download rather than a skip for the
// reason in the package doc.
func requireBinary(t *testing.T) string {
	t.Helper()
	binary, err := syncthingbin.Resolve()
	if err != nil {
		t.Fatalf("this gate runs a real Syncthing %s and could not get one.\n"+
			"Run `just test-sync-transport`, which provisions it first, or set %s to a %s binary.\n%v",
			pinned, syncthingbin.EnvBinary, pinned, err)
	}
	return binary
}

// newDevice lays out one device's paths and ports without touching the disk.
//
// The two channel directories are derived from internal/vaultsync's own layout
// rather than spelled out here. That is the point of the harness: if the
// protocol ever moves where a channel lives, the transport follows it, and a
// test that still passed against a hand-written path would be describing a
// directory nothing uses.
func newDevice(t *testing.T, binary, label, root, scratch string) *Device {
	t.Helper()
	home := filepath.Join(scratch, "syncthing-"+label)
	device := &Device{
		Label:    label,
		Root:     root,
		channel:  "bnotes-" + label,
		outgoing: vaultsync.OutgoingDir(root),
		incoming: vaultsync.IncomingDir(root),
		home:     home,
		sync:     vaultfixture.ReserveLoopbackAddr(t),
		admin:    vaultfixture.ReserveLoopbackAddr(t),
		apiKey:   newAPIKey(t),
		logPath:  filepath.Join(home, "syncthing.log"),
		binary:   binary,
		t:        t,
	}
	device.refuseUnlessDisposable()
	return device
}

// SyncAddress is the loopback endpoint this device was told to listen on, and
// the only one it may use. A test reads it back out of the running process and
// compares, because an address a release ignored is an address it replaced with
// the default, which binds every interface on the machine.
func (d *Device) SyncAddress() string { return d.sync }

// refuseUnlessDisposable is the one guarantee this package cannot be wrong
// about: a Syncthing started here must not be able to see a real vault.
//
// It delegates every refusal to internal/vaultfixture rather than restating any
// of them. The sandbox each path is judged against is the thing that path is
// supposed to be inside — a channel inside its own vault, a Syncthing home
// inside the pair's scratch directory — so a mistake in the layout above shows
// up here as a refusal rather than as a program writing somewhere unexpected.
func (d *Device) refuseUnlessDisposable() {
	d.t.Helper()
	for _, path := range []string{d.outgoing, d.incoming} {
		if err := vaultfixture.IsolationError(path, d.Root); err != nil {
			d.t.Fatalf("refusing to hand %s's channel to Syncthing: %v", d.Label, err)
		}
	}
	if err := vaultfixture.IsolationError(d.home, filepath.Dir(d.home)); err != nil {
		d.t.Fatalf("refusing to give %s a Syncthing home: %v", d.Label, err)
	}
}

// prepare creates what Syncthing needs to exist before it will serve a folder,
// and generates the device identity.
//
//bn:direct-write refuseUnlessDisposable refused unless these are throwaway fixture paths under the system temporary directory, and every path written here is a staging channel or a Syncthing home, never a vault's content
func (d *Device) prepare() {
	d.t.Helper()
	for _, dir := range []string{d.outgoing, d.incoming, d.home} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			d.t.Fatalf("%s: mkdir %q: %v", d.Label, dir, err)
		}
	}
	// A folder without its marker is a folder Syncthing refuses to serve, which
	// is its defence against synchronising into a directory that failed to
	// mount. Creating the marker is the transport's job in a real deployment too.
	for _, dir := range []string{d.outgoing, d.incoming} {
		marker := filepath.Join(dir, ".stfolder")
		if err := os.MkdirAll(marker, 0o755); err != nil {
			d.t.Fatalf("%s: mkdir %q: %v", d.Label, marker, err)
		}
	}
	d.ID = d.generateIdentity()
}

// generateIdentity runs the release's own key generation and reads the identity
// back out of the file it wrote.
//
// The device ID is a fingerprint of a freshly generated certificate, so it
// cannot be chosen; reading it out of the generated configuration is more
// durable than parsing it out of the log line beside it.
func (d *Device) generateIdentity() string {
	d.t.Helper()
	out, err := d.run("generate", "--home", d.home, "--no-default-folder")
	if err != nil {
		d.t.Fatalf("%s: syncthing generate: %v\n%s", d.Label, err, out)
	}
	body, err := os.ReadFile(filepath.Join(d.home, "config.xml"))
	if err != nil {
		d.t.Fatalf("%s: read generated configuration: %v", d.Label, err)
	}
	var generated xmlConfig
	if err := xml.Unmarshal(body, &generated); err != nil {
		d.t.Fatalf("%s: parse generated configuration: %v", d.Label, err)
	}
	if len(generated.Devices) != 1 || generated.Devices[0].ID == "" {
		d.t.Fatalf("%s: generated configuration names %d devices, want its own", d.Label, len(generated.Devices))
	}
	return generated.Devices[0].ID
}

// writeConfig replaces the generated configuration with this harness's.
//
//bn:direct-write the target is this device's Syncthing home, which refuseUnlessDisposable refused unless it is throwaway fixture storage
func (d *Device) writeConfig() {
	d.t.Helper()
	body, err := renderConfig(d, d.peer)
	if err != nil {
		d.t.Fatalf("%s: %v", d.Label, err)
	}
	if err := os.WriteFile(filepath.Join(d.home, "config.xml"), body, 0o600); err != nil {
		d.t.Fatalf("%s: write configuration: %v", d.Label, err)
	}
}

// newAPIKey mints a per-instance administration key, so that the only thing
// which can drive these processes is the test that started them.
func newAPIKey(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generate an API key: %v", err)
	}
	return hex.EncodeToString(raw)
}

// AwaitConnected blocks until each device reports a live connection to the
// other, which is the precondition every scenario here starts from.
func (tr *Transport) AwaitConnected() {
	tr.t.Helper()
	tr.Await("the two devices to connect", func() bool {
		return tr.Local.Connected() && tr.Peer.Connected()
	})
}

// awaitTimeout and awaitPoll bound every wait in this package.
//
// The timeout is long because it covers a reconnection interval plus a rescan on
// a machine that is also running the rest of a test suite, and it is finite
// because the failure this harness exists to catch — a package that never
// arrives — is indistinguishable from a slow one until something declares it.
const (
	awaitTimeout = 90 * time.Second
	awaitPoll    = 100 * time.Millisecond
)

// Await blocks until done reports true, and fails the test with both devices'
// logs when it never does.
//
// The logs are the whole reason this is a method rather than a bare loop in each
// test. A transport assertion that times out says only "it did not arrive", and
// the next question is always what the two processes thought they were doing.
func (tr *Transport) Await(what string, done func() bool) {
	tr.t.Helper()
	deadline := time.Now().Add(awaitTimeout)
	for {
		if done() {
			return
		}
		if time.Now().After(deadline) {
			tr.t.Fatalf("waited %s for %s and it did not happen\n%s\n%s",
				awaitTimeout, what, tr.Local.logTail(), tr.Peer.logTail())
		}
		time.Sleep(awaitPoll)
	}
}

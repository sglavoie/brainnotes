package synctransport

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// apiTimeout bounds one administration call. These are local requests to a
// process on loopback; anything that takes seconds is a process that is not
// answering, and the caller is nearly always inside a poll loop that will ask
// again.
const apiTimeout = 10 * time.Second

// FolderView is what a running device believes about one of its folders, read
// back out of the process rather than out of the file this harness wrote.
//
// Reading it back is the difference between "the configuration says staging
// only" and "the program is serving staging only", and the second is the claim
// worth testing.
type FolderView struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	Type string `json:"type"`
}

// Folders returns every folder this device is serving.
func (d *Device) Folders() []FolderView {
	d.t.Helper()
	var folders []FolderView
	if err := d.get("/rest/config/folders", nil, &folders); err != nil {
		d.t.Fatalf("%s: read folders: %v", d.Label, err)
	}
	return folders
}

// Options returns this device's global options as the running process reports
// them, undecoded.
//
// A raw map rather than a struct, deliberately: decoding into named fields turns
// an option this release renamed into a zero value, and a containment check that
// reads false because the key is gone is worse than no check at all. Compared
// against Containment, which names the keys it expects to find.
func (d *Device) Options() map[string]any {
	d.t.Helper()
	var options map[string]any
	if err := d.get("/rest/config/options", nil, &options); err != nil {
		d.t.Fatalf("%s: read options: %v", d.Label, err)
	}
	return options
}

// ConfigVersion is the configuration schema the running process settled on, which
// is not necessarily the one this harness wrote: a release that does not know a
// schema migrates it on startup and carries on, and this is the only place that
// shows.
func (d *Device) ConfigVersion() int {
	d.t.Helper()
	var config struct {
		Version *int `json:"version"`
	}
	if err := d.get("/rest/config", nil, &config); err != nil {
		d.t.Fatalf("%s: read configuration: %v", d.Label, err)
	}
	if config.Version == nil {
		d.t.Fatalf("%s: its configuration reports no schema version", d.Label)
	}
	return *config.Version
}

// Disconnect cuts this device off from its peer, and stays cut until Reconnect.
//
// It is implemented by pausing the peer *device*, not by pausing a folder or by
// killing the process: a paused device keeps indexing its own files and keeps
// its database, so what the scenario isolates is the delivery and nothing else.
// That is what a laptop closing its lid looks like to the other end.
// Both ends are waited for, not just the one that was told. The peer learns of
// the close when it processes it, some milliseconds later, and a scenario that
// starts asserting on the strength of this device's answer alone would
// occasionally catch the peer still reporting a live connection — and would
// report that as "the link came back on its own", which is a diagnosis of
// something that never happened.
func (d *Device) Disconnect() {
	d.t.Helper()
	d.peerState("/rest/system/pause")
	d.Await("the link to drop at both ends", func() bool {
		return !d.Connected() && !d.peer.Connected()
	})
}

// Reconnect undoes Disconnect and waits for the link to come back, at both ends
// and for the same reason.
func (d *Device) Reconnect() {
	d.t.Helper()
	d.peerState("/rest/system/resume")
	d.Await("the link to return at both ends", func() bool {
		return d.Connected() && d.peer.Connected()
	})
}

func (d *Device) peerState(path string) {
	d.t.Helper()
	if err := d.post(path, url.Values{"device": {d.peer.ID}}, nil); err != nil {
		d.t.Fatalf("%s: %s peer: %v", d.Label, path, err)
	}
}

// Revert restores this device's receive-only folder to what its owner holds,
// discarding whatever was changed or removed locally.
//
// It is the operator's escape hatch made available to a test, and it is how
// TestRealTransportCleanupIsSenderOwned demonstrates that a receiver's deletion
// was recorded as a local deviation rather than as a retirement: the bytes come
// back, from the sender, because the sender never agreed they were gone.
func (d *Device) Revert() {
	d.t.Helper()
	if err := d.post("/rest/db/revert", url.Values{"folder": {d.peer.channel}}, nil); err != nil {
		d.t.Fatalf("%s: revert %s: %v", d.Label, d.peer.channel, err)
	}
}

// Deviations counts the items in this device's receive-only folder that differ
// from what its owner published. Zero means the receiver has changed nothing;
// anything else is the count Syncthing is refusing to send back.
func (d *Device) Deviations() int {
	d.t.Helper()
	var status struct {
		ReceiveOnlyTotalItems int `json:"receiveOnlyTotalItems"`
	}
	if err := d.get("/rest/db/status", url.Values{"folder": {d.peer.channel}}, &status); err != nil {
		d.t.Fatalf("%s: read %s status: %v", d.Label, d.peer.channel, err)
	}
	return status.ReceiveOnlyTotalItems
}

// Await is Transport.Await for one device, for the waits that are about a single
// process. It reports both logs for the same reason.
func (d *Device) Await(what string, done func() bool) {
	d.t.Helper()
	deadline := time.Now().Add(awaitTimeout)
	for {
		if done() {
			return
		}
		if time.Now().After(deadline) {
			d.t.Fatalf("%s: waited %s for %s and it did not happen\n%s\n%s",
				d.Label, awaitTimeout, what, d.logTail(), d.peer.logTail())
		}
		time.Sleep(awaitPoll)
	}
}

// ping is the readiness probe and the liveness probe. It returns an error rather
// than failing the test, because both of its callers are loops that expect it to
// fail for a while.
func (d *Device) ping() error {
	return d.get("/rest/system/ping", nil, nil)
}

// Connected reports whether this device currently holds a live connection to its
// peer. A device that is not running is not connected, which lets a poll loop
// run across a restart without special-casing it.
//
// It is exported because a scenario that publishes into a severed link has to be
// able to say the link was severed. Without it, a test that waits for a package
// not to arrive passes just as well when the transport was never going to deliver
// it for some other reason.
func (d *Device) Connected() bool {
	if d.proc == nil {
		return false
	}
	var body struct {
		Connections map[string]struct {
			Connected bool `json:"connected"`
		} `json:"connections"`
	}
	if err := d.get("/rest/system/connections", nil, &body); err != nil {
		return false
	}
	return body.Connections[d.peer.ID].Connected
}

func (d *Device) get(path string, query url.Values, out any) error {
	return d.call(http.MethodGet, path, query, out)
}

func (d *Device) post(path string, query url.Values, out any) error {
	return d.call(http.MethodPost, path, query, out)
}

// call makes one authenticated request to this device's private API.
//
// The key is per-instance and the listener is on loopback, which together are
// the whole of "private": two fixtures running at once cannot drive each other,
// and nothing off this machine can drive either.
func (d *Device) call(method, path string, query url.Values, out any) error {
	endpoint := "http://" + d.admin + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequest(method, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", d.apiKey)
	client := &http.Client{Timeout: apiTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, body)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s %s: decode response: %w", method, path, err)
	}
	return nil
}

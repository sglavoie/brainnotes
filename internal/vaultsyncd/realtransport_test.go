//go:build synctransport

package vaultsyncd

import (
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/synctransport"
	"github.com/sglavoie/brainnotes/internal/vaultfixture"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

// The scenarios in this file are the ones roundtrip_test.go cannot ask, because
// their subject is the transport and roundtrip_test.go's transport is a function
// call. Each of them runs two real Syncthing processes over two disposable
// vaults; see internal/synctransport for what that means and what it refuses.
//
// They all run behind the `synctransport` build tag, from
// `just test-sync-transport`. Run one with:
//
//	go test -tags synctransport -run TestRealTransportCarries ./internal/vaultsyncd/

const realNote = "Sync/Real.md"

// TestRealTransportCarriesAnEditIntoThePeerVault is the whole handshake with
// nothing faked: an edit, a package, a program neither vault controls, an
// application, a receipt back, a retirement, and a released receipt.
//
// Every step waits rather than asserts-and-continues, because over a real
// transport "it has not happened yet" and "it will never happen" look the same
// for the first few seconds. What is asserted at each step is a vault's own
// belief, which is the only evidence that does not depend on trusting Syncthing.
func TestRealTransportCarriesAnEditIntoThePeerVault(t *testing.T) {
	w := enroll(t)
	l := w.live(t)

	w.write(w.root(), realNote, localBody)
	w.once(w.root())
	published := onlyPackage(t, w.channel(vaultsync.OutgoingDir(w.root())))

	applied := l.settle(w.peerRoot(), "the peer to apply "+published, func() bool {
		return l.body(w.peerRoot(), realNote) == localBody
	})
	if imported := applied.imported(t); imported.Changed == 0 {
		t.Fatalf("the peer's vault holds the edit but its drain changed nothing: %+v", imported)
	}
	if got := w.receipts(vaultsync.OutgoingDir(w.peerRoot())); !contains(got, published) {
		t.Fatalf("the peer published receipts %v, want one for %s", got, published)
	}
	coverOutgoing(t, w.root())

	// The receipt crosses back on the same transport. With independently
	// verified backup coverage, the sender then has grounds to remove the package.
	retiring := l.settle(w.root(), "the sender to retire "+published, func() bool {
		return !contains(w.channel(vaultsync.OutgoingDir(w.root())), published)
	})
	if retired := retiring.retired(t).Retired; !contains(retired, published) {
		t.Fatalf("the package left the channel but the pass retired %v", retired)
	}

	// And the retirement crosses forward, which is what tells the peer its
	// receipt has nothing left to acknowledge.
	l.settle(w.peerRoot(), "the peer to release its receipt", func() bool {
		return !contains(w.receipts(vaultsync.OutgoingDir(w.peerRoot())), published)
	})

	requireVaultsUntouched(t, w)
}

// TestRealTransportNamesOnlyStagingDirectories asks the two running processes
// what they are serving, and requires the answer to be four staging directories
// and nothing else.
//
// This is the claim the whole staging-channel design exists to make, and it is
// worth asking the program rather than the file this repository wrote: a
// configuration Syncthing migrated, defaulted, or ignored would still look
// correct on disk. The types matter as much as the paths — a send-receive folder
// anywhere here would make both devices authors of one channel, and retirement
// would stop meaning anything.
func TestRealTransportNamesOnlyStagingDirectories(t *testing.T) {
	w := enroll(t)
	l := w.live(t)

	cases := []struct {
		device *synctransport.Device
		root   string
	}{
		{l.st.Local, w.root()},
		{l.st.Peer, w.peerRoot()},
	}
	for _, c := range cases {
		folders := c.device.Folders()
		staging := filepath.Join(c.root, ".bnotes", "sync")
		for _, folder := range folders {
			if !strings.HasPrefix(filepath.Clean(folder.Path), staging+string(filepath.Separator)) {
				t.Fatalf("%s serves %q, which is not under %s", c.device.Label, folder.Path, staging)
			}
		}
		want := []string{
			"bnotes-local=" + ownership(c.device.Label, "local"),
			"bnotes-peer=" + ownership(c.device.Label, "peer"),
		}
		if got := folderTypes(folders); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s serves %v, want %v", c.device.Label, got, want)
		}
	}
}

// TestRealTransportRunsTheConfigurationItWasGiven asks both processes which
// schema they ended up on and which doors they have open.
//
// Neither is visible in the file this harness wrote. A release that does not know
// the schema migrates it on startup and keeps the folders, so every other test
// here would still pass against a configuration Syncthing rewrote — which is the
// outcome the pin between syncthingbin.Version and synctransport.ConfigVersion
// exists to prevent, and the reason the pairing is checked rather than asserted
// in a comment. The options are the network containment guarantee: discovery,
// relays, NAT traversal, auto-upgrade and crash reporting are each a way a
// fixture could reach something that is not its own other half.
func TestRealTransportRunsTheConfigurationItWasGiven(t *testing.T) {
	w := enroll(t)
	l := w.live(t)

	for _, device := range []*synctransport.Device{l.st.Local, l.st.Peer} {
		if got := device.ConfigVersion(); got != synctransport.ConfigVersion {
			t.Fatalf("%s is running schema version %d, not the %d this harness wrote: "+
				"the release migrated the configuration, so nothing it declares is load-bearing",
				device.Label, got, synctransport.ConfigVersion)
		}
		reported := device.Options()
		closed := synctransport.Containment()
		if len(closed) == 0 {
			t.Fatal("the containment table is empty, so this loop asserts nothing")
		}
		for key, want := range closed {
			got, named := reported[key]
			if !named {
				t.Fatalf("%s reports no option %q: the release renamed it and this assertion covers nothing", device.Label, key)
			}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("%s reports %s=%v, want %v", device.Label, key, got, want)
			}
		}
		// The address is the containment guarantee the flags cannot state: an
		// ignored listenAddress becomes the default, which is every interface.
		want := []any{"tcp://" + device.SyncAddress()}
		if got := reported["listenAddresses"]; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s listens on %v, want only %v", device.Label, got, want)
		}
	}
}

// TestRealTransportDeliversOutOfOrderAndConvergesOnTheLaterEdit withholds the
// first of two packages so the peer is offered the second one first.
//
// Nothing about a real transport promises an order. Syncthing delivers what it
// has, and a package can be held back by a device that was offline when it was
// published, so the receiver must be able to see a change whose predecessor it
// has never seen, decline to apply it, and still converge when the gap is filled.
// The assertion in the middle is the important one: the peer's vault does not
// have the note at all, rather than having a half-applied version of it.
func TestRealTransportDeliversOutOfOrderAndConvergesOnTheLaterEdit(t *testing.T) {
	w := enroll(t)
	w.write(w.root(), realNote, "first\n")
	w.once(w.root())
	first := onlyPackage(t, w.channel(vaultsync.OutgoingDir(w.root())))
	w.write(w.root(), realNote, "second\n")
	w.once(w.root())
	second := added(t, w.channel(vaultsync.OutgoingDir(w.root())), first)

	release := withhold(t, w.root(), w.pair.Dir, first)
	l := w.live(t)

	l.settle(w.peerRoot(), "the peer to receive "+second, func() bool {
		return l.delivered(w.peerRoot(), second)
	})
	l.heldThroughout(w.peerRoot(), realNote,
		"the peer was offered a change whose predecessor it has never seen")

	release()
	l.settle(w.peerRoot(), "the peer to converge once the gap is filled", func() bool {
		return l.body(w.peerRoot(), realNote) == "second\n"
	})
	if got := w.receipts(vaultsync.OutgoingDir(w.peerRoot())); len(got) != 2 {
		t.Fatalf("the peer published receipts %v, want one for each package", got)
	}
}

// TestRealTransportSurvivesADisconnect publishes while the link is down.
//
// A disconnected peer is the ordinary case for this deployment — a laptop and a
// Raspberry Pi are rarely both awake — and the design's answer is that delivery
// is delayed rather than failed. The device is paused rather than killed so that
// the only thing removed is the connection: both processes keep indexing, keep
// their databases, and keep believing exactly what they believed a moment ago.
func TestRealTransportSurvivesADisconnect(t *testing.T) {
	w := enroll(t)
	l := w.live(t)
	l.st.Local.Disconnect()

	w.write(w.root(), realNote, localBody)
	w.once(w.root())
	published := onlyPackage(t, w.channel(vaultsync.OutgoingDir(w.root())))

	// Asked of both ends, and asked *while* the package is undelivered rather
	// than before it was published: "it did not arrive" is only evidence about a
	// disconnect if the link was still down the whole time it did not arrive.
	l.stays("a package reached a disconnected peer", func() bool {
		if l.st.Local.Connected() || l.st.Peer.Connected() {
			t.Fatalf("the link came back on its own; the scenario tested nothing")
		}
		return !l.delivered(w.peerRoot(), published)
	})
	if pass := w.once(w.peerRoot()); pass.imported(t).Answered != 0 {
		t.Fatalf("the peer answered a package it cannot have: %+v", pass.imported(t))
	}

	l.st.Local.Reconnect()
	l.settle(w.peerRoot(), "the peer to apply "+published+" after reconnecting", func() bool {
		return l.body(w.peerRoot(), realNote) == localBody
	})
}

// TestRealTransportResumesAfterARestart takes a device away entirely, publishes
// while it is gone, and brings it back on the same identity and database.
//
// This is the case a paused device does not cover: the process that has to
// notice the package is not merely unreachable, it does not exist, and when it
// comes back it has to work out what it missed from what it wrote down rather
// than from what it remembered. A new identity would be a different scenario and
// a much weaker one, which is why internal/synctransport's Restart reuses the
// home.
func TestRealTransportResumesAfterARestart(t *testing.T) {
	w := enroll(t)
	l := w.live(t)

	// One package crosses first, so the restart interrupts a working transport
	// rather than an untested one.
	w.write(w.root(), realNote, "before the restart\n")
	w.once(w.root())
	first := onlyPackage(t, w.channel(vaultsync.OutgoingDir(w.root())))
	l.settle(w.peerRoot(), "the peer to apply the first edit", func() bool {
		return l.body(w.peerRoot(), realNote) == "before the restart\n"
	})

	l.st.Peer.Stop()
	if l.st.Peer.Running() {
		t.Fatalf("the peer is still running after Stop")
	}
	w.write(w.root(), realNote, "while it was away\n")
	w.once(w.root())
	// The first package may or may not have been retired by now — that depends on
	// a receipt this scenario is not waiting for — so the second one is named by
	// what is new rather than by what is alone.
	published := added(t, w.channel(vaultsync.OutgoingDir(w.root())), first)

	l.st.Peer.Restart()
	l.st.AwaitConnected()
	l.settle(w.peerRoot(), "the restarted peer to apply "+published, func() bool {
		return l.body(w.peerRoot(), realNote) == "while it was away\n"
	})
}

// TestRealTransportDeliversAnIncompletePackageAndTheVaultRefusesIt strips a
// package's manifest before it is offered.
//
// This is what an interrupted copy looks like from the receiving side: files are
// present, the one that says what they are is not, and nothing distinguishes
// "still arriving" from "will never arrive". The vault's answer to both is the
// same and is the safe one — hold it, change nothing, acknowledge nothing — and
// holding is what lets the still-arriving case resolve itself next pass.
func TestRealTransportDeliversAnIncompletePackageAndTheVaultRefusesIt(t *testing.T) {
	w := enroll(t)
	w.write(w.root(), realNote, localBody)
	w.once(w.root())
	published := onlyPackage(t, w.channel(vaultsync.OutgoingDir(w.root())))
	truncateManifest(t, w.root(), published)

	l := w.live(t)
	requireDeliveredAndRefused(t, l, published)
}

// TestRealTransportDeliversAnAlteredPackageAndTheVaultRefusesIt changes the
// payload bytes and leaves the manifest describing what they used to be.
//
// The transport carries this perfectly — that is what a transport does — and the
// refusal has to come from the package's own integrity record. It is the same
// outcome as the incomplete case on purpose: a receiver cannot tell corruption
// from interruption, and a design that treated them differently would have to
// guess which one it was looking at.
func TestRealTransportDeliversAnAlteredPackageAndTheVaultRefusesIt(t *testing.T) {
	w := enroll(t)
	w.write(w.root(), realNote, localBody)
	w.once(w.root())
	published := onlyPackage(t, w.channel(vaultsync.OutgoingDir(w.root())))
	alterPayloads(t, w.root(), published)

	l := w.live(t)
	requireDeliveredAndRefused(t, l, published)
}

// TestRealTransportCleanupIsSenderOwned is the claim the directional folders
// exist to make, stated as a sequence of removals.
//
// A receiver that deletes a package it has applied has not retired anything. Its
// incoming folder is receive-only, so Syncthing records the deletion as a local
// deviation and keeps offering the bytes; the package is still the sender's, the
// sender still has it, and reverting brings the receiver's copy back from the
// sender. What does remove it everywhere is the sender removing it, on the
// evidence of a receipt — and that removal crosses.
func TestRealTransportCleanupIsSenderOwned(t *testing.T) {
	w := enroll(t)
	l := w.live(t)

	w.write(w.root(), realNote, localBody)
	w.once(w.root())
	published := onlyPackage(t, w.channel(vaultsync.OutgoingDir(w.root())))
	l.settle(w.peerRoot(), "the peer to apply "+published, func() bool {
		return l.body(w.peerRoot(), realNote) == localBody
	})

	discardDelivered(t, w.peerRoot(), published)
	l.st.Peer.Await("the peer's deletion to be recorded as a local deviation", func() bool {
		return l.st.Peer.Deviations() > 0
	})
	// One window, both halves of the claim: the sender still holds the package —
	// the receiver's deletion did not travel — and the receiver still does not,
	// so what Revert brings back below is genuinely a copy it had thrown away
	// rather than one that quietly returned before anything asked.
	l.stays("the receiver's deletion reached the sender's channel, or its own copy returned unasked", func() bool {
		return contains(w.channel(vaultsync.OutgoingDir(w.root())), published) &&
			!l.delivered(w.peerRoot(), published)
	})

	// Reverting asks the sender for what the receiver threw away, which it can
	// only answer because it never agreed the package was gone.
	l.st.Peer.Revert()
	l.st.Peer.Await("the sender's copy to come back", func() bool {
		return l.delivered(w.peerRoot(), published)
	})

	// The sender holds the receipt by now, so this pass retires on evidence, and
	// that removal is the one the receiver honours. Retirement also requires the
	// exact package to be in a verified private backup; without this coverage the
	// safe result is to retain the sender's last local recovery copy.
	coverOutgoing(t, w.root())
	l.settle(w.root(), "the sender to retire "+published, func() bool {
		return !contains(w.channel(vaultsync.OutgoingDir(w.root())), published)
	})
	l.st.Local.Await("the retirement to reach the peer", func() bool {
		return !l.delivered(w.peerRoot(), published)
	})
}

// requireDeliveredAndRefused is the shared half of the incomplete and altered
// scenarios: the package arrives, and the vault stays exactly as it was.
//
// Both halves are asserted. A test that only checked the vault was unchanged
// would pass just as well against a transport that delivered nothing at all,
// which is the failure these two scenarios are least able to tolerate.
func requireDeliveredAndRefused(t *testing.T, l *live, published string) {
	t.Helper()
	w := l.world
	l.settle(w.peerRoot(), "the peer to receive "+published, func() bool {
		return l.delivered(w.peerRoot(), published)
	})
	l.heldThroughout(w.peerRoot(), realNote, "the peer's drain of an unverifiable package")

	if got := w.receipts(vaultsync.OutgoingDir(w.peerRoot())); len(got) != 0 {
		t.Fatalf("the peer acknowledged %v, having applied nothing", got)
	}
	if dirty := w.pair.Peer.Dirty(t); !onlyStagingIsDirty(dirty) {
		t.Fatalf("the peer's working tree changed: %v", dirty)
	}
}

// requireVaultsUntouched states the boundary from the vault's side: whatever
// Syncthing did, it did it inside .bnotes/sync.
//
// The sentinel note is internal/vaultfixture's, and it is here for the reason it
// exists: nothing in this scenario names it, so a transport that had been pointed
// at a vault directory rather than a staging directory would show up as that file
// changing or as Git noticing something outside .bnotes.
func requireVaultsUntouched(t *testing.T, w *world) {
	t.Helper()
	unrelated := vaultfixture.DefaultFiles()[vaultfixture.UnrelatedNote]
	for _, vault := range []*vaultfixture.Vault{w.pair.Local, w.pair.Peer} {
		if got := vault.Read(t, vaultfixture.UnrelatedNote); got != unrelated {
			t.Fatalf("%s: the sentinel note reads %q", vault.Name, got)
		}
		if dirty := vault.Dirty(t); !onlyStagingIsDirty(dirty) {
			t.Fatalf("%s: the working tree holds %v, want nothing outside .bnotes", vault.Name, dirty)
		}
	}
}

// onlyStagingIsDirty reports whether every porcelain entry is under .bnotes,
// which is where this repository's untracked state — the locks and the sync
// channels — has always lived.
func onlyStagingIsDirty(lines []string) bool {
	for _, line := range lines {
		if len(line) < 4 || !strings.HasPrefix(strings.TrimSpace(line[2:]), ".bnotes/") {
			return false
		}
	}
	return true
}

// ownership names the folder type a device gives a channel: its own is the one
// it sends, and its peer's is the one it only receives.
func ownership(device, owner string) string {
	if device == owner {
		return "sendonly"
	}
	return "receiveonly"
}

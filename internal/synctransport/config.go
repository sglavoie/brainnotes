package synctransport

import (
	"encoding/xml"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/syncthingbin"
)

// ConfigVersion is the configuration schema the pinned release reads. It travels
// with syncthingbin.Version: writing a schema a release does not know makes it
// migrate the file on startup, and a migration is a silent rewrite of the exact
// thing these tests are trying to state precisely.
//
// It is exported because the pairing has to be *checked* rather than asserted in
// a comment. A migration preserves folders, so every other test here would still
// pass against a rewritten configuration;
// TestRealTransportRunsTheConfigurationItWasGiven asks the running process which
// schema it ended up on and fails when it is not this one.
const ConfigVersion = 37

// configVersion is ConfigVersion as the document's attribute spells it.
var configVersion = strconv.Itoa(ConfigVersion)

// Folder types. Every folder in this harness is one or the other, on purpose:
// there is no send-receive folder anywhere, because a channel with two writers
// is not the channel this architecture describes.
const (
	typeSendOnly    = "sendonly"
	typeReceiveOnly = "receiveonly"
)

// rescanIntervalS and watcherDelayS make the transport quick rather than
// polite. A real deployment rescans every hour and debounces for ten seconds;
// a test that did that would take an hour. The filesystem watcher is what
// actually delivers here and the rescan is the backstop for a platform where
// the watcher is unavailable, which is why it is still on.
const (
	rescanIntervalS    = 5
	watcherDelayS      = 1
	reconnectIntervalS = 5
)

// RescanInterval is rescanIntervalS as a duration, exported so that a test which
// waits for something *not* to happen can derive its window from the backstop
// rather than restate it.
//
// A quiet period shorter than this proves nothing on a platform where the
// filesystem watcher failed to start: the sender may simply not have looked yet.
const RescanInterval = rescanIntervalS * time.Second

// xmlConfig is the subset of Syncthing's configuration this harness states.
//
// It is written rather than patched. Syncthing's own `generate` produces a file
// full of defaults — dynamic addresses, global discovery, relays, the machine's
// hostname as a device name — and starting from it would mean this package's
// guarantees were a list of things it remembered to turn off. Writing the whole
// document means every element that is here is here deliberately, and anything
// absent takes the release's documented default.
type xmlConfig struct {
	XMLName xml.Name    `xml:"configuration"`
	Version string      `xml:"version,attr"`
	Folders []xmlFolder `xml:"folder"`
	Devices []xmlDevice `xml:"device"`
	GUI     xmlGUI      `xml:"gui"`
	Options xmlOptions  `xml:"options"`
}

type xmlFolder struct {
	ID               string           `xml:"id,attr"`
	Label            string           `xml:"label,attr"`
	Path             string           `xml:"path,attr"`
	Type             string           `xml:"type,attr"`
	RescanIntervalS  int              `xml:"rescanIntervalS,attr"`
	FSWatcherEnabled bool             `xml:"fsWatcherEnabled,attr"`
	FSWatcherDelayS  int              `xml:"fsWatcherDelayS,attr"`
	Devices          []xmlFolderShare `xml:"device"`
	MinDiskFree      xmlDiskFree      `xml:"minDiskFree"`
}

type xmlFolderShare struct {
	ID string `xml:"id,attr"`
}

// xmlDiskFree is set to zero because the default is a percentage of the volume,
// and a developer's nearly-full laptop would pause a folder rather than fail a
// test, which reads as a hang.
type xmlDiskFree struct {
	Unit  string `xml:"unit,attr"`
	Value int    `xml:",chardata"`
}

// xmlDevice names a peer and where to reach it. The address is always an
// explicit loopback endpoint: with discovery off there is nothing else that
// could tell one process where the other is, which is the property that keeps
// this harness from finding a Syncthing on the developer's actual network.
type xmlDevice struct {
	ID        string   `xml:"id,attr"`
	Name      string   `xml:"name,attr"`
	Addresses []string `xml:"address"`
}

// xmlGUI is the private administration surface. It is bound to loopback, has a
// per-instance key, and exists only so the tests can say "disconnect now" and
// "what do you believe about this folder" to a running process.
type xmlGUI struct {
	Enabled bool   `xml:"enabled,attr"`
	TLS     bool   `xml:"tls,attr"`
	Address string `xml:"address"`
	APIKey  string `xml:"apikey"`
}

// xmlOptions is the list of ways a Syncthing can reach the outside world, all
// turned off, plus the one address it may listen on.
type xmlOptions struct {
	ListenAddresses       []string `xml:"listenAddress"`
	GlobalAnnounceEnabled bool     `xml:"globalAnnounceEnabled"`
	LocalAnnounceEnabled  bool     `xml:"localAnnounceEnabled"`
	RelaysEnabled         bool     `xml:"relaysEnabled"`
	NATEnabled            bool     `xml:"natEnabled"`
	AnnounceLANAddresses  bool     `xml:"announceLANAddresses"`
	StartBrowser          bool     `xml:"startBrowser"`
	CrashReportingEnabled bool     `xml:"crashReportingEnabled"`
	URAccepted            int      `xml:"urAccepted"`
	AutoUpgradeIntervalH  int      `xml:"autoUpgradeIntervalH"`
	ReconnectionIntervalS int      `xml:"reconnectionIntervalS"`
}

// renderConfig produces the configuration document for one device.
//
// self is the device the file belongs to and peer is the other one; both appear
// in the device list, because Syncthing's configuration describes a cluster and
// omitting yourself from it is how you tell it the file is somebody else's.
func renderConfig(self, peer *Device) ([]byte, error) {
	doc := xmlConfig{
		Version: configVersion,
		Folders: []xmlFolder{
			share(self.channel, typeSendOnly, self.outgoing, self, peer),
			share(peer.channel, typeReceiveOnly, self.incoming, self, peer),
		},
		Devices: []xmlDevice{
			{ID: self.ID, Name: self.Label, Addresses: []string{"tcp://" + self.sync}},
			{ID: peer.ID, Name: peer.Label, Addresses: []string{"tcp://" + peer.sync}},
		},
		GUI:     xmlGUI{Enabled: true, TLS: false, Address: self.admin, APIKey: self.apiKey},
		Options: options(self.sync),
	}
	body, err := xml.MarshalIndent(doc, "", "    ")
	if err != nil {
		return nil, fmt.Errorf("synctransport: render %s configuration: %w", self.Label, err)
	}
	return append(body, '\n'), nil
}

// share describes one directional folder. Both devices list the same folder id
// with the same two members and opposite types, which is what makes it one
// folder with an owner rather than two folders that happen to agree.
func share(id, kind, path string, self, peer *Device) xmlFolder {
	return xmlFolder{
		ID:               id,
		Label:            id,
		Path:             path,
		Type:             kind,
		RescanIntervalS:  rescanIntervalS,
		FSWatcherEnabled: true,
		FSWatcherDelayS:  watcherDelayS,
		Devices:          []xmlFolderShare{{ID: self.ID}, {ID: peer.ID}},
		MinDiskFree:      xmlDiskFree{Unit: "B", Value: 0},
	}
}

// options closes every door but the one the peer comes through.
//
// Each of these is a way a test process could reach, or be reached by,
// something that is not the other half of this fixture: announcing itself on the
// local network, asking Syncthing's discovery servers where its peer is, falling
// back to a public relay, opening a port on the router, reporting usage, mailing
// a crash, or upgrading itself to a release this file's schema does not match.
func options(listen string) xmlOptions {
	return xmlOptions{
		ListenAddresses:       []string{"tcp://" + listen},
		GlobalAnnounceEnabled: false,
		LocalAnnounceEnabled:  false,
		RelaysEnabled:         false,
		NATEnabled:            false,
		AnnounceLANAddresses:  false,
		StartBrowser:          false,
		CrashReportingEnabled: false,
		URAccepted:            -1,
		AutoUpgradeIntervalH:  0,
		ReconnectionIntervalS: reconnectIntervalS,
	}
}

// Containment is every scalar option this harness declares, keyed the way the
// running process names them, so a test can require the program to agree with the
// file.
//
// The folder set is read back out of the API for a stated reason — a
// configuration the release migrated, defaulted or ignored still looks correct on
// disk — and that argument is stronger here than it is for folders: this is the
// *network* guarantee, the one that says a fixture cannot reach anything but its
// own other half. Values are compared as text by the caller, because the API
// answers in JSON where every number is a float, and a key absent from the answer
// is a failure rather than a zero: a release that renamed one of these would
// otherwise turn the assertion into a formality.
//
// It is **derived from xmlOptions rather than listed beside it**, since a table
// maintained by hand is a table that silently stops covering the next door
// somebody closes. Syncthing's JSON names each of these scalars exactly as its XML
// does, which is what makes the derivation legitimate. The one field skipped is
// the listen address — a slice, spelled `listenAddress` here and
// `listenAddresses` there, holding a port reserved per run, so it can be neither
// derived nor stated as a constant. The caller asserts it against the device it
// belongs to, which is what Device.SyncAddress is for, and it is as much the
// containment guarantee as any flag above: a release that ignored it would fall
// back to binding every interface on the machine.
func Containment() map[string]any {
	declared := reflect.ValueOf(options(""))
	fields := declared.Type()
	closed := make(map[string]any, fields.NumField())
	for i := range fields.NumField() {
		field := fields.Field(i)
		if field.Type.Kind() == reflect.Slice {
			continue
		}
		closed[field.Tag.Get("xml")] = declared.Field(i).Interface()
	}
	return closed
}

// pinned is the version this configuration is written for, re-exported so a
// failure message can say which release refused it.
const pinned = syncthingbin.Version

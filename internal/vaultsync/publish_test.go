package vaultsync

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPublishLaysOutADurablePackage checks the shape a receiver reads: a
// directory named for the package identity, the payload bytes under it, and the
// manifest that names them.
func TestPublishLaysOutADurablePackage(t *testing.T) {
	r := enroll(t)
	channel := r.channel(t)
	body := []byte("alpha body\n")

	manifest, dir := r.publish(t, channel, func(b *Builder) {
		b.Write("Alpha.md", body, Absent())
	})

	if got, want := filepath.Base(dir), manifest.PackageID; got != want {
		t.Errorf("package directory is %q, want the package identity %q", got, want)
	}
	if filepath.Dir(dir) != channel {
		t.Errorf("package was published outside the channel: %q", dir)
	}

	payload := filepath.Join(dir, PayloadsName, manifest.Payloads[0].Name)
	onDisk, err := os.ReadFile(payload)
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if string(onDisk) != string(body) {
		t.Errorf("payload holds %q, want %q", onDisk, body)
	}

	// The receipts sub-channel exists as soon as the channel does, so the peer
	// has somewhere to answer without creating a directory in someone else's
	// channel.
	if info, err := os.Stat(ReceiptsDir(channel)); err != nil || !info.IsDir() {
		t.Errorf("publishing did not open the receipts sub-channel: %v", err)
	}
}

// TestPublishLeavesNoStagingBehind is the other half of the manifest's atomic
// appearance: the record is staged under a temporary before it is linked into
// place, and a temporary still sitting in a channel when Publish returns is a
// file the transport would copy to the peer as though it were part of the
// package.
func TestPublishLeavesNoStagingBehind(t *testing.T) {
	r := enroll(t)
	_, dir := r.publish(t, r.channel(t), func(b *Builder) {
		b.Write("Alpha.md", []byte("alpha body\n"), Absent())
	})

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if len(names) != 2 || names[0] != ManifestName || names[1] != PayloadsName {
		t.Errorf("a published package holds %v, want exactly the manifest and the payload directory", names)
	}
}

// TestPublishRoundTripsTheManifest is the other half: what a receiver reads back
// is the package that was published, tombstones included. A delete that arrived
// as an ordinary absence would be a delete nothing can order against a
// concurrent edit.
func TestPublishRoundTripsTheManifest(t *testing.T) {
	r := enroll(t)
	manifest, dir := r.publish(t, r.channel(t), func(b *Builder) {
		b.Write("Alpha.md", []byte("alpha body\n"), Absent())
		b.Delete("Beta.md", Existing(HashBytes([]byte("beta\n"))))
	})

	reread, err := ReadManifest(dir)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if reread.PackageID != manifest.PackageID || len(reread.Changes()) != len(manifest.Changes()) {
		t.Errorf("the manifest on disk is not the one published:\n%+v\n%+v", reread, manifest)
	}
	if !reread.Changes()[1].Tombstone() {
		t.Error("the tombstone did not survive the round trip")
	}
}

// TestPublishIsImmutable is the guarantee the exclusive mkdir exists for: a
// second publication under one identity fails, and the bytes a peer may already
// have read are still there afterwards.
func TestPublishIsImmutable(t *testing.T) {
	r := enroll(t)
	channel := r.channel(t)

	builder, err := NewBuilder(r.peer, 1, r.clock.Tick())
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	manifest, payloads, err := builder.
		BeginOperation("", r.clock.Now()).
		Write("Alpha.md", []byte("first\n"), Absent()).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	dir, err := Publish(channel, manifest, payloads)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Same identity, different bytes: exactly the case that must not replace
	// what is already published.
	second := *manifest
	second.Operations = []Operation{{
		OperationID: manifest.Operations[0].OperationID,
		RecordedAt:  manifest.Operations[0].RecordedAt,
		Changes: []Change{{
			Path:         "Alpha.md",
			Action:       ActionWrite,
			Predecessors: []string{},
			ResultSHA256: HashBytes([]byte("second\n")),
			ResultSize:   int64(len("second\n")),
			Payload:      HashBytes([]byte("second\n")) + payloadSuffix,
		}},
	}}
	second.Payloads = []Payload{{
		Name:   HashBytes([]byte("second\n")) + payloadSuffix,
		SHA256: HashBytes([]byte("second\n")),
		Size:   int64(len("second\n")),
	}}
	_, err = Publish(channel, &second, map[string][]byte{second.Payloads[0].Name: []byte("second\n")})
	requireIs(t, err, ErrPackageExists, "republishing one identity")

	data, err := ReadPayload(dir, manifest, manifest.Payloads[0].Name)
	if err != nil {
		t.Fatalf("ReadPayload: %v", err)
	}
	if string(data) != "first\n" {
		t.Errorf("the refused publication replaced the payload: %q", data)
	}
}

// TestPublishRefusesBeforeAnythingExists covers the other half of immutability:
// a package that was never publishable must not leave a directory a reader would
// have to interpret.
func TestPublishRefusesBeforeAnythingExists(t *testing.T) {
	r := enroll(t)

	cases := []struct {
		name    string
		want    error
		corrupt func(*Manifest, map[string][]byte)
	}{
		{
			name: "a manifest that is not a package",
			want: ErrUnsupportedFormat,
			corrupt: func(m *Manifest, _ map[string][]byte) {
				m.Format = "something-else"
			},
		},
		{
			name: "a private path",
			want: ErrPrivatePath,
			corrupt: func(m *Manifest, _ map[string][]byte) {
				m.Operations[0].Changes[0].Path = ".bnotes/recall.json"
			},
		},
		{
			name: "a payload the caller did not supply",
			want: ErrIncompletePackage,
			corrupt: func(m *Manifest, payloads map[string][]byte) {
				delete(payloads, m.Payloads[0].Name)
			},
		},
		{
			name: "a payload the inventory does not list",
			want: ErrIncompletePackage,
			corrupt: func(_ *Manifest, payloads map[string][]byte) {
				payloads["extra.bin"] = []byte("extra")
			},
		},
		{
			name: "payload bytes that are not what the inventory says",
			want: ErrIncompletePackage,
			corrupt: func(m *Manifest, payloads map[string][]byte) {
				payloads[m.Payloads[0].Name] = []byte("tampered with, same length!")
				m.Payloads[0].Size = int64(len("tampered with, same length!"))
				m.Operations[0].Changes[0].ResultSize = m.Payloads[0].Size
			},
		},
		{
			name: "payload bytes of the wrong length",
			want: ErrIncompletePackage,
			corrupt: func(m *Manifest, payloads map[string][]byte) {
				payloads[m.Payloads[0].Name] = []byte("short")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			channel := r.channel(t)
			manifest, payloads := r.draft(t, func(b *Builder) {
				b.Write("Alpha.md", []byte("alpha body\n"), Absent())
			})
			tc.corrupt(manifest, payloads)

			dir, err := Publish(channel, manifest, payloads)
			requireIs(t, err, tc.want, tc.name)
			if dir != "" {
				t.Errorf("a refused Publish returned a directory: %q", dir)
			}
			if _, err := os.Stat(filepath.Join(channel, manifest.PackageID)); !os.IsNotExist(err) {
				t.Errorf("a refused Publish left a package directory behind: %v", err)
			}
		})
	}
}

// TestPublishDiscardsAPackageItCouldNotFinish covers the window the refusals
// above never enter: the exclusive mkdir has already succeeded and the failure
// happens while the payloads are being written, before the manifest exists.
//
// That is the one region where a partial package is on disk and removing it is
// still free — no manifest means no Accept can have succeeded against it — and
// it is where discardPartial runs. Every other Publish failure either happens
// before the directory exists or after the commit point, so without this case
// the discard is never exercised by anything.
//
// The lever is a payload name longer than a filesystem will hold in one
// component. The format deliberately does not bound the length (containment is
// about the name being a single component, not about how long it is), so the
// manifest validates, its inventory matches the supplied bytes, and the write
// is what fails.
func TestPublishDiscardsAPackageItCouldNotFinish(t *testing.T) {
	r := enroll(t)
	channel := r.channel(t)
	manifest, payloads := r.draft(t, func(b *Builder) {
		b.Write("Alpha.md", []byte("alpha body\n"), Absent())
	})

	// Longer than the 255-byte component limit both target filesystems enforce.
	unwritable := strings.Repeat("a", 400) + payloadSuffix
	original := manifest.Payloads[0].Name
	manifest.Payloads[0].Name = unwritable
	manifest.Operations[0].Changes[0].Payload = unwritable
	payloads[unwritable] = payloads[original]
	delete(payloads, original)

	// Stated rather than assumed: if a future length rule moves this refusal
	// earlier, this test stops reaching the discard and must be told so instead
	// of quietly passing for a different reason.
	if err := manifest.Validate(); err != nil {
		t.Fatalf("the manifest no longer reaches the write step: %v", err)
	}

	dir, err := Publish(channel, manifest, payloads)
	if err == nil {
		t.Fatal("publishing a payload no filesystem can name succeeded")
	}
	if dir != "" {
		t.Errorf("a failed Publish returned a directory: %q", dir)
	}
	if _, err := os.Stat(filepath.Join(channel, manifest.PackageID)); !os.IsNotExist(err) {
		t.Errorf("a publication that failed before the commit point left its directory behind: %v", err)
	}

	// And the channel is still usable afterwards: the failure took the package
	// with it and nothing else.
	if _, published := r.publish(t, channel, func(b *Builder) {
		b.Write("Beta.md", []byte("beta body\n"), Absent())
	}); published == "" {
		t.Error("the channel was unusable after a failed publication")
	}
}

// TestPublishWritesEveryPayloadBeforeTheManifestThatNamesThem is the commit
// point stated as an assertion rather than as a comment on Publish.
//
// The ordering is the whole guarantee: a manifest is a decision — it says a
// package exists and names the bytes it carries — so it must not exist until
// those bytes do. Swap the two steps and the race test still passes, because a
// reader that finds a manifest with no payloads answers ErrIncompletePackage
// and a polling reader treats that as "still arriving". Nothing else in this
// package constrains the order at all.
//
// So ask the filesystem, which recorded it. Every payload is fsynced before the
// manifest is staged, so the manifest's modification time cannot precede any
// payload's — and under the swapped order it would precede all of them. The one
// thing that could make this vacuous is a filesystem whose timestamps are too
// coarse to separate the writes, which the payload spread below is checked for
// directly rather than hoped about.
func TestPublishWritesEveryPayloadBeforeTheManifestThatNamesThem(t *testing.T) {
	r := enroll(t)

	// Several payloads rather than one: each is a separate create-write-fsync,
	// so together they span enough wall time for any usable timestamp resolution
	// to separate the first from the last.
	const payloadCount = 8
	manifest, dir := r.publish(t, r.channel(t), func(b *Builder) {
		for i := range payloadCount {
			b.Write(fmt.Sprintf("Note-%d.md", i), []byte(fmt.Sprintf("body %d\n", i)), Absent())
		}
	})
	if len(manifest.Payloads) != payloadCount {
		t.Fatalf("the package carries %d payloads, want %d", len(manifest.Payloads), payloadCount)
	}

	oldest, newest := payloadTimeSpan(t, dir, manifest)

	// The premise: this filesystem can tell two of the publisher's own writes
	// apart. Without it the comparison below holds for any write order at all
	// and would be a test that cannot fail.
	if !oldest.Before(newest) {
		t.Skipf(
			"every payload reports the same modification time (%s); this filesystem's timestamps cannot order %d fsynced writes, so the ordering is untestable here",
			newest, payloadCount,
		)
	}

	info, err := os.Stat(filepath.Join(dir, ManifestName))
	if err != nil {
		t.Fatalf("stat manifest: %v", err)
	}
	// A strict inversion rather than "not strictly after". The manifest is
	// written immediately after the last payload, so the two can legitimately
	// land on the same tick even on a filesystem that separated the payload
	// spread above; demanding a strict gap there would fail a correct publisher.
	// The swapped order does not survive the weaker check either: the manifest
	// would precede every payload, and oldest is strictly before newest.
	if info.ModTime().Before(newest) {
		t.Errorf(
			"the manifest was written at %s, before the last payload at %s: the record naming the bytes reached the disk before the bytes did",
			info.ModTime(), newest,
		)
	}
}

// payloadTimeSpan is the earliest and latest modification time the filesystem
// recorded across a package's payload files.
func payloadTimeSpan(t *testing.T, dir string, m *Manifest) (oldest, newest time.Time) {
	t.Helper()
	for _, payload := range m.Payloads {
		info, err := os.Stat(filepath.Join(dir, PayloadsName, payload.Name))
		if err != nil {
			t.Fatalf("stat payload: %v", err)
		}
		at := info.ModTime()
		if oldest.IsZero() || at.Before(oldest) {
			oldest = at
		}
		if at.After(newest) {
			newest = at
		}
	}
	return oldest, newest
}

// TestMissingAncestorsNamesEveryDirectoryThatHadToBeCreated covers the input
// ensureChannel's fsync loop is built out of. The fsyncs themselves are not
// observable from a test, so the observable half is stated instead: which
// directories a publication had to bring into existence, deepest first.
func TestMissingAncestorsNamesEveryDirectoryThatHadToBeCreated(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	deep := filepath.Join(root, "sync", "outgoing", "receipts")

	missing, err := missingAncestors(deep)
	if err != nil {
		t.Fatalf("missingAncestors: %v", err)
	}
	want := []string{deep, filepath.Dir(deep), filepath.Dir(filepath.Dir(deep))}
	if len(missing) != len(want) {
		t.Fatalf("missingAncestors = %v, want %v", missing, want)
	}
	for i := range want {
		if missing[i] != want[i] {
			t.Errorf("missingAncestors[%d] = %q, want %q", i, missing[i], want[i])
		}
	}

	// Nothing to commit once they exist: a second publication into a live
	// channel must not re-fsync directories that were already durable.
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	again, err := missingAncestors(deep)
	if err != nil {
		t.Fatalf("missingAncestors: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("missingAncestors on an existing directory = %v, want nothing", again)
	}
}

// TestPublishCarriesNoPayloadDirectoryForTombstones pins that a package of pure
// deletions is complete with no bytes at all — VerifyPayloads must not read an
// absent directory as an undelivered one.
func TestPublishCarriesNoPayloadDirectoryForTombstones(t *testing.T) {
	r := enroll(t)
	channel := r.channel(t)

	manifest, dir := r.publish(t, channel, func(b *Builder) {
		b.Delete("Alpha.md", Existing(HashBytes([]byte("alpha\n"))))
		b.Delete("Beta.md", Existing(HashBytes([]byte("beta\n"))))
	})
	if len(manifest.Payloads) != 0 {
		t.Fatalf("a package of tombstones carries %d payloads", len(manifest.Payloads))
	}
	if _, err := os.Stat(filepath.Join(dir, PayloadsName)); !os.IsNotExist(err) {
		t.Errorf("a package with no bytes created a payload directory: %v", err)
	}
	if _, err := Accept(r.local, dir); err != nil {
		t.Errorf("a complete package of tombstones was not accepted: %v", err)
	}
}

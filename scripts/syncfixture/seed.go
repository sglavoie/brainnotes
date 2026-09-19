package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultimport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// world is what this program tells the browser suite, and every field is
// something a spec cannot derive: an identity minted here, a path chosen here, or
// a classification the engine made.
type world struct {
	LocalDevice string              `json:"localDevice"`
	PeerDevice  string              `json:"peerDevice"`
	Expected    map[string]string   `json:"expected"`
	Conflicts   map[string]conflict `json:"conflicts"`
	Dependent   dependent           `json:"dependent"`
}

// conflict is one held operation as the review page will present it. Kind is read
// back from the hold rather than asserted, so the manifest carries the engine's
// answer and never this program's guess at it.
type conflict struct {
	PackageID   string `json:"packageId"`
	OperationID string `json:"operationId"`
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	RenamedTo   string `json:"renamedTo,omitempty"`
	Local       string `json:"local,omitempty"`
	Remote      string `json:"remote,omitempty"`
	Baseline    string `json:"baseline,omitempty"`
}

// dependent is the package held behind the text conflict rather than by one of its
// own: it is what the blocked-dependency notice on that operation is counting.
type dependent struct {
	PackageID   string `json:"packageId"`
	OperationID string `json:"operationId"`
	Path        string `json:"path"`
}

// published is one package this program put in the incoming channel.
type published struct {
	PackageID   string
	OperationID string
}

// seed does the whole job in the one order it can be done in: diverge, commit,
// enroll, publish, drain, then check what the engine decided against what was
// intended.
func seed(p paths) (*world, error) {
	baselineConfig, err := os.ReadFile(filepath.Join(p.local, configPath))
	if err != nil {
		return nil, fmt.Errorf("read the baseline configuration: %w", err)
	}
	if err := diverge(p.local, baselineConfig); err != nil {
		return nil, err
	}
	if err := commit(p.local, p.home, "sync fixture: this device's side of every conflict"); err != nil {
		return nil, err
	}
	local, peer, err := enroll(p)
	if err != nil {
		return nil, err
	}
	sent, err := publish(p.local, peer, baselineConfig)
	if err != nil {
		return nil, err
	}
	held, err := drain(p.local)
	if err != nil {
		return nil, err
	}
	expected, err := writeExpected(p.expected)
	if err != nil {
		return nil, err
	}
	return describe(local, peer, expected, sent, held)
}

// diverge writes this device's version of every contested path.
//
// `Sync/Rename.md` is deliberately never written. What makes the incoming rename
// *divergent* is exactly that its source is gone here and its destination is free:
// this replica filed the note under `Sync/Archive/Rename.md` instead, for a reason
// the package cannot see, and internal/vaultimport holds that for a person rather
// than guessing between "moved elsewhere" and "deleted on purpose".
//
// Every file is dated seededAt rather than now, for the reason given there.
//
//bn:direct-write fixture seeding, inside a sandbox confine has already proved
func diverge(root string, baselineConfig []byte) error {
	for _, file := range []struct {
		rel     string
		content []byte
	}{
		{textPath, []byte(textLocal)},
		{attachmentPath, attachment("local")},
		{renameArchive, []byte(renameBaseline)},
		{doomedPath, []byte(doomedLocal)},
		{configPath, append(append([]byte(nil), baselineConfig...), localSetting...)},
	} {
		full := filepath.Join(root, filepath.FromSlash(file.rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("create the parent of %q: %w", file.rel, err)
		}
		if err := os.WriteFile(full, file.content, 0o644); err != nil {
			return fmt.Errorf("write %q: %w", file.rel, err)
		}
		if err := os.Chtimes(full, seededAt, seededAt); err != nil {
			return fmt.Errorf("date %q: %w", file.rel, err)
		}
	}
	return nil
}

// commit snapshots the seeded content with plain git, excluding `.bnotes` the way
// internal/web's own synchronization test helpers do.
//
// Deliberately not internal/vaultgit. That engine commits a *journaled* mutation
// and refuses anything it cannot account for, and there is no operation to journal
// here: this is baseline scenery being put in place before the vault is enrolled,
// not a change anybody made. The engine under test is the import path, and every
// spec's `gitStatus` assertion only needs the working tree to end up clean.
func commit(root, home, message string) error {
	for _, args := range [][]string{
		{"add", "-A", "--", ".", ":(exclude).bnotes"},
		{"commit", "-q", "-m", message},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = gitEnv(home)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s in %q: %w: %s", args[0], root, err, out)
		}
	}
	return nil
}

// gitEnv mirrors prepare.mjs's gitEnv for the same reason it is built rather than
// inherited: a developer's locale, identity, or global core.excludesFile must not
// change what a fixture commit contains.
func gitEnv(home string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"TMPDIR=" + os.Getenv("TMPDIR"),
		"GIT_CONFIG_GLOBAL=" + filepath.Join(home, ".gitconfig"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
		"TZ=UTC",
		"GIT_AUTHOR_NAME=Fixture User",
		"GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=Fixture User",
		"GIT_COMMITTER_EMAIL=fixture@example.invalid",
	}
}

// enroll makes the two vaults replicas of one vault, in the order acceptance
// requires: the served vault establishes the baseline, the producing vault joins
// it, and the served vault records the producer back, because a one-sided
// enrollment is a vault that sends and is never answered.
//
// The producing vault gets an enrollment record and nothing else — no note, no
// commit. A package carries its expectations as hashes and is published straight
// into the served vault's incoming channel, so the producer never has to hold the
// content it describes. That is also what keeps isolation.spec.js's "the peer vault
// is still clean" true: everything written there is under `.bnotes/sync/`, which
// internal/vaultgit's scope excludes and fixture.js's gitStatus filters out.
func enroll(p paths) (*vaultsync.Enrollment, *vaultsync.Enrollment, error) {
	at := time.Date(2024, time.March, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return at }
	local, err := vaultsync.Establish(p.local, vaultsync.Options{Label: "browser", Now: clock})
	if err != nil {
		return nil, nil, fmt.Errorf("enroll the served vault: %w", err)
	}
	peer, err := vaultsync.Join(p.peer, local.Invite(), vaultsync.Options{Label: "other", Now: clock})
	if err != nil {
		return nil, nil, fmt.Errorf("enroll the producing vault: %w", err)
	}
	local, err = vaultsync.RecordPeer(p.local, vaultsync.Peer{
		DeviceID:    peer.DeviceID,
		Label:       peer.Label,
		JoinedEpoch: peer.Epoch,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("record the producing device as a peer: %w", err)
	}
	return local, peer, nil
}

// publish puts one package per label in the served vault's incoming channel.
//
// The operation identities are minted for all of them first, because the dependent
// package has to name the text operation as its predecessor and an identity is
// never re-derived. Sequences are 1..n in label order, and no package states a
// `Requires`: a stated dependency is matched against an exact position the vault
// has applied, so requiring the package ahead of it would hold the whole channel
// behind the first conflict instead of holding five conflicts and one dependent.
func publish(root string, peer *vaultsync.Enrollment, baselineConfig []byte) (map[string]published, error) {
	ids := make(map[string]string, len(labels))
	for _, label := range labels {
		id, err := vaultsync.NewOperationID()
		if err != nil {
			return nil, fmt.Errorf("mint the %s operation identity: %w", label, err)
		}
		ids[label] = id
	}
	start := time.Date(2024, time.March, 2, 9, 0, 0, 0, time.UTC)
	sent := make(map[string]published, len(labels))
	for i, label := range labels {
		at := start.Add(time.Duration(i) * time.Minute)
		builder, err := vaultsync.NewBuilder(peer, uint64(i+1), at)
		if err != nil {
			return nil, fmt.Errorf("start the %s package: %w", label, err)
		}
		builder.BeginOperation(ids[label], at)
		compose(builder, label, ids, baselineConfig)
		manifest, _, err := builder.PublishTo(vaultsync.IncomingDir(root))
		if err != nil {
			return nil, fmt.Errorf("publish the %s package: %w", label, err)
		}
		sent[label] = published{PackageID: manifest.PackageID, OperationID: ids[label]}
	}
	return sent, nil
}

// compose states one package's changes. Every expectation names the baseline the
// two replicas last agreed on, which the served vault no longer holds for any of
// them — that mismatch is the conflict.
func compose(b *vaultsync.Builder, label string, ids map[string]string, baselineConfig []byte) {
	switch label {
	case "text":
		b.Write(textPath, []byte(textRemote), expect(textBaseline))
	case "attachment":
		b.Write(attachmentPath, attachment("remote"),
			vaultsync.Existing(vaultsync.HashBytes(attachment("baseline"))))
	case "rename":
		// A rename is a delete of the source and a write of the destination carrying
		// the source's bytes; internal/vaultimport recovers the pairing from the
		// package itself and never trusts it from a label.
		b.Delete(renamePath, expect(renameBaseline))
		b.Write(renameDest, []byte(renameBaseline), vaultsync.Absent())
	case "deletion":
		b.Delete(doomedPath, expect(doomedBaseline))
	case "settings":
		b.Write(configPath, append(append([]byte(nil), baselineConfig...), remoteSetting...),
			vaultsync.Existing(vaultsync.HashBytes(baselineConfig)))
	case "dependent":
		b.Write(dependentPath, []byte(dependentBody), vaultsync.Absent(), ids["text"])
	}
}

// expect is the expectation for a baseline stated as text.
func expect(baseline string) vaultsync.Expectation {
	return vaultsync.Existing(vaultsync.HashBytes([]byte(baseline)))
}

// drain makes the holds durable, which is the only thing that makes them real:
// every version the review page shows for the local side is kept by this pass and
// exists nowhere else afterwards.
func drain(root string) ([]vaultimport.Held, error) {
	report, err := vaultimport.Drain(root, vaultimport.Options{Command: "syncfixture (e2e seeding)"})
	if err != nil {
		return nil, fmt.Errorf("drain the incoming channel: %w", err)
	}
	if len(report.Applied) != 0 {
		return nil, fmt.Errorf("the drain applied %d packages and every one of them was supposed to be held",
			len(report.Applied))
	}
	if err := errors.Join(report.Cleanup, report.Unfinished); err != nil {
		return nil, fmt.Errorf("the drain reported a failure with nothing applied: %w", err)
	}
	return report.Held, nil
}

// writeExpected puts the two attachment versions somewhere the spec can read them.
//
// They are bytes no JSON should carry — invalid UTF-8 does not survive a manifest
// round trip — and the spec has to compare the resolved file against one of them
// exactly, because "the accept-incoming choice installed the other device's
// attachment" is not a claim any rendered text can make. The directory is outside
// both vaults, so nothing here shows up in a `gitStatus` assertion.
//
//bn:direct-write fixture artifacts, inside a sandbox confine has already proved
func writeExpected(dir string) (map[string]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create the expected-bytes directory: %w", err)
	}
	written := make(map[string]string, 2)
	for _, version := range []string{"local", "remote"} {
		full := filepath.Join(dir, "attachment-"+version+".bin")
		if err := os.WriteFile(full, attachment(version), 0o644); err != nil {
			return nil, fmt.Errorf("write the %s attachment: %w", version, err)
		}
		written["attachment"+strings.ToUpper(version[:1])+version[1:]] = full
	}
	return written, nil
}

// describe cross-checks what the engine decided against what this program
// intended, and answers the manifest fields the spec reads.
//
// The check is the point rather than a precaution. Every expectation above is a
// prediction about internal/vaultimport's classification, and a prediction that
// turned out wrong would otherwise reach the browser as a review page the spec
// fails against for reasons no assertion explains. So it fails here, loudly, with
// the whole report named — and the kind it reports is read back from the hold.
func describe(local, peer *vaultsync.Enrollment, expected map[string]string,
	sent map[string]published, held []vaultimport.Held) (*world, error) {
	byPackage := make(map[string]vaultimport.Held, len(held))
	for _, one := range held {
		byPackage[one.PackageID] = one
	}
	if len(byPackage) != len(labels) {
		return nil, fmt.Errorf("the drain held %d packages, want %d: %s",
			len(byPackage), len(labels), summarize(held))
	}
	seeded := &world{
		LocalDevice: local.DeviceID,
		PeerDevice:  peer.DeviceID,
		Expected:    expected,
		Conflicts:   make(map[string]conflict, len(labels)-1),
	}
	for _, label := range labels {
		one, there := byPackage[sent[label].PackageID]
		if !there {
			return nil, fmt.Errorf("the %s package is not held at all: %s", label, summarize(held))
		}
		if err := record(seeded, label, sent[label], one); err != nil {
			return nil, err
		}
	}
	return seeded, nil
}

// record files one hold under its label, refusing anything that is not the hold
// this fixture is built on.
func record(seeded *world, label string, sent published, one vaultimport.Held) error {
	if label == "dependent" {
		if one.Reason != vaultimport.HoldPredecessor {
			return fmt.Errorf("the dependent package is held as %q (%s), want %q",
				one.Reason, one.Detail, vaultimport.HoldPredecessor)
		}
		seeded.Dependent = dependent{PackageID: sent.PackageID, OperationID: sent.OperationID, Path: dependentPath}
		return nil
	}
	if one.Reason != vaultimport.HoldConflict {
		return fmt.Errorf("the %s package is held as %q (%s), want %q",
			label, one.Reason, one.Detail, vaultimport.HoldConflict)
	}
	if one.Conflict == nil || len(one.Conflict.Paths) != 1 {
		return fmt.Errorf("the %s package holds %s, want exactly one contested path",
			label, contestedCount(one))
	}
	contested := one.Conflict.Paths[0]
	seeded.Conflicts[label] = conflict{
		PackageID:   sent.PackageID,
		OperationID: sent.OperationID,
		Path:        contested.Path,
		Kind:        string(contested.Kind),
		RenamedTo:   contested.RenamedTo,
		Local:       marks[label].local,
		Remote:      marks[label].remote,
		Baseline:    marks[label].baseline,
	}
	return nil
}

// contestedCount describes how many contested paths a hold carries, including the
// case internal/vaultimport documents: a conflict it refused and a second
// inspection could not reproduce has no record at all.
func contestedCount(one vaultimport.Held) string {
	if one.Conflict == nil {
		return "no record"
	}
	return fmt.Sprintf("%d contested paths", len(one.Conflict.Paths))
}

// summarize is the whole report in one line, for a failure that has to be
// diagnosable from a prepare step's stderr.
func summarize(held []vaultimport.Held) string {
	lines := make([]string, 0, len(held))
	for _, one := range held {
		lines = append(lines, fmt.Sprintf("%s=%s(%s)", one.PackageID, one.Reason, one.Detail))
	}
	sort.Strings(lines)
	return strings.Join(lines, " ")
}

package vaultsyncd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
)

// TestACompanionRecordsItsBuildAndAReaderNoticesADifferentOne is the version
// skew check: the companion that last ran says which build it was, and a reader
// that is a different build says so without treating the vault as unhealthy.
func TestACompanionRecordsItsBuildAndAReaderNoticesADifferentOne(t *testing.T) {
	w := enroll(t)
	w.write(w.root(), localNote, localBody)
	opts := w.options()
	opts.Build = "running-build"
	w.onceWith(w.root(), opts)

	record, err := LoadRecord(w.root())
	if err != nil || record.Build != "running-build" {
		t.Fatalf("record build = %q, %v; want the companion's build", record.Build, err)
	}

	read := w.options().readOptions()
	read.Build = "running-build"
	same := readStatus(t, w.root(), read)
	if same.CompanionBuild != "running-build" || same.BuildMismatch {
		t.Fatalf("same build read = %q mismatch=%v", same.CompanionBuild, same.BuildMismatch)
	}
	read.Build = "installed-build"
	other := readStatus(t, w.root(), read)
	if !other.BuildMismatch {
		t.Fatalf("a different reader build was not flagged: %+v", other)
	}
	if other.NeedsPerson() != same.NeedsPerson() {
		t.Fatal("a build mismatch must not change whether the vault needs a person")
	}
}

func readStatus(t *testing.T, root string, opts ReadOptions) *Status {
	t.Helper()
	status, err := Read(context.Background(), root, opts)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return status
}

// TestAnOlderCompanionLeavesANewerRecordAlone. Discarding an unusable record is
// right for a corrupt one and wrong for one a newer build wrote: resetting it
// would overwrite that build's schedule with a format it then throws away.
func TestAnOlderCompanionLeavesANewerRecordAlone(t *testing.T) {
	w := enroll(t)
	w.write(w.root(), localNote, localBody)
	w.once(w.root())
	newer := []byte("{\n  \"version\": 99,\n  \"workers\": {}\n}\n")
	if err := os.WriteFile(RecordPath(w.root()), newer, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadRecord(w.root()); !errors.Is(err, ErrNewerRecord) {
		t.Fatalf("LoadRecord on a newer record = %v, want ErrNewerRecord", err)
	}
	var events []Event
	opts := w.options()
	opts.Observe = func(event Event) { events = append(events, event) }
	pass := w.onceWith(w.root(), opts)
	if len(pass.Units) != 0 || !pass.Failed() {
		t.Fatalf("a pass against a newer record = %+v, want no units and a failure", pass)
	}
	if !contains(kinds(events), EventProblem) {
		t.Fatalf("the companion said nothing about the newer record: %v", kinds(events))
	}
	after, err := os.ReadFile(RecordPath(w.root()))
	if err != nil || !bytes.Equal(after, newer) {
		t.Fatalf("the newer record was rewritten: %q, %v", after, err)
	}
	if status := w.status(w.root()); !problemAbout(status, "companion record") || !status.NeedsPerson() {
		t.Fatalf("a newer record read as healthy: %+v", status)
	}
}

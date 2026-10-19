package vaultgit

import (
	"os"
	"testing"
)

func TestGuardStartIsRecordedAndReadableWithoutOpeningTheEngine(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if _, found, err := ReadGuardRecord(h.root); found || err != nil {
		t.Fatalf("before any guard = found %v, %v", found, err)
	}
	written, err := h.mgr.RecordGuardStart("guard-build")
	if err != nil {
		t.Fatal(err)
	}
	read, found, err := ReadGuardRecord(h.root)
	if err != nil || !found {
		t.Fatalf("ReadGuardRecord = found %v, %v", found, err)
	}
	if read.Build != "guard-build" || read.PID != os.Getpid() || !read.StartedAt.Equal(written.StartedAt) {
		t.Fatalf("guard record = %+v, want %+v", read, written)
	}
}

func TestAGuardRecordFromAnotherSchemaIsAnErrorNotAbsence(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if err := h.mgr.state.writeJSON(guardRecordFileName, GuardRecord{Version: stateVersion + 1, Build: "newer"}); err != nil {
		t.Fatal(err)
	}
	if _, found, err := ReadGuardRecord(h.root); !found || err == nil {
		t.Fatalf("newer guard record = found %v, %v; want an error", found, err)
	}
}

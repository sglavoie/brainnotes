package vaultsyncd

import (
	"errors"
	"testing"
	"time"
)

// A stored retry further out than MaxBackoff can only predate a backwards clock
// step, so the worker is ready instead of parked for as long as the clock jumped.
func TestAWorkerIsReadyAfterTheClockStepsBack(t *testing.T) {
	written := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	record := &Record{}
	record.failed(WorkerExport, written, errors.New("boom"))
	if record.ready(WorkerExport, written) {
		t.Fatal("a worker must not be ready immediately after a failure")
	}
	if record.ready(WorkerExport, written.Add(-MinBackoff/2)) {
		t.Fatal("a retry inside MaxBackoff must still be honoured")
	}
	if !record.ready(WorkerExport, written.Add(-24*time.Hour)) {
		t.Fatal("a retry a day past MaxBackoff must be ready after the clock steps back")
	}
}

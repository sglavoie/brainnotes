package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/buildinfo"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
)

// -version answers without resolving a vault, so it works from anywhere.
func TestVersionPrintsTheBuildWithoutResolvingAVault(t *testing.T) {
	t.Setenv("BNOTES_VAULT", t.TempDir()+"/missing")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"-version"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("-version exited %d: %s", code, stderr.String())
	}
	if got, want := stdout.String(), "bn-vault-guard "+buildinfo.ID()+"\n"; got != want {
		t.Fatalf("-version printed %q, want %q", got, want)
	}
}

func TestAnUnknownFlagIsAUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"-forever"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("an unknown flag exited %d, want %d", code, exitUsage)
	}
	if code := run(context.Background(), []string{"extra"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("a positional argument exited %d, want %d", code, exitUsage)
	}
}

// A guard that starts says which build it is, in its log and in the record a
// skew check reads.
func TestRunLogsAndRecordsItsBuild(t *testing.T) {
	root := newVault(t, "off")
	initGit(t, root)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr bytes.Buffer
	out := &cancelOnFirstWrite{buf: &stdout, cancel: cancel}
	if code := run(ctx, nil, out, &stderr); code != exitOK {
		t.Fatalf("exit code %d (stderr: %q)", code, stderr.String())
	}
	events := decodeLog(t, stdout.String())
	if events[0]["event"] != string(vaultgit.WatchEventStarted) || events[0]["build"] != buildinfo.ID() {
		t.Fatalf("started record = %v, want build %q", events[0], buildinfo.ID())
	}
	record, found, err := vaultgit.ReadGuardRecord(root)
	if err != nil || !found || record.Build != buildinfo.ID() || record.PID == 0 {
		t.Fatalf("guard record = %+v, found %v, %v", record, found, err)
	}
	if strings.Contains(stderr.String(), "guard record") {
		t.Fatalf("the guard record write failed: %q", stderr.String())
	}
}

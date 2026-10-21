package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/vaultsyncd"
)

func useBuild(t *testing.T, id string) {
	t.Helper()
	previous := buildID
	buildID = func() string { return id }
	t.Cleanup(func() { buildID = previous })
}

func TestVersionPrintsTheBuildWithoutResolvingAVault(t *testing.T) {
	useBuild(t, "0123456789abcdef")
	t.Setenv("BNOTES_VAULT", t.TempDir()+"/missing")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := run(context.Background(), []string{"-version"}, stdout, stderr); code != exitOK {
		t.Fatalf("-version exited %d: %s", code, stderr)
	}
	if got := stdout.String(); got != "bn-vault-sync 0123456789abcdef\n" {
		t.Fatalf("-version printed %q", got)
	}
	if code := run(context.Background(), []string{"-version", "-check"}, stdout, stderr); code != exitUnreadable {
		t.Fatalf("-version with another mode exited %d, want %d", code, exitUnreadable)
	}
}

// The installed loop refuses a held vault with the same status -once does, and
// that status is non-zero so the LaunchAgent keeps retrying.
func TestTheLoopRefusesAHeldVaultWithTheOneShotStatus(t *testing.T) {
	pair := enrolledPair(t)
	release, acquired, err := vault.TryLockPath(vaultsyncd.LockPath(pair.Local.Root))
	if err != nil || !acquired {
		t.Fatalf("take the companion's lock: acquired=%v err=%v", acquired, err)
	}
	defer release()

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	if code := run(context.Background(), nil, stdout, stderr); code != exitUnreadable {
		t.Fatalf("the loop against a held vault exited %d, want %d", code, exitUnreadable)
	}
	if stderr.Len() == 0 {
		t.Fatal("the loop refused silently")
	}
}

// -check flags a companion that last ran as another build, which is what a
// LaunchAgent still running the previous binary after an install looks like,
// and does not let that change its answer about the vault.
func TestCheckFlagsACompanionRunningAnotherBuild(t *testing.T) {
	enrolledPair(t)
	useBuild(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	run(context.Background(), []string{"-once"}, stdout, stderr)

	stdout.Reset()
	stderr.Reset()
	same := run(context.Background(), []string{"-check"}, stdout, stderr)
	if status := decodeStatus(t, stdout); status.BuildMismatch || strings.Contains(stderr.String(), "warning") {
		t.Fatalf("the same build was flagged: %+v %s", status, stderr)
	}

	useBuild(t, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	stdout.Reset()
	stderr.Reset()
	code := run(context.Background(), []string{"-check"}, stdout, stderr)
	status := decodeStatus(t, stdout)
	if !status.BuildMismatch || !strings.HasPrefix(status.CompanionBuild, "aaaa") {
		t.Fatalf("-check did not flag the other build: %+v", status)
	}
	if !strings.Contains(stderr.String(), "aaaaaaaaaaaa") || !strings.Contains(stderr.String(), "bbbbbbbbbbbb") {
		t.Fatalf("the warning does not name both builds: %s", stderr)
	}
	if code != same {
		t.Fatalf("a build mismatch changed -check's exit from %d to %d", same, code)
	}
}

package mutationmatrix

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/attachments"
	"github.com/sglavoie/brainnotes/internal/brief"
	"github.com/sglavoie/brainnotes/internal/checkpoint"
	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/vault"
	"github.com/sglavoie/brainnotes/internal/vaultgit"
	"github.com/sglavoie/brainnotes/internal/vaultsync"
)

const appCrashRootEnv = "BN_TEST_APP_CHECKPOINT_ROOT"
const appCrashReadyEnv = "BN_TEST_APP_CHECKPOINT_READY"
const appWriterDomainEnv = "BN_TEST_APP_WRITER_DOMAIN"

func TestApplicationCheckpointDeathHelper(t *testing.T) {
	root, ready := os.Getenv(appCrashRootEnv), os.Getenv(appCrashReadyEnv)
	if root == "" || ready == "" {
		t.Skip("helper")
	}
	held, err := checkpoint.Hold(root, checkpoint.Options{Reason: "application death matrix"})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	if err := os.WriteFile(ready, []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {}
}

func TestApplicationWriterDeathHelper(t *testing.T) {
	root, ready, domain := os.Getenv(appCrashRootEnv), os.Getenv(appCrashReadyEnv), os.Getenv(appWriterDomainEnv)
	if root == "" || ready == "" || domain == "" {
		t.Skip("helper")
	}
	result := runApplicationDomain(t, root, domain)
	if err := os.WriteFile(ready, []byte(result), 0o600); err != nil {
		t.Fatal(err)
	}
	select {}
}

func runApplicationDomain(t *testing.T, root, domain string) string {
	t.Helper()
	cfg, err := config.Load(config.LoadOptions{VaultFlag: root, Getenv: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	v, err := vault.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, root: root, cfg: cfg, v: v}
	runner, ok := applicationDomainRunners[domain]
	if !ok {
		t.Fatal(fmt.Errorf("unknown application domain %q", domain))
	}
	result, err := runner(t, h)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

type applicationDomainRunner func(*testing.T, *harness) (string, error)

var applicationDomainRunners = map[string]applicationDomainRunner{
	"note-journal-git": func(t *testing.T, h *harness) (string, error) {
		return "done", setStatus(t, h, "Alpha.md", "done")
	},
	"configuration-journal-git": func(t *testing.T, h *harness) (string, error) {
		return "done", runCLI(t, h, "config", "set", autosaveKey, "90s")
	},
	"upload": func(_ *testing.T, h *harness) (string, error) {
		store, err := attachments.NewStore(h.root, "attachments", 1024)
		if err != nil {
			return "", err
		}
		staged, err := store.Stage(attachments.StageRequest{Owner: "death-test", Filename: "a.bin"}, bytes.NewReader([]byte("payload")), time.Time{})
		return staged.ID, err
	},
	"activity": func(_ *testing.T, h *harness) (string, error) {
		_, err := brief.NewFileStateStore(h.root).RecordVisit(brief.LocalDate("2026-09-19"), time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC))
		return "done", err
	},
	"sync-transition": func(_ *testing.T, h *harness) (string, error) {
		_, err := vaultsync.Establish(h.root, vaultsync.Options{Label: "death-test"})
		return "done", err
	},
}

func TestAcknowledgedApplicationWritesSurviveWriterDeath(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		assert func(*testing.T, *harness, string, string, string, string)
	}{
		{"note-journal-git", "Alpha.md", assertDeadJournaledWriter},
		{"configuration-journal-git", config.FileName, assertDeadJournaledWriter},
		{"upload", "", assertDeadUploadWriter},
		{"activity", "", assertDeadActivityWriter},
		{"sync-transition", "", assertDeadSyncWriter},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, nil)
			beforeContent := ""
			if tc.path != "" {
				beforeContent = h.readFile(tc.path)
			}
			beforeHead := h.head()
			result := runAndKillApplicationWriter(t, h.root, tc.name)
			tc.assert(t, h, result, beforeContent, beforeHead, tc.path)
			assertCheckpointAvailable(t, h.root)
		})
	}
}

func runAndKillApplicationWriter(t *testing.T, root, domain string) string {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestApplicationWriterDeathHelper$")
	cmd.Env = append(os.Environ(), appCrashRootEnv+"="+root, appCrashReadyEnv+"="+ready, appWriterDomainEnv+"="+domain)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	result := waitForApplicationReady(t, cmd, ready, &output)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	return result
}

func waitForApplicationReady(t *testing.T, cmd *exec.Cmd, ready string, output *bytes.Buffer) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		// WriteFile creates the file before writing the acknowledgement. An
		// empty file is not yet permission to kill the writer.
		if data, err := os.ReadFile(ready); err == nil && len(data) > 0 {
			return string(data)
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("application writer did not acknowledge: %s", output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertDeadJournaledWriter(t *testing.T, h *harness, _ string, beforeContent, beforeHead, path string) {
	t.Helper()
	assertApplicationStateAdvanced(t, h, path, beforeContent, beforeHead, filepath.Join(h.root, config.DefaultJournalDir))
}

func assertDeadUploadWriter(t *testing.T, h *harness, id, _, _, _ string) {
	t.Helper()
	store, err := attachments.NewStore(h.root, "attachments", 1024)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := store.Lookup("death-test", id)
	if err != nil {
		t.Fatalf("acknowledged upload after writer death: %v", err)
	}
	data, err := os.ReadFile(staged.BlobPath)
	if err != nil || !bytes.Equal(data, []byte("payload")) {
		t.Fatalf("acknowledged upload bytes = %q, err=%v", data, err)
	}
}

func assertDeadActivityWriter(t *testing.T, h *harness, _, _, _, _ string) {
	t.Helper()
	activity, err := brief.NewFileStateStore(h.root).RollingActivity(time.Date(2026, 9, 19, 13, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if activity.Last7Days.VisitDays != 1 {
		t.Fatalf("acknowledged activity after writer death = %+v", activity)
	}
}

func assertDeadSyncWriter(t *testing.T, h *harness, _, _, _, _ string) {
	t.Helper()
	enrollment, err := vaultsync.Load(h.root)
	if err != nil {
		t.Fatalf("acknowledged sync transition after writer death: %v", err)
	}
	if enrollment.Label != "death-test" || enrollment.Epoch != 1 {
		t.Fatalf("acknowledged sync transition = %+v", enrollment)
	}
}

func assertCheckpointAvailable(t *testing.T, root string) {
	t.Helper()
	held, err := checkpoint.Hold(root, checkpoint.Options{Reason: "after writer death", Drain: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
	vaultgit.ForgetShared(root)
}

func TestApplicationDomainsRecoverAfterCheckpointClientDeath(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T, *harness)
	}{
		{"note-journal-git", func(t *testing.T, h *harness) {
			if err := setStatus(t, h, "Alpha.md", "done"); err != nil {
				t.Fatal(err)
			}
		}},
		{"configuration-journal-git", func(t *testing.T, h *harness) {
			if err := runCLI(t, h, "config", "set", autosaveKey, "90s"); err != nil {
				t.Fatal(err)
			}
		}},
		{"upload", func(t *testing.T, h *harness) {
			s, err := attachments.NewStore(h.root, "attachments", 1024)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Stage(attachments.StageRequest{Owner: "draft", Filename: "a.bin"}, bytes.NewReader([]byte("payload")), time.Time{}); err != nil {
				t.Fatal(err)
			}
		}},
		{"activity", func(t *testing.T, h *harness) {
			if _, err := brief.NewFileStateStore(h.root).RecordVisit(brief.LocalDate("2026-09-19"), time.Now()); err != nil {
				t.Fatal(err)
			}
		}},
		{"sync-transition", func(t *testing.T, h *harness) {
			if _, err := vaultsync.Establish(h.root, vaultsync.Options{Label: "test"}); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, nil)
			killCheckpointClient(t, h.root)
			tc.run(t, h)
			held, err := checkpoint.Hold(h.root, checkpoint.Options{Reason: "verify domain receipt"})
			if err != nil {
				t.Fatal(err)
			}
			if err := held.Release(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func killCheckpointClient(t *testing.T, root string) {
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command(os.Args[0], "-test.run=^TestApplicationCheckpointDeathHelper$")
	cmd.Env = append(os.Environ(), appCrashRootEnv+"="+root, appCrashReadyEnv+"="+ready)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatal("checkpoint helper did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
}

// These cases use the real note/config entry points and real Git engine. They
// assert state, not just the presence of Join calls: while held, content,
// journal, and history all stay at the same receipt boundary; after release the
// acknowledged operation advances all of them together.
func TestCheckpointStopsApplicationWritesBeforeContentJournalAndHistory(t *testing.T) {
	tests := []struct {
		name string
		path string
		run  func(*testing.T, *harness) error
	}{
		{name: "note", path: "Alpha.md", run: func(t *testing.T, h *harness) error { return setStatus(t, h, "Alpha.md", "done") }},
		{name: "configuration", path: config.FileName, run: func(t *testing.T, h *harness) error {
			return runCLI(t, h, "config", "set", autosaveKey, "90s")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { assertApplicationWriterCheckpointed(t, test.path, test.run) })
	}
}

func assertApplicationWriterCheckpointed(t *testing.T, path string, run func(*testing.T, *harness) error) {
	h := newHarness(t, nil)
	beforeContent, beforeHead := h.readFile(path), h.head()
	journalRoot := filepath.Join(h.root, config.DefaultJournalDir)
	held, err := checkpoint.Hold(h.root, checkpoint.Options{Reason: "application consistency", Drain: time.Second})
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- run(t, h) }()
	select {
	case err := <-done:
		t.Fatalf("writer completed during checkpoint: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	assertApplicationStateHeld(t, h, path, beforeContent, beforeHead, journalRoot)
	if err := held.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("writer after checkpoint: %v", err)
	}
	assertApplicationStateAdvanced(t, h, path, beforeContent, beforeHead, journalRoot)
}

func assertApplicationStateHeld(t *testing.T, h *harness, path, beforeContent, beforeHead, journalRoot string) {
	if h.readFile(path) != beforeContent {
		t.Fatal("content changed during checkpoint")
	}
	if h.head() != beforeHead {
		t.Fatal("history changed during checkpoint")
	}
	if entries := journalEntries(journalRoot); len(entries) != 0 {
		t.Fatalf("journal advanced during checkpoint: %v", entries)
	}
}

func assertApplicationStateAdvanced(t *testing.T, h *harness, path, beforeContent, beforeHead, journalRoot string) {
	if h.readFile(path) == beforeContent {
		t.Fatal("content did not advance after release")
	}
	if h.head() == beforeHead {
		t.Fatal("history did not advance after release")
	}
	if entries := journalEntries(journalRoot); len(entries) != 1 {
		t.Fatalf("journal entries after write = %v, want one", entries)
	}
}

func journalEntries(root string) []string {
	entries, _ := filepath.Glob(filepath.Join(root, "*", "manifest.json"))
	return entries
}

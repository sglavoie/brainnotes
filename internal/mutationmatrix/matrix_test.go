package mutationmatrix

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/backup"
	"github.com/sglavoie/sglavoie_brainnotes/internal/cli"
	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/web"
)

// mutation is one production writer, reached the way production reaches it.
//
// Everything a case declares is about the operation, never about the plumbing:
// which paths it is allowed to put in the history, whether it writes an undo
// entry, and what the same operation looks like when it has nothing to do. The
// six assertions are the same for all of them and live in the runner, so a new
// writer is covered by describing it rather than by writing another test.
type mutation struct {
	// name identifies the case, prefixed by the surface it is reached through so
	// a failure names the boundary that regressed.
	name string
	// prepare runs before the baseline is taken. Its own writes and commits are
	// setup, not the thing under test.
	prepare func(t *testing.T, h *harness) any
	// apply performs the mutation. state is whatever prepare returned.
	apply func(t *testing.T, h *harness, state any) error
	// touches is the exact set of vault-relative paths the snapshot may carry,
	// undo manifests aside.
	touches []string
	// journaled reports whether the operation records an undo entry. Durable
	// sidecar state does not: there is nothing for a person to undo, and the
	// snapshot stages the state file by exact path instead.
	journaled bool
	// noop is the same operation in a state where it has nothing to change. It
	// runs after prepare and must leave the history exactly where it found it.
	noop func(t *testing.T, h *harness, state any) error
	// overHTTP marks a case whose error crossed a network boundary. Only the
	// wire contract survives that trip — the status, the stable code, and the
	// message — so the Go type is not asserted for one. That is not a weaker
	// check but a different one: for these rows the status and the code carry
	// the whole distinction a client has, and the runner holds them to it.
	overHTTP bool
}

func mutations() []mutation {
	return []mutation{
		{
			name: "noteops/capture",
			apply: func(t *testing.T, h *harness, _ any) error {
				_, err := noteops.ApplyCapture(h.root, "Inbox.md", noteops.CaptureRequest{
					Heading: "## Captures",
					Text:    "a captured thought",
					At:      time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC),
				}, &h.cfg.Frontmatter, noteops.WithCaptureCommand("bn capture"))
				return err
			},
			touches:   []string{"Inbox.md"},
			journaled: true,
		},
		{
			name: "noteops/create",
			apply: func(t *testing.T, h *harness, _ any) error {
				_, err := noteops.CreateStubFromBrokenTarget(h.root, "Alpha.md", "Missing Note",
					noteops.WithCreateCommand("bn create-stub"))
				return err
			},
			touches:   []string{"Missing Note.md"},
			journaled: true,
		},
		{
			name: "noteops/frontmatter",
			apply: func(t *testing.T, h *harness, _ any) error {
				return setStatus(t, h, "Alpha.md", "done")
			},
			touches:   []string{"Alpha.md"},
			journaled: true,
			noop: func(t *testing.T, h *harness, _ any) error {
				// Alpha.md already carries status: draft, so this is the same code
				// path with nothing to write.
				return setStatus(t, h, "Alpha.md", "draft")
			},
		},
		{
			name: "noteops/bulk",
			apply: func(t *testing.T, h *harness, _ any) error {
				mutations, err := noteops.ParseFrontmatterMutations([]string{"status=done"}, "", h.cfg)
				if err != nil {
					return err
				}
				_, err = noteops.BulkSetFrontmatter(h.root, []noteops.BulkSource{
					{Key: "Alpha.md", RelPath: "Alpha.md"},
					{Key: "Notes/Gamma.md", RelPath: "Notes/Gamma.md"},
				}, mutations, &h.cfg.Frontmatter, noteops.WithBulkCommand("bn web bulk set"))
				return err
			},
			touches:   []string{"Alpha.md", "Notes/Gamma.md"},
			journaled: true,
		},
		{
			name: "noteops/trash",
			apply: func(t *testing.T, h *harness, _ any) error {
				_, err := noteops.TrashNote(h.root, "Alpha.md", noteops.WithTrashCommand("bn web trash"))
				return err
			},
			touches:   []string{"Alpha.md", "Trash/Alpha.md"},
			journaled: true,
		},
		{
			name: "noteops/rename",
			apply: func(t *testing.T, h *harness, _ any) error {
				_, err := noteops.RenameNote(h.v, h.cfg, h.abs("Alpha.md"), "Alpha Renamed",
					noteops.WithRenameCommand("bn rename Alpha.md 'Alpha Renamed'"))
				return err
			},
			touches:   []string{"Alpha Renamed.md", "Alpha.md"},
			journaled: true,
		},
		{
			name: "cli/set",
			apply: func(t *testing.T, h *harness, _ any) error {
				return runCLI(t, h, "set", "Alpha.md", "status=done")
			},
			touches:   []string{"Alpha.md"},
			journaled: true,
			noop: func(t *testing.T, h *harness, _ any) error {
				return runCLI(t, h, "set", "Alpha.md", "status=draft")
			},
		},
		{
			name: "cli/config-set",
			prepare: func(t *testing.T, h *harness) any {
				// A stored floor is what makes the no-op below reachable: with no
				// assignment there is nothing for a set to already agree with.
				if err := runCLI(t, h, "config", "set", autosaveKey, "1m"); err != nil {
					t.Fatalf("prepare a stored autosave floor: %v", err)
				}
				return nil
			},
			apply: func(t *testing.T, h *harness, _ any) error {
				return runCLI(t, h, "config", "set", autosaveKey, "2m")
			},
			touches:   []string{config.FileName},
			journaled: true,
			noop: func(t *testing.T, h *harness, _ any) error {
				// 60s is the stored 1m spelled differently, so this is the same
				// code path with nothing to write.
				return runCLI(t, h, "config", "set", autosaveKey, "60s")
			},
		},
		{
			name: "cli/config-unset",
			prepare: func(t *testing.T, h *harness) any {
				if err := runCLI(t, h, "config", "set", autosaveKey, "1m"); err != nil {
					t.Fatalf("prepare a stored autosave floor: %v", err)
				}
				return nil
			},
			apply: func(t *testing.T, h *harness, _ any) error {
				return runCLI(t, h, "config", "unset", autosaveKey)
			},
			touches:   []string{config.FileName},
			journaled: true,
			// The no-op unset — a key that is not assigned — cannot be expressed
			// here, because this row's prepare is what puts the key there.
			// TestConfigMutationsWithNothingToDoLeaveNoHistory covers it.
		},
		{
			name: "web/capture",
			apply: func(t *testing.T, h *harness, _ any) error {
				return postCapture(t, h, "a captured thought")
			},
			touches:   []string{"Inbox.md"},
			journaled: true,
			overHTTP:  true,
		},
		{
			name: "restore/undo",
			prepare: func(t *testing.T, h *harness) any {
				result, err := noteops.SetFrontmatter(h.root, "Alpha.md", statusMutations(t, h, "done"),
					&h.cfg.Frontmatter, noteops.WithFrontmatterCommand("bn set Alpha.md status=done"))
				if err != nil {
					t.Fatalf("prepare an entry to undo: %v", err)
				}
				if result.Journal == nil {
					t.Fatal("the prepared mutation recorded no journal entry to undo")
				}
				return result.Journal.ID
			},
			apply: func(t *testing.T, h *harness, state any) error {
				_, err := noteops.Undo(h.root, state.(string), noteops.UndoOptions{Command: "bn undo"})
				return err
			},
			touches:   []string{"Alpha.md"},
			journaled: true,
		},
		{
			name: "vaultstate/cockpit",
			prepare: func(t *testing.T, h *harness) any {
				// Clearing an absent task is what creates the state file, so the
				// no-op below is a second write of bytes that are already there
				// rather than the file's first appearance.
				if err := cockpit.NewStateStore(h.root).Clear(cockpitTask); err != nil {
					t.Fatalf("prepare cockpit state: %v", err)
				}
				return nil
			},
			apply: func(t *testing.T, h *harness, _ any) error {
				return cockpit.NewStateStore(h.root).MarkDone(cockpitTask)
			},
			touches: []string{".bnotes/cockpit.json"},
			noop: func(t *testing.T, h *harness, _ any) error {
				return cockpit.NewStateStore(h.root).Clear(cockpitTask)
			},
		},
	}
}

var cockpitTask = cockpit.Task{ID: "matrix-task", Signature: "matrix-signature"}

// autosaveKey is the one configuration key `bn config set`/`unset` accept.
const autosaveKey = "autosave.min_interval"

func statusMutations(t *testing.T, h *harness, value string) []noteops.FrontmatterMutation {
	t.Helper()
	mutations, err := noteops.ParseFrontmatterMutations([]string{"status=" + value}, "", h.cfg)
	if err != nil {
		t.Fatalf("parse status mutation: %v", err)
	}
	return mutations
}

func setStatus(t *testing.T, h *harness, rel, value string) error {
	t.Helper()
	_, err := noteops.SetFrontmatter(h.root, rel, statusMutations(t, h, value), &h.cfg.Frontmatter,
		noteops.WithFrontmatterCommand("bn set "+rel+" status="+value))
	return err
}

// runCLI drives a real command through the Cobra tree, resolving the vault the
// way the binary does. Nothing about the command is stubbed: --vault sends
// PersistentPreRunE to the fixture's own brainnotes.toml, so what runs is the
// production wiring from argv down.
func runCLI(t *testing.T, h *harness, args ...string) error {
	t.Helper()
	cmd := cli.NewRootCmd()
	cmd.SetArgs(append([]string{"--vault", h.root}, args...))
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	return cmd.Execute()
}

const matrixCSRFToken = "mutation-matrix-token"

// postCapture drives the web capture route and translates the response back
// into the operation error the rest of the matrix reasons about.
//
// The translation is itself an assertion: it checks that the HTTP status the
// handler chose is the one the shared mapping assigns to the code in the body.
// That is what keeps a forbidden location a 403, a concurrent conflict a 409,
// and a post-mutation snapshot failure a 500 that says the mutation completed —
// on this route and on any route a later case adds.
func postCapture(t *testing.T, h *harness, text string) error {
	t.Helper()
	srv, err := web.NewServer(h.cfg, h.v, web.Options{CSRFToken: matrixCSRFToken})
	if err != nil {
		return err
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	form := url.Values{}
	form.Set("text", text)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/capture", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build capture request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-BN-CSRF", matrixCSRFToken)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("post capture: %v", err)
	}
	defer resp.Body.Close()

	var payload struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode capture response: %v", err)
	}
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	opErr := &noteops.Error{Code: noteops.ErrorCode(payload.Code), Message: payload.Message}
	if want := noteops.HTTPStatusForError(opErr); resp.StatusCode != want {
		t.Fatalf("POST /api/capture answered %d for code %q, want %d", resp.StatusCode, payload.Code, want)
	}
	return opErr
}

// TestMutationMatrix is the cross-layer half of the vault's write contract.
//
// internal/audit proves from the source that no production writer skips the
// safety layers. This proves from the outside that going through them actually
// delivers what they promise, identically, for every surface a person can reach
// a mutation by.
func TestMutationMatrix(t *testing.T) {
	for _, c := range mutations() {
		t.Run(c.name, func(t *testing.T) {
			t.Run("allowed root mutates in exactly one commit of exactly its paths", func(t *testing.T) {
				h := newHarness(t, nil)
				state := c.runPrepare(t, h)
				before := h.commitCount()

				if err := c.apply(t, h, state); err != nil {
					t.Fatalf("%s on an ordinary vault: %v", c.name, err)
				}

				if got := h.commitCount(); got != before+1 {
					t.Fatalf("commit count %d -> %d, want exactly one new commit", before, got)
				}
				changed := h.changedPaths("HEAD")
				if got := nonJournalPaths(changed); !reflect.DeepEqual(got, sorted(c.touches)) {
					t.Fatalf("the snapshot holds %v, want exactly %v (subject %q)",
						got, sorted(c.touches), h.subject("HEAD"))
				}
				if c.journaled != hasJournalPaths(changed) {
					t.Fatalf("journaled = %v but the snapshot %s an undo manifest",
						c.journaled, presence(hasJournalPaths(changed)))
				}
			})

			t.Run("an unrelated working-tree edit is not swept in", func(t *testing.T) {
				h := newHarness(t, nil)
				state := c.runPrepare(t, h)
				// A note a person is midway through editing, which no operation in
				// the table has any reason to touch.
				h.writeFile(unrelatedNote, "---\nstatus: draft\ntitle: Beta\n---\n\nBeta body, mid-edit.\n")

				if err := c.apply(t, h, state); err != nil {
					t.Fatalf("%s alongside an unrelated edit: %v", c.name, err)
				}

				for _, p := range h.changedPaths("HEAD") {
					if p == unrelatedNote {
						t.Fatalf("the snapshot for %s swept in %s", c.name, unrelatedNote)
					}
				}
				committed, ok := h.show("HEAD", unrelatedNote)
				if !ok {
					t.Fatalf("%s is no longer in the history at all", unrelatedNote)
				}
				if committed != baseFiles[unrelatedNote] {
					t.Fatalf("the history now holds the mid-edit %s:\n%s", unrelatedNote, committed)
				}
			})

			if c.noop != nil {
				t.Run("an operation with nothing to do leaves no empty commit", func(t *testing.T) {
					h := newHarness(t, nil)
					state := c.runPrepare(t, h)
					before := h.head()

					if err := c.noop(t, h, state); err != nil {
						t.Fatalf("%s with nothing to do: %v", c.name, err)
					}

					if got := h.head(); got != before {
						t.Fatalf("a no-op %s committed %q", c.name, h.subject("HEAD"))
					}
				})
			}

			t.Run("a failed snapshot is reported honestly and blocks the next mutation", func(t *testing.T) {
				h := newHarness(t, nil)
				state := c.runPrepare(t, h)
				before := h.head()
				h.breakSnapshots()

				err := c.apply(t, h, state)
				if code := noteops.CodeOf(err); code != noteops.CodeSnapshotFailed {
					t.Fatalf("%s with an unwritable history = %v (code %q), want %q",
						c.name, err, code, noteops.CodeSnapshotFailed)
				}
				if !c.overHTTP && !noteops.IsSnapshotFailure(err) {
					t.Fatalf("%s returned %T, want the typed snapshot failure an in-process caller can match on", c.name, err)
				}
				for _, phrase := range []string{"completed and is on disk", "Nothing was rolled back"} {
					if !strings.Contains(err.Error(), phrase) {
						t.Fatalf("the message must say the change stands; it said %q", err)
					}
				}
				if got := h.head(); got != before {
					t.Fatalf("the branch moved despite a failed snapshot: %s -> %s", before, got)
				}
				// The change really is on disk. That is the whole reason this is not
				// reported as a plain failure.
				for _, p := range c.touches {
					if !h.differsFromHead(p) {
						t.Fatalf("%s claims %s changed, but the working tree matches the history", c.name, p)
					}
				}

				// A healthy git is not enough: the local failure latched, and the
				// next mutation is refused until a person clears it.
				h.repairSnapshots()
				vaultgit.ForgetShared(h.root)
				_, next := noteops.BeginOperation(h.root, "probe", "bn probe", nil)
				if next == nil {
					t.Fatal("a mutation after a failed snapshot was allowed to start")
				}
				if code := noteops.CodeOf(next); code != noteops.CodeSafety {
					t.Fatalf("the refusal is coded %q, want %q", code, noteops.CodeSafety)
				}
				if !vaultgit.IsBlocked(next) {
					t.Fatalf("the refusal is %v, want it to name the latched block", next)
				}
			})

			t.Run("a refused location fails before any artifact exists", func(t *testing.T) {
				h := newHarness(t, nil)
				state := c.runPrepare(t, h)
				if !protectRoot(t, h.root) {
					t.Skip("this platform has no storage-location policy to enforce")
				}
				before := h.head()
				tree := h.tree()

				err := c.apply(t, h, state)
				if err == nil {
					t.Fatalf("%s was allowed to run under refused storage", c.name)
				}
				if code := noteops.CodeOf(err); code != noteops.CodeForbidden {
					t.Fatalf("code = %q, want %q (error %v)", code, noteops.CodeForbidden, err)
				}
				if got := h.head(); got != before {
					t.Fatalf("a refused %s still committed %q", c.name, h.subject("HEAD"))
				}
				if got := h.tree(); !reflect.DeepEqual(got, tree) {
					t.Fatalf("a refused %s left artifacts behind:\nbefore %v\nafter  %v", c.name, tree, got)
				}
			})
		})
	}
}

// runPrepare executes an optional prepare step, keeping the call sites in the
// runner free of nil checks.
func (c mutation) runPrepare(t *testing.T, h *harness) any {
	t.Helper()
	if c.prepare == nil {
		return nil
	}
	return c.prepare(t, h)
}

func sorted(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := append([]string(nil), paths...)
	sort.Strings(out)
	return out
}

func presence(has bool) string {
	if has {
		return "holds"
	}
	return "does not hold"
}

// TestBackupRestoreIsPreflightedAndSnapshotted covers the one writer the matrix
// cannot describe in its own terms.
//
// A backup restore is wholesale by definition: it puts an archive over the vault
// and its removals are authorized by the operator running the command, not by an
// undo manifest. So it is the single documented exception that snapshots the
// working tree rather than exact paths — which means the two things worth
// proving about it are that the refusal still happens before anything is
// extracted, and that a successful restore is still exactly one commit.
func TestBackupRestoreIsPreflightedAndSnapshotted(t *testing.T) {
	h := newHarness(t, nil)

	archive := createBackup(t, h)
	h.writeFile("Alpha.md", "---\nstatus: done\ntitle: Alpha\n---\n\nAlpha body, edited.\n")
	h.rawGit("add", "Alpha.md")
	h.rawGit("commit", "-q", "-m", "user edit")
	before := h.commitCount()
	original := baseFiles["Alpha.md"]

	result, err := noteops.RestoreBackup(h.root, archive, backupRestoreOptions())
	if err != nil {
		t.Fatalf("RestoreBackup: %v", err)
	}
	if result == nil {
		t.Fatal("RestoreBackup returned no result")
	}
	if got := h.readFile("Alpha.md"); got != original {
		t.Fatalf("the restore did not put the archived note back:\n%s", got)
	}
	if got := h.commitCount(); got != before+1 {
		t.Fatalf("commit count %d -> %d, want exactly one new commit", before, got)
	}
	committed, ok := h.show("HEAD", "Alpha.md")
	if !ok || committed != original {
		t.Fatalf("the snapshot does not hold the restored note (present=%v):\n%s", ok, committed)
	}

	// The refusal half: a blocked vault must not be extracted over.
	h.breakSnapshots()
	if _, err := noteops.SetFrontmatter(h.root, "Alpha.md", statusMutations(t, h, "done"),
		&h.cfg.Frontmatter, noteops.WithFrontmatterCommand("bn set")); !noteops.IsSnapshotFailure(err) {
		t.Fatalf("failed to latch a block: %v", err)
	}
	h.repairSnapshots()
	vaultgit.ForgetShared(h.root)

	blockedTree := h.tree()
	_, err = noteops.RestoreBackup(h.root, archive, backupRestoreOptions())
	if err == nil {
		t.Fatal("a restore was allowed to run on a blocked vault")
	}
	if noteops.IsSnapshotFailure(err) {
		t.Fatalf("the refusal must precede the extraction, not follow it: %v", err)
	}
	if !vaultgit.IsBlocked(err) {
		t.Fatalf("the refusal is %v, want it to name the latched block", err)
	}
	if got := h.tree(); !reflect.DeepEqual(got, blockedTree) {
		t.Fatalf("a refused restore changed the vault:\nbefore %v\nafter  %v", blockedTree, got)
	}
}

// TestBackupRestoreWipeKeepsTheHistoryItCommitsInto is the one case where the
// most destructive command bn has meets the layer that is supposed to make
// destruction survivable.
//
// `bn restore --wipe` empties the vault before it extracts. An archive holds
// neither `backups/` nor `.git/` — both are excluded when it is written — so a
// wipe that removed either would delete the only copy of something and no
// archive anywhere could put it back. For `.git/` that is the entire content
// history, which is also what the restore is about to commit into: wiping it
// would mean the operation destroys its own receipt and the record of every
// version the wipe just discarded.
//
// So the assertion that matters is not that the working tree came back. It is
// that the note the wipe removed is still reachable in the history afterwards.
func TestBackupRestoreWipeKeepsTheHistoryItCommitsInto(t *testing.T) {
	h := newHarness(t, nil)
	archive := createBackup(t, h)

	// Delta is in the vault and in its history but not in the archive, so it is
	// what the wipe removes and therefore the deletion the restore has to record.
	const delta = "Delta.md"
	deltaBody := "---\nstatus: draft\ntitle: Delta\n---\n\nDelta body.\n"
	h.writeFile(delta, deltaBody)
	h.rawGit("add", delta)
	h.rawGit("commit", "-q", "-m", "user adds Delta")
	before := h.commitCount()

	result, err := noteops.RestoreBackup(h.root, archive, backup.RestoreOptions{Wipe: true})
	if err != nil {
		t.Fatalf("RestoreBackup --wipe: %v", err)
	}
	if result == nil || result.Mode != "wipe" {
		t.Fatalf("RestoreBackup returned %+v, want a wipe result", result)
	}

	if !h.exists(".git") {
		t.Fatal("the wipe removed the vault's Git repository")
	}
	if h.exists(delta) {
		t.Fatalf("%s is not in the archive, so the wipe should have removed it", delta)
	}
	if got := h.readFile("Alpha.md"); got != baseFiles["Alpha.md"] {
		t.Fatalf("the restore did not put the archived note back:\n%s", got)
	}

	if got := h.commitCount(); got != before+1 {
		t.Fatalf("commit count %d -> %d, want exactly one new commit", before, got)
	}
	if got := h.changedPaths("HEAD"); !reflect.DeepEqual(got, []string{delta}) {
		t.Fatalf("the restore commit touched %v, want only %q", got, delta)
	}
	if _, ok := h.show("HEAD", delta); ok {
		t.Fatalf("%s is still in the snapshot the restore took", delta)
	}

	// The point of preserving `.git`: what the wipe discarded is still there to
	// be recovered from.
	if committed, ok := h.show("HEAD~1", delta); !ok || committed != deltaBody {
		t.Fatalf("the wiped note is not recoverable from the history (present=%v):\n%s", ok, committed)
	}
}

// TestConfigMutationsWithNothingToDoLeaveNoHistory covers the config-command
// answers the matrix rows cannot express.
//
// A row's no-op runs after its prepare, and for `config unset` the prepare is
// precisely what assigns the key, so "unsetting a key that is not there" has
// nowhere to live in the table. Neither does the case a converging script
// actually produces — asserting the default against a vault that assigns
// nothing — because a row's no-op runs against a vault its own apply has
// already moved off the default. A dry run has nowhere to live either: it is
// not a mutation, and the table describes mutations.
//
// All of them are the same claim, which is the one thing a scriptable config
// surface has to get right to be safe in a loop: an invocation with nothing to
// do touches neither the file, nor the undo journal, nor the history — so a
// script that reasserts the vault's policy on every run does not produce a
// commit on every run. "Nothing to do" is measured against the policy in force,
// not against the bytes: this vault assigns no floor at all, and every call
// below names the floor it is already on.
func TestConfigMutationsWithNothingToDoLeaveNoHistory(t *testing.T) {
	h := newHarness(t, nil)
	before := h.head()
	original := h.readFile(config.FileName)

	for _, args := range [][]string{
		{"config", "unset", autosaveKey},
		{"config", "set", autosaveKey, config.DefaultAutosaveMinInterval},
		{"config", "set", autosaveKey, "300s"},
		{"config", "set", autosaveKey, "1m", "--dry-run"},
		{"config", "set", autosaveKey, config.DefaultAutosaveMinInterval, "--dry-run"},
		{"config", "unset", autosaveKey, "--dry-run"},
	} {
		if err := runCLI(t, h, args...); err != nil {
			t.Fatalf("bn %s: %v", strings.Join(args, " "), err)
		}
		if got := h.head(); got != before {
			t.Fatalf("`bn %s` committed %q", strings.Join(args, " "), h.subject("HEAD"))
		}
		if got := h.readFile(config.FileName); got != original {
			t.Fatalf("`bn %s` rewrote %s:\n%s", strings.Join(args, " "), config.FileName, got)
		}
	}

	// Not even the undo journal: nothing was captured, so nothing was recorded.
	if h.exists(".bnotes") {
		t.Fatalf(".bnotes exists; a command with nothing to do left artifacts: %v", h.tree())
	}
}

// TestConfigSetRecordsTheImmediateCommitUnderItsOwnOperation pins the half of
// the lifecycle the matrix rows measure only by shape. The rows prove exactly
// one commit of exactly brainnotes.toml; this proves that commit is the
// explicit, immediate one these commands are supposed to make, recorded under
// the fixed operation token rather than under anything derived from the value
// the user typed.
func TestConfigSetRecordsTheImmediateCommitUnderItsOwnOperation(t *testing.T) {
	h := newHarness(t, nil)

	if err := runCLI(t, h, "config", "set", autosaveKey, "90s"); err != nil {
		t.Fatalf("bn config set: %v", err)
	}
	if subject := h.subject("HEAD"); !strings.Contains(subject, "config-set") {
		t.Fatalf("the commit subject is %q, want it to name the config-set operation", subject)
	}
	if got := h.readFile(config.FileName); !strings.Contains(got, `min_interval = "1m30s"`) {
		t.Fatalf("%s does not hold the canonical value:\n%s", config.FileName, got)
	}

	if err := runCLI(t, h, "config", "unset", autosaveKey); err != nil {
		t.Fatalf("bn config unset: %v", err)
	}
	if subject := h.subject("HEAD"); !strings.Contains(subject, "config-unset") {
		t.Fatalf("the commit subject is %q, want it to name the config-unset operation", subject)
	}
	// The token is the whole commit subject's vocabulary on purpose: the value
	// the user passed is note-adjacent user input and belongs in the local undo
	// journal, not in a message every clone of the vault carries.
	for _, leaked := range []string{"90s", "1m30s", autosaveKey} {
		if subject := h.subject("HEAD~1"); strings.Contains(subject, leaked) {
			t.Fatalf("the commit subject %q leaked %q", subject, leaked)
		}
	}
}

// TestWebSafetyFailuresAreDistinguishable500s pins the one HTTP convention the
// status code alone cannot carry.
//
// A refusal before the write and a snapshot failure after it are both "the
// server could not do this safely", and both are 500. But they are opposite
// answers to the only question the person on the other end has — did my note
// change? — so the response has to distinguish them, and the code and the
// message are the only things that can. A regression that collapsed the two
// would still return 500 and would still look fine in a status assertion.
func TestWebSafetyFailuresAreDistinguishable500s(t *testing.T) {
	h := newHarness(t, nil)
	h.breakSnapshots()

	failed := postCapture(t, h, "the capture that landed")
	if code := noteops.CodeOf(failed); code != noteops.CodeSnapshotFailed {
		t.Fatalf("a snapshot failure answered %q, want %q (%v)", code, noteops.CodeSnapshotFailed, failed)
	}
	if !strings.Contains(failed.Error(), "completed and is on disk") {
		t.Fatalf("the response must say the capture stands; it said %q", failed)
	}
	if !strings.Contains(h.readFile("Inbox.md"), "the capture that landed") {
		t.Fatal("the response said the capture is on disk and it is not")
	}

	h.repairSnapshots()
	vaultgit.ForgetShared(h.root)

	refused := postCapture(t, h, "the capture that never started")
	if code := noteops.CodeOf(refused); code != noteops.CodeSafety {
		t.Fatalf("a refusal answered %q, want %q (%v)", code, noteops.CodeSafety, refused)
	}
	if strings.Contains(refused.Error(), "completed and is on disk") {
		t.Fatalf("a refusal before the write must not claim the capture happened: %q", refused)
	}
	if strings.Contains(h.readFile("Inbox.md"), "never started") {
		t.Fatal("a refused capture was written anyway")
	}
}

// createBackup archives the vault as it stands. The archive lands under
// <vault>/backups, which the history excludes wholesale, so making one is not
// itself a mutation the engine records.
func createBackup(t *testing.T, h *harness) string {
	t.Helper()
	result, err := backup.Create(h.root, backup.CreateOptions{
		Now: func() time.Time { return time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("backup.Create: %v", err)
	}
	return result.ArchivePath
}

// backupRestoreOptions overlays rather than wipes, which keeps this case about
// preflight and the shape of the snapshot. What a wipe does to the history it
// commits into is its own case above.
func backupRestoreOptions() backup.RestoreOptions {
	return backup.RestoreOptions{}
}

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

	"github.com/sglavoie/sglavoie_brainnotes/internal/cli"
	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
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

// mutations is the whole table. It is assembled from groups rather than written
// as one literal only because of its length: the groups are the layer a writer
// is reached through, and the runner treats every row identically.
func mutations() []mutation {
	all := noteopsMutations()
	all = append(all, cliMutations()...)
	all = append(all, sourceMutations()...)
	all = append(all, settingsMutations()...)
	all = append(all, webWriteMutations()...)
	return append(all, surfaceMutations()...)
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

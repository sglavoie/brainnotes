package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultfixture"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultimport"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// The synchronization surface's fixture: two enrolled replicas, real packages,
// and a server over the one that has to decide about them.
//
// Nothing here is hand-written state. The enrollment runs the flow a person runs,
// every package goes through vaultsync.Builder, and every hold is recorded by
// vaultimport.Drain refusing to apply one — so a test that passes is evidence
// about these handlers rather than about a fixture that agreed with them. A
// hand-written .bnotes/sync/state.json would have made the conflicts trivially
// available and would have stopped proving that this surface can describe what
// the engine actually records.

const syncTestToken = "sync-test-token"

// The bodies the two replicas write over one starting point. Each pair differs
// from the seed as well as from each other, so a version shown by the review
// route can only have come from the branch it claims.
const (
	syncLocalAlpha  = "---\nstatus: draft\ntitle: Alpha\n---\n\nAlpha body, rewritten here.\n"
	syncRemoteAlpha = "---\nstatus: review\ntitle: Alpha\n---\n\nAlpha body, rewritten on the peer.\n"
	syncLocalBeta   = "---\nstatus: draft\ntitle: Beta\n---\n\nBeta body, rewritten here.\n"
	syncLocalGamma  = "---\nstatus: draft\ntitle: Gamma\n---\n\nGamma body, rewritten here.\n"
)

// syncAttachmentPath is the contested attachment. It is a real non-text payload,
// because "this version cannot be shown and cannot be merged" is a claim about
// bytes and a fixture holding UTF-8 would never reach it.
const syncAttachmentPath = "Attachments/diagram.png"

var (
	syncSeedAttachment   = "\x89PNG\r\n\x1a\n\x00\x00seed\xff\xfe"
	syncLocalAttachment  = "\x89PNG\r\n\x1a\n\x00\x00local\xff\xfe"
	syncRemoteAttachment = "\x89PNG\r\n\x1a\n\x00\x00remote\xff\xfe"
)

// syncWorld is the fixture: the replica pair, the peer's export cursor, and the
// server serving the local vault.
type syncWorld struct {
	pair  *vaultfixture.Pair
	clock *vaultfixture.Clock
	local *vaultsync.Enrollment
	peer  *vaultsync.Enrollment
	// sequence is the peer's position in its own stream, advanced per package so
	// two packages never claim one position.
	sequence uint64
	// packages and operations map a label a test uses to the two identities the
	// package carries. The operation identity is minted here rather than by the
	// builder, because a causal predecessor points at an operation and a chain of
	// them has to be nameable before the packages exist.
	packages   map[string]string
	operations map[string]string
	srv        *Server
	ts         *httptest.Server
}

// newSyncWorld enrolls two replicas and serves the local one. It delivers no
// packages: a test says which disagreements it is about.
func newSyncWorld(t *testing.T, customize ...func(*Options)) *syncWorld {
	t.Helper()

	files := vaultfixture.DefaultFiles()
	files[syncAttachmentPath] = syncSeedAttachment
	world := &syncWorld{
		pair:       vaultfixture.NewPair(t, files),
		clock:      vaultfixture.NewClock(t),
		packages:   map[string]string{},
		operations: map[string]string{},
	}
	world.enroll(t)
	world.serve(t, customize...)
	return world
}

func (w *syncWorld) enroll(t *testing.T) {
	t.Helper()

	local, err := vaultsync.Establish(w.pair.Local.Root, vaultsync.Options{Label: "mac", Now: w.clock.Now})
	if err != nil {
		t.Fatalf("Establish: %v", err)
	}
	w.clock.Tick()
	peer, err := vaultsync.Join(w.pair.Peer.Root, local.Invite(),
		vaultsync.Options{Label: "pi", Now: w.clock.Now})
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	w.clock.Tick()
	local, err = vaultsync.RecordPeer(w.pair.Local.Root,
		vaultsync.Peer{DeviceID: peer.DeviceID, Label: peer.Label})
	if err != nil {
		t.Fatalf("RecordPeer: %v", err)
	}
	w.local, w.peer = local, peer
}

// serve starts a server over the local vault, in the mode the options name.
//
// The configuration is the fixture's own loaded file rather than one built here,
// because an enrolled vault's loader is stricter than a struct literal: a fixture
// that bypassed it could hold a machine-local key in the portable file and no
// test would notice until a real vault refused to start.
func (w *syncWorld) serve(t *testing.T, customize ...func(*Options)) {
	t.Helper()

	cfg := w.pair.Local.LoadConfig(t)
	disabled := false
	cfg.Serve.Addr = "127.0.0.1:0"
	cfg.Serve.OpenBrowser = &disabled
	opts := Options{CSRFToken: syncTestToken}
	for _, apply := range customize {
		apply(&opts)
	}
	opened, err := w.open(cfg, opts)
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	srv, err := NewServer(cfg, opened, opts)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	w.srv = srv
	w.ts = httptest.NewServer(srv.Handler())
	t.Cleanup(w.ts.Close)
}

func (w *syncWorld) open(cfg *config.Config, opts Options) (*vault.Vault, error) {
	if opts.ReadOnly {
		return vault.OpenReadOnly(cfg)
	}
	return vault.Open(cfg)
}

func (w *syncWorld) root() string { return w.pair.Local.Root }

// believes is the expectation the peer states for a path both replicas still
// agree on, read from the peer because a package states the *producer's* belief.
func (w *syncWorld) believes(t *testing.T, rel string) vaultsync.Expectation {
	t.Helper()
	return vaultsync.Existing(vaultsync.HashBytes([]byte(w.pair.Peer.Read(t, rel))))
}

// deliver publishes one package from the peer into the local vault's incoming
// channel, which is where the transport would have left it, and remembers its
// identity under label.
func (w *syncWorld) deliver(t *testing.T, label string, build func(*vaultsync.Builder)) string {
	t.Helper()

	operationID, err := vaultsync.NewOperationID()
	if err != nil {
		t.Fatalf("NewOperationID: %v", err)
	}
	return w.deliverOperation(t, label, operationID, build)
}

// deliverOperation is deliver with the operation identity chosen by the caller,
// which is what a package declaring a causal predecessor needs.
func (w *syncWorld) deliverOperation(
	t *testing.T,
	label, operationID string,
	build func(*vaultsync.Builder),
) string {
	t.Helper()

	w.sequence++
	builder, err := vaultsync.NewBuilder(w.peer, w.sequence, w.clock.Tick())
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	w.operations[label] = operationID
	builder.BeginOperation(operationID, w.clock.Now())
	build(builder)
	manifest, _, err := builder.PublishTo(vaultsync.IncomingDir(w.root()))
	if err != nil {
		t.Fatalf("PublishTo: %v", err)
	}
	w.packages[label] = manifest.PackageID
	return manifest.PackageID
}

// drain runs the incoming channel the way the companion does, which is what makes
// every hold in these tests durable rather than asserted.
//
// A refusal is not a failure here: a channel full of conflicts is precisely the
// state under test, and Drain reports per-package verdicts rather than erroring
// about them. It errors only about the vault, and that is worth failing on.
func (w *syncWorld) drain(t *testing.T) *vaultimport.Drained {
	t.Helper()

	report, err := vaultimport.Drain(w.root(),
		vaultimport.Options{Command: "bn sync --test", Now: w.clock.Now})
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	return report
}

// commit records this vault's own edits the way a vault in use holds them:
// committed content, and nothing of .bnotes.
//
// The exclusion is not tidiness. internal/vaultgit stages exact paths and .bnotes
// is outside its scope, so a real vault's history never contains a retained
// conflict version. A fixture committing with a plain `git add -A` would track
// them, and then the resolution's own cleanup — releasing the hold and discarding
// those bytes — would look to the Git preflight like a tracked path deleted with
// nothing authorizing it, which latches and refuses every later mutation. That
// failure is a fixture artefact and reads exactly like a bug in this surface.
func (w *syncWorld) commit(t *testing.T, message string) string {
	t.Helper()

	w.pair.Local.Git(t, "add", "-A", "--", ".", ":(exclude).bnotes")
	w.pair.Local.Git(t, "commit", "-q", "-m", message)
	return w.pair.Local.Head(t)
}

// holdTextConflict makes both replicas rewrite one note: the plain text conflict,
// and the only kind a manual merge can be composed for.
func (w *syncWorld) holdTextConflict(t *testing.T, label string) string {
	t.Helper()

	packaged := w.believes(t, "Alpha.md")
	w.pair.Local.Write(t, "Alpha.md", syncLocalAlpha)
	return w.deliver(t, label, func(b *vaultsync.Builder) {
		b.Write("Alpha.md", []byte(syncRemoteAlpha), packaged)
	})
}

// holdAttachmentConflict contests a payload that is not text. Both versions
// exist, neither can be shown, and a merge editor must not be offered.
func (w *syncWorld) holdAttachmentConflict(t *testing.T, label string) string {
	t.Helper()

	packaged := w.believes(t, syncAttachmentPath)
	w.pair.Local.Write(t, syncAttachmentPath, syncLocalAttachment)
	return w.deliver(t, label, func(b *vaultsync.Builder) {
		b.Write(syncAttachmentPath, []byte(syncRemoteAttachment), packaged)
	})
}

// holdRenameConflict has the peer move a note this vault moved somewhere else.
func (w *syncWorld) holdRenameConflict(t *testing.T, label string) string {
	t.Helper()

	moved := w.pair.Local.Read(t, "Notes/Gamma.md")
	packaged := w.believes(t, "Notes/Gamma.md")
	w.pair.Local.Write(t, "Archive/Gamma.md", moved)
	if err := os.Remove(w.pair.Local.Path("Notes/Gamma.md")); err != nil {
		t.Fatalf("move the note locally: %v", err)
	}
	return w.deliver(t, label, func(b *vaultsync.Builder) {
		b.Delete("Notes/Gamma.md", packaged)
		b.Write("Notes/Delta.md", []byte(moved), vaultsync.Absent())
	})
}

// holdDeletionConflict has the peer delete a note this vault has since changed.
func (w *syncWorld) holdDeletionConflict(t *testing.T, label string) string {
	t.Helper()

	packaged := w.believes(t, "Beta.md")
	w.pair.Local.Write(t, "Beta.md", syncLocalBeta)
	return w.deliver(t, label, func(b *vaultsync.Builder) {
		b.Delete("Beta.md", packaged)
	})
}

// holdSettingsConflict contests the declarations that govern every note, which is
// the kind that refuses "keep both".
func (w *syncWorld) holdSettingsConflict(t *testing.T, label string) string {
	t.Helper()

	baseline := w.pair.Local.Read(t, config.FileName)
	packaged := vaultsync.Existing(vaultsync.HashBytes([]byte(baseline)))
	w.pair.Local.Write(t, config.FileName,
		baseline+"\n[frontmatter.properties.area]\ntype = \"string\"\n")
	remote := baseline + "\n[frontmatter.properties.org]\ntype = \"string\"\n"
	return w.deliver(t, label, func(b *vaultsync.Builder) {
		b.Write(config.FileName, []byte(remote), packaged)
	})
}

// dependent delivers a package that waits on another's operation, which is how a
// test gets a blocked dependency without corrupting anything.
func (w *syncWorld) dependent(t *testing.T, label, path, body, predecessor string) string {
	t.Helper()

	return w.deliver(t, label, func(b *vaultsync.Builder) {
		b.Write(path, []byte(body), vaultsync.Absent(), predecessor)
	})
}

// packageOf and operationOf are the two identities a delivered package carries.
func (w *syncWorld) packageOf(t *testing.T, label string) string {
	t.Helper()

	packageID, known := w.packages[label]
	if !known {
		t.Fatalf("no package was delivered as %q", label)
	}
	return packageID
}

func (w *syncWorld) operationOf(t *testing.T, label string) string {
	t.Helper()

	operationID, known := w.operations[label]
	if !known {
		t.Fatalf("no package was delivered as %q", label)
	}
	return operationID
}

func (w *syncWorld) inspect(t *testing.T) []vaultimport.Pending {
	t.Helper()

	pending, err := vaultimport.Inspect(w.root())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	return pending
}

// get reads one route through the whole handler stack, guard included.
func (w *syncWorld) get(t *testing.T, route string) (int, string) {
	t.Helper()

	resp, err := http.Get(w.ts.URL + route) // #nosec G107 -- test-only local listener URL
	if err != nil {
		t.Fatalf("GET %s: %v", route, err)
	}
	return resp.StatusCode, readResponseBody(t, resp)
}

// post sends a JSON body with the CSRF header, which is the way the page's own
// fetches are shaped.
func (w *syncWorld) post(t *testing.T, route string, body any) (int, string) {
	t.Helper()

	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode %s request: %v", route, err)
	}
	req, err := http.NewRequest(http.MethodPost, w.ts.URL+route, strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrfHeaderName, syncTestToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", route, err)
	}
	return resp.StatusCode, readResponseBody(t, resp)
}

// conflicts reads the review route and decodes it, failing on anything but 200.
func (w *syncWorld) conflicts(t *testing.T) syncConflictsView {
	t.Helper()

	status, body := w.get(t, syncConflictsAPIRoutePath)
	if status != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", syncConflictsAPIRoutePath, status, body)
	}
	var view syncConflictsView
	decodeSyncJSON(t, body, &view)
	return view
}

func (w *syncWorld) status(t *testing.T) syncStatusView {
	t.Helper()

	status, body := w.get(t, syncStatusAPIRoutePath)
	if status != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", syncStatusAPIRoutePath, status, body)
	}
	var view syncStatusView
	decodeSyncJSON(t, body, &view)
	return view
}

// preview plans a resolution and returns the plan, or the refusal body.
func (w *syncWorld) preview(t *testing.T, req syncResolveRequest) (int, syncPlanView, string) {
	t.Helper()

	status, body := w.post(t, syncPreviewAPIRoutePath, req)
	if status != http.StatusOK {
		return status, syncPlanView{}, body
	}
	var view syncPlanView
	decodeSyncJSON(t, body, &view)
	return status, view, body
}

func decodeSyncJSON(t *testing.T, body string, into any) {
	t.Helper()

	if err := json.Unmarshal([]byte(body), into); err != nil {
		t.Fatalf("decode %T from %s: %v", into, body, err)
	}
}

// operationNamed finds one operation in the review payload.
func operationNamed(t *testing.T, view syncConflictsView, packageID string) syncConflictOperationView {
	t.Helper()

	for _, operation := range view.Operations {
		if operation.PackageID == packageID {
			return operation
		}
	}
	t.Fatalf("no operation for %s in %d reported: %+v", packageID, len(view.Operations), view.Operations)
	return syncConflictOperationView{}
}

// pathNamed finds one contested path of an operation.
func pathNamed(t *testing.T, operation syncConflictOperationView, rel string) syncContestedPathView {
	t.Helper()

	for _, path := range operation.Paths {
		if path.Path == rel {
			return path
		}
	}
	t.Fatalf("%s does not contest %q: %+v", operation.PackageID, rel, operation.Paths)
	return syncContestedPathView{}
}

// choiceNamed finds one choice among a path's available or refused ones.
func choiceNamed(t *testing.T, path syncContestedPathView, choice string) (syncChoiceView, bool) {
	t.Helper()

	for _, available := range path.Available {
		if available.Choice == choice {
			return available, true
		}
	}
	for _, refused := range path.Refused {
		if refused.Choice == choice {
			return refused, false
		}
	}
	t.Fatalf("%q offers neither %s nor a reason for refusing it: %+v", path.Path, choice, path)
	return syncChoiceView{}, false
}

// assertVersionsShown fails unless a contested path carries both versions, which
// is the claim the whole review surface rests on.
func assertVersionsShown(t *testing.T, path syncContestedPathView, local, remote string) {
	t.Helper()

	if path.Local.Text != local {
		t.Fatalf("%s: this vault's version = %q, want %q", path.Path, path.Local.Text, local)
	}
	if path.Remote.Text != remote {
		t.Fatalf("%s: the peer's version = %q, want %q", path.Path, path.Remote.Text, remote)
	}
}

// assertChoicesOffered fails unless each named choice is available and worded. An
// offered choice with no sentence is a radio button nobody can act on.
func assertChoicesOffered(t *testing.T, path syncContestedPathView, choices ...vaultimport.Choice) {
	t.Helper()

	for _, choice := range choices {
		view, available := choiceNamed(t, path, string(choice))
		if !available {
			t.Fatalf("%s: %s is not on offer", path.Path, choice)
		}
		if view.Label == "" || view.Effect == "" {
			t.Fatalf("%s: %s is offered with nothing said about it: %+v", path.Path, choice, view)
		}
	}
}

// assertEffectsAreContentNeutral fails when a choice offered over content this
// page could not show describes itself as a decision about text.
//
// "Text" is the whole test because it is the whole defect: every kind-specific
// sentence in sync_choice_wording.go is written about a note somebody is reading,
// and a person shown two digests and told one of them "keeps the text it has here"
// has been invited to weigh something they were never given.
func assertEffectsAreContentNeutral(t *testing.T, path syncContestedPathView) {
	t.Helper()

	for _, choice := range path.Available {
		if strings.Contains(strings.ToLower(choice.Effect), "text") {
			t.Fatalf("%s: %s is worded as a decision about text: %q",
				path.Path, choice.Choice, choice.Effect)
		}
	}
}

// assertChoiceWorded fails unless a choice is offered carrying exactly the
// sentence it is supposed to carry, and returns it for whatever else a test asks.
// complaint says what a mismatch would mean, because two long sentences printed
// side by side say which characters differ and never what that costs a person.
func assertChoiceWorded(
	t *testing.T,
	path syncContestedPathView,
	choice vaultimport.Choice,
	want, complaint string,
) syncChoiceView {
	t.Helper()

	view, available := choiceNamed(t, path, string(choice))
	if !available {
		t.Fatalf("%s: %s is not on offer, so the engine's own answer changed: %+v",
			path.Path, choice, path)
	}
	if view.Effect != want {
		t.Fatalf("%s: %s: %s: %q", path.Path, choice, complaint, view.Effect)
	}
	return view
}

// assertNamesTheVersionItDiscards is the other half of the claim above, and it is
// here rather than inline because the two are one claim: neutral wording is about
// the *effect* sentence only, and the version that stops being in the vault is
// still named. A page that went neutral on both would be asking for a destructive
// decision without saying anything was destroyed.
func assertNamesTheVersionItDiscards(t *testing.T, path syncContestedPathView) {
	t.Helper()

	for choice, discards := range map[vaultimport.Choice]string{
		vaultimport.KeepCurrent:    syncDiscardsRemote,
		vaultimport.AcceptIncoming: syncDiscardsLocal,
	} {
		view, _ := choiceNamed(t, path, string(choice))
		if view.Discards != discards {
			t.Fatalf("%s: %s discards %q, want %q", path.Path, choice, view.Discards, discards)
		}
	}
}

// errorCodeOf reads the management envelope's code, which is what a client acts
// on and is the thing these tests assert rather than the sentence.
func errorCodeOf(t *testing.T, body string) string {
	t.Helper()

	var envelope struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	decodeSyncJSON(t, body, &envelope)
	if envelope.OK || envelope.Code == "" {
		t.Fatalf("expected a refusal naming a code, got %s", body)
	}
	return envelope.Code
}

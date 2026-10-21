package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cockpit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

func TestBuildDiagnosticsSummaryAggregatesIssues(t *testing.T) {
	v, snapshot, _ := newFilterTestSnapshot(t, map[string]string{
		"Work/Source.md":    "---\ntags: [alpha, alpha, rare-source]\nstatus: active\n---\n[[Target]] [[Missing]]\n",
		"Work/Target.md":    "---\ntags: [alpha, rare-target]\nstatus: active\n---\nNo links.\n",
		"Archive/Target.md": "---\ntags: [beta]\nstatus: someday\n---\nNo links.\n",
		"Island.md":         "---\ntags: [lonely]\nstatus: someday\ndead_end: false\n---\nNo links.\n",
	})
	cfg := &config.Config{
		Frontmatter: config.FrontmatterConfig{
			Properties: map[string]config.PropertyDefinition{
				"status": {Type: config.KindString, Required: true, Allowed: []string{"active", "done"}},
				"type":   {Type: config.KindString, Required: true},
			},
		},
	}

	got, err := buildDiagnosticsSummary(v, cfg)
	if err != nil {
		t.Fatalf("buildDiagnosticsSummary: %v", err)
	}

	if len(snapshot.notes) != 4 {
		t.Fatalf("test snapshot should include every valid note, got %d", len(snapshot.notes))
	}

	if got.BrokenTargets[0].Label != "missing" || got.BrokenTargets[0].Count != 1 {
		t.Fatalf("broken target aggregate = %#v, want missing count 1", got.BrokenTargets)
	}
	if got.BrokenTargets[0].Items[0].Title != "Source" || got.BrokenTargets[0].Items[0].Location == "" {
		t.Fatalf("broken target source details missing: %#v", got.BrokenTargets[0].Items)
	}
	if got.BrokenTargets[0].Items[0].FolderPath != "/library?folder=Work" {
		t.Fatalf("broken target folder path = %q, want /library?folder=Work", got.BrokenTargets[0].Items[0].FolderPath)
	}

	if titles := diagnosticTitles(got.OrphanNotes); !slices.Equal(titles, []string{"Island", "Source"}) {
		t.Fatalf("orphan notes = %#v, want Island and Source; notes=%#v", titles, got.OrphanNotes)
	}
	if titles := diagnosticTitles(got.DeadEndNotes); !slices.Equal(titles, []string{"Target", "Target"}) {
		t.Fatalf("dead-end notes = %#v, want both Target notes and not exempt Island; notes=%#v", titles, got.DeadEndNotes)
	}

	if len(got.DuplicateTitles) != 1 || got.DuplicateTitles[0].Label != "Target" || got.DuplicateTitles[0].Count != 2 {
		t.Fatalf("duplicate title groups = %#v, want Target count 2", got.DuplicateTitles)
	}

	if got.FrontmatterErrors != nil {
		t.Fatalf("valid fixture should have no frontmatter errors: %#v", got.FrontmatterErrors)
	}
	if len(got.SchemaViolations) != 2 {
		t.Fatalf("schema groups count = %d, want 2; groups=%#v", len(got.SchemaViolations), got.SchemaViolations)
	}
	if got.SchemaViolations[0].Label != "status invalid value \"someday\" (allowed: active, done)" {
		t.Fatalf("first schema label = %q; groups=%#v", got.SchemaViolations[0].Label, got.SchemaViolations)
	}
	if got.SchemaViolations[0].Count != 2 {
		t.Fatalf("someday violation count = %d, want 2", got.SchemaViolations[0].Count)
	}

	longTail := longTailByProperty(got.LongTailValues)
	if values := longTail["tags"]; len(values) == 0 || values[0].Value != "beta" || values[0].Count != 1 {
		t.Fatalf("tags long-tail values = %#v, want rare values first", values)
	}

	cards := diagnosticCardsByKey(got.Cards)
	if cards["broken"].Count != 1 || cards["orphan"].Count != 2 || cards["dead_end"].Count != 2 ||
		cards["duplicate_titles"].Count != 2 || cards["schema_violations"].Count != 6 {
		t.Fatalf("diagnostic card counts = %#v", got.Cards)
	}
	if cards["broken"].Path != "/health#diagnostic-broken-targets" ||
		cards["orphan"].Path != "/health#diagnostic-orphans" ||
		cards["dead_end"].Path != "/health#diagnostic-dead-ends" ||
		cards["duplicate_titles"].Path != "/health#diagnostic-duplicate-titles" {
		t.Fatalf("diagnostic card filter paths = %#v", got.Cards)
	}
}

func TestBuildDiagnosticsSummaryCollectsFrontmatterParseErrors(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Good.md", "---\nstatus: active\n---\n[[Missing]]\n")
	writeTestFile(t, root, "Bad.md", "---\n: bad\n---\nBody\n")
	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")

	got, err := buildDiagnosticsSummary(v, cfg)
	if err != nil {
		t.Fatalf("buildDiagnosticsSummary: %v", err)
	}

	if len(got.FrontmatterErrors) != 1 {
		t.Fatalf("frontmatter errors = %#v, want one", got.FrontmatterErrors)
	}
	if got.FrontmatterErrors[0].Path != "Bad.md" {
		t.Fatalf("frontmatter error path = %q, want Bad.md", got.FrontmatterErrors[0].Path)
	}
	if len(got.BrokenTargets) != 1 || got.BrokenTargets[0].Label != "missing" {
		t.Fatalf("valid notes should still be diagnosed after parse error: %#v", got.BrokenTargets)
	}
	if diagnosticCardsByKey(got.Cards)["frontmatter_errors"].Count != 1 {
		t.Fatalf("frontmatter card count not updated: %#v", got.Cards)
	}
}

func TestServerIndexToleratesFrontmatterParseErrors(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Good.md": []byte("---\nstatus: active\n---\nBody\n"),
		"Bad.md":  []byte("---\n: bad\n---\nBody\n"),
	})

	resp, err := http.Get(ts.URL + "/") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "<h1>Daily Brief</h1>") {
		t.Fatalf("index did not render expected heading: %q", body)
	}
}

func TestServerHealthRendersSkippedNotesFromSharedSnapshot(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Good.md", "---\nstatus: active\n---\n[[Missing]]\n")
	writeTestFile(t, root, "Bad.md", "---\n: bad\n---\nBody\n")
	// A path with HTML metacharacters proves the skipped path is rendered safely.
	writeTestFile(t, root, "Blocked <script>.md", "---\ntitle: Blocked\n---\nBody\n")
	cfg, v := testVaultOpen(t, root, "127.0.0.1:0")

	srv, err := NewServer(cfg, v, Options{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	// Drive one blocked load through the shared snapshot the homepage now uses.
	blockedPath := filepath.Join(root, "Blocked <script>.md")
	load, _ := newBlockingLoader(t, blockedPath)
	srv.state.load = load
	srv.state.loadTimeout = 50 * time.Millisecond

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/health") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want %d; body=%q", resp.StatusCode, http.StatusOK, body)
	}

	// The skipped note gets its own diagnostic section...
	if !strings.Contains(body, `id="diagnostic-skipped-notes"`) {
		t.Fatalf("index missing skipped-notes section: %q", body)
	}
	// ...with the vault-relative path HTML-escaped, never as raw markup.
	if !strings.Contains(body, "Blocked &lt;script&gt;.md") {
		t.Fatalf("skipped path not rendered escaped: %q", body)
	}
	if strings.Contains(body, "Blocked <script>.md") {
		t.Fatalf("skipped path leaked unescaped markup: %q", body)
	}
	// A skipped note must never surface as a loaded/recent note link.
	if strings.Contains(body, `href="/n/blocked`) {
		t.Fatalf("skipped note leaked into loaded content routes: %q", body)
	}
	// Frontmatter parse errors keep their existing, separate treatment.
	if !strings.Contains(body, `id="diagnostic-frontmatter-errors"`) {
		t.Fatalf("index dropped frontmatter-error section: %q", body)
	}
	if !strings.Contains(body, "Bad.md") {
		t.Fatalf("frontmatter parse error not rendered: %q", body)
	}
}

func TestBuildDiagnosticsSummarySchemaEmptyBehavior(t *testing.T) {
	v, _, _ := newFilterTestSnapshot(t, map[string]string{
		"Note.md": "---\nstatus: someday\n---\nBody\n",
	})
	cfg := &config.Config{Frontmatter: config.FrontmatterConfig{Properties: map[string]config.PropertyDefinition{}}}

	got, err := buildDiagnosticsSummary(v, cfg)
	if err != nil {
		t.Fatalf("buildDiagnosticsSummary: %v", err)
	}
	if got.SchemaViolations != nil {
		t.Fatalf("empty schema should not produce violations: %#v", got.SchemaViolations)
	}
	if diagnosticCardsByKey(got.Cards)["schema_violations"].Count != 0 {
		t.Fatalf("empty schema should have zero schema card count: %#v", got.Cards)
	}
}

func TestBuildDiagnosticsSummaryLongTailIgnoresConfiguredProperties(t *testing.T) {
	// type and kind each have a dominant head (note / essay, three notes) plus a
	// lone rare value (idea / poem) that qualifies as long-tail. Ignoring "type"
	// must drop it from the diagnostics card while "kind" still shows, and the
	// card must select the identical set as the cockpit queue (no drift).
	v, _, _ := newFilterTestSnapshot(t, map[string]string{
		"A.md": "---\ntype: note\nkind: essay\n---\n[[B]]\n",
		"B.md": "---\ntype: note\nkind: essay\n---\n[[A]]\n",
		"D.md": "---\ntype: note\nkind: essay\n---\n[[A]]\n",
		"C.md": "---\ntype: idea\nkind: poem\n---\n[[A]]\n",
	})
	cfg := &config.Config{}
	cfg.Cockpit.LongTailIgnoreProperties = []string{"type"}

	got, err := buildDiagnosticsSummary(v, cfg)
	if err != nil {
		t.Fatalf("buildDiagnosticsSummary: %v", err)
	}

	longTail := longTailByProperty(got.LongTailValues)
	if _, ok := longTail["type"]; ok {
		t.Fatalf("ignored property type should be absent from the card: %#v", got.LongTailValues)
	}
	if values := longTail["kind"]; len(values) != 1 || values[0].Value != "poem" {
		t.Fatalf("kind long-tail values = %#v, want poem still flagged", values)
	}

	// The card and the cockpit queue must select the identical set.
	queue, err := cockpit.BuildQueue(v, cfg)
	if err != nil {
		t.Fatalf("BuildQueue: %v", err)
	}
	cardDetails := make([]string, 0)
	for property, values := range longTail {
		for _, value := range values {
			cardDetails = append(cardDetails, property+" = "+value.Value)
		}
	}
	queueDetails := make([]string, 0)
	for _, task := range queue.Tasks {
		if task.Kind == cockpit.RuleLongTailValue {
			queueDetails = append(queueDetails, task.Detail)
		}
	}
	sort.Strings(cardDetails)
	sort.Strings(queueDetails)
	if !slices.Equal(cardDetails, queueDetails) {
		t.Fatalf("card set %#v != cockpit queue set %#v", cardDetails, queueDetails)
	}
}

func TestLoadDiagnosticsClassifiesHealthyParseAndTimeout(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Good.md", "---\nstatus: active\n---\n[[Missing]]\n")
	writeTestFile(t, root, "Bad.md", "---\n: bad\n---\nBody\n")
	writeTestFile(t, root, "Blocked.md", "---\ntitle: Blocked\n---\nBody\n")
	_, v := testVaultOpen(t, root, "127.0.0.1:0")

	blockedPath := filepath.Join(root, "Blocked.md")
	load, callCount := newBlockingLoader(t, blockedPath)

	result, err := loadVaultNotes(v, load, 50*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("loadVaultNotes: %v", err)
	}

	if len(result.loaded) != 1 || result.loaded[0].rel != "Good.md" {
		t.Fatalf("loaded = %#v, want only Good.md", result.loaded)
	}
	if len(result.parseErrors) != 1 || result.parseErrors[0].Path != "Bad.md" {
		t.Fatalf("parseErrors = %#v, want Bad.md", result.parseErrors)
	}
	if len(result.skipped) != 1 || result.skipped[0].Path != "Blocked.md" {
		t.Fatalf("skipped = %#v, want Blocked.md", result.skipped)
	}
	if len(result.timedOut) != 1 || result.timedOut[0] != blockedPath {
		t.Fatalf("timedOut = %#v, want %q", result.timedOut, blockedPath)
	}
	if got := callCount(blockedPath); got != 1 {
		t.Fatalf("blocked loader called %d times, want 1", got)
	}
}

func TestLoadDiagnosticsSkipsQuarantinedWithoutRead(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Good.md", "---\nstatus: active\n---\nBody\n")
	writeTestFile(t, root, "Blocked.md", "---\ntitle: Blocked\n---\nBody\n")
	_, v := testVaultOpen(t, root, "127.0.0.1:0")

	blockedPath := filepath.Join(root, "Blocked.md")
	load, callCount := newBlockingLoader(t) // nothing blocks; the path is pre-quarantined
	quarantined := map[string]struct{}{blockedPath: {}}

	result, err := loadVaultNotes(v, load, 50*time.Millisecond, quarantined)
	if err != nil {
		t.Fatalf("loadVaultNotes: %v", err)
	}

	if len(result.loaded) != 1 || result.loaded[0].rel != "Good.md" {
		t.Fatalf("loaded = %#v, want only Good.md", result.loaded)
	}
	if len(result.skipped) != 1 || result.skipped[0].Path != "Blocked.md" {
		t.Fatalf("skipped = %#v, want Blocked.md", result.skipped)
	}
	if len(result.timedOut) != 0 {
		t.Fatalf("a pre-quarantined skip must not re-report timedOut: %#v", result.timedOut)
	}
	if got := callCount(blockedPath); got != 0 {
		t.Fatalf("quarantined path was read %d times, want 0", got)
	}
}

func diagnosticTitles(notes []diagnosticNote) []string {
	titles := make([]string, 0, len(notes))
	for _, note := range notes {
		titles = append(titles, note.Title)
	}
	return titles
}

func diagnosticCardsByKey(cards []diagnosticCard) map[string]diagnosticCard {
	out := make(map[string]diagnosticCard, len(cards))
	for _, card := range cards {
		out[card.Key] = card
	}
	return out
}

func longTailByProperty(groups []diagnosticLongTailGroup) map[string][]diagnosticValueCount {
	out := make(map[string][]diagnosticValueCount, len(groups))
	for _, group := range groups {
		out[group.Property] = group.Values
	}
	return out
}

package brief

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/recall"
	"github.com/sglavoie/brainnotes/internal/vault"
)

type selectorStateStub struct {
	noteExposures map[string]time.Time
	noteOpens     map[string]time.Time
	pairExposures map[string]time.Time
}

func (stub *selectorStateStub) ReadDailySelection(LocalDate) (DailySelection, bool, error) {
	return DailySelection{}, false, nil
}

func (stub *selectorStateStub) SaveDailySelection(DailySelection) error {
	return nil
}

func (stub *selectorStateStub) ReplaceDailySelection(LocalDate, []Card) (DailySelection, error) {
	return DailySelection{}, nil
}

func (stub *selectorStateStub) RecordVisit(LocalDate, time.Time) (RollingActivity, error) {
	return RollingActivity{}, nil
}

func (stub *selectorStateStub) RecordOpen(CardID, time.Time) (RollingActivity, error) {
	return RollingActivity{}, nil
}

func (stub *selectorStateStub) LastNoteExposure(noteRelPath string) (time.Time, bool, error) {
	normalized, err := normalizeBriefPath(noteRelPath)
	if err != nil {
		return time.Time{}, false, err
	}
	last, ok := stub.noteExposures[normalized]
	return last, ok, nil
}

func (stub *selectorStateStub) LastNoteOpen(noteRelPath string) (time.Time, bool, error) {
	normalized, err := normalizeBriefPath(noteRelPath)
	if err != nil {
		return time.Time{}, false, err
	}
	last, ok := stub.noteOpens[normalized]
	return last, ok, nil
}

func (stub *selectorStateStub) LastPairExposure(sourceRelPath, candidateRelPath string) (time.Time, bool, error) {
	sourceRelPath, candidateRelPath, _, err := normalizeBriefPair(sourceRelPath, candidateRelPath)
	if err != nil {
		return time.Time{}, false, err
	}
	key := sourceRelPath + "\x00" + candidateRelPath
	last, ok := stub.pairExposures[key]
	return last, ok, nil
}

func (stub *selectorStateStub) RollingActivity(time.Time) (RollingActivity, error) {
	return RollingActivity{}, nil
}

func (stub *selectorStateStub) setPairExposure(t *testing.T, sourcePath, candidatePath string, at time.Time) {
	t.Helper()
	sourcePath, candidatePath, _, err := normalizeBriefPair(sourcePath, candidatePath)
	if err != nil {
		t.Fatalf("normalizeBriefPair(%q, %q): %v", sourcePath, candidatePath, err)
	}
	if stub.pairExposures == nil {
		stub.pairExposures = make(map[string]time.Time)
	}
	stub.pairExposures[sourcePath+"\x00"+candidatePath] = at
}

func TestBuildSelectionCorpusAppliesExclusionsAndSubstantiveFilter(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	now := time.Date(2026, time.August, 4, 10, 0, 0, 0, time.UTC)
	writeSelectionNote(t, root, "Keep.md", `---
type: note
tags: [alpha, beta]
---
# Keep

Useful prose.
`, now)
	writeSelectionNote(t, root, "Archive/Skip.md", `---
type: note
---
# Skip
Archive note.
`, now)
	writeSelectionNote(t, root, "Journal.md", `---
type: journal
---
# Journal
Private.
`, now)
	writeSelectionNote(t, root, "Empty.md", `---
type: note
---
`, now)

	loaded, graph := loadSelectionFixture(t, root)
	corpus, err := BuildSelectionCorpus(root, loaded, graph, SelectionCorpusOptions{
		MetadataProperties: []string{"tags", "type"},
		ExcludeFolders:     []string{"Archive"},
		ExcludeTypes:       []string{"journal"},
	})
	if err != nil {
		t.Fatalf("BuildSelectionCorpus: %v", err)
	}
	if got, want := len(corpus.Snapshot.Notes), 1; got != want {
		t.Fatalf("snapshot note count = %d, want %d", got, want)
	}
	note := corpus.Snapshot.Notes[0]
	if note.Ref.RelPath != "Keep.md" {
		t.Fatalf("snapshot path = %q, want Keep.md", note.Ref.RelPath)
	}
	if got, want := note.MetadataValues["tags"], []string{"alpha", "beta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("metadata tags = %v, want %v", got, want)
	}
	if note.BlockCount <= 0 {
		t.Fatalf("BlockCount = %d, want > 0", note.BlockCount)
	}
	if excerpt := corpus.excerpts["Keep.md"]; strings.TrimSpace(excerpt) == "" {
		t.Fatal("expected non-empty excerpt for Keep.md")
	}
}

func TestDeterministicSelectorSelectContinuePrefersSubstanceThenPath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	now := time.Date(2026, time.August, 4, 10, 0, 0, 0, time.UTC)
	writeSelectionNote(t, root, "zeta.md", "# Zeta\nOne block.\n", now)
	writeSelectionNote(t, root, "alpha.md", "# Alpha\nFirst paragraph.\n\nSecond paragraph.\n", now)
	writeSelectionNote(t, root, "beta.md", "# Beta\nOne paragraph.\n\nTwo paragraph.\n", now)

	loaded, graph := loadSelectionFixture(t, root)
	corpus, err := BuildSelectionCorpus(root, loaded, graph, SelectionCorpusOptions{})
	if err != nil {
		t.Fatalf("BuildSelectionCorpus: %v", err)
	}
	selector, err := NewDeterministicSelector(corpus, DeterministicSelectorOptions{
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewDeterministicSelector: %v", err)
	}

	result, err := selector.SelectContinue(corpus.Snapshot)
	if err != nil {
		t.Fatalf("SelectContinue: %v", err)
	}
	if result.Card == nil {
		t.Fatalf("SelectContinue returned unavailable slot: %#v", result.Unavailable)
	}
	if got, want := result.Card.Source.RelPath, "alpha.md"; got != want {
		t.Fatalf("continue source = %q, want %q", got, want)
	}
	if strings.TrimSpace(result.Card.Excerpt) == "" {
		t.Fatal("continue excerpt should not be empty")
	}
}

func TestDeterministicSelectorSelectRediscoverUsesEffectiveActivityAndCooldown(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	now := time.Date(2026, time.August, 20, 9, 0, 0, 0, time.UTC)
	writeSelectionNote(t, root, "Continue.md", "# Continue\nKeep moving.\n", now.AddDate(0, 0, -1))
	writeSelectionNote(t, root, "CandidateA.md", "# Candidate A\nAlpha text.\n", now.AddDate(0, 0, -20))
	writeSelectionNote(t, root, "CandidateB.md", "# Candidate B\nBeta text.\n", now.AddDate(0, 0, -10))

	loaded, graph := loadSelectionFixture(t, root)
	corpus, err := BuildSelectionCorpus(root, loaded, graph, SelectionCorpusOptions{})
	if err != nil {
		t.Fatalf("BuildSelectionCorpus: %v", err)
	}
	state := &selectorStateStub{
		noteOpens: map[string]time.Time{
			"CandidateA.md": now.Add(-2 * time.Hour),
		},
		noteExposures: map[string]time.Time{},
	}
	selector, err := NewDeterministicSelector(corpus, DeterministicSelectorOptions{
		State: state,
		Now:   func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewDeterministicSelector: %v", err)
	}

	current := &Card{Source: NoteRef{RelPath: "Continue.md", Title: "Continue"}}
	result, err := selector.SelectRediscover(corpus.Snapshot, current)
	if err != nil {
		t.Fatalf("SelectRediscover: %v", err)
	}
	if result.Card == nil {
		t.Fatalf("SelectRediscover returned unavailable slot: %#v", result.Unavailable)
	}
	if got, want := result.Card.Source.RelPath, "CandidateB.md"; got != want {
		t.Fatalf("rediscover source with open-history tie-break = %q, want %q", got, want)
	}

	state.noteExposures["CandidateA.md"] = now.AddDate(0, 0, -1)
	state.noteExposures["CandidateB.md"] = now.AddDate(0, 0, -1)
	result, err = selector.SelectRediscover(corpus.Snapshot, current)
	if err != nil {
		t.Fatalf("SelectRediscover with cooldown saturation: %v", err)
	}
	if result.Card == nil {
		t.Fatalf("SelectRediscover cooldown relaxation returned unavailable: %#v", result.Unavailable)
	}
	if got, want := result.Card.Source.RelPath, "CandidateB.md"; got != want {
		t.Fatalf("rediscover source after cooldown relaxation = %q, want %q", got, want)
	}
}

func TestDeterministicSelectorSelectConnectFiltersCooldownAndPreservesRankingFields(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	now := time.Date(2026, time.August, 20, 10, 0, 0, 0, time.UTC)
	writeSelectionNote(t, root, "Source.md", "# Source\nalpha beta [[Bridge]]\n", now)
	writeSelectionNote(t, root, "Bridge.md", "# Bridge\n[[CandidateOne]] [[CandidateTwo]]\n", now)
	writeSelectionNote(t, root, "CandidateOne.md", "# Candidate One\nalpha beta gamma delta\n", now)
	writeSelectionNote(t, root, "CandidateTwo.md", "# Candidate Two\nalpha beta gamma\n", now)

	loaded, graph := loadSelectionFixture(t, root)
	corpus, err := BuildSelectionCorpus(root, loaded, graph, SelectionCorpusOptions{
		MetadataProperties: []string{"tags"},
	})
	if err != nil {
		t.Fatalf("BuildSelectionCorpus: %v", err)
	}
	state := &selectorStateStub{}
	selector, err := NewDeterministicSelector(corpus, DeterministicSelectorOptions{
		State: state,
		Now:   func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewDeterministicSelector: %v", err)
	}

	ranked, err := selector.ranker.Rank("Source.md", recall.ModeConnect)
	if err != nil {
		t.Fatalf("rank connect: %v", err)
	}
	if len(ranked) < 2 {
		t.Fatalf("expected at least two connect candidates, got %d", len(ranked))
	}
	first := ranked[0]
	second := ranked[1]
	state.setPairExposure(t, "Source.md", first.Path, now.AddDate(0, 0, -1))

	result, err := selector.SelectConnect(corpus.Snapshot, NoteRef{RelPath: "Source.md", Title: "Source"})
	if err != nil {
		t.Fatalf("SelectConnect: %v", err)
	}
	if result.Card == nil || result.Card.Candidate == nil {
		t.Fatalf("SelectConnect returned unavailable slot: %#v", result.Unavailable)
	}
	if got, want := result.Card.Candidate.RelPath, second.Path; got != want {
		t.Fatalf("connect candidate after pair cooldown = %q, want %q", got, want)
	}
	if got, want := result.Card.Metadata.Score, second.Score; got != want {
		t.Fatalf("connect score = %v, want %v", got, want)
	}
	if got, want := result.Card.Metadata.Relationship, string(second.Relationship); got != want {
		t.Fatalf("connect relationship = %q, want %q", got, want)
	}
	if got, want := result.Card.Metadata.Bridge, connectBridge(second); got != want {
		t.Fatalf("connect bridge = %q, want %q", got, want)
	}
	if got, want := result.Card.Metadata.Reasons, connectReasonStrings(second.Reasons); !reflect.DeepEqual(got, want) {
		t.Fatalf("connect reasons = %v, want %v", got, want)
	}

	state.setPairExposure(t, "Source.md", second.Path, now.AddDate(0, 0, -1))
	result, err = selector.SelectConnect(corpus.Snapshot, NoteRef{RelPath: "Source.md", Title: "Source"})
	if err != nil {
		t.Fatalf("SelectConnect cooldown relaxation: %v", err)
	}
	if result.Card == nil || result.Card.Candidate == nil {
		t.Fatalf("SelectConnect cooldown relaxation unavailable: %#v", result.Unavailable)
	}
	if got, want := result.Card.Candidate.RelPath, first.Path; got != want {
		t.Fatalf("connect candidate after full cooldown relaxation = %q, want %q", got, want)
	}
}

func writeSelectionNote(t *testing.T, root, relPath, body string, modifiedAt time.Time) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", relPath, err)
	}
	if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write note %q: %v", relPath, err)
	}
	if !modifiedAt.IsZero() {
		if err := os.Chtimes(fullPath, modifiedAt, modifiedAt); err != nil {
			t.Fatalf("chtimes %q: %v", relPath, err)
		}
	}
}

func loadSelectionFixture(t *testing.T, root string) ([]*vault.Note, *query.Graph) {
	t.Helper()
	cfg := &config.Config{Vault: root}
	v, err := vault.Open(cfg)
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	notes, err := v.Notes()
	if err != nil {
		t.Fatalf("vault.Notes: %v", err)
	}
	return notes, query.BuildGraphForRoot(v.Root, notes)
}

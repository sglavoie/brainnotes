package index

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// This file measures what the cache actually costs, because the claim it was
// built on — "a stat instead of a read plus a parse" — was only half true and
// nothing here could tell you so. A warm load skips the read and still parses
// every note, and the counts below are what make that statement checkable
// rather than a comment someone has to trust.

// benchVaultSize is the note count the benchmarks run over. ~500 notes is the
// size the plan's whole performance argument was reasoned against, and it is
// large enough that a per-note cost is visible above the walk itself.
const benchVaultSize = 500

// costVaultSize is the note count the counting test runs over. It is small
// because the assertions are exact counts, not timings, and a fixture that
// takes a second to write buys nothing.
const costVaultSize = 24

// loadCounts tallies the two per-note costs a load can pay. Load holds ix.mu
// and walks sequentially, so an unsynchronised counter is safe here.
type loadCounts struct {
	reads  int
	parses int
}

func (c *loadCounts) reset() { c.reads, c.parses = 0, 0 }

// read is the WithReader seam: every byte the index reads from disk passes
// through it.
func (c *loadCounts) read(path string) ([]byte, error) {
	c.reads++
	return os.ReadFile(path)
}

// parse is the Index.parseNote seam: every note the index turns into a
// *vault.Note passes through it, cached bytes or freshly read ones alike.
func (c *loadCounts) parse(root, path string, data []byte) (*vault.Note, error) {
	c.parses++
	return vault.NewNoteFromSource(root, path, data)
}

// countingIndex is an index whose reads and parses are both counted.
func countingIndex(tb testing.TB, v *vault.Vault, c *loadCounts) *Index {
	tb.Helper()

	ix, err := New(v, WithReader(c.read), WithLogger(nil))
	if err != nil {
		tb.Fatalf("new index: %v", err)
	}
	ix.parseNote = c.parse
	return ix
}

// directLoadCounted is the direct load path with its two costs made visible.
// vault.Note.Load is a read followed by a parse of exactly these bytes —
// internal/vault's TestNewNoteFromSourceRoundTripsIdenticallyToLoad pins that
// the pair produces a note identical to Load's, and
// TestDirectDecompositionMatchesNoteLoad below re-pins it over this fixture —
// so counting the decomposition counts what a direct load really does.
func directLoadCounted(tb testing.TB, v *vault.Vault, c *loadCounts) []*vault.Note {
	tb.Helper()

	var notes []*vault.Note
	err := v.Walk(vault.WalkFilter{}, func(note *vault.Note) error {
		data, readErr := c.read(note.Path)
		if readErr != nil {
			return nil
		}
		parsed, parseErr := c.parse(v.Root, note.Path, data)
		if parseErr != nil {
			return nil
		}
		notes = append(notes, parsed)
		return nil
	})
	if err != nil {
		tb.Fatalf("direct walk: %v", err)
	}
	return notes
}

// syntheticVault writes n notes across a few folders, each with a realistic
// frontmatter block and a body carrying wikilinks, then backdates them all so
// the second load of the same vault takes the clean stat-only path rather than
// re-reading everything inside staleWindow.
func syntheticVault(tb testing.TB, n int) *vault.Vault {
	tb.Helper()

	folders := []string{"Notes", "Areas", "Areas/Deep", "Archive", "Projects"}
	statuses := []string{"wip", "done", "idea"}

	root := tb.TempDir()
	for i := range n {
		rel := fmt.Sprintf("%s/Note %04d.md", folders[i%len(folders)], i)
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			tb.Fatalf("mkdir %s: %v", filepath.Dir(abs), err)
		}
		if err := os.WriteFile(abs, []byte(syntheticNote(i, n, statuses)), 0o644); err != nil {
			tb.Fatalf("write %s: %v", abs, err)
		}
	}

	cfg := &config.Config{Vault: root}
	cfg.Frontmatter.Properties = map[string]config.PropertyDefinition{
		"tags":     {Type: "list", Items: "string"},
		"status":   {Type: "string"},
		"rating":   {Type: "int"},
		"parent":   {Type: "wikilink"},
		"reviewed": {Type: "bool"},
	}
	v, err := vault.Open(cfg)
	if err != nil {
		tb.Fatalf("open vault: %v", err)
	}
	backdate(tb, root, time.Hour)
	return v
}

// syntheticNote is one note of the shape this vault is optimised for: a
// multi-key frontmatter block including a list and a wikilink, and a body of a
// few paragraphs with links into the rest of the vault.
func syntheticNote(i, n int, statuses []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\nstatus: %s\ntags: [core, ops, area-%d]\nrating: %d\nreviewed: %t\nparent: \"[[Note %04d]]\"\n---\n",
		statuses[i%len(statuses)], i%7, i%5, i%2 == 0, (i+1)%n)
	for line := range 20 {
		fmt.Fprintf(&b, "Paragraph %d of note %04d, linking to [[Note %04d]] and [[Note %04d]].\n\n",
			line, i, (i*7+line)%n, (i*13+line)%n)
	}
	return b.String()
}

// markdownCount is the number of notes v.Walk visits, which is what both load
// paths pay their per-note costs on.
func markdownCount(tb testing.TB, v *vault.Vault) int {
	tb.Helper()

	count := 0
	if err := v.Walk(vault.WalkFilter{}, func(*vault.Note) error {
		count++
		return nil
	}); err != nil {
		tb.Fatalf("walk: %v", err)
	}
	return count
}

// TestLoadCostsAreOneReadPerMissAndOneParsePerNote is the measurement the
// index shipped without. The read is the only cost the cache removes: a warm
// load performs zero reads and still performs one frontmatter parse per note,
// exactly as many as a direct load does.
func TestLoadCostsAreOneReadPerMissAndOneParsePerNote(t *testing.T) {
	v := syntheticVault(t, costVaultSize)
	notes := markdownCount(t, v)
	if notes != costVaultSize {
		t.Fatalf("walk visited %d notes, want %d", notes, costVaultSize)
	}

	var direct loadCounts
	directNotes := directLoadCounted(t, v, &direct)
	if direct.reads != notes || direct.parses != notes {
		t.Errorf("direct load: %d reads and %d parses, want %d of each", direct.reads, direct.parses, notes)
	}

	var indexed loadCounts
	ix := countingIndex(t, v, &indexed)

	cold := mustLoad(t, ix)
	if indexed.reads != notes || indexed.parses != notes {
		t.Errorf("cold index load: %d reads and %d parses, want %d of each", indexed.reads, indexed.parses, notes)
	}
	if got, want := relPaths(t, v.Root, cold.Notes), relPaths(t, v.Root, directNotes); !slices.Equal(got, want) {
		t.Fatalf("cold index note set = %v, want %v", got, want)
	}

	indexed.reset()
	warm := mustLoad(t, ix)
	if indexed.reads != 0 {
		t.Errorf("warm index load: %d reads, want 0 — the cache did not serve every entry", indexed.reads)
	}
	if indexed.parses != notes {
		t.Errorf("warm index load: %d parses, want %d — every walked note is parsed afresh, cached or not", indexed.parses, notes)
	}
	if got, want := relPaths(t, v.Root, warm.Notes), relPaths(t, v.Root, directNotes); !slices.Equal(got, want) {
		t.Fatalf("warm index note set = %v, want %v", got, want)
	}
	if warm.Changed {
		t.Error("an untouched vault reported Changed, so the warm pass did not exercise the all-clean path")
	}
}

// TestDirectDecompositionMatchesNoteLoad keeps the counted direct path honest:
// read-then-NewNoteFromSource must produce the same note vault.Note.Load
// produces, or the read and parse counts above would be describing something
// the production direct path does not do.
func TestDirectDecompositionMatchesNoteLoad(t *testing.T) {
	v := syntheticVault(t, 3)

	var counts loadCounts
	decomposed := directLoadCounted(t, v, &counts)
	loaded := directLoadNotes(t, v)
	if len(decomposed) != len(loaded) || len(loaded) == 0 {
		t.Fatalf("decomposed %d notes, Load produced %d", len(decomposed), len(loaded))
	}
	for i := range loaded {
		if !reflect.DeepEqual(decomposed[i], loaded[i]) {
			t.Fatalf("note %s: the decomposed direct load differs from vault.Note.Load", loaded[i].Path)
		}
	}
}

// BenchmarkDirectLoad walks the vault and reads and parses every note, which is
// what internal/web's directNoteSource does on every snapshot rebuild.
func BenchmarkDirectLoad(b *testing.B) {
	v := syntheticVault(b, benchVaultSize)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if n := len(directLoadNotes(b, v)); n != benchVaultSize {
			b.Fatalf("loaded %d notes, want %d", n, benchVaultSize)
		}
	}
}

// BenchmarkColdIndexLoad is the worst case for the cache: nothing cached, so
// every note is read, hashed and parsed, and the state file is written. It
// should be slower than a direct load, and the gap is what a warm load has to
// earn back.
func BenchmarkColdIndexLoad(b *testing.B) {
	v := syntheticVault(b, benchVaultSize)
	ix, _ := newTestIndex(b, v)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		ix.Reset()
		b.StartTimer()

		result, err := ix.Load()
		if err != nil {
			b.Fatalf("index load: %v", err)
		}
		if len(result.Notes) != benchVaultSize {
			b.Fatalf("loaded %d notes, want %d", len(result.Notes), benchVaultSize)
		}
	}
}

// BenchmarkWarmIndexLoad is the case the cache exists for: every entry clean,
// so each note costs a stat and a parse and no read at all. The distance
// between this and BenchmarkDirectLoad is the whole benefit; the distance
// remaining above zero is mostly the parse the cache does not remove.
func BenchmarkWarmIndexLoad(b *testing.B) {
	v := syntheticVault(b, benchVaultSize)
	ix, _ := newTestIndex(b, v)
	if _, err := ix.Load(); err != nil {
		b.Fatalf("priming load: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		result, err := ix.Load()
		if err != nil {
			b.Fatalf("index load: %v", err)
		}
		if len(result.Notes) != benchVaultSize {
			b.Fatalf("loaded %d notes, want %d", len(result.Notes), benchVaultSize)
		}
		if result.Changed {
			b.Fatal("an untouched vault reported Changed, so this is not measuring a warm load")
		}
	}
}

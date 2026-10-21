package queryexpr

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/vault"
)

// relPaths renders an evaluation result as vault-relative slash paths.
func relPaths(t *testing.T, root string, notes []*vault.Note) []string {
	t.Helper()
	out := make([]string, 0, len(notes))
	for _, n := range notes {
		rel, err := filepath.Rel(root, n.Path)
		if err != nil {
			t.Fatalf("rel: %v", err)
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out
}

func TestMemoryEvaluator_Evaluate(t *testing.T) {
	root, notes, env := fixtureVault(t)
	eval := NewMemoryEvaluator(root, notes, env)

	cases := []struct {
		src   string
		limit int
		want  []string
	}{
		{src: "tag:beta", want: []string{"Archive/Old.md", "Beta.md", "Hub.md"}},
		{src: "", want: []string{"Alpha.md", "Archive/Old.md", "Beta.md", "Gamma.md", "Hub.md", "Solo.md"}},
		{src: "tag:beta", limit: 2, want: []string{"Archive/Old.md", "Beta.md"}},
		{src: "orphan:true", want: []string{"Archive/Old.md", "Hub.md", "Solo.md"}},
		{src: "status:none", want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			plan, err := Compile(tc.src, env)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			plan.Limit = tc.limit
			got, err := eval.Evaluate(context.Background(), plan)
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if strings.Join(relPaths(t, root, got), ",") != strings.Join(tc.want, ",") {
				t.Errorf("evaluate %q = %v, want %v", tc.src, relPaths(t, root, got), tc.want)
			}
		})
	}
}

// TestMemoryEvaluator_BuildsGraphLazily pins the point of NeedsGraph: a
// non-graph query must run against an evaluator that never built one, and a
// graph query must work without the caller supplying it.
func TestMemoryEvaluator_BuildsGraphLazily(t *testing.T) {
	root, notes, env := fixtureVault(t)
	env.Graph = nil
	eval := NewMemoryEvaluator(root, notes, env).(*memoryEvaluator)

	plain, err := Compile("tag:beta", env)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := eval.Evaluate(context.Background(), plain); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if eval.env.Graph != nil {
		t.Fatal("a query needing no graph built one anyway")
	}

	graphy, err := Compile("orphan:true", env)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := eval.Evaluate(context.Background(), graphy); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	built := eval.env.Graph
	if built == nil {
		t.Fatal("a query needing a graph did not build one")
	}
	if _, err := eval.Evaluate(context.Background(), graphy); err != nil {
		t.Fatalf("re-evaluate: %v", err)
	}
	if eval.env.Graph != built {
		t.Error("the graph was rebuilt on a second evaluation instead of being reused")
	}
}

func TestMemoryEvaluator_Sort(t *testing.T) {
	root, notes, env := fixtureVault(t)
	eval := NewMemoryEvaluator(root, notes, env)

	plan, err := Compile("tag:beta", env)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	plan.Sort = SortSpec{Key: "path", Descending: true}
	got, err := eval.Evaluate(context.Background(), plan)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	want := []string{"Hub.md", "Beta.md", "Archive/Old.md"}
	if strings.Join(relPaths(t, root, got), ",") != strings.Join(want, ",") {
		t.Errorf("descending order = %v, want %v", relPaths(t, root, got), want)
	}

	// Descending plus a limit takes the last notes by path, not the first.
	plan.Limit = 1
	got, err = eval.Evaluate(context.Background(), plan)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if paths := relPaths(t, root, got); len(paths) != 1 || paths[0] != "Hub.md" {
		t.Errorf("descending limit 1 = %v, want [Hub.md]", paths)
	}

	// An ordering this evaluator cannot honour is an error, never a silent
	// fallback to path order.
	plan.Sort = SortSpec{Key: "relevance", RelativeTo: "Hub"}
	if _, err := eval.Evaluate(context.Background(), plan); err == nil {
		t.Error("an unsupported sort key was silently ignored")
	}
}

func TestMemoryEvaluator_ContextCancellation(t *testing.T) {
	root, notes, env := fixtureVault(t)
	eval := NewMemoryEvaluator(root, notes, env)

	plan, err := Compile("", env)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := eval.Evaluate(ctx, plan); err != context.Canceled {
		t.Fatalf("evaluate on a cancelled context = %v, want context.Canceled", err)
	}
}

func TestMemoryEvaluator_NilPlan(t *testing.T) {
	root, notes, env := fixtureVault(t)
	eval := NewMemoryEvaluator(root, notes, env)
	if _, err := eval.Evaluate(context.Background(), nil); err == nil {
		t.Fatal("a nil plan was accepted")
	}
}

// TestNewMemoryEvaluator_DefaultsRoot pins that folder: works from an Env whose
// Root was left empty, because the evaluator already knows the root.
func TestNewMemoryEvaluator_DefaultsRoot(t *testing.T) {
	root, notes, env := fixtureVault(t)
	env.Root = ""
	eval := NewMemoryEvaluator(root, notes, env)

	plan, err := Compile("folder:Archive", env)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got, err := eval.Evaluate(context.Background(), plan)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if paths := relPaths(t, root, got); len(paths) != 1 || paths[0] != "Archive/Old.md" {
		t.Fatalf("folder:Archive = %v, want [Archive/Old.md]", paths)
	}
}

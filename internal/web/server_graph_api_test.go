package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestServer_GraphAPIJSON_Golden(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Source.md":     []byte("---\ntitle: Source\n---\n[[Target]]\n"),
		"Target.md":     []byte("---\ntitle: Target\n---\nTarget body\n"),
		"Loner.md":      []byte("---\ntitle: Loner\n---\nNo links\n"),
		"Work/Alpha.md": []byte("---\ntitle: Alpha\n---\nAlpha body\n"),
	})

	payload, _, contentType := fetchGraphJSON(t, ts.URL+"/api/graph.json")
	if !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("content-type: got %q, want application/json...", contentType)
	}

	got, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("marshal indent: %v", err)
	}
	got = append(got, '\n')

	want, err := os.ReadFile(filepath.Join("testdata", "graph_api.golden.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("graph json mismatch\n--- got ---\n%s\n--- want ---\n%s", string(got), string(want))
	}
}

func TestServer_GraphAPIJSON_DegreeMetadata(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, denseGraphVaultFixture())

	payload, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json")
	nodes := graphNodeDataByID(payload)

	hub := nodes["hub"]
	if hub.InDegree != 6 || hub.OutDegree != 5 || hub.Degree != 11 {
		t.Fatalf("hub degrees: got in=%d out=%d total=%d, want in=6 out=5 total=11", hub.InDegree, hub.OutDegree, hub.Degree)
	}

	island := nodes["island"]
	if island.InDegree != 0 || island.OutDegree != 0 || island.Degree != 0 {
		t.Fatalf("island degrees: got in=%d out=%d total=%d, want all zero", island.InDegree, island.OutDegree, island.Degree)
	}
}

func TestServer_GraphAPIJSON_DeterministicOrdering(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"C.md": []byte("---\ntitle: C\n---\nBody\n"),
		"A.md": []byte("---\ntitle: A\n---\n[[C]] [[B]] [[B]]\n"),
		"B.md": []byte("---\ntitle: B\n---\n[[C]]\n"),
	})

	first, raw1, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json")
	_, raw2, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json")
	_, raw3, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json")
	if raw1 != raw2 || raw2 != raw3 {
		t.Fatalf("graph api output is not deterministic across calls")
	}

	for i := 1; i < len(first.Nodes); i++ {
		prev := first.Nodes[i-1].Data.ID
		curr := first.Nodes[i].Data.ID
		if prev > curr {
			t.Fatalf("nodes not sorted: %q before %q", prev, curr)
		}
	}

	for i := 1; i < len(first.Edges); i++ {
		prev := first.Edges[i-1].Data.Source + "\x00" + first.Edges[i-1].Data.Target
		curr := first.Edges[i].Data.Source + "\x00" + first.Edges[i].Data.Target
		if prev > curr {
			t.Fatalf("edges not sorted: %#v before %#v", first.Edges[i-1].Data, first.Edges[i].Data)
		}
	}
}

func TestServer_GraphAPIJSON_SelfLoopExclusion(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Self.md":  []byte("---\ntitle: Self\n---\n[[Self]] [[Peer]]\n"),
		"Peer.md":  []byte("---\ntitle: Peer\n---\nBody\n"),
		"Other.md": []byte("---\ntitle: Other\n---\nBody\n"),
	})

	payload, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json")
	for _, edge := range payload.Edges {
		if edge.Data.Source == edge.Data.Target {
			t.Fatalf("unexpected self-loop edge: %#v", edge.Data)
		}
	}
}

func TestServer_GraphAPIJSON_QueryFiltersTitleBodyAndEdges(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"NeedleA.md":  []byte("---\ntitle: Needle A\n---\n[[NeedleB]] [[Other]]\n"),
		"NeedleB.md":  []byte("---\ntitle: Needle B\n---\nBody\n"),
		"BodyOnly.md": []byte("---\ntitle: Body Only\n---\ncontains nEeDlE token\n"),
		"Other.md":    []byte("---\ntitle: Other\n---\nBody\n"),
	})

	payload, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json?q=NEEDLE")
	if len(payload.Nodes) != 3 {
		t.Fatalf("node count: got %d, want %d", len(payload.Nodes), 3)
	}

	nodeIDs := make(map[string]struct{}, len(payload.Nodes))
	for _, node := range payload.Nodes {
		nodeIDs[node.Data.ID] = struct{}{}
	}

	for _, id := range []string{"needlea", "needleb", "bodyonly"} {
		if _, ok := nodeIDs[id]; !ok {
			t.Fatalf("expected node %q to be included; got %#v", id, payload.Nodes)
		}
	}
	if _, ok := nodeIDs["other"]; ok {
		t.Fatalf("expected non-matching node to be excluded; got %#v", payload.Nodes)
	}

	if len(payload.Edges) != 1 {
		t.Fatalf("edge count: got %d, want %d", len(payload.Edges), 1)
	}
	edge := payload.Edges[0].Data
	if edge.Source != "needlea" || edge.Target != "needleb" {
		t.Fatalf("unexpected edge: got %#v, want NeedleA->NeedleB only", edge)
	}
}

func TestServer_GraphAPIJSON_FiltersNodesEdgesAndFilteredDegrees(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Work/Alpha.md":    []byte("---\ntitle: Alpha\ntags: [keep]\nstatus: active\n---\n[[Beta]] [[Gamma]]\n"),
		"Work/Beta.md":     []byte("---\ntitle: Beta\ntags: [keep]\nstatus: active\n---\n[[Gamma]]\n"),
		"Work/Gamma.md":    []byte("---\ntitle: Gamma\ntags: [drop]\nstatus: active\n---\nBody\n"),
		"Archive/Delta.md": []byte("---\ntitle: Delta\ntags: [keep]\nstatus: active\n---\n[[Alpha]]\n"),
	})

	payload, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json?tag=keep&folder=Work")
	if !slices.Equal(graphNodeIDs(payload), []string{"work/alpha", "work/beta"}) {
		t.Fatalf("filtered node ids: got %#v, want work/alpha and work/beta", graphNodeIDs(payload))
	}
	if len(payload.Edges) != 1 {
		t.Fatalf("edge count: got %d, want 1", len(payload.Edges))
	}
	edge := payload.Edges[0].Data
	if edge.Source != "work/alpha" || edge.Target != "work/beta" {
		t.Fatalf("filtered edge: got %#v, want work/alpha->work/beta", edge)
	}

	nodes := graphNodeDataByID(payload)
	assertGraphNodeDegrees(t, "alpha", nodes["work/alpha"], [3]int{1, 2, 3}, [3]int{0, 1, 1})
	assertGraphNodeDegrees(t, "beta", nodes["work/beta"], [3]int{1, 1, 2}, [3]int{1, 0, 1})
}

// assertGraphNodeDegrees checks a node's full and filtered degrees, each given
// as {in, out, total}.
func assertGraphNodeDegrees(t *testing.T, label string, node graphJSONNodeData, full, filtered [3]int) {
	t.Helper()
	if got := [3]int{node.InDegree, node.OutDegree, node.Degree}; got != full {
		t.Fatalf("%s full degrees: got in=%d out=%d total=%d, want in=%d out=%d total=%d",
			label, got[0], got[1], got[2], full[0], full[1], full[2])
	}
	got := [3]int{node.FilteredInDegree, node.FilteredOutDegree, node.FilteredDegree}
	if got != filtered {
		t.Fatalf("%s filtered degrees: got in=%d out=%d total=%d, want in=%d out=%d total=%d",
			label, got[0], got[1], got[2], filtered[0], filtered[1], filtered[2])
	}
}

// graphIssueMetadata is the issue-flag subset of a graph node, compared whole.
type graphIssueMetadata struct {
	HasBroken   bool
	BrokenCount int
	IsOrphan    bool
	IsDeadEnd   bool
	OutDegree   int
}

func graphNodeIssueMetadata(node graphJSONNodeData) graphIssueMetadata {
	return graphIssueMetadata{
		HasBroken:   node.HasBroken,
		BrokenCount: node.BrokenCount,
		IsOrphan:    node.IsOrphan,
		IsDeadEnd:   node.IsDeadEnd,
		OutDegree:   node.OutDegree,
	}
}

// withoutOutDegree drops the out-degree for checks that do not constrain it.
func (m graphIssueMetadata) withoutOutDegree() graphIssueMetadata {
	m.OutDegree = 0
	return m
}

func TestServer_GraphAPIJSON_IssueMetadataAndBrokenFilter(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Broken.md": []byte("---\ntitle: Broken\n---\n[[Missing]]\n"),
		"Linked.md": []byte("---\ntitle: Linked\n---\n[[Broken]]\n"),
		"Island.md": []byte("---\ntitle: Island\ndead_end: false\n---\nNo links\n"),
	})

	payload, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json")
	nodes := graphNodeDataByID(payload)

	broken := nodes["broken"]
	wantBroken := graphIssueMetadata{HasBroken: true, BrokenCount: 1, IsOrphan: false, IsDeadEnd: true}
	if got := graphNodeIssueMetadata(broken); got.withoutOutDegree() != wantBroken {
		t.Fatalf("broken metadata: got %#v, want broken count 1, not orphan, dead-end", broken)
	}
	linked := nodes["linked"]
	wantLinked := graphIssueMetadata{HasBroken: false, BrokenCount: 0, IsOrphan: true, IsDeadEnd: false}
	if got := graphNodeIssueMetadata(linked); got.withoutOutDegree() != wantLinked {
		t.Fatalf("linked metadata: got %#v, want clean orphan with outgoing link", linked)
	}
	island := nodes["island"]
	wantIsland := graphIssueMetadata{HasBroken: false, BrokenCount: 0, IsOrphan: true, IsDeadEnd: false, OutDegree: 0}
	if graphNodeIssueMetadata(island) != wantIsland {
		t.Fatalf("island metadata: got %#v, want clean orphan with out-degree zero but exempt from dead-end", island)
	}

	brokenOnly, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json?broken=1")
	if !slices.Equal(graphNodeIDs(brokenOnly), []string{"broken"}) {
		t.Fatalf("broken filter node ids: got %#v, want broken", graphNodeIDs(brokenOnly))
	}
	if len(brokenOnly.Edges) != 0 {
		t.Fatalf("broken filter edges: got %#v, want none", brokenOnly.Edges)
	}
}

func TestServer_GraphAPIJSON_InvalidFilterParams(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Note.md": []byte("---\ntitle: Note\n---\nBody\n"),
	})

	resp, err := http.Get(ts.URL + "/api/graph.json?broken=maybe") // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /api/graph.json: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestServer_GraphAPIJSON_QueryKeepsFullGraphDegreeMetadata(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Target.md":  []byte("---\ntitle: Target\n---\nrareword body [[Other]]\n"),
		"SourceA.md": []byte("---\ntitle: Source A\n---\n[[Target]]\n"),
		"SourceB.md": []byte("---\ntitle: Source B\n---\n[[Target]]\n"),
		"Other.md":   []byte("---\ntitle: Other\n---\nBody\n"),
	})

	payload, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json?q=rareword")
	if len(payload.Nodes) != 1 {
		t.Fatalf("node count: got %d, want 1", len(payload.Nodes))
	}
	if len(payload.Edges) != 0 {
		t.Fatalf("edge count: got %d, want 0", len(payload.Edges))
	}

	target := payload.Nodes[0].Data
	if target.ID != "target" {
		t.Fatalf("node id: got %q, want target", target.ID)
	}
	if target.InDegree != 2 || target.OutDegree != 1 || target.Degree != 3 {
		t.Fatalf("target degrees: got in=%d out=%d total=%d, want in=2 out=1 total=3", target.InDegree, target.OutDegree, target.Degree)
	}
}

func TestServer_GraphAPIJSON_QueryIsUncapped(t *testing.T) {
	files := make(map[string][]byte, 60)
	for i := 0; i < 60; i++ {
		rel := fmt.Sprintf("Graph%02d.md", i)
		files[rel] = []byte(fmt.Sprintf("---\ntitle: Graph %02d\n---\nneedle body text\n", i))
	}
	ts, _ := newHTTPServerForFiles(t, files)

	payload, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json?q=needle")
	if len(payload.Nodes) != 60 {
		t.Fatalf("node count: got %d, want %d", len(payload.Nodes), 60)
	}
}

func TestServer_GraphAPIJSON_EmptyQueryMatchesDefaultGraph(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Source.md": []byte("---\ntitle: Source\n---\n[[Target]]\n"),
		"Target.md": []byte("---\ntitle: Target\n---\nBody\n"),
		"Other.md":  []byte("---\ntitle: Other\n---\nBody\n"),
	})

	_, rawDefault, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json")
	_, rawEmpty, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json?q=")
	_, rawWhitespace, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json?q="+url.QueryEscape("  "))
	if rawDefault != rawEmpty || rawDefault != rawWhitespace {
		t.Fatalf("empty graph query should match default output")
	}
}

func TestServer_GraphAPIJSON_FocusModeKeepsFullGraphAndFlagsMatches(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"NeedleA.md":  []byte("---\ntitle: Needle A\n---\n[[NeedleB]] [[Other]]\n"),
		"NeedleB.md":  []byte("---\ntitle: Needle B\n---\nBody\n"),
		"BodyOnly.md": []byte("---\ntitle: Body Only\n---\ncontains nEeDlE token\n"),
		"Other.md":    []byte("---\ntitle: Other\n---\nBody\n"),
	})

	unfiltered, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json")
	payload, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json?q=NEEDLE&q_mode=focus")

	if !slices.Equal(graphNodeIDs(payload), graphNodeIDs(unfiltered)) {
		t.Fatalf("focus mode node ids: got %#v, want the unfiltered set %#v", graphNodeIDs(payload), graphNodeIDs(unfiltered))
	}
	if len(payload.Edges) != len(unfiltered.Edges) {
		t.Fatalf("focus mode edge count: got %d, want %d", len(payload.Edges), len(unfiltered.Edges))
	}

	nodes := graphNodeDataByID(payload)
	for id, want := range map[string]bool{
		"needlea":  true,
		"needleb":  true,
		"bodyonly": true,
		"other":    false,
	} {
		if nodes[id].MatchesQuery != want {
			t.Fatalf("matchesQuery for %q: got %t, want %t", id, nodes[id].MatchesQuery, want)
		}
	}
}

func TestServer_GraphAPIJSON_FocusModeStillAppliesFacets(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Work/Alpha.md":    []byte("---\ntitle: Alpha\ntags: [keep]\n---\nneedle body\n"),
		"Work/Beta.md":     []byte("---\ntitle: Beta\ntags: [keep]\n---\nplain body\n"),
		"Archive/Delta.md": []byte("---\ntitle: Delta\ntags: [drop]\n---\nneedle body\n"),
	})

	payload, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json?q=needle&q_mode=focus&tag=keep")
	if !slices.Equal(graphNodeIDs(payload), []string{"work/alpha", "work/beta"}) {
		t.Fatalf("focus mode with facet node ids: got %#v, want work/alpha and work/beta", graphNodeIDs(payload))
	}

	nodes := graphNodeDataByID(payload)
	if !nodes["work/alpha"].MatchesQuery {
		t.Fatalf("expected work/alpha to match the query, got %#v", nodes["work/alpha"])
	}
	if nodes["work/beta"].MatchesQuery {
		t.Fatalf("expected work/beta not to match the query, got %#v", nodes["work/beta"])
	}
}

func TestServer_GraphAPIJSON_FilterModeMarksIncludedNodesAsMatches(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"NeedleA.md": []byte("---\ntitle: Needle A\n---\nBody\n"),
		"Other.md":   []byte("---\ntitle: Other\n---\nBody\n"),
	})

	withQuery, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json?q=needle")
	nodes := graphNodeDataByID(withQuery)
	if !nodes["needlea"].MatchesQuery {
		t.Fatalf("filter mode should flag the included node as a match, got %#v", nodes["needlea"])
	}

	noQuery, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json")
	for _, node := range noQuery.Nodes {
		if node.Data.MatchesQuery {
			t.Fatalf("no query should leave matchesQuery false, got %#v", node.Data)
		}
	}
}

func TestServer_GraphAPIJSON_UnknownQueryModeFallsBackToFilter(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"NeedleA.md": []byte("---\ntitle: Needle A\n---\nBody\n"),
		"Other.md":   []byte("---\ntitle: Other\n---\nBody\n"),
	})

	_, rawFilter, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json?q=needle")
	_, rawUnknown, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json?q=needle&q_mode=wat")
	if rawFilter != rawUnknown {
		t.Fatalf("unknown q_mode should behave like filter mode")
	}
}

func TestServer_GraphAPIJSON_ExprNarrowsTheIncludedSet(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Work/Alpha.md":    []byte("---\ntitle: Alpha\ntags: [keep]\nstatus: wip\n---\n[[Beta]]\n"),
		"Work/Beta.md":     []byte("---\ntitle: Beta\ntags: [keep]\nstatus: done\n---\nBody\n"),
		"Archive/Gamma.md": []byte("---\ntitle: Gamma\ntags: [drop]\nstatus: wip\n---\nBody\n"),
	})

	exprOnly, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json?expr="+url.QueryEscape("status:wip"))
	if !slices.Equal(graphNodeIDs(exprOnly), []string{"archive/gamma", "work/alpha"}) {
		t.Fatalf("expr-only node ids: got %#v, want archive/gamma and work/alpha", graphNodeIDs(exprOnly))
	}

	withFacet, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json?tag=keep&expr="+url.QueryEscape("status:wip"))
	if !slices.Equal(graphNodeIDs(withFacet), []string{"work/alpha"}) {
		t.Fatalf("expr must compose with the facet chip: got %#v, want work/alpha", graphNodeIDs(withFacet))
	}
}

func TestServer_GraphAPIJSON_FocusModeKeepsExprAsAStructuralFilter(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Work/Alpha.md":    []byte("---\ntitle: Alpha\nstatus: wip\n---\nneedle body\n"),
		"Work/Beta.md":     []byte("---\ntitle: Beta\nstatus: wip\n---\nplain body\n"),
		"Archive/Gamma.md": []byte("---\ntitle: Gamma\nstatus: done\n---\nneedle body\n"),
	})

	payload, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json?q=needle&q_mode=focus&expr="+url.QueryEscape("status:wip"))
	if !slices.Equal(graphNodeIDs(payload), []string{"work/alpha", "work/beta"}) {
		t.Fatalf("focus mode must still drop nodes the expression excludes: got %#v", graphNodeIDs(payload))
	}

	nodes := graphNodeDataByID(payload)
	if !nodes["work/alpha"].MatchesQuery {
		t.Fatalf("expected work/alpha to match the free-text query, got %#v", nodes["work/alpha"])
	}
	if nodes["work/beta"].MatchesQuery {
		t.Fatalf("expected work/beta to be dimmed rather than removed, got %#v", nodes["work/beta"])
	}
}

func TestServer_GraphAPIJSON_InvalidExprReturnsStructuredError(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, map[string][]byte{
		"Note.md": []byte("---\ntitle: Note\n---\nBody\n"),
	})

	resp, err := http.Get(ts.URL + "/api/graph.json?expr=" + url.QueryEscape("statuz:wip")) // #nosec G107 -- test-only local URL
	if err != nil {
		t.Fatalf("GET /api/graph.json: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	var payload filterErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode error payload: %v", err)
	}
	if payload.Error.Param != "expr" || payload.Error.Position == nil {
		t.Fatalf("graph filter error payload: %#v", payload.Error)
	}
}

func TestServer_GraphAPIJSON_ExprResolvesGraphFields(t *testing.T) {
	ts, _ := newHTTPServerForFiles(t, denseGraphVaultFixture())

	payload, _, _ := fetchGraphJSON(t, ts.URL+"/api/graph.json?expr="+url.QueryEscape("orphan:true"))
	// Leaf links out to Hub but nothing links back, so it is an orphan too.
	if !slices.Equal(graphNodeIDs(payload), []string{"island", "leaf"}) {
		t.Fatalf("graph-reading expression: got %#v, want island and leaf", graphNodeIDs(payload))
	}
}

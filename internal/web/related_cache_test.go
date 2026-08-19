package web

import (
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
)

func TestServer_RelatedAPI_CachesCorpusUntilNoteReload(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "Source.md", "---\ntitle: Source\n---\nshared words\n")
	writeTestFile(t, root, "Candidate.md", "---\ntitle: Candidate\n---\nshared words\n")
	cfg, vault := testVaultOpen(t, root, "127.0.0.1:0")
	srv, err := NewServer(cfg, vault, Options{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	now := time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)
	srv.state.now = func() time.Time { return now }
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	getRelatedResponse(t, ts.URL, url.Values{"source": {"source"}})
	first := currentRelatedCorpus(t, srv)
	getRelatedResponse(t, ts.URL, url.Values{"source": {"source"}, "mode": {"connect"}})
	second := currentRelatedCorpus(t, srv)
	if first != second {
		t.Fatalf("related corpus rebuilt for repeated requests against an unchanged snapshot")
	}

	writeTestFile(t, root, "Added.md", "---\ntitle: Added\n---\nshared words\n")
	now = now.Add(2 * time.Second)
	getRelatedResponse(t, ts.URL, url.Values{"source": {"source"}})
	reloaded := currentRelatedCorpus(t, srv)
	if reloaded == first {
		t.Fatalf("related corpus reused across a note reload")
	}
	if len(reloaded.documents) != 3 || len(reloaded.notePaths) != 3 {
		t.Fatalf("reloaded corpus documents/paths = %d/%d, want 3/3", len(reloaded.documents), len(reloaded.notePaths))
	}
}

func TestServer_RelatedAPI_RebuildsCorpusOnConfigurationReload(t *testing.T) {
	const withoutMetadata = "[related]\nmetadata_properties = []\n"
	const withMetadata = "[related]\nmetadata_properties = [\"topic\"]\n"
	ts, srv, root := newReloadableServer(t, withoutMetadata, Options{})
	writeTestFile(t, root, "Candidate.md", "---\ntitle: Candidate\ntopic: shared\n---\nBody marker.\n")

	getRelatedResponse(t, ts.URL, url.Values{"source": {"source"}})
	first := currentRelatedCorpus(t, srv)
	if candidate := relatedCachedDocument(t, first, "Candidate.md"); len(candidate.Metadata) != 0 {
		t.Fatalf("initial candidate metadata = %#v, want none", candidate.Metadata)
	}

	rewriteConfig(t, root, withMetadata)
	getRelatedResponse(t, ts.URL, url.Values{"source": {"source"}})
	reloaded := currentRelatedCorpus(t, srv)
	if reloaded == first {
		t.Fatalf("related corpus reused across a configuration generation")
	}
	if candidate := relatedCachedDocument(t, reloaded, "Candidate.md"); len(candidate.Metadata) != 1 {
		t.Fatalf("reloaded candidate metadata = %#v, want configured topic", candidate.Metadata)
	}
}

func currentRelatedCorpus(t *testing.T, srv *Server) *relatedCorpus {
	t.Helper()
	srv.state.mu.Lock()
	defer srv.state.mu.Unlock()
	if srv.state.relatedCorpus == nil {
		t.Fatal("related corpus was not published")
	}
	return srv.state.relatedCorpus
}

func relatedCachedDocument(t *testing.T, corpus *relatedCorpus, path string) recall.Document {
	t.Helper()
	document, ok := documentByPath(corpus.documents, path)
	if !ok {
		t.Fatalf("cached document %q not found", path)
	}
	return document
}

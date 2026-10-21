package cli

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/brainnotes/internal/query"
	"github.com/sglavoie/brainnotes/internal/recall"
	"github.com/sglavoie/brainnotes/internal/textnorm"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// recallNow is the clock every recall-reading command pins itself to. It is
// shared by ranking, state writes, and feedback date parsing. Tests replace it
// to pin local-calendar and recency behavior — which is what makes
// `bn find --sort relevance` reproducible byte for byte.
var recallNow = time.Now

// recallSetup is the per-command recall context: one instant, read once, and a
// state store bound to it.
//
// Reading the clock once per command run and handing it out as a closure is the
// invariant this type exists to hold. Ranking recency, snooze expiry, and any
// write all have to agree on "now" no matter how long the command takes, and a
// command that called time.Now twice could rank against one instant and record
// against another. `bn related` established the pattern; every recall-reading
// command now inherits it from here.
type recallSetup struct {
	now   time.Time
	store *recall.StateStore
}

// newRecallSetup pins the clock and opens the vault's recall sidecar for
// reading. Validate surfaces a corrupt recall.json up front, before a query has
// asked for a single pair signal, and it never creates or changes a file: the
// sidecar is created lazily by the first explicit write.
func newRecallSetup(root string, now time.Time) (*recallSetup, error) {
	store := recall.NewStateStore(root, recall.WithClock(func() time.Time { return now }))
	if err := store.Validate(); err != nil {
		return nil, err
	}
	return &recallSetup{now: now, store: store}, nil
}

// signals exposes the state store as the read-only ranking interface.
//
// A command that only ranks takes this rather than the store itself, so the
// sidecar's write methods — RecordOpened above all — are not reachable through
// the value it holds. That is how `bn find` stays on the read side of the
// recall read/write boundary (docs/internals/recall-context.md): it never
// records anything, and there is no method on a RecallSignalSource with which
// it could.
func (s *recallSetup) signals() recall.RecallSignalSource {
	if s == nil {
		return nil
	}
	return s.store
}

// rankOptions binds the pinned clock and the read-only signal source into the
// options every ranker in this package is built with.
func (s *recallSetup) rankOptions() recall.RankOptions {
	if s == nil {
		return recall.RankOptions{}
	}
	now := s.now
	return recall.RankOptions{
		Now:    func() time.Time { return now },
		Recall: s.signals(),
	}
}

// recallCorpus is one command run's view of the vault as a recall corpus: the
// extracted documents, the link graph they were ranked against, and the ranker
// built over both.
//
// It is the single corpus builder `bn related`, `bn context`, and
// `bn find --sort relevance` share. Keeping one builder is what makes
// relevance ordering in `find` mean exactly what a `related` ranking means,
// rather than a second scoring path that could drift.
type recallCorpus struct {
	documents []recall.Document
	graph     *query.Graph
	ranker    *recall.Ranker
}

// recallExtractOptions reads the configured metadata properties off the vault.
// The slice is copied because ExtractOptions is retained by the extractor.
func recallExtractOptions(v *vault.Vault) recall.ExtractOptions {
	return recall.ExtractOptions{
		MetadataProperties: append([]string(nil), v.Config().Related.MetadataProperties...),
	}
}

// buildRecallCorpus extracts the recall corpus over notes and builds a ranker
// with rankOptions bound.
//
// g is the caller's already-built link graph, or nil to build one here. A
// caller that already paid for a graph (as `bn find` does whenever its filter
// reads one) passes it in rather than paying twice.
func buildRecallCorpus(
	v *vault.Vault,
	notes []*vault.Note,
	g *query.Graph,
	rankOptions recall.RankOptions,
) (*recallCorpus, error) {
	documents, err := recall.ExtractCorpus(v.Root, notes, recallExtractOptions(v))
	if err != nil {
		return nil, err
	}
	if g == nil {
		g = query.BuildGraphForRoot(v.Root, notes)
	}
	ranker, err := recall.BuildRanker(documents, g, rankOptions)
	if err != nil {
		return nil, err
	}
	return &recallCorpus{documents: documents, graph: g, ranker: ranker}, nil
}

// recallPath renders a note's absolute path as the vault-relative, NFC
// normalized, slash-separated key recall.Document uses.
//
// recall derives the same key internally from the same three steps. The two are
// kept in step by Ranker.RankSelected itself, which rejects a candidate path it
// has not indexed: a drift here is a loud error rather than a silently missing
// result.
func recallPath(root, notePath string) (string, bool) {
	rel, err := filepath.Rel(root, notePath)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return textnorm.NFC(rel), true
}

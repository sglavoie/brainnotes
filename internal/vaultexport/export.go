package vaultexport

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// defaultMaxPackages bounds how many packages one call publishes, so a vault
// with a long unexported history makes progress in bounded passes rather than
// holding the producer guard for the length of its whole history. The cursor is
// durable after every package, so the next call resumes exactly where this one
// stopped.
const defaultMaxPackages = 64

// defaultMaxBaselineBytes bounds one baseline package. A baseline is the only
// package built from something other than a single commit, so it is the only one
// whose size this producer chooses; every incremental package is exactly as large
// as the commit it describes.
const defaultMaxBaselineBytes int64 = 32 << 20

// Options configure one export pass.
type Options struct {
	// Root is the vault root. Required.
	Root string
	// ChannelDir overrides where packages are published. It defaults to this
	// vault's outgoing channel, and a caller should leave it alone outside
	// tests: a device writes into exactly one channel.
	ChannelDir string
	// GitExecutable overrides the git found on PATH.
	GitExecutable string
	// Now is the clock seam, used only for the manifest's descriptive
	// CreatedAt. Nothing here orders anything by time.
	Now func() time.Time
	// MaxPackages bounds how many packages this pass publishes. Zero means
	// defaultMaxPackages.
	MaxPackages int
	// MaxBaselineBytes bounds one baseline package's payload bytes. Zero means
	// defaultMaxBaselineBytes. A single file larger than the budget is still
	// sent, alone: a vault must not become unexportable because one attachment
	// is big.
	MaxBaselineBytes int64
	// AuthorizeDeletions is retained for source compatibility.
	// Deprecated: committed regular-file deletions are exported automatically.
	AuthorizeDeletions []string
}

// SkipReason says why a commit produced no package.
type SkipReason string

const (
	// SkipImported is a commit that applied a peer's operation. Publishing it
	// would send the peer its own change back.
	SkipImported SkipReason = "imported"
	// SkipPrivate is a commit that changed nothing portable — a sidecar state
	// file, a journal entry, this vault's own machine-local configuration.
	SkipPrivate SkipReason = "private"
)

// Skipped is one commit the cursor advanced past without publishing.
type Skipped struct {
	Commit string
	Reason SkipReason
}

// Published is one package this pass put into the channel.
type Published struct {
	// PackageID is the identity it was published under.
	PackageID string
	// Dir is the package directory in the channel.
	Dir string
	// Sequence is its position in this device's stream.
	Sequence uint64
	// Commit is the commit it describes. For a baseline package it is the
	// commit the baseline is pinned to, and several packages share it.
	Commit string
	// Changes is how many paths it carries.
	Changes int
}

// Result reports one pass.
type Result struct {
	// Baseline is whether this pass was describing a first export rather than
	// replaying commits.
	Baseline bool
	// Packages are what was published, in publication order.
	Packages []Published
	// Skipped are the commits the cursor advanced past without publishing.
	Skipped []Skipped
	// Cursor is the commit the export state now names, empty while a baseline
	// is still unfinished. It is reported when the pass halts too, and it is the
	// cursor as of the halt rather than as of the last full pass: the commits
	// before the refused one were published and recorded durably, and a caller
	// told only that the pass failed would have no way to learn how far it got.
	Cursor string
	// PendingCommits is how many commits this pass left unexported, which is
	// non-zero when MaxPackages bound the pass.
	PendingCommits int
	// PendingPaths is how many baseline paths are still to be sent.
	PendingPaths int
	// BurnedSequences are sequence numbers spent on a publication that was
	// interrupted before its manifest landed. The package directories they name
	// are inert — a peer reads one as an incomplete package forever — and the
	// commits they described are re-exported under fresh identities. They are
	// reported rather than swallowed because they leave a visible gap in this
	// device's stream and an operator deserves to know where it came from.
	BurnedSequences []uint64
}

// Export publishes as much of the vault's committed history as it can into the
// outgoing channel, advancing a durable cursor as it goes.
//
// It is safe to call repeatedly and safe to interrupt. Every package is followed
// by a durable state write, and every state write is preceded by a reservation,
// so a pass that dies at any point leaves the next one able to tell what
// happened: the sequence was never spent, or it was spent on a package that
// landed, or it was spent on one that did not.
func Export(ctx context.Context, opts Options) (*Result, error) {
	root, err := filepath.Abs(strings.TrimSpace(opts.Root))
	if err != nil || strings.TrimSpace(opts.Root) == "" {
		return nil, fmt.Errorf("vaultexport: vault root is required")
	}
	local, err := vaultsync.Load(root)
	if err != nil {
		return nil, err
	}
	history, err := vaultgit.OpenHistory(ctx, root, vaultgit.HistoryOptions{GitExecutable: opts.GitExecutable})
	if err != nil {
		return nil, err
	}

	// One producer guard for the whole pass, and the record's own lock only per
	// write. The cursor, the sequence, and the reservation are read, decided, and
	// written together, and a second exporter that interleaved with this one
	// would publish two packages under one sequence — but an import recording
	// provenance must not wait behind a whole pass. See lockProducer.
	barrier, err := joinBarrier(root)
	if err != nil {
		return nil, err
	}
	defer barrier()
	release, err := lockProducer(root)
	if err != nil {
		return nil, err
	}
	defer release()

	exp, err := newExporter(root, local, history, opts)
	if err != nil {
		return nil, err
	}
	// The result comes back even when the pass halts, and a caller must read it
	// either way. Everything in it is already durable by the time it is reported —
	// a published package, an advanced cursor, a burned sequence — so discarding it
	// on error would not undo any of them, it would only make them unreportable.
	// BurnedSequences is the one that cannot be recovered afterwards: the gap it
	// names is visible in this device's stream forever and nothing else records
	// where it came from.
	runErr := exp.run(ctx)
	return exp.result, runErr
}

// exporter is one pass. It exists so the state, the channel, and the budget are
// carried rather than threaded through every function, and so every state
// transition goes through commitState.
type exporter struct {
	root        string
	local       *vaultsync.Enrollment
	history     *vaultgit.History
	channelDir  string
	now         func() time.Time
	maxPackages int
	maxBytes    int64

	state  *State
	result *Result
	// consumed are the import ledger entries this pass has deleted. The ledger
	// has a second writer that holds no producer guard — RecordImport — so every
	// write merges what is on the disk back in, and this is what keeps that merge
	// from resurrecting an entry the pass already used. See commitState.
	consumed map[string]struct{}
}

func newExporter(root string, local *vaultsync.Enrollment, history *vaultgit.History, opts Options) (*exporter, error) {
	state, err := loadState(root, local)
	if err != nil {
		return nil, err
	}
	exp := &exporter{
		root:        root,
		local:       local,
		history:     history,
		channelDir:  strings.TrimSpace(opts.ChannelDir),
		now:         opts.Now,
		maxPackages: opts.MaxPackages,
		maxBytes:    opts.MaxBaselineBytes,
		state:       state,
		result:      &Result{},
		consumed:    map[string]struct{}{},
	}
	if exp.channelDir == "" {
		exp.channelDir = vaultsync.OutgoingDir(root)
	}
	if exp.now == nil {
		exp.now = time.Now
	}
	if exp.maxPackages <= 0 {
		exp.maxPackages = defaultMaxPackages
	}
	if exp.maxBytes <= 0 {
		exp.maxBytes = defaultMaxBaselineBytes
	}
	return exp, nil
}

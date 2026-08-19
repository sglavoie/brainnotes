package noteops

import (
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// NoteFrontmatterWrite is one note's share of a multi-note frontmatter write.
type NoteFrontmatterWrite struct {
	// RelPath is the note path, vault-relative or absolute.
	RelPath string
	// Mutations are applied to that note in order.
	Mutations []FrontmatterMutation
}

// SetFrontmatterAtomic applies each write's mutations to its own note and
// commits every changed note through a single transaction, so an ordering that
// spans notes cannot half-apply. The transaction validates every precondition
// and stages every replacement before touching a single destination, and takes
// one exclusive lock per path in sorted order, so a concurrent edit to any note
// in the set fails the whole call with nothing written.
//
// The set has no size limit. A set too large to hold one lock descriptor per
// path escalates to a single exclusive vault-wide lock, which is what lets an
// ordering that spans a whole list — a pin renormalize rewrites every note in
// it — commit as one undoable action however long the list is.
//
// Atomicity is still per-action, not per-process: a rename cannot be un-renamed
// by the kernel, so rollback is compensating rather than transactional. A path
// another writer changed after this transaction applied it is never compensated,
// since replaying the prior bytes would destroy that edit. When a compensating
// write fails or is refused that way, the call returns a *TxnRollbackError
// naming exactly which paths were left mixed.
//
// Notes whose mutations net to no change are skipped entirely, and a call in
// which no note changes writes nothing and journals nothing. Every changed note
// lands in one journal entry, so `bn undo` reverts the whole set together.
//
// BulkSetFrontmatter is the best-effort counterpart: it applies each note in its
// own transaction and reports per-note failures. Use it when partial progress is
// acceptable, and this when it is not.
func SetFrontmatterAtomic(vaultRoot string, writes []NoteFrontmatterWrite, fm *config.FrontmatterConfig, opts ...FrontmatterOption) (*FrontmatterResult, error) {
	options := defaultFrontmatterOptions("bn web frontmatter set atomic")
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	if len(writes) == 0 {
		return nil, Errorf(CodeInvalidRequest, "frontmatter: no notes selected")
	}
	sources := make([]PathResolution, 0, len(writes))
	planned := make([]plannedFrontmatterWrite, 0, len(writes))
	seen := make(map[string]bool, len(writes))
	for _, write := range writes {
		if strings.TrimSpace(write.RelPath) == "" {
			return nil, Errorf(CodeInvalidRequest, "frontmatter: empty note path")
		}
		source, err := resolveFrontmatterSource(vaultRoot, write.RelPath)
		if err != nil {
			return nil, err
		}
		if seen[source.AbsPath] {
			return nil, Errorf(CodeInvalidRequest, "frontmatter: %s appears twice in one atomic write", source.RelPath)
		}
		seen[source.AbsPath] = true
		sources = append(sources, source)

		change, err := planFrontmatterWrite(source.AbsPath, write.Mutations, fm)
		if err != nil {
			return nil, err
		}
		if change != nil {
			planned = append(planned, *change)
		}
	}

	if len(planned) == 0 {
		return &FrontmatterResult{Sources: sources, Changed: false}, nil
	}
	if options.beforeAtomicCommit != nil {
		if err := options.beforeAtomicCommit(); err != nil {
			return nil, err
		}
	}

	j := NewJournal(vaultRoot, options.command, options.journalOptions...)
	if err := commitFrontmatterWrites(vaultRoot, planned, j, options.requires, options.lockEscalation); err != nil {
		return nil, err
	}
	entry, err := j.Finalize()
	if err != nil {
		return nil, err
	}
	return &FrontmatterResult{
		Sources: sources,
		Journal: entry,
		Changed: true,
	}, nil
}

// plannedFrontmatterWrite is one note's serialized replacement plus the revision
// it was planned against.
type plannedFrontmatterWrite struct {
	path   string
	data   []byte
	expect vault.FileExpectation
}

// planFrontmatterWrite loads and mutates one note in memory, returning nil when
// the mutations net to no change.
func planFrontmatterWrite(path string, mutations []FrontmatterMutation, fm *config.FrontmatterConfig) (*plannedFrontmatterWrite, error) {
	note, front, err := loadFrontmatterNote(path)
	if err != nil {
		return nil, err
	}
	before := snapshotFrontmatterKeys(front, mutations)
	changed, err := ApplyFrontmatterMutationsToMap(front, mutations, fm)
	if err != nil {
		return nil, err
	}
	if !changed || !frontmatterMutationsNetChanged(front, before, mutations) {
		return nil, nil
	}
	data, err := note.Marshal(fm)
	if err != nil {
		return nil, WrapError(CodeInternal, "frontmatter: marshal note", err)
	}
	return &plannedFrontmatterWrite{
		path:   note.Path,
		data:   data,
		expect: vault.ExpectHash(note.SourceHash()),
	}, nil
}

func commitFrontmatterWrites(vaultRoot string, planned []plannedFrontmatterWrite, journaler captureJournal, requires []fileRequirement, lockEscalation int) error {
	txn, err := vault.NewFileTxn(vaultRoot)
	if err != nil {
		return WrapError(CodeInternal, "frontmatter: open transaction", err)
	}
	txn.SetLockEscalationThresholdForTest(lockEscalation)
	for _, write := range planned {
		if err := txn.Write(write.path, write.data, 0, write.expect); err != nil {
			return classifyTxnError("frontmatter: plan write", err)
		}
	}
	for _, requirement := range requires {
		if err := txn.Require(requirement.path, requirement.expect); err != nil {
			return classifyTxnError("frontmatter: pin dependency", err)
		}
	}
	var txnJournal vault.TxnJournal
	if journaler != nil {
		txnJournal = journaler
	}
	if err := txn.Commit(txnJournal); err != nil {
		return classifyTxnError("frontmatter: save notes", err)
	}
	return nil
}

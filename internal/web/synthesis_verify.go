package web

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/recall"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// revalidateSynthesisContext refuses a packet whose world moved on. It forces a
// fresh load so a note renamed, moved, or removed since the packet was built is
// seen immediately instead of up to one snapshot TTL later.
func (s *Server) revalidateSynthesisContext(record synthesisContextRecord) error {
	snapshot, err := s.state.refreshSnapshot()
	if err != nil {
		return noteops.WrapError(noteops.CodeInternal, "synthesis: load snapshot", err)
	}
	return s.verifySynthesisContext(snapshot, record)
}

// verifySynthesisContext reports whether every note the packet describes still
// resolves to the same vault path holding the same bytes, and whether
// brainnotes.toml is untouched. Resolving through the snapshot is what catches a
// move, a deletion, or a note that has since been quarantined; the hash check is
// what catches an in-place edit.
func (s *Server) verifySynthesisContext(snapshot vaultSnapshot, record synthesisContextRecord) error {
	digest, err := s.synthesisConfigDigest()
	if err != nil {
		return err
	}
	if digest != record.ConfigDigest {
		return noteops.Errorf(noteops.CodeConflict, "synthesis: this vault's configuration changed since the context was built")
	}
	for _, identity := range record.notes() {
		note, _, ok := noteForRoute(snapshot, identity.Key)
		if !ok || noteVaultPath(s.vault.Root, note) != identity.RelPath {
			return noteops.Errorf(noteops.CodeConflict,
				"synthesis: %s moved or is no longer available since the context was built", identity.RelPath)
		}
		absPath := filepath.Join(s.vault.Root, filepath.FromSlash(identity.RelPath))
		if err := vault.VerifyState(absPath, vault.ExpectHash(identity.SHA256)); err != nil {
			return noteops.Errorf(noteops.CodeConflict, "synthesis: %s changed since the context was built", identity.RelPath)
		}
	}
	return nil
}

// synthesisConfigDigest fingerprints the whole configuration as it is on disk.
// It reads the files instead of reusing the published ConfigStatus hashes
// because an edit that fails to validate leaves the last valid configuration —
// and its digests — published, and a packet built before that edit still has to
// be reported as stale. A server with no configuration file on disk digests to
// the empty string, which compares equal to itself and therefore never reports
// staleness.
//
// It covers both layers. brainnotes.local.toml names the agent this vault runs
// and the timeout it runs under, so a change to it changes what a generation
// does with brainnotes.toml untouched.
func (s *Server) synthesisConfigDigest() (string, error) {
	digest, _, err := s.synthesisConfigState()
	return digest, err
}

// synthesisConfigState reads both layers once and reports the two forms a
// synthesis write needs: the composite digest a freshness check compares, and
// the per-file requirements a transaction can pin. A composite digest is not a
// file state, so it can only ever answer the first question — pinning it as one
// would expect a hash no file on disk holds.
//
// The paths are vault-relative because that is the form a SynthesisRequirement
// carries; noteops resolves and contains them. An absent overlay is required to
// stay absent, so one created between the preview and the commit is a conflict.
func (s *Server) synthesisConfigState() (string, []noteops.SynthesisRequirement, error) {
	root := strings.TrimSpace(s.configState.root)
	if root == "" {
		return "", nil, nil
	}
	sources, err := config.ReadSources(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil, nil
		}
		return "", nil, noteops.WrapError(noteops.CodeInternal, "synthesis: read configuration", err)
	}
	fingerprint := sources.Fingerprint()
	local := configLayerPin{Hash: fingerprint.Local, Present: fingerprint.LocalPresent}
	return fingerprint.Composite, []noteops.SynthesisRequirement{
		{Path: config.FileName, Expect: vault.ExpectHash(fingerprint.Portable)},
		{Path: config.LocalFileName, Expect: local.expectation()},
	}, nil
}

// synthesisBundleError maps the curated builder's sentinels onto the shared
// management codes. A selection problem is the caller's to fix; a byte overflow
// is a property of the notes, so it is a conflict the user resolves by choosing
// fewer or smaller notes.
func synthesisBundleError(err error) error {
	switch {
	case errors.Is(err, recall.ErrCuratedSupporterLimit),
		errors.Is(err, recall.ErrCuratedAnchorSupporter),
		errors.Is(err, recall.ErrCuratedDuplicateSupporter):
		return noteops.WrapError(noteops.CodeInvalidRequest, "synthesis: invalid note selection", err)
	case errors.Is(err, recall.ErrCuratedSupporterMissing):
		return noteops.WrapError(noteops.CodeNotFound, "synthesis: a selected note is unavailable", err)
	case errors.Is(err, recall.ErrCuratedSupporterEmpty):
		return noteops.WrapError(noteops.CodeInvalidRequest,
			"synthesis: a selected note has no excerptable content", err)
	case errors.Is(err, recall.ErrCuratedContextTooLarge),
		errors.Is(err, recall.ErrSourceBodyTooLarge),
		errors.Is(err, recall.ErrRequiredContextTooLarge):
		return noteops.WrapError(noteops.CodeConflict, "synthesis: context exceeds the byte limit", err)
	default:
		return noteops.WrapError(noteops.CodeInternal, "synthesis: build context", err)
	}
}

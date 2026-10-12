package web

import (
	"net/http"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

func (s *Server) parseSettingMutationRequest(r *http.Request) (settingMutationRequest, error) {
	var req settingMutationRequest
	if err := decodePropertyRequest(r, &req); err != nil {
		return settingMutationRequest{}, err
	}
	if err := s.checkPropertyCSRF(r, req.Token); err != nil {
		return settingMutationRequest{}, err
	}
	req.Token = ""
	req.Key = strings.TrimSpace(req.Key)
	if req.Key == "" {
		return settingMutationRequest{}, noteops.Errorf(noteops.CodeInvalidRequest, "settings: key is required")
	}
	spec, ok := config.SettingSpecFor(req.Key)
	if !ok {
		// Not found rather than forbidden, and the same answer for a machine-local
		// key as for a key that does not exist: whether this vault's overlay
		// assigns an editor binary is not something a request can probe for.
		return settingMutationRequest{}, noteops.Errorf(noteops.CodeNotFound,
			"settings: %q is not an editable setting", req.Key)
	}
	return normalizeSettingRequest(req, spec)
}

func normalizeSettingRequest(req settingMutationRequest, spec config.SettingSpec) (settingMutationRequest, error) {
	switch req.Op {
	case settingOpSet:
		if req.Value == nil {
			return settingMutationRequest{}, noteops.Errorf(noteops.CodeInvalidRequest, "settings: set requires a value")
		}
		if config.SettingKind(req.Value.Kind) != spec.Kind {
			return settingMutationRequest{}, noteops.Errorf(noteops.CodeInvalidRequest,
				"settings: %s holds a %s value, not a %s", spec.Key, spec.Kind, req.Value.Kind)
		}
		normalized := settingValueOf(req.Value.setting())
		req.Value = &normalized
	case settingOpUnset:
		req.Value = nil
	default:
		return settingMutationRequest{}, noteops.Errorf(noteops.CodeInvalidRequest,
			"settings: unsupported op %q", string(req.Op))
	}
	return req, nil
}

func settingsCommand(op settingMutationOp) string {
	if op == settingOpUnset {
		return settingsUnsetCommand
	}
	return settingsSetCommand
}

// settingsStaleConfigError is the one message every stale-preview path returns.
// brainnotes.toml now has four independent writers, and they cannot corrupt each
// other because configPin's ExpectHash makes the loser a 409; naming the remedy
// is what keeps that correct outcome from reading as a generic failure.
func settingsStaleConfigError() error {
	return noteops.Errorf(noteops.CodePreviewStale,
		"settings: %s changed, re-check the diff", config.FileName)
}

// settingsWriteError rewrites the losing side of that race into the same
// message, wherever it surfaced: the receipt's file digests, the replan, or the
// transaction precondition at the commit boundary.
//
// It asks vault.IsTxnConflict rather than matching noteops.CodeConflict, and the
// difference is the whole point. A transaction precondition that failed means
// this file really did change. Every other conflict reaching here means
// something else entirely — the Git safety lock is held by another write, the
// overlay now shadows this key, the configured templates directory is unusable —
// and each of those carries the one sentence that names its own remedy. Telling
// a person to re-check a diff that is still correct would send them around a
// loop that cannot end.
func settingsWriteError(err error) error {
	// A rollback that itself failed is asked about first, because it unwraps to
	// the conflict that caused it: the transaction put some of the change back
	// and could not put the rest back, so the file is in a state nobody wrote and
	// the message naming it is the only one that helps. Rewriting that into
	// "re-check the diff" would report a vault needing manual recovery as a
	// routine race somebody else won.
	if vault.IsTxnRollbackFailure(err) {
		return err
	}
	if noteops.CodeOf(err) == noteops.CodePreviewStale || vault.IsTxnConflict(err) {
		return noteops.WrapError(noteops.CodePreviewStale,
			"settings: "+config.FileName+" changed, re-check the diff", err)
	}
	return err
}

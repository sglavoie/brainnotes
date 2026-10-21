package web

import (
	"strings"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/journal"
	"github.com/sglavoie/brainnotes/internal/noteops"
	"github.com/sglavoie/brainnotes/internal/vault"
)

// planTemplateMutation resolves the destination, reads what is there now, and
// renders the change as a line diff.
func planTemplateMutation(root string, cfg *config.Config, req templateMutationRequest) (*templatePlan, error) {
	dest, name, err := templateDestination(root, cfg, req.Name)
	if err != nil {
		return nil, err
	}
	current, exists, err := readTemplateBytes(dest.AbsPath)
	if err != nil {
		return nil, err
	}
	plan := &templatePlan{
		Op:      req.Op,
		Name:    name,
		RelPath: dest.RelPath,
		AbsPath: dest.AbsPath,
		Created: !exists,
		Expect:  vault.ExpectMissing(),
	}
	if exists {
		plan.SourceDigest = vault.HashBytes(current)
		plan.Expect = vault.ExpectHash(plan.SourceDigest)
	}
	if err := checkTemplateRevision(plan, req, exists); err != nil {
		return nil, err
	}

	if req.Op == templateOpDelete {
		plan.Removed = true
		plan.Changed = true
		plan.Diff = config.DiffText(current, nil)
		return plan, nil
	}

	data := []byte(req.Content)
	plan.Data = data
	plan.Revision = vault.HashBytes(data)
	plan.Changed = !exists || plan.Revision != plan.SourceDigest
	if plan.Changed {
		plan.Diff = config.DiffText(current, data)
	}
	return plan, nil
}

// checkTemplateRevision refuses an edit planned against bytes the file no longer
// holds, in the vocabulary noteops.ReadSource established: the empty revision is
// "this file does not exist", and any other value is the digest it held.
//
// A delete of a file that is already gone is refused rather than treated as a
// no-op, because a request naming a template says the user believes it is there,
// and reporting success would hide that somebody else removed it.
func checkTemplateRevision(plan *templatePlan, req templateMutationRequest, exists bool) error {
	revision := strings.TrimSpace(req.Revision)
	switch {
	case req.Op == templateOpDelete && !exists:
		return noteops.Errorf(noteops.CodeNotFound, "templates: %s does not exist", plan.RelPath)
	case !exists && revision != "":
		return templateRevisionError(plan.RelPath)
	case exists && revision != plan.SourceDigest:
		return templateRevisionError(plan.RelPath)
	}
	return nil
}

func templateRevisionError(relPath string) error {
	return noteops.Errorf(noteops.CodeConflict,
		"templates: %s changed since it was loaded, so nothing was written", relPath)
}

// commitTemplatePlan writes or removes the template in one journaled
// transaction, so the change is in the undo history and in the content history
// like every other vault mutation.
//
// Both configuration layers are pinned read-only. The plan wrote neither of
// them, but the destination was derived from templates.dir, so a configuration
// replaced between planning and committing would mean the file lands in a
// directory the preview never named.
func (s *Server) commitTemplatePlan(plan *templatePlan, pin configPin, opts ...journal.RecorderOption) (*journal.Entry, error) {
	if plan == nil || !plan.Changed {
		return nil, nil
	}
	entry, err := noteops.BeginOperation(
		s.vault.Root, "templates", templateCommand(plan.Op), []string{plan.AbsPath}, opts...)
	if err != nil {
		return nil, err
	}
	defer entry.Release()
	txn, err := vault.NewFileTxn(s.vault.Root)
	if err != nil {
		return nil, noteops.WrapError(noteops.CodeInternal, "templates: open transaction", err)
	}
	if err := stageTemplateChange(txn, plan); err != nil {
		return nil, err
	}
	if err := requireTemplateConfigPins(txn, pin); err != nil {
		return nil, err
	}

	if err := txn.Commit(entry); err != nil {
		return nil, noteops.ClassifyTxnError("templates: write", err)
	}
	return entry.Finalize()
}

// stageTemplateChange puts the planned write or removal into the transaction,
// each pinned to the expectation the plan was built on — the digest the bytes
// were read at, or "this file does not exist" for a create.
func stageTemplateChange(txn *vault.FileTxn, plan *templatePlan) error {
	if plan.Removed {
		if err := txn.Remove(plan.AbsPath, plan.Expect); err != nil {
			return noteops.ClassifyTxnError("templates: plan removal", err)
		}
		return nil
	}
	if err := txn.Write(plan.AbsPath, plan.Data, 0, plan.Expect); err != nil {
		return noteops.ClassifyTxnError("templates: plan write", err)
	}
	return nil
}

// requireTemplateConfigPins adds both configuration layers as read-only
// preconditions, including the machine-local overlay's absence: an overlay
// appearing between the plan and the commit could move templates.dir, and the
// file would land in a directory the preview never named.
func requireTemplateConfigPins(txn *vault.FileTxn, pin configPin) error {
	for _, layer := range pin.layers() {
		if err := txn.Require(layer.Path, layer.expectation()); err != nil {
			return noteops.WrapError(noteops.CodeInternal, "templates: pin "+layer.name(), err)
		}
	}
	return nil
}

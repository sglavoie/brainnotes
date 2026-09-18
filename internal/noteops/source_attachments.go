package noteops

import (
	"os"
	"path"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
)

// AttachmentCommit is one staged upload a save is asking to put in the vault
// beside the note that references it.
//
// The bytes are already on this disk, in a private staging area, and this
// describes where they have to end up. Size and Digest are what staging
// measured while it received them; they are carried rather than re-measured so
// the transaction can verify the file it is about to move is still the file
// that was weighed, and refuse if it is not.
type AttachmentCommit struct {
	// UploadID is the opaque handle the client holds. It is not a path and is
	// used only to report back which upload each committed file came from.
	UploadID string
	// RelPath is the vault-relative destination staging reserved for this
	// upload.
	RelPath string
	// SourcePath is the absolute path of the staged bytes.
	SourcePath string
	Size       int64
	Digest     string
}

// SavedAttachment is one attachment a save left in the vault.
type SavedAttachment struct {
	UploadID string `json:"upload_id"`
	RelPath  string `json:"rel_path"`
}

// maxSaveAttachments bounds how many uploads one save may commit.
//
// The bound is about the transaction, not about politeness: every attachment
// adds a locked path, a staged file and a journaled snapshot, and a save is a
// single interactive request. A note referencing more than this many *new*
// files in one edit is a client bug or an abuse, and either way the honest
// answer is a refusal rather than a request that holds the vault guard for
// minutes.
const maxSaveAttachments = 64

// attachmentPlan is what a save's attachment list resolved to.
type attachmentPlan struct {
	// pending are the ones this save's transaction has to write.
	pending []plannedAttachment
	// saved is every attachment the save answers with, pending or already
	// there, in the order the request listed them.
	saved []SavedAttachment
}

// plannedAttachment is one attachment as the transaction needs it.
type plannedAttachment struct {
	abs    string
	source string
	size   int64
	digest string
}

func (p attachmentPlan) paths() []string {
	paths := make([]string, 0, len(p.pending))
	for _, item := range p.pending {
		paths = append(paths, item.abs)
	}
	return paths
}

// refuseUnresolvedAttachments turns the caller's "I could not find these
// uploads" into the refusal the note deserves.
//
// It is a separate step from planning because of *when* it runs: after a keyed
// save has been given the chance to replay, so a retry whose staging the first
// attempt already cleared is answered rather than refused, and before anything
// is written, so a save that really is storing new bytes never stores a link to
// a file no upload is going to produce.
//
// The ids are named in the message. They are opaque and unguessable, which makes
// them useless to anyone who did not stage them, and the only thing a client can
// use to work out which of its pending uploads has to be sent again.
func refuseUnresolvedAttachments(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return Errorf(CodeNotFound, "source: %d referenced upload(s) are no longer staged: %s",
		len(ids), strings.Join(ids, ", "))
}

// planAttachmentCommits decides, for every upload the save references, whether
// it still has to be written or is already exactly where it belongs.
//
// The second case is what makes a resubmitted save idempotent in the attachment
// half as well. An unkeyed save that converges never reaches a transaction, and
// a keyed one replays its record; but a save retried after a lost response with
// *different* note bytes plans fresh, and re-writing an attachment the first
// attempt already committed would fail the transaction's create-only
// precondition. Recognizing "the file there is byte-for-byte this upload"
// answers that as the no-op it is.
//
// Anything else standing in the destination is a conflict and is never
// overwritten. An attachment is not a note: nobody pinned a revision to it, so
// there is no precondition that could make replacing it safe.
func planAttachmentCommits(vaultRoot string, commits []AttachmentCommit) (attachmentPlan, error) {
	if len(commits) == 0 {
		return attachmentPlan{}, nil
	}
	if len(commits) > maxSaveAttachments {
		return attachmentPlan{}, Errorf(CodeInvalidRequest,
			"source: a save may commit at most %d attachments, not %d", maxSaveAttachments, len(commits))
	}
	plan := attachmentPlan{saved: make([]SavedAttachment, 0, len(commits))}
	seenPath := make(map[string]bool, len(commits))
	seenID := make(map[string]bool, len(commits))
	for _, commit := range commits {
		item, err := planOneAttachment(vaultRoot, commit, seenPath, seenID)
		if err != nil {
			return attachmentPlan{}, err
		}
		plan.saved = append(plan.saved, SavedAttachment{UploadID: item.uploadID, RelPath: item.relPath})
		if item.pending != nil {
			plan.pending = append(plan.pending, *item.pending)
		}
	}
	return plan, nil
}

// plannedCommit is one resolved attachment: what to report, and the write it
// still needs, if any.
type plannedCommit struct {
	uploadID string
	relPath  string
	pending  *plannedAttachment
}

func planOneAttachment(vaultRoot string, commit AttachmentCommit, seenPath, seenID map[string]bool) (plannedCommit, error) {
	uploadID := strings.TrimSpace(commit.UploadID)
	if uploadID == "" {
		return plannedCommit{}, Errorf(CodeInvalidRequest, "source: an attachment must name the upload it commits")
	}
	if seenID[uploadID] {
		return plannedCommit{}, Errorf(CodeInvalidRequest, "source: upload %s is listed twice", uploadID)
	}
	seenID[uploadID] = true

	digest, err := attachmentDigest(commit)
	if err != nil {
		return plannedCommit{}, err
	}
	dest, err := resolveAttachmentDestination(vaultRoot, commit.RelPath)
	if err != nil {
		return plannedCommit{}, err
	}
	if seenPath[dest.RelPath] {
		return plannedCommit{}, Errorf(CodeInvalidRequest,
			"source: two attachments claim %s, so nothing was written", dest.RelPath)
	}
	seenPath[dest.RelPath] = true

	settled, err := attachmentAlreadyCommitted(dest, commit.Size, digest)
	if err != nil {
		return plannedCommit{}, err
	}
	resolved := plannedCommit{uploadID: uploadID, relPath: dest.RelPath}
	if settled {
		return resolved, nil
	}
	resolved.pending = &plannedAttachment{
		abs:    dest.AbsPath,
		source: strings.TrimSpace(commit.SourcePath),
		size:   commit.Size,
		digest: digest,
	}
	return resolved, nil
}

// attachmentDigest checks the measurements staging took.
//
// They are the transaction's only way to tell the staged file apart from
// whatever else might be at that path by the time it copies, so a malformed one
// is refused here rather than allowed to become an unexplained commit failure.
func attachmentDigest(commit AttachmentCommit) (string, error) {
	digest := strings.ToLower(strings.TrimSpace(commit.Digest))
	if !validSourceRevision(digest) {
		return "", Errorf(CodeInvalidRequest,
			"source: attachment %s has no content digest", strings.TrimSpace(commit.UploadID))
	}
	if commit.Size < 0 {
		return "", Errorf(CodeInvalidRequest,
			"source: attachment %s has a negative size", strings.TrimSpace(commit.UploadID))
	}
	if strings.TrimSpace(commit.SourcePath) == "" {
		return "", Errorf(CodeInvalidRequest,
			"source: attachment %s names no staged file", strings.TrimSpace(commit.UploadID))
	}
	return digest, nil
}

// resolveAttachmentDestination decides where an attachment is allowed to land.
//
// It deliberately does not reuse the note's vault.WalkExclusion check. The
// default ignore set contains "Attachments", which fold-matches the default
// attachment directory, so asking that question here would refuse the one
// location attachments are configured to use. Exclusion from the *note walk* is
// exactly right for an attachment — it is not a note and must never be walked
// as one.
//
// What is asked instead is narrower and about safety rather than about
// classification:
//
//   - no hidden component, so an upload cannot be aimed at .git, .obsidian, or
//     the .bnotes state the vault's own machinery owns;
//   - no Markdown extension, so an upload cannot become a note — one that did
//     would be walked, parsed, indexed and linked, and nothing downstream
//     expects arbitrary uploaded bytes there;
//   - in vaultgit scope, so the file is covered by the content history and by
//     `bn undo` like the note that references it. An attachment the history
//     does not track is one an undo would leave behind.
func resolveAttachmentDestination(vaultRoot, relPath string) (PathResolution, error) {
	clean, err := CleanRelPath(relPath)
	if err != nil {
		return PathResolution{}, err
	}
	for _, part := range strings.Split(clean, "/") {
		if strings.HasPrefix(part, ".") {
			return PathResolution{}, Errorf(CodeForbidden,
				"source: %s is not an attachment location, so nothing was written", clean)
		}
	}
	if isNoteExtension(path.Ext(clean)) {
		return PathResolution{}, Errorf(CodeInvalidRequest,
			"source: %s would be a note, not an attachment", clean)
	}
	if !vaultgit.InScope(clean) {
		return PathResolution{}, Errorf(CodeForbidden,
			"source: %s is outside the vault's tracked content, so nothing was written", clean)
	}
	dest, err := PathForRel(vaultRoot, clean)
	if err != nil {
		return PathResolution{}, err
	}
	if dest.RelPath != clean {
		return PathResolution{}, Errorf(CodeForbidden, "source: an attachment must not resolve through a symlink")
	}
	return dest, nil
}

func isNoteExtension(ext string) bool {
	switch strings.ToLower(ext) {
	case ".md", ".markdown":
		return true
	}
	return false
}

// attachmentAlreadyCommitted reports whether the destination already holds
// exactly these bytes.
//
// Size is compared before the digest so the common answer — a name taken by
// something else entirely — costs a stat rather than a read of the whole file.
func attachmentAlreadyCommitted(dest PathResolution, size int64, digest string) (bool, error) {
	info, err := os.Lstat(dest.AbsPath)
	switch {
	case os.IsNotExist(err):
		return false, nil
	case err != nil:
		return false, WrapError(CodeInternal, "source: inspect attachment destination", err)
	case !info.Mode().IsRegular():
		return false, Errorf(CodeConflict,
			"source: %s is not a regular file, so nothing was written", dest.RelPath)
	case info.Size() != size:
		return false, newAttachmentExistsError(dest.RelPath)
	}
	have, _, err := vault.HashFile(dest.AbsPath)
	if err != nil {
		return false, WrapError(CodeInternal, "source: read attachment destination", err)
	}
	if have != digest {
		return false, newAttachmentExistsError(dest.RelPath)
	}
	return true, nil
}

func newAttachmentExistsError(relPath string) error {
	return Errorf(CodeConflict,
		"source: %s already holds different content, so nothing was written", relPath)
}

// stageAttachments adds every pending attachment to the note's own transaction.
//
// WriteFrom rather than Write: the bytes are already in a file and a save may
// carry tens of megabytes of them, so they are copied staged-file to
// staged-file and never held in this process's memory. It is create-only by
// construction, which is the precondition an attachment needs — planning
// already refused every destination that exists and is not this upload.
func stageAttachments(txn *vault.FileTxn, plan attachmentPlan) error {
	for _, item := range plan.pending {
		if err := txn.WriteFrom(item.abs, item.source, item.size, item.digest, 0); err != nil {
			return classifyTxnError("source: plan attachment", err)
		}
	}
	return nil
}

// The whole-file Markdown surface: /api/source, /api/source/preview and
// /api/source/save.
//
// Every one of these types carries `revision`, and that is the shape of the
// contract rather than an incidental field. A source read hands out the hash of
// the exact bytes it returned; a save is refused unless it echoes the revision
// it was planned against; and the answer to a save — including a replay and a
// converged no-op — carries the revision the *next* edit must be planned
// against. A client that drops the field cannot make a second save.

export type SourceDocument = {
  key: string;
  path: string;
  rel_path: string;
  revision: string;
  content: string;
};

export type SourcePreviewRequest = {
  content: string;
  token: string;
};

export type SourceSaveRequest = {
  key: string;
  content: string;
  revision: string;
  save_id: string;
  token: string;
} & SourceAttachmentFields;

// SourceAttachmentFields are what turns staged bytes into vault bytes.
//
// A save is the *only* thing that commits an upload: /api/attachments/upload
// puts the file in private staging under an owner, and these two fields are how
// the save that writes the note names the uploads it is claiming. Both travel
// together or neither means anything — the owner is what the server checks the
// ids against, so an id without its owner resolves to nothing at all.
//
// `attachment` is repeated on the wire, once per id, which is why this is a
// list and not a joined string: internal/web/attachments_api.go reads
// r.Form["attachment"], and a comma-joined value would resolve as one id nobody
// staged.
export type SourceAttachmentFields = {
  owner?: string;
  attachments?: readonly string[];
};

// SavedAttachment is one upload the vault now holds, as the save answered.
// It is read rather than assumed because a replayed save answers from an
// earlier call's record: the ids a request carried and the ids a response names
// are the same list only for a save that actually did the writing.
export type SavedAttachment = {
  upload_id: string;
  rel_path: string;
};

// SourceCreateRequest is the one request on this surface with no revision, and
// that is the contract rather than an omission: /api/source/create writes a note
// that does not exist yet, so there are no bytes for an edit to have been
// planned against. It names a path instead of a key for the same reason — there
// is no note to resolve a route key from. validateSaveSourceRequest in
// internal/noteops/source_save.go refuses a create that carries a revision and a
// save that does not.
export type SourceCreateRequest = {
  rel_path: string;
  content: string;
  save_id: string;
  token: string;
} & SourceAttachmentFields;

// SourceSaveSuccess is a save the server accepted. `changed` is false for a
// note that already held the submitted bytes and `replayed` is true for a
// resubmission the idempotency ledger answered, so a client can word the
// outcome without claiming a write it may not have performed.
export type SourceSaveSuccess = {
  ok: true;
  message: string;
  key: string;
  path: string;
  rel_path: string;
  revision: string;
  changed: boolean;
  created: boolean;
  replayed: boolean;
  // attachments is every upload this save accounts for, in the order the
  // request named them. It is the client's signal to stop owing them: an id
  // here is in the vault, and its staged copy is gone.
  attachments: SavedAttachment[];
};

// SourceSaveFailure is a refused save.
//
// `content` and `revision` are populated for a stale-revision refusal — the
// note's current bytes, so the editor can show what it is up against instead of
// telling somebody to copy their draft somewhere and reload. `changed` is true
// only for a snapshot failure, where the bytes *are* on disk and the history
// does not cover them; a client that ignores it would resubmit against a
// revision its own save already replaced.
export type SourceSaveFailure = {
  ok: false;
  code: string;
  message: string;
  revision: string;
  content: string;
  changed: boolean;
  // attachments is populated for the one refusal that wrote something — the
  // snapshot failure — and empty for every other, because a refused save
  // committed no upload. It carries the same meaning as on the success arm, so a
  // client can stop owing an id without having to ask which arm it arrived on.
  attachments: SavedAttachment[];
};

export type SourceSaveResponse = SourceSaveSuccess | SourceSaveFailure;

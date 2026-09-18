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
};

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
};

export type SourceSaveResponse = SourceSaveSuccess | SourceSaveFailure;

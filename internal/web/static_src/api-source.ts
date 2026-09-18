import { normalizeParamValue } from "./filters";
import { asRecord, readBoolean, readRecordValue } from "./api-internal";
import type {
  SourceDocument,
  SourcePreviewRequest,
  SourceSaveRequest,
  SourceSaveResponse,
} from "./api-types";

export const normalizeSourceDocument = (payload: unknown): SourceDocument => {
  const record = asRecord(payload) || {};
  return {
    key: normalizeParamValue(record.key),
    path: normalizeParamValue(record.path),
    rel_path: normalizeParamValue(readRecordValue(record, ["rel_path", "RelPath"])),
    revision: normalizeParamValue(record.revision),
    // Deliberately not normalizeParamValue: that trims, and the note's source is
    // the exact bytes the revision hashes to. Trimming a trailing newline off
    // here would make every first save of an untouched note a rewrite.
    content: typeof record.content === "string" ? record.content : "",
  };
};

export const normalizeSourcePreviewHTML = (payload: unknown): string => {
  const record = asRecord(payload) || {};
  return normalizeParamValue(
    readRecordValue(record, ["rendered_html", "RenderedHTML", "renderedHtml"])
  );
};

// normalizeSourceSaveResponse reads either arm of the save envelope.
//
// `ok` decides which one, rather than the HTTP status, because the one failure
// that matters most does not look like a failure from the outside: a snapshot
// error answers 500 with `changed: true`, meaning the bytes landed. The caller
// has to be able to tell that from a refusal, and the payload is where that is
// said.
export const normalizeSourceSaveResponse = (payload: unknown): SourceSaveResponse => {
  const record = asRecord(payload) || {};
  if (readBoolean(record.ok)) {
    return {
      ok: true,
      message: normalizeParamValue(record.message),
      key: normalizeParamValue(record.key),
      path: normalizeParamValue(record.path),
      rel_path: normalizeParamValue(readRecordValue(record, ["rel_path", "RelPath"])),
      revision: normalizeParamValue(record.revision),
      changed: readBoolean(record.changed),
      created: readBoolean(record.created),
      replayed: readBoolean(record.replayed),
    };
  }
  return {
    ok: false,
    code: normalizeParamValue(record.code),
    message: normalizeParamValue(record.message),
    revision: normalizeParamValue(record.revision),
    content: typeof record.content === "string" ? record.content : "",
    changed: readBoolean(record.changed),
  };
};

export const buildSourcePreviewForm = (request: SourcePreviewRequest): URLSearchParams => {
  const body = new URLSearchParams();
  body.set("content", request.content);
  body.set("token", request.token);
  return body;
};

export const buildSourceSaveForm = (request: SourceSaveRequest): URLSearchParams => {
  const body = new URLSearchParams();
  body.set("key", request.key);
  body.set("content", request.content);
  body.set("revision", request.revision);
  body.set("save_id", request.save_id);
  body.set("token", request.token);
  return body;
};

// sourceSaveConflict names the one refusal an editor answers with a choice
// rather than a message: the note changed under the edit, nothing was written,
// and the server sent back the bytes that are in the way.
//
// `code` alone does not say that, and neither does the presence of a revision.
// `conflict` is what the save path answers for *every* precondition that failed
// at the commit boundary — `classifySourceSaveTxnError` in
// internal/noteops/source_save.go turns any transaction conflict into a
// revision error, including the configuration pin moving under a save that had
// nothing to do with the note. `writeSourceSaveError` then re-reads the note for
// all of them, so a config-pin refusal arrives carrying a revision and content
// too: the note's own, unchanged, because the transaction rolled back.
//
// What separates the two is whether that revision is the one the edit was
// planned against. A revision that moved is somebody else's version; a revision
// that did not is a refusal with nothing to merge — showing the panel there
// would offer "discard mine and load the note" over the user's own starting
// text, which throws a draft away in exchange for nothing.
//
// The bytes are deliberately not consulted. `content` is `omitempty` on the
// wire, so a note somebody just emptied comes back with a moved revision and no
// content at all; keying on content would turn that into a plain refusal — no
// panel, no re-pin, and every later save answered by the same 409 with no way
// out of it. An emptied note is exactly the version most worth showing somebody
// before they overwrite it.
export const sourceSaveConflict = (
  response: SourceSaveResponse,
  baseRevision: string
): { revision: string; content: string } | null =>
  !response.ok &&
  response.code === "conflict" &&
  response.revision !== "" &&
  response.revision !== baseRevision
    ? { revision: response.revision, content: response.content }
    : null;

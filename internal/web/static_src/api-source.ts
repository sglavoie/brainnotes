import { normalizeParamValue } from "./filters";
import { asRecord, readBoolean, readRecordValue } from "./api-internal";
import type {
  SavedAttachment,
  SourceAttachmentFields,
  SourceCreateRequest,
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
      attachments: normalizeSavedAttachments(record.attachments),
    };
  }
  return {
    ok: false,
    code: normalizeParamValue(record.code),
    message: normalizeParamValue(record.message),
    revision: normalizeParamValue(record.revision),
    content: typeof record.content === "string" ? record.content : "",
    changed: readBoolean(record.changed),
    attachments: normalizeSavedAttachments(record.attachments),
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
  return appendAttachmentFields(body, request);
};

export const buildSourceCreateForm = (request: SourceCreateRequest): URLSearchParams => {
  const body = new URLSearchParams();
  body.set("rel_path", request.rel_path);
  body.set("content", request.content);
  body.set("save_id", request.save_id);
  body.set("token", request.token);
  return appendAttachmentFields(body, request);
};

// appendAttachmentFields writes the owner once and the ids once each.
//
// `append` rather than `set` for the ids is the whole contract: the server reads
// r.Form["attachment"] and commits one upload per value, so a second `set` would
// silently drop every attachment but the last. The owner is written only when
// there are ids to own — a save with no attachments must not start naming a
// draft identity the server has no use for.
const appendAttachmentFields = (
  body: URLSearchParams,
  request: SourceAttachmentFields
): URLSearchParams => {
  const ids = request.attachments ?? [];
  if (ids.length === 0) {
    return body;
  }
  body.set("owner", request.owner ?? "");
  for (const id of ids) {
    body.append("attachment", id);
  }
  return body;
};

const normalizeSavedAttachments = (value: unknown): SavedAttachment[] => {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.flatMap((item) => {
    const record = asRecord(item);
    const uploadID = normalizeParamValue(readRecordValue(record || {}, ["upload_id", "UploadID"]));
    if (!record || uploadID === "") {
      return [];
    }
    return [{
      upload_id: uploadID,
      rel_path: normalizeParamValue(readRecordValue(record, ["rel_path", "RelPath"])),
    }];
  });
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

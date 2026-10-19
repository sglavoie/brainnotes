import { normalizeParamValue } from "./filters";
import {
  asRecord,
  normalizeManagementResult,
  readBoolean,
  readRecordValue,
} from "./api-internal";
import type {
  CaptureRequest,
  CaptureResult,
  CreateNoteRequest,
  FrontmatterSetRequest,
  FrontmatterUnsetRequest,
  NoteInsightRequest,
  NoteInsightResult,
  NoteKeyMutationRequest,
  OpenNoteRequest,
  OpenNoteResult,
} from "./api-types";

export const managementMutationEventName = "bn:management:mutated";

export const normalizeNoteInsightResponse = (payload: unknown): NoteInsightResult => {
  const base = normalizeManagementResult(payload);
  const record = asRecord(payload) || {};
  return {
    ok: base.ok,
    code: base.ok ? undefined : base.code,
    message: base.message,
    enabled: readBoolean(readRecordValue(record, ["enabled", "Enabled"])),
    key: normalizeParamValue(readRecordValue(record, ["key", "Key"])),
    output: normalizeParamValue(readRecordValue(record, ["output", "Output"])),
    rendered_html: normalizeParamValue(
      readRecordValue(record, ["rendered_html", "RenderedHTML", "renderedHtml"])
    ),
  };
};

export const buildNoteInsightForm = (request: NoteInsightRequest): URLSearchParams => {
  const body = new URLSearchParams();
  body.set("key", request.key);
  if (request.prompt.trim() !== "") {
    body.set("prompt", request.prompt.trim());
  }
  body.set("token", request.token);
  return body;
};

export const buildNoteKeyMutationForm = (
  request: NoteKeyMutationRequest
): URLSearchParams => {
  const body = new URLSearchParams();
  body.set("key", request.key);
  body.set("token", request.token);
  return body;
};

export const buildCreateNoteForm = (request: CreateNoteRequest): URLSearchParams => {
  const body = buildNoteKeyMutationForm(request);
  body.set("target", request.target);
  return body;
};

export const buildFrontmatterSetForm = (
  request: FrontmatterSetRequest
): URLSearchParams => {
  const body = buildNoteKeyMutationForm(request);
  body.set("property", request.property);
  body.set("op", request.op);
  body.set("value", request.value);
  return body;
};

export const buildFrontmatterUnsetForm = (
  request: FrontmatterUnsetRequest
): URLSearchParams => {
  const body = buildNoteKeyMutationForm(request);
  body.set("property", request.property);
  return body;
};

export const buildCaptureForm = (request: CaptureRequest): URLSearchParams => {
  const body = new URLSearchParams();
  body.set("text", request.text);
  body.set("token", request.token);
  if (request.captureID) body.set("capture_id", request.captureID);
  return body;
};

// detail carries the exact bullet the server wrote, including its timestamp;
// the browser never formats one itself because the server owns the clock.
export const normalizeCaptureResponse = (payload: unknown): CaptureResult => {
  const base = normalizeManagementResult(payload);
  const record = asRecord(payload) || {};
  return {
    ok: base.ok,
    path: base.ok ? base.path : undefined,
    code: base.ok ? undefined : base.code,
    message: base.message || "",
    detail: normalizeParamValue(readRecordValue(record, ["detail", "Detail"])),
    rel_path: normalizeParamValue(readRecordValue(record, ["rel_path", "RelPath"])),
  };
};

export const buildOpenNoteForm = (request: OpenNoteRequest): URLSearchParams =>
  buildNoteKeyMutationForm(request);

export const normalizeManagementErrorMessage = (status: number, detail: unknown): string => {
  const result = normalizeManagementResult(detail);
  if (!result.ok && result.message) {
    return result.message;
  }

  const message = asRecord(detail) ? "" : normalizeParamValue(detail);
  return message || `Request failed (${status}).`;
};

// noteNavigationDestination reads the route a server that cannot start an
// editor answers an Open action with. A server that launched a process answers
// without one, so an empty string means "the editor opened it" rather than an
// error.
//
// The `/n/` prefix is a requirement, not a sanity check: this value is handed
// to window.location.assign, and a payload naming any other destination — an
// absolute URL to another host most of all — would turn an Open button into an
// open redirect. It is the one definition of a navigable Open destination;
// both the Brief's Open and the note/fix-queue Open route through it.
export const noteNavigationDestination = (payload: unknown): string => {
  const record = asRecord(payload);
  if (!record) {
    return "";
  }
  const destination = normalizeParamValue(readRecordValue(record, ["destination", "Destination"]));
  return destination.startsWith("/n/") ? destination : "";
};

// normalizeOpenErrorMessage explains a refused Open action. The open routes and
// the request guard both answer with the management JSON envelope; plain text
// is still read so a proxy's error page is not rendered as a bare status code.
export const normalizeOpenErrorMessage = (status: number, detail: unknown): string => {
  if (asRecord(detail)) {
    const result = normalizeManagementResult(detail);
    if (!result.ok && result.message) {
      return result.message;
    }
    return `Open failed (${status}).`;
  }
  const message = normalizeParamValue(detail);
  return message || `Open failed (${status}).`;
};

export const normalizeOpenSuccess = (ok: boolean, status: number, detail: unknown): OpenNoteResult => {
  if (ok) {
    return { ok: true };
  }
  return {
    ok: false,
    message: normalizeOpenErrorMessage(status, detail),
  };
};

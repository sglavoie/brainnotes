import { normalizeParamValue } from "./filters";
import {
  asRecord,
  normalizeManagementResult,
  readBoolean,
  readRecordValue,
} from "./api-internal";
import type {
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

export const normalizeOpenErrorMessage = (status: number, detail: unknown): string => {
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

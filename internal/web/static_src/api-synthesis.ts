import { responseFailure, unansweredRequest } from "./request-outcome";
import { asRecord, normalizeManagementResult, readBoolean, readCount } from "./api-internal";
import { normalizeManagementErrorMessage } from "./api-notes";
import { jsonPostRequest, readResponsePayload } from "./daily-brief-http";
import { normalizeParamValue } from "./filters";
import type {
  SynthesisApplyRequest,
  SynthesisApplyResponse,
  SynthesisContextRequest,
  SynthesisContextResponse,
  SynthesisExcerpt,
  SynthesisFetchRequest,
  SynthesisGenerateRequest,
  SynthesisGenerateResponse,
  SynthesisManifest,
  SynthesisNoteIdentity,
  SynthesisPreviewRequest,
  SynthesisPreviewResponse,
} from "./api-types";

export const synthesisContextPath = "/api/synthesis/context";
export const synthesisGeneratePath = "/api/synthesis/generate";
export const synthesisPreviewPath = "/api/synthesis/preview";
export const synthesisApplyPath = "/api/synthesis/apply";

// SynthesisRequestError carries the management error code beside the message.
// The workbench reacts to the code rather than the prose: not_found means the
// stored context or receipt is gone, and preview_stale means the receipt
// described a world that has since moved.
export class SynthesisRequestError extends Error {
  readonly code: string;

  constructor(message: string, code: string) {
    super(message);
    this.name = "SynthesisRequestError";
    this.code = code;
  }
}

const synthesisRequestError = (
  status: number,
  payload: unknown
): SynthesisRequestError => {
  const result = normalizeManagementResult(payload);
  return new SynthesisRequestError(
    normalizeManagementErrorMessage(status, payload),
    result.ok ? "" : result.code || ""
  );
};

export const buildSynthesisContextForm = (
  request: SynthesisContextRequest
): URLSearchParams => {
  const body = new URLSearchParams();
  body.set("source", request.source);
  for (const candidate of request.candidates) {
    body.append("candidate", candidate);
  }
  if (request.purpose !== "") {
    body.set("purpose", request.purpose);
  }
  if (request.prompt !== "") {
    body.set("prompt", request.prompt);
  }
  body.set("token", request.token);
  return body;
};

export const buildSynthesisContextFetchRequest = (
  endpoint: string,
  request: SynthesisContextRequest
): SynthesisFetchRequest => ({
  url: endpoint || synthesisContextPath,
  init: jsonPostRequest(request.token, buildSynthesisContextForm(request).toString()),
});

const normalizeSynthesisIdentity = (value: unknown): SynthesisNoteIdentity | null => {
  const record = asRecord(value);
  if (!record) {
    return null;
  }
  const identity = {
    key: normalizeParamValue(record.key),
    title: normalizeParamValue(record.title),
    path: normalizeParamValue(record.path),
    route: normalizeParamValue(record.route),
    folder: normalizeParamValue(record.folder),
  };
  return identity.key !== "" && identity.path !== "" && identity.route !== "" ? identity : null;
};

const normalizeSynthesisExcerpt = (value: unknown): SynthesisExcerpt | null => {
  const record = asRecord(value);
  if (!record) {
    return null;
  }
  const path = normalizeParamValue(record.path);
  return path === ""
    ? null
    : {
        path,
        start_line: readCount(record.start_line),
        end_line: readCount(record.end_line),
      };
};

export const normalizeSynthesisManifest = (value: unknown): SynthesisManifest => {
  const record = asRecord(value) || {};
  return {
    source_path: normalizeParamValue(record.source_path),
    max_bytes: readCount(record.max_bytes),
    byte_count: readCount(record.byte_count),
    notes: Array.isArray(record.notes)
      ? record.notes.flatMap((note) => {
          const identity = normalizeSynthesisIdentity(note);
          return identity ? [identity] : [];
        })
      : [],
    excerpts: Array.isArray(record.excerpts)
      ? record.excerpts.flatMap((excerpt) => {
          const normalized = normalizeSynthesisExcerpt(excerpt);
          return normalized ? [normalized] : [];
        })
      : [],
  };
};

// The packet Markdown is read as a raw string: it is the exact bytes the server
// built, so trimming or coercing it would misreport what a later step sends.
export const normalizeSynthesisContextResponse = (
  payload: unknown
): SynthesisContextResponse | null => {
  const record = asRecord(payload);
  if (!record) {
    return null;
  }
  const anchor = normalizeSynthesisIdentity(record.anchor);
  const contextID = normalizeParamValue(record.context_id);
  if (!anchor || contextID === "") {
    return null;
  }
  const supporters = Array.isArray(record.supporters)
    ? record.supporters.flatMap((supporter) => {
        const identity = normalizeSynthesisIdentity(supporter);
        return identity ? [identity] : [];
      })
    : [];
  return {
    context_id: contextID,
    expires_at: normalizeParamValue(record.expires_at),
    vault_id: normalizeParamValue(record.vault_id),
    agent_enabled: readBoolean(record.agent_enabled),
    purpose: normalizeParamValue(record.purpose),
    prompt: typeof record.prompt === "string" ? record.prompt : "",
    anchor,
    supporters,
    manifest: normalizeSynthesisManifest(record.manifest),
    markdown: typeof record.markdown === "string" ? record.markdown : "",
  };
};

export const requestSynthesisContext = async (
  fetcher: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>,
  endpoint: string,
  request: SynthesisContextRequest
): Promise<SynthesisContextResponse> => {
  const built = buildSynthesisContextFetchRequest(endpoint, request);
  const response = await fetcher(built.url, built.init);
  const payload = await readResponsePayload(response);
  if (!response.ok) {
    throw synthesisRequestError(response.status, payload);
  }
  const normalized = normalizeSynthesisContextResponse(payload);
  if (
    !normalized ||
    normalized.anchor.key !== request.source ||
    normalized.supporters.length !== request.candidates.length
  ) {
    throw new Error("The synthesis context response did not match the request.");
  }
  return normalized;
};

export const buildSynthesisGenerateForm = (
  request: SynthesisGenerateRequest
): URLSearchParams => {
  const body = new URLSearchParams();
  body.set("context_id", request.context_id);
  body.set("token", request.token);
  return body;
};

export const normalizeSynthesisGenerateResponse = (
  payload: unknown
): SynthesisGenerateResponse => {
  const record = asRecord(payload) || {};
  return {
    enabled: readBoolean(record.enabled),
    message: normalizeParamValue(record.message),
    context_id: normalizeParamValue(record.context_id),
    output: typeof record.output === "string" ? record.output : "",
    rendered_html: typeof record.rendered_html === "string" ? record.rendered_html : "",
  };
};

export const requestSynthesisGenerate = async (
  fetcher: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>,
  endpoint: string,
  request: SynthesisGenerateRequest
): Promise<SynthesisGenerateResponse> => {
  const response = await fetcher(
    endpoint || synthesisGeneratePath,
    jsonPostRequest(request.token, buildSynthesisGenerateForm(request).toString())
  );
  const payload = await readResponsePayload(response);
  if (!response.ok) {
    throw synthesisRequestError(response.status, payload);
  }
  const normalized = normalizeSynthesisGenerateResponse(payload);
  if (normalized.enabled && normalized.output === "") {
    throw new Error("The generation command returned no output.");
  }
  return normalized;
};

// The draft is sent exactly as typed. The server normalizes line endings and
// edge whitespace itself, so trimming here would preview bytes the plan would
// not have produced.
export const buildSynthesisPreviewForm = (
  request: SynthesisPreviewRequest
): URLSearchParams => {
  const body = new URLSearchParams();
  body.set("context_id", request.context_id);
  body.set("title", request.title);
  body.set("folder", request.folder);
  body.set("draft", request.draft);
  body.set("token", request.token);
  return body;
};

export const normalizeSynthesisPreviewResponse = (
  payload: unknown
): SynthesisPreviewResponse | null => {
  const record = asRecord(payload);
  if (!record) {
    return null;
  }
  const previewID = normalizeParamValue(record.preview_id);
  const destination = normalizeParamValue(record.destination);
  if (previewID === "" || destination === "") {
    return null;
  }
  return {
    preview_id: previewID,
    expires_at: normalizeParamValue(record.expires_at),
    context_id: normalizeParamValue(record.context_id),
    destination,
    markdown: typeof record.markdown === "string" ? record.markdown : "",
    rendered_html: typeof record.rendered_html === "string" ? record.rendered_html : "",
  };
};

export const requestSynthesisPreview = async (
  fetcher: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>,
  endpoint: string,
  request: SynthesisPreviewRequest
): Promise<SynthesisPreviewResponse> => {
  const response = await fetcher(
    endpoint || synthesisPreviewPath,
    jsonPostRequest(request.token, buildSynthesisPreviewForm(request).toString())
  );
  const payload = await readResponsePayload(response);
  if (!response.ok) {
    throw synthesisRequestError(response.status, payload);
  }
  const normalized = normalizeSynthesisPreviewResponse(payload);
  if (!normalized) {
    throw new Error("The synthesis preview response was incomplete.");
  }
  return normalized;
};

export const buildSynthesisApplyForm = (
  request: SynthesisApplyRequest
): URLSearchParams => {
  const body = new URLSearchParams();
  body.set("preview_id", request.preview_id);
  body.set("token", request.token);
  return body;
};

export const normalizeSynthesisApplyResponse = (
  payload: unknown
): SynthesisApplyResponse | null => {
  const result = normalizeManagementResult(payload);
  if (!result.ok || !result.path) {
    return null;
  }
  return {
    message: result.message || "Created note.",
    key: result.key || "",
    path: result.path,
    rel_path: result.rel_path || "",
    journal_id: result.journal_id || "",
  };
};

export const requestSynthesisApply = async (
  fetcher: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>,
  endpoint: string,
  request: SynthesisApplyRequest
): Promise<SynthesisApplyResponse> => {
  let response: Response;
  try {
    response = await fetcher(
      endpoint || synthesisApplyPath,
      jsonPostRequest(request.token, buildSynthesisApplyForm(request).toString())
    );
  } catch {
    throw new SynthesisRequestError(unansweredRequest("write").message, "unconfirmed");
  }
  const payload = await readResponsePayload(response);
  const failure = responseFailure(response, payload, "write");
  if (failure?.uncertain) throw new SynthesisRequestError(failure.message, "unconfirmed");
  if (failure) {
    throw synthesisRequestError(response.status, payload);
  }
  const normalized = normalizeSynthesisApplyResponse(payload);
  if (!normalized) {
    throw new SynthesisRequestError(unansweredRequest("write").message, "unconfirmed");
  }
  return normalized;
};

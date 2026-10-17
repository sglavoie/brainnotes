import { normalizeParamValue } from "./filters";
import { asRecord, normalizeManagementResult, readBoolean, readCount } from "./api-internal";
import { normalizePropertyConfigStatus } from "./api-properties";
import { normalizePropertyConfigDiff } from "./api-properties-mutation";
import type {
  SavedViewSummary,
  SavedViewsRequestBody,
  SavedViewsResult,
} from "./api-types";

export const SAVED_VIEWS_ENDPOINT = "/api/views";
export const SAVED_VIEWS_PREVIEW_ENDPOINT = "/api/views/preview";
export const SAVED_VIEWS_APPLY_ENDPOINT = "/api/views/apply";

// normalizeSavedViewSummary narrows one entry. A summary with no name is
// dropped by the caller: it cannot be linked to, saved over, or reported.
const normalizeSavedViewSummary = (input: unknown): SavedViewSummary | null => {
  const record = asRecord(input);
  if (!record) {
    return null;
  }
  const name = normalizeParamValue(record.name);
  if (name === "") {
    return null;
  }
  return {
    name,
    description: normalizeParamValue(record.description),
    query: normalizeParamValue(record.query),
    ...(normalizeParamValue(record.search) ? { search: normalizeParamValue(record.search) } : {}),
    normalized: normalizeParamValue(record.normalized),
    sort: normalizeParamValue(record.sort),
    relative_to: normalizeParamValue(record.relative_to),
    limit: readCount(record.limit),
    enabled: readBoolean(record.enabled),
    detail: typeof record.detail === "string" ? record.detail : "",
    needs_graph: readBoolean(record.needs_graph),
  };
};

export const normalizeSavedViewSummaries = (input: unknown): SavedViewSummary[] => {
  if (!Array.isArray(input)) {
    return [];
  }
  return input.flatMap((item) => {
    const summary = normalizeSavedViewSummary(item);
    return summary ? [summary] : [];
  });
};

export const normalizeSavedViewsResponse = (payload: unknown): SavedViewsResult => {
  const base = normalizeManagementResult(payload);
  const record = asRecord(payload) || {};
  return {
    ok: base.ok,
    code: base.ok ? undefined : base.code,
    message: base.message,
    generation: readCount(record.generation),
    action: normalizeParamValue(record.action),
    name: normalizeParamValue(record.name),
    new_name: normalizeParamValue(record.new_name),
    views: normalizeSavedViewSummaries(record.views),
    created: readBoolean(record.created),
    config_diff: normalizePropertyConfigDiff(record.config_diff),
    preview_id: normalizeParamValue(record.preview_id),
    expires_at: normalizeParamValue(record.expires_at),
    journal_id: normalizeParamValue(record.journal_id),
    config: normalizePropertyConfigStatus(record.config),
  };
};

// buildSavedViewsRequestInit renders one JSON mutation request. The CSRF token
// travels in the header *and* the body: these endpoints decode JSON, and the
// server reads the header first, falling back to the body field.
export const buildSavedViewsRequestInit = (
  token: string,
  body: SavedViewsRequestBody
): RequestInit => ({
  method: "POST",
  headers: {
    Accept: "application/json",
    "Content-Type": "application/json",
    "X-BN-CSRF": token,
  },
  body: JSON.stringify(body),
});

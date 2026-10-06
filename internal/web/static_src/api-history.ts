import { normalizeParamValue } from "./filters";
import { asRecord, normalizeManagementResult, readBoolean, readCount } from "./api-internal";
import type {
  HistoryClassCount,
  HistoryEntry,
  HistoryFile,
  HistoryOption,
  HistoryPreviewChange,
  HistoryPreviewRequest,
  HistoryPreviewResult,
  HistoryQuery,
  HistoryResult,
  HistoryWarning,
  RawRecord,
  UndoConflict,
  UndoRequest,
} from "./api-types";

export const HISTORY_ENDPOINT = "/api/history";
export const HISTORY_PREVIEW_ENDPOINT = "/api/history/preview";
export const UNDO_ENDPOINT = "/api/undo";

export const buildUndoForm = (request: UndoRequest): URLSearchParams => {
  const body = new URLSearchParams();
  body.set("token", request.token);
  if (request.entryID !== undefined && request.entryID.trim() !== "") {
    body.set("id", request.entryID.trim());
  }
  if (request.force) {
    body.set("force", "1");
  }
  if (request.confirmForce) {
    body.set("confirm_force", "1");
  }
  if (request.previewID !== undefined && request.previewID.trim() !== "") {
    body.set("preview_id", request.previewID.trim());
  }
  return body;
};

export const buildHistoryPreviewForm = (request: HistoryPreviewRequest): URLSearchParams => {
  const body = new URLSearchParams();
  body.set("token", request.token);
  if (request.entryID !== undefined && request.entryID.trim() !== "") {
    body.set("id", request.entryID.trim());
  }
  return body;
};

/**
 * buildHistoryQuery renders one window onto the journal.
 *
 * An empty filter is omitted rather than sent empty, because the server refuses a
 * value it does not recognize and "" is not an origin. Repeated values are what
 * the route reads, so each selected origin and class is its own parameter.
 */
export const buildHistoryQuery = (query: HistoryQuery): URLSearchParams => {
  const params = new URLSearchParams();
  setPositiveInt(params, "limit", query.limit);
  setPositiveInt(params, "offset", query.offset);
  appendNames(params, "origin", query.origins);
  appendNames(params, "class", query.classes);
  const path = query.pathMatch === "exact" ? query.path || "" : (query.path || "").trim();
  if (path !== "") {
    params.set("path", path);
  }
  if (query.pathMatch === "exact") params.set("path_match", "exact");
  return params;
};

const setPositiveInt = (params: URLSearchParams, name: string, value: number | undefined): void => {
  if (value !== undefined && Number.isFinite(value) && value > 0) {
    params.set(name, String(Math.trunc(value)));
  }
};

const appendNames = (params: URLSearchParams, name: string, values: string[] | undefined): void => {
  for (const value of values || []) {
    if (value.trim() !== "") {
      params.append(name, value.trim());
    }
  }
};

const normalizeHistoryFile = (value: unknown): HistoryFile[] => {
  const file = asRecord(value);
  if (!file) {
    return [];
  }
  return [{
    path: normalizeParamValue(file.path),
    before: normalizeParamValue(file.before),
    after: normalizeParamValue(file.after),
    class: normalizeParamValue(file.class),
    recorded: readBoolean(file.recorded),
    travels: readBoolean(file.travels),
  }];
};

const normalizeClassCounts = (value: unknown): HistoryClassCount[] => {
  const raw = Array.isArray(value) ? value : [];
  return raw.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    const name = normalizeParamValue(record.class);
    if (name === "") {
      return [];
    }
    return [{
      class: name,
      label: normalizeParamValue(record.label) || name,
      count: readCount(record.count),
    }];
  });
};

const normalizeEntry = (value: unknown): HistoryEntry[] => {
  const record = asRecord(value);
  if (!record) {
    return [];
  }
  const id = normalizeParamValue(record.id);
  if (id === "") {
    return [];
  }
  const files = (Array.isArray(record.files) ? record.files : []).flatMap(normalizeHistoryFile);
  return [{
    id,
    timestamp: normalizeParamValue(record.timestamp),
    command: normalizeParamValue(record.command),
    origin: normalizeParamValue(record.origin),
    origin_label: normalizeParamValue(record.origin_label),
    file_count: readCount(record.file_count),
    classes: normalizeClassCounts(record.classes),
    files,
  }];
};

export const normalizeHistoryEntries = (payload: unknown): HistoryEntry[] => {
  const data = asRecord(payload);
  const rawEntries = data && Array.isArray(data.entries) ? data.entries : [];
  return rawEntries.flatMap(normalizeEntry);
};

const normalizeOptions = (value: unknown): HistoryOption[] => {
  const raw = Array.isArray(value) ? value : [];
  return raw.flatMap((item) => {
    const record = asRecord(item);
    const name = normalizeParamValue(record?.value);
    if (!record || name === "") {
      return [];
    }
    return [{ value: name, label: normalizeParamValue(record.label) || name }];
  });
};

const normalizeWarnings = (value: unknown): HistoryWarning[] => {
  const raw = Array.isArray(value) ? value : [];
  return raw.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    return [{
      id: normalizeParamValue(record.id),
      message: normalizeParamValue(record.message),
    }];
  });
};

/**
 * normalizeHistoryResult reads one page of the journal.
 *
 * warning_count is read separately from the warnings array and is never inferred
 * from its length: the server caps the list and reports the true total beside it,
 * so a page showing twenty unreadable entries out of two hundred has to be able
 * to say so.
 */
export const normalizeHistoryResult = (payload: unknown): HistoryResult => {
  const base = normalizeManagementResult(payload);
  const record = asRecord(payload) || {};
  if (!base.ok) {
    return base;
  }
  const warnings = normalizeWarnings(record.warnings);
  return {
    ...base,
    entries: normalizeHistoryEntries(payload),
    total: readCount(record.total),
    offset: readCount(record.offset),
    limit: readCount(record.limit),
    has_more: readBoolean(record.has_more),
    origins: normalizeOptions(record.origins),
    classes: normalizeOptions(record.classes),
    warnings,
    warning_count: Math.max(readCount(record.warning_count), warnings.length),
  };
};

const normalizeConflicts = (value: unknown): UndoConflict[] => {
  const raw = Array.isArray(value) ? value : [];
  return raw.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    return [{
      path: normalizeParamValue(record.path),
      expected_state: normalizeParamValue(record.expected_state),
      current_state: normalizeParamValue(record.current_state),
    }];
  });
};

const normalizePreviewChange = (value: unknown): HistoryPreviewChange[] => {
  const record = asRecord(value);
  if (!record) {
    return [];
  }
  return [{
    path: normalizeParamValue(record.path),
    current_state: normalizeParamValue(record.current_state),
    restored_state: normalizeParamValue(record.restored_state),
    class: normalizeParamValue(record.class),
    recorded: readBoolean(record.recorded),
    travels: readBoolean(record.travels),
    conflict: readBoolean(record.conflict),
  }];
};

/**
 * normalizeHistoryPreview reads what a restore would do, and the receipt that
 * authorizes doing it.
 *
 * A reply with no receipt is treated as a refusal even when it says ok: the
 * receipt is the only thing that makes the confirmation possible, and reporting
 * success without one would offer a button the server will decline.
 */
export const normalizeHistoryPreview = (payload: unknown): HistoryPreviewResult => {
  const base = normalizeManagementResult(payload);
  const record: RawRecord = asRecord(payload) || {};
  if (!base.ok) {
    return base;
  }
  const previewID = normalizeParamValue(record.preview_id);
  if (previewID === "") {
    return { ok: false, code: "preview_required", message: "The preview came back without a receipt." };
  }
  return {
    ...base,
    preview_id: previewID,
    expires_at: normalizeParamValue(record.expires_at),
    entry_id: normalizeParamValue(record.entry_id),
    command: normalizeParamValue(record.command),
    origin: normalizeParamValue(record.origin),
    origin_label: normalizeParamValue(record.origin_label),
    timestamp: normalizeParamValue(record.timestamp),
    requires_force: readBoolean(record.requires_force),
    changes: (Array.isArray(record.changes) ? record.changes : []).flatMap(normalizePreviewChange),
    conflicts: normalizeConflicts(record.conflicts),
  };
};

export const normalizeUndoConflicts = normalizeConflicts;

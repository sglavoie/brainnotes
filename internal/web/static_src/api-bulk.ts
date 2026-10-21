import { normalizeParamValue } from "./filters";
import {
  asRecord,
  normalizeManagementResult,
  readBoolean,
  readCount,
} from "./api-internal";
import type {
  BulkAction,
  BulkAffectedPath,
  BulkApplyRequest,
  BulkFailure,
  BulkFrontmatterChange,
  BulkManagementItem,
  BulkManagementPath,
  BulkManagementResult,
  BulkMovePreview,
  BulkNotesRequest,
  BulkPreviewManagementResult,
  BulkPreviewRequest,
  BulkPreviewRow,
  BulkPreviewValue,
} from "./api-types";

const normalizeBulkAction = (value: unknown): BulkAction | undefined => {
  switch (normalizeParamValue(value)) {
    case "trash":
    case "move":
    case "frontmatter_set":
    case "frontmatter_unset":
      return normalizeParamValue(value) as BulkAction;
    default:
      return undefined;
  }
};

const normalizeBulkManagementPath = (value: unknown): BulkManagementPath => {
  const record = asRecord(value) || {};
  return {
    key: normalizeParamValue(record.key),
    path: normalizeParamValue(record.path),
    rel_path: normalizeParamValue(record.rel_path),
  };
};

const normalizeBulkManagementItem = (value: unknown): BulkManagementItem | null => {
  const record = asRecord(value);
  if (!record) {
    return null;
  }
  return {
    key: normalizeParamValue(record.key) || undefined,
    path: normalizeParamValue(record.path) || undefined,
    rel_path: normalizeParamValue(record.rel_path) || undefined,
    changed: readBoolean(record.changed),
  };
};

export const normalizeBulkFailures = (value: unknown): BulkFailure[] => {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    const key = normalizeParamValue(record.key);
    const relPath = normalizeParamValue(record.rel_path);
    const code = normalizeParamValue(record.code);
    const message = normalizeParamValue(record.message);
    if (key === "" && relPath === "" && code === "" && message === "") {
      return [];
    }
    return [{
      key: key || undefined,
      rel_path: relPath || undefined,
      code: code || undefined,
      message: message || undefined,
    }];
  });
};

const normalizeBulkAffectedPaths = (value: unknown): BulkAffectedPath[] => {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    return [{
      role: normalizeParamValue(record.role),
      key: normalizeParamValue(record.key),
      path: normalizeParamValue(record.path),
      rel_path: normalizeParamValue(record.rel_path),
      exists: readBoolean(record.exists),
    }];
  });
};

export const normalizeBulkPreviewValue = (value: unknown): BulkPreviewValue => {
  const record = asRecord(value);
  if (!record) {
    return { present: false };
  }
  if (!readBoolean(record.present)) {
    return { present: false };
  }
  return {
    present: true,
    value: record.value,
  };
};

export const normalizeBulkFrontmatterChanges = (value: unknown): BulkFrontmatterChange[] => {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    const key = normalizeParamValue(record.key);
    if (key === "") {
      return [];
    }
    return [{
      key,
      before: normalizeBulkPreviewValue(record.before),
      after: normalizeBulkPreviewValue(record.after),
      changed: readBoolean(record.changed),
    }];
  });
};

const normalizeBulkMovePreview = (value: unknown): BulkMovePreview | undefined => {
  const record = asRecord(value);
  if (!record) {
    return undefined;
  }
  return {
    rewrite_count: readCount(record.rewrite_count),
    rewrite_file_count: readCount(record.rewrite_file_count),
    body_rewrite_count: readCount(record.body_rewrite_count),
    frontmatter_rewrite_count: readCount(record.frontmatter_rewrite_count),
  };
};

export const normalizeExpiryString = (value: unknown): string => {
  const raw = normalizeParamValue(value);
  if (raw === "") {
    return "";
  }
  const parsed = new Date(raw);
  return Number.isNaN(parsed.getTime()) ? "" : parsed.toISOString();
};

export const normalizeBulkPreviewRows = (value: unknown): BulkPreviewRow[] => {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    const source = normalizeBulkManagementPath(record.source);
    const destinationRecord = asRecord(record.destination);
    return [{
      key: normalizeParamValue(record.key),
      path: normalizeParamValue(record.path),
      rel_path: normalizeParamValue(record.rel_path),
      source,
      destination: destinationRecord ? normalizeBulkManagementPath(destinationRecord) : undefined,
      changed: readBoolean(record.changed),
      move: normalizeBulkMovePreview(record.move),
      frontmatter: normalizeBulkFrontmatterChanges(record.frontmatter),
      affected: normalizeBulkAffectedPaths(record.affected),
    }];
  });
};

export const normalizeBulkNoteKeys = (keys: unknown[]): string[] => {
  const out: string[] = [];
  const seen = new Set<string>();
  for (const rawKey of keys) {
    const key = normalizeParamValue(rawKey);
    if (key === "") {
      continue;
    }
    const folded = key.toLowerCase();
    if (seen.has(folded)) {
      continue;
    }
    seen.add(folded);
    out.push(key);
  }
  return out;
};

const buildBulkRequestForm = (request: BulkNotesRequest): URLSearchParams => {
  const body = new URLSearchParams();
  body.set("action", request.action);
  body.set("token", request.token);
  for (const key of normalizeBulkNoteKeys(request.keys)) {
    body.append("keys[]", key);
  }
  if (request.folder !== undefined) {
    body.set("folder", request.folder);
  }
  if (request.property !== undefined) {
    body.set("property", request.property);
  }
  if (request.op !== undefined) {
    body.set("op", request.op);
  }
  if (request.value !== undefined) {
    body.set("value", request.value);
  }
  if (request.previewID !== undefined && request.previewID.trim() !== "") {
    body.set("preview_id", request.previewID.trim());
  }
  if (request.operationID) body.set("operation_id", request.operationID);
  return body;
};

export const buildBulkPreviewForm = (request: BulkPreviewRequest): URLSearchParams =>
  buildBulkRequestForm(request);

export const buildBulkApplyForm = (request: BulkApplyRequest): URLSearchParams =>
  buildBulkRequestForm(request);

export const normalizeBulkManagementResult = (payload: unknown): BulkManagementResult => {
  const base = normalizeManagementResult(payload);
  const record = asRecord(payload) || {};
  return {
    ...base,
    changed: readCount(record.changed),
    unchanged: readCount(record.unchanged),
    failed: readCount(record.failed),
    results: Array.isArray(record.results)
      ? record.results.flatMap((item) => {
          const normalized = normalizeBulkManagementItem(item);
          return normalized ? [normalized] : [];
        })
      : [],
    failures: normalizeBulkFailures(record.failures),
  };
};

export const normalizeBulkPreviewManagementResult = (
  payload: unknown
): BulkPreviewManagementResult => {
  const base = normalizeManagementResult(payload);
  const record = asRecord(payload) || {};
  return {
    ...base,
    action: normalizeBulkAction(record.action),
    changed: readCount(record.changed),
    unchanged: readCount(record.unchanged),
    failed: readCount(record.failed),
    preview_id: normalizeParamValue(record.preview_id) || undefined,
    expires_at: normalizeExpiryString(record.expires_at) || undefined,
    results: normalizeBulkPreviewRows(record.results),
    failures: normalizeBulkFailures(record.failures),
  };
};

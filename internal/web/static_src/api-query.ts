import { asRecord, readBoolean, readCount, normalizeStringArray } from "./api-internal";
import type {
  QueryCompletion,
  QueryCompletionKind,
  QueryCompletionsResponse,
  QueryReplaceRange,
  QueryValidateError,
  QueryValidateResponse,
} from "./api-types";

export const queryValidateAPIPath = "/api/query/validate";
export const queryCompletionsAPIPath = "/api/query/completions";

/**
 * Reads a string field verbatim. The shared `normalizeParamValue` trims, which
 * every other normalizer wants and this one must not: trailing whitespace in an
 * expression is a real code unit, and dropping it would slide every offset the
 * caret and the replace range are expressed in.
 */
const readText = (value: unknown): string => (typeof value === "string" ? value : "");

/** Clamps an offset into [0, limit], so a malformed payload cannot slice wild. */
const readOffset = (value: unknown, limit: number): number => {
  const parsed = Math.trunc(readCount(value));
  if (parsed < 0) {
    return 0;
  }
  return parsed > limit ? limit : parsed;
};

const normalizeValidateError = (
  value: unknown,
  query: string
): QueryValidateError | null => {
  const record = asRecord(value);
  if (!record) {
    return null;
  }
  const limit = query.length;
  const start = readOffset(record.start, limit);
  const end = readOffset(record.end, limit);
  return {
    message: readText(record.message),
    start,
    end: end < start ? start : end,
    line: Math.max(1, Math.trunc(readCount(record.line)) || 1),
    column: Math.max(1, Math.trunc(readCount(record.column)) || 1),
    positioned: readBoolean(record.positioned),
    suggestions: normalizeStringArray(record.suggestions),
  };
};

export const normalizeQueryValidateResponse = (payload: unknown): QueryValidateResponse => {
  const record = asRecord(payload) || {};
  const query = readText(record.query);
  const ok = readBoolean(record.ok);
  return {
    ok,
    query,
    normalized: readText(record.normalized),
    fields_used: normalizeStringArray(record.fields_used),
    needs_graph: readBoolean(record.needs_graph),
    // A valid query has nothing to point at, so an error object alongside ok
    // is a payload bug rather than something to render a caret from.
    error: ok ? null : normalizeValidateError(record.error, query),
  };
};

const normalizeCompletionKind = (value: unknown): QueryCompletionKind =>
  readText(value) === "value" ? "value" : "field";

const normalizeCompletion = (value: unknown): QueryCompletion | null => {
  const record = asRecord(value);
  if (!record) {
    return null;
  }
  const display = readText(record.value);
  const insert = readText(record.insert) || display;
  if (display === "" || insert === "") {
    return null;
  }
  return {
    value: display,
    insert,
    kind: readText(record.kind),
    detail: readText(record.detail),
    count: readCount(record.count),
  };
};

const normalizeReplaceRange = (value: unknown, limit: number): QueryReplaceRange => {
  const record = asRecord(value) || {};
  const start = readOffset(record.start, limit);
  const end = readOffset(record.end, limit);
  return { start, end: end < start ? start : end };
};

export const normalizeQueryCompletionsResponse = (
  payload: unknown
): QueryCompletionsResponse => {
  const record = asRecord(payload) || {};
  const query = readText(record.query);
  const limit = query.length;
  const rawCompletions = Array.isArray(record.completions) ? record.completions : [];
  const completions: QueryCompletion[] = [];
  for (const raw of rawCompletions) {
    const completion = normalizeCompletion(raw);
    if (completion) {
      completions.push(completion);
    }
  }
  return {
    ok: readBoolean(record.ok),
    query,
    kind: normalizeCompletionKind(record.kind),
    field: readText(record.field),
    arg: readText(record.arg),
    prefix: readText(record.prefix),
    pos: readOffset(record.pos, limit),
    replace: normalizeReplaceRange(record.replace, limit),
    completions,
  };
};

export const buildQueryValidateURL = (expr: string): string => {
  const params = new URLSearchParams();
  params.set("expr", expr);
  return `${queryValidateAPIPath}?${params.toString()}`;
};

export const buildQueryCompletionsURL = (expr: string, pos: number): string => {
  const params = new URLSearchParams();
  params.set("expr", expr);
  params.set("pos", String(Math.max(0, Math.trunc(pos))));
  return `${queryCompletionsAPIPath}?${params.toString()}`;
};

import { normalizeParamValue } from "./filters";
import type { BriefActivitySummary, ManagementResult, RawRecord } from "./api-types";

export const asRecord = (value: unknown): RawRecord | null =>
  value && typeof value === "object" ? (value as RawRecord) : null;

export const normalizeStringArray = (value: unknown): string[] =>
  Array.isArray(value)
    ? value.map((item) => normalizeParamValue(item)).filter((item) => item !== "")
    : [];

export const readCount = (value: unknown): number => {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : 0;
};

export const readBoolean = (value: unknown): boolean => {
  if (typeof value === "boolean") {
    return value;
  }
  const normalized = normalizeParamValue(value).toLowerCase();
  return normalized === "true" || normalized === "1";
};

export const readRecordValue = (record: RawRecord, keys: string[]): unknown => {
  for (const key of keys) {
    if (Object.prototype.hasOwnProperty.call(record, key)) {
      return record[key];
    }
  }
  return undefined;
};

export const normalizeBriefActivitySummary = (value: unknown): BriefActivitySummary => {
  const record = asRecord(value) || {};
  return {
    WindowDays: readCount(readRecordValue(record, ["WindowDays", "window_days", "windowDays"])),
    VisitDays: readCount(readRecordValue(record, ["VisitDays", "visit_days", "visitDays"])),
    OpenCount: readCount(readRecordValue(record, ["OpenCount", "open_count", "openCount"])),
  };
};

export const normalizeManagementResult = (payload: unknown): ManagementResult => {
  const record = asRecord(payload) || {};
  return readBoolean(record.ok)
    ? {
        ok: true,
        message: normalizeParamValue(record.message) || undefined,
        key: normalizeParamValue(record.key) || undefined,
        path: normalizeParamValue(record.path) || undefined,
        rel_path: normalizeParamValue(record.rel_path) || undefined,
        journal_id: normalizeParamValue(record.journal_id) || undefined,
      }
    : {
        ok: false,
        code: normalizeParamValue(record.code) || undefined,
        message: normalizeParamValue(record.message) || undefined,
      };
};

import { normalizeParamValue, notePathForKey } from "./filters";
import { asRecord, normalizeStringArray, readBoolean } from "./api-internal";
import type { SearchResult } from "./api-types";

export const normalizeSearchResults = (payload: unknown): SearchResult[] => {
  if (!Array.isArray(payload)) {
    return [];
  }
  return payload.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    return [
      {
        key: normalizeParamValue(record.key),
        path: normalizeParamValue(record.path),
        title: normalizeParamValue(record.title),
        wikilink: normalizeParamValue(record.wikilink),
        folder: normalizeParamValue(record.folder),
        modified: normalizeParamValue(record.modified),
        matched_fields: normalizeStringArray(record.matched_fields),
        issues: normalizeStringArray(record.issues),
        snippet: normalizeParamValue(record.snippet),
        pinned: readBoolean(record.pinned),
        ...(Number.isSafeInteger(record.position) && Number(record.position) > 0 ? { position: Number(record.position) } : {}),
      },
    ];
  });
};

export const searchResultPath = (result: SearchResult | null | undefined): string => {
  if (!result) {
    return "";
  }
  const path = typeof result.path === "string" ? result.path.trim() : "";
  if (path.startsWith("/n/")) {
    return path;
  }
  return notePathForKey(result.key);
};

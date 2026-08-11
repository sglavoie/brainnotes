import { normalizeParamValue } from "./filters";
import { asRecord, readCount } from "./api-internal";
import type { HistoryEntry, UndoRequest } from "./api-types";

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
  return body;
};

export const normalizeHistoryEntries = (payload: unknown): HistoryEntry[] => {
  const data = asRecord(payload);
  const rawEntries = data && Array.isArray(data.entries) ? data.entries : [];
  return rawEntries.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    const id = normalizeParamValue(record.id);
    if (id === "") {
      return [];
    }
    const rawFiles = Array.isArray(record.files) ? record.files : [];
    const files = rawFiles.flatMap((rawFile) => {
      const file = asRecord(rawFile);
      if (!file) {
        return [];
      }
      return [{
        path: normalizeParamValue(file.path),
        before: normalizeParamValue(file.before),
        after: normalizeParamValue(file.after),
      }];
    });
    return [{
      id,
      timestamp: normalizeParamValue(record.timestamp),
      command: normalizeParamValue(record.command),
      file_count: readCount(record.file_count),
      files,
    }];
  });
};

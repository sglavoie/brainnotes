import { normalizeParamValue } from "./filters";
import {
  asRecord,
  normalizeManagementResult,
  normalizeStringArray,
  readBoolean,
  readCount,
} from "./api-internal";
import { normalizeExpiryString } from "./api-bulk";
import type {
  CockpitAction,
  CockpitAffectedNote,
  CockpitCounts,
  CockpitItemMutationRequest,
  CockpitQueueResult,
  CockpitTask,
  CockpitWarning,
  FixApplyResult,
  FixMutationRequest,
  FixPreviewResult,
  FixSkip,
  NotePreview,
  NotePreviewChange,
  NotePreviewText,
} from "./api-types";

const emptyCockpitCounts = (): CockpitCounts => ({
  active: 0,
  done_hidden: 0,
  snoozed_hidden: 0,
  total_generated: 0,
});

export const normalizeCockpitCounts = (value: unknown): CockpitCounts => {
  const record = asRecord(value);
  if (!record) {
    return emptyCockpitCounts();
  }
  return {
    active: readCount(record.active),
    done_hidden: readCount(record.done_hidden),
    snoozed_hidden: readCount(record.snoozed_hidden),
    total_generated: readCount(record.total_generated),
  };
};

export const normalizeCockpitActions = (value: unknown): CockpitAction[] => {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    const kind = normalizeParamValue(record.kind);
    if (kind === "") {
      return [];
    }
    return [{ kind, label: normalizeParamValue(record.label) || kind }];
  });
};

export const normalizeCockpitAffectedNotes = (value: unknown): CockpitAffectedNote[] => {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    const affected = {
      title: normalizeParamValue(record.title),
      rel_path: normalizeParamValue(record.rel_path),
      folder: normalizeParamValue(record.folder) || undefined,
      location: normalizeParamValue(record.location) || undefined,
    };
    if (
      affected.title === "" &&
      affected.rel_path === "" &&
      !affected.folder &&
      !affected.location
    ) {
      return [];
    }
    return [affected];
  });
};

export const normalizeCockpitTasks = (value: unknown): CockpitTask[] => {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    const id = normalizeParamValue(record.id);
    if (id === "") {
      return [];
    }
    return [{
      id,
      signature: normalizeParamValue(record.signature),
      kind: normalizeParamValue(record.kind),
      priority: readCount(record.priority),
      title: normalizeParamValue(record.title),
      summary: normalizeParamValue(record.summary),
      detail: normalizeParamValue(record.detail),
      count: readCount(record.count),
      affected: normalizeCockpitAffectedNotes(record.affected),
      actions: normalizeCockpitActions(record.actions),
      triage_only: readBoolean(record.triage_only),
    }];
  });
};

export const normalizeCockpitWarnings = (value: unknown): CockpitWarning[] => {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    const message = normalizeParamValue(record.message);
    const path = normalizeParamValue(record.path);
    if (message === "" && path === "") {
      return [];
    }
    return [{ message, path: path || undefined }];
  });
};

export const normalizeCockpitQueueResponse = (payload: unknown): CockpitQueueResult => {
  const base = normalizeManagementResult(payload);
  const record = asRecord(payload) || {};
  return {
    ok: base.ok,
    code: base.ok ? undefined : base.code,
    message: base.message,
    tasks: normalizeCockpitTasks(record.tasks),
    counts: normalizeCockpitCounts(record.counts),
    warnings: normalizeCockpitWarnings(record.warnings),
  };
};

export const normalizeNotePreviewText = (value: unknown): NotePreviewText => {
  const record = asRecord(value);
  if (!record) {
    return { present: false, text: "" };
  }
  return {
    present: readBoolean(record.present),
    text: normalizeParamValue(record.text),
  };
};

export const normalizeNotePreviewChanges = (value: unknown): NotePreviewChange[] => {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    const kind = normalizeParamValue(record.kind);
    if (kind === "") {
      return [];
    }
    return [{
      kind,
      key: normalizeParamValue(record.key) || undefined,
      line: readCount(record.line) || undefined,
      old: normalizeNotePreviewText(record.old),
      new: normalizeNotePreviewText(record.new),
    }];
  });
};

export const normalizeNotePreviews = (value: unknown): NotePreview[] => {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    return [{
      rel_path: normalizeParamValue(record.rel_path),
      changes: normalizeNotePreviewChanges(record.changes),
    }];
  });
};

export const normalizeFixSkips = (value: unknown): FixSkip[] => {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    const reason = normalizeParamValue(record.reason);
    const relPath = normalizeParamValue(record.rel_path);
    const target = normalizeParamValue(record.target);
    if (reason === "" && relPath === "" && target === "") {
      return [];
    }
    return [{
      rel_path: relPath || undefined,
      target: target || undefined,
      reason,
    }];
  });
};

export const normalizeFixPreviewResponse = (payload: unknown): FixPreviewResult => {
  const base = normalizeManagementResult(payload);
  const record = asRecord(payload) || {};
  return {
    ok: base.ok,
    code: base.ok ? undefined : base.code,
    message: base.message,
    action: normalizeParamValue(record.action),
    task_id: normalizeParamValue(record.task_id),
    previews: normalizeNotePreviews(record.previews),
    skipped: normalizeFixSkips(record.skipped),
    preview_id: normalizeParamValue(record.preview_id),
    expires_at: normalizeExpiryString(record.expires_at),
  };
};

export const normalizeFixApplyResponse = (payload: unknown): FixApplyResult => {
  const base = normalizeManagementResult(payload);
  const record = asRecord(payload) || {};
  return {
    ok: base.ok,
    code: base.ok ? undefined : base.code,
    message: base.message,
    action: normalizeParamValue(record.action),
    task_id: normalizeParamValue(record.task_id),
    changed: normalizeStringArray(record.changed),
    created: normalizeStringArray(record.created),
    skipped: normalizeFixSkips(record.skipped),
    journal_id: normalizeParamValue(record.journal_id) || undefined,
  };
};

export const buildCockpitItemForm = (
  request: CockpitItemMutationRequest
): URLSearchParams => {
  const body = new URLSearchParams();
  body.set("id", request.id);
  body.set("action", request.action);
  body.set("token", request.token);
  if (request.action === "snooze" && request.snooze) {
    body.set("snooze", request.snooze);
  }
  return body;
};

export const buildFixPreviewForm = (request: FixMutationRequest): URLSearchParams => {
  const body = new URLSearchParams();
  body.set("id", request.id);
  body.set("action", request.action);
  body.set("token", request.token);
  return body;
};

export const buildFixApplyForm = (request: FixMutationRequest): URLSearchParams => {
  const body = buildFixPreviewForm(request);
  if (request.previewID !== undefined && request.previewID.trim() !== "") {
    body.set("preview_id", request.previewID.trim());
  }
  return body;
};

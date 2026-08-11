import { normalizeParamValue } from "./filters";
import {
  asRecord,
  normalizeBriefActivitySummary,
  normalizeManagementResult,
  normalizeStringArray,
  readBoolean,
  readRecordValue,
} from "./api-internal";
import type {
  BriefCandidateMetadata,
  BriefCard,
  BriefDaily,
  BriefInsightRequest,
  BriefInsightResult,
  BriefItemRequest,
  BriefItemResult,
  BriefNoteRef,
  BriefRollingActivity,
  BriefSlotResult,
  BriefUnavailableSlot,
  BriefViewRequest,
  BriefViewResult,
} from "./api-types";

export const normalizeBriefRollingActivity = (value: unknown): BriefRollingActivity => {
  const record = asRecord(value) || {};
  return {
    Last7Days: normalizeBriefActivitySummary(
      readRecordValue(record, ["Last7Days", "last_7_days", "last7Days"])
    ),
    Last14Days: normalizeBriefActivitySummary(
      readRecordValue(record, ["Last14Days", "last_14_days", "last14Days"])
    ),
  };
};

const normalizeBriefNoteRef = (value: unknown): BriefNoteRef => {
  const record = asRecord(value) || {};
  return {
    RelPath: normalizeParamValue(readRecordValue(record, ["RelPath", "rel_path", "relPath"])),
    Title: normalizeParamValue(readRecordValue(record, ["Title", "title"])),
  };
};

const normalizeBriefCandidateMetadata = (value: unknown): BriefCandidateMetadata => {
  const record = asRecord(value) || {};
  return {
    Score: Number(readRecordValue(record, ["Score", "score"])) || 0,
    Relationship: normalizeParamValue(readRecordValue(record, ["Relationship", "relationship"])),
    Bridge: normalizeParamValue(readRecordValue(record, ["Bridge", "bridge"])),
    Reasons: normalizeStringArray(readRecordValue(record, ["Reasons", "reasons"])),
  };
};

const normalizeBriefCard = (value: unknown): BriefCard | null => {
  const record = asRecord(value);
  if (!record) {
    return null;
  }
  const id = normalizeParamValue(readRecordValue(record, ["ID", "id"]));
  if (id === "") {
    return null;
  }
  const candidate = asRecord(readRecordValue(record, ["Candidate", "candidate"]));
  return {
    ID: id,
    Slot: normalizeParamValue(readRecordValue(record, ["Slot", "slot"])),
    Source: normalizeBriefNoteRef(readRecordValue(record, ["Source", "source"])),
    Candidate: candidate ? normalizeBriefNoteRef(candidate) : undefined,
    Explanation: normalizeParamValue(readRecordValue(record, ["Explanation", "explanation"])),
    Excerpt: normalizeParamValue(readRecordValue(record, ["Excerpt", "excerpt"])),
    Metadata: normalizeBriefCandidateMetadata(readRecordValue(record, ["Metadata", "metadata"])),
  };
};

const normalizeBriefUnavailable = (value: unknown): BriefUnavailableSlot | undefined => {
  const record = asRecord(value);
  if (!record) {
    return undefined;
  }
  const message = normalizeParamValue(readRecordValue(record, ["Message", "message"]));
  const slot = normalizeParamValue(readRecordValue(record, ["Slot", "slot"]));
  if (slot === "" && message === "") {
    return undefined;
  }
  return {
    Slot: slot,
    Message: message,
  };
};

export const normalizeBriefSlotResults = (value: unknown): BriefSlotResult[] => {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    const slot = normalizeParamValue(readRecordValue(record, ["Slot", "slot"]));
    const card = normalizeBriefCard(readRecordValue(record, ["Card", "card"]));
    const unavailable = normalizeBriefUnavailable(readRecordValue(record, ["Unavailable", "unavailable"]));
    if (slot === "" && card === null && unavailable === undefined) {
      return [];
    }
    return [{
      Slot: slot,
      Card: card || undefined,
      Unavailable: unavailable,
    }];
  });
};

export const normalizeBriefDaily = (value: unknown): BriefDaily | undefined => {
  const record = asRecord(value);
  if (!record) {
    return undefined;
  }
  return {
    Date: normalizeParamValue(readRecordValue(record, ["Date", "date"])),
    Slots: normalizeBriefSlotResults(readRecordValue(record, ["Slots", "slots"])),
  };
};

export const normalizeBriefViewResponse = (payload: unknown): BriefViewResult => {
  const base = normalizeManagementResult(payload);
  const record = asRecord(payload) || {};
  return {
    ok: base.ok,
    code: base.ok ? undefined : base.code,
    message: base.message,
    activity: normalizeBriefRollingActivity(readRecordValue(record, ["activity", "Activity"])),
  };
};

export const normalizeBriefItemResponse = (payload: unknown): BriefItemResult => {
  const base = normalizeManagementResult(payload);
  const record = asRecord(payload) || {};
  return {
    ok: base.ok,
    code: base.ok ? undefined : base.code,
    message: base.message,
    action: normalizeParamValue(readRecordValue(record, ["action", "Action"])),
    card_id: normalizeParamValue(readRecordValue(record, ["card_id", "CardID", "cardId"])),
    slot: normalizeParamValue(readRecordValue(record, ["slot", "Slot"])),
    daily: normalizeBriefDaily(readRecordValue(record, ["daily", "Daily"])),
    activity: normalizeBriefRollingActivity(readRecordValue(record, ["activity", "Activity"])),
    affected: normalizeBriefSlotResults(readRecordValue(record, ["affected", "Affected"])),
  };
};

export const normalizeBriefInsightResponse = (payload: unknown): BriefInsightResult => {
  const base = normalizeManagementResult(payload);
  const record = asRecord(payload) || {};
  return {
    ok: base.ok,
    code: base.ok ? undefined : base.code,
    message: base.message,
    enabled: readBoolean(readRecordValue(record, ["enabled", "Enabled"])),
    card_id: normalizeParamValue(readRecordValue(record, ["card_id", "CardID", "cardId"])),
    slot: normalizeParamValue(readRecordValue(record, ["slot", "Slot"])),
    output: normalizeParamValue(readRecordValue(record, ["output", "Output"])),
    rendered_html: normalizeParamValue(
      readRecordValue(record, ["rendered_html", "RenderedHTML", "renderedHtml"])
    ),
  };
};

export const buildBriefViewForm = (request: BriefViewRequest): URLSearchParams => {
  const body = new URLSearchParams();
  body.set("token", request.token);
  return body;
};

export const buildBriefItemForm = (request: BriefItemRequest): URLSearchParams => {
  const body = new URLSearchParams();
  body.set("card_id", request.cardID);
  body.set("action", request.action);
  body.set("token", request.token);
  return body;
};

export const buildBriefInsightForm = (request: BriefInsightRequest): URLSearchParams => {
  const body = new URLSearchParams();
  body.set("card_id", request.cardID);
  if (request.prompt.trim() !== "") {
    body.set("prompt", request.prompt.trim());
  }
  body.set("token", request.token);
  return body;
};

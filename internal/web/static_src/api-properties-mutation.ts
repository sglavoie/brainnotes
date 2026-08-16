import { normalizeParamValue } from "./filters";
import {
  asRecord,
  normalizeManagementResult,
  readBoolean,
  readCount,
} from "./api-internal";
import { normalizeNotePreviews } from "./api-cockpit";
import { normalizePropertyConfigStatus } from "./api-properties";
import type {
  ConfigStatusResult,
  PropertyApplyRequest,
  PropertyBlocker,
  PropertyConfigDiffLine,
  PropertyMutationRequest,
  PropertyMutationResult,
  PropertySchemaRequest,
  PropertyValueEditRequest,
  PropertyViolationProjection,
  PropertyViolationTotals,
} from "./api-types";

export const PROPERTY_VALUE_ENDPOINT = "/api/properties/value";
export const PROPERTY_PREVIEW_ENDPOINT = "/api/properties/preview";
export const PROPERTY_APPLY_ENDPOINT = "/api/properties/apply";
export const PROPERTY_SCHEMA_PREVIEW_ENDPOINT = "/api/properties/schema/preview";
export const PROPERTY_SCHEMA_APPLY_ENDPOINT = "/api/properties/schema/apply";
export const CONFIG_STATUS_ENDPOINT = "/api/config/status";

// PROPERTY_BLOCKER_REPAIR_SKIP is the one advisory blocker kind. Every other
// kind suppresses the receipt server-side, so the plan cannot be applied.
export const PROPERTY_BLOCKER_REPAIR_SKIP = "repair_skip";

export const normalizePropertyBlockers = (input: unknown): PropertyBlocker[] => {
  if (!Array.isArray(input)) {
    return [];
  }
  return input.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    const kind = normalizeParamValue(record.kind);
    if (kind === "") {
      return [];
    }
    return [{
      rel_path: normalizeParamValue(record.rel_path),
      property: normalizeParamValue(record.property),
      kind,
      detail: normalizeParamValue(record.detail),
      fatal: kind !== PROPERTY_BLOCKER_REPAIR_SKIP,
    }];
  });
};

export const propertyPlanIsBlocked = (blockers: PropertyBlocker[]): boolean =>
  blockers.some((blocker) => blocker.fatal);

export const normalizePropertyConfigDiff = (input: unknown): PropertyConfigDiffLine[] => {
  if (!Array.isArray(input)) {
    return [];
  }
  return input.flatMap((item) => {
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
      line: readCount(record.line),
      text: typeof record.text === "string" ? record.text : "",
    }];
  });
};

const normalizeViolationTotals = (input: unknown): PropertyViolationTotals => {
  const record = asRecord(input) || {};
  return {
    missing: readCount(record.missing),
    type: readCount(record.type),
    value: readCount(record.value),
    notes: readCount(record.notes),
  };
};

export const normalizePropertyViolationProjection = (
  input: unknown
): PropertyViolationProjection => {
  const record = asRecord(input) || {};
  return {
    before: normalizeViolationTotals(record.before),
    after: normalizeViolationTotals(record.after),
  };
};

export const normalizePropertyMutationResponse = (
  payload: unknown
): PropertyMutationResult => {
  const base = normalizeManagementResult(payload);
  const record = asRecord(payload) || {};
  return {
    ok: base.ok,
    code: base.ok ? undefined : base.code,
    message: base.message,
    generation: readCount(record.generation),
    action: normalizeParamValue(record.action),
    property: normalizeParamValue(record.property),
    new_name: normalizeParamValue(record.new_name),
    selected: readCount(record.selected),
    affected: readCount(record.affected),
    previews: normalizeNotePreviews(record.previews),
    blockers: normalizePropertyBlockers(record.blockers),
    config_diff: normalizePropertyConfigDiff(record.config_diff),
    projected_violations: normalizePropertyViolationProjection(record.projected_violations),
    preview_id: normalizeParamValue(record.preview_id),
    expires_at: normalizeParamValue(record.expires_at),
    journal_id: normalizeParamValue(record.journal_id),
    config: normalizePropertyConfigStatus(record.config),
  };
};

export type PropertyRequestBody =
  | PropertyMutationRequest
  | PropertyValueEditRequest
  | PropertySchemaRequest
  | PropertyApplyRequest;

// buildPropertyRequestInit renders one JSON mutation request. The CSRF token
// travels in the header because these endpoints decode the body as JSON and so
// cannot read a form field.
export const buildPropertyRequestInit = (
  token: string,
  body: PropertyRequestBody
): RequestInit => ({
  method: "POST",
  headers: {
    Accept: "application/json",
    "Content-Type": "application/json",
    "X-BN-CSRF": token,
  },
  body: JSON.stringify(body),
});

export const readReceiptExpiryLabel = (expiresAt: string, now: Date = new Date()): string => {
  if (expiresAt === "") {
    return "";
  }
  const expiry = new Date(expiresAt);
  if (Number.isNaN(expiry.getTime())) {
    return "";
  }
  const seconds = Math.round((expiry.getTime() - now.getTime()) / 1000);
  if (seconds <= 0) {
    return "This preview has expired. Preview again to apply it.";
  }
  if (seconds < 60) {
    return `This preview expires in ${seconds} ${seconds === 1 ? "second" : "seconds"}.`;
  }
  const minutes = Math.round(seconds / 60);
  return `This preview expires in about ${minutes} ${minutes === 1 ? "minute" : "minutes"}.`;
};

export const propertyViolationDelta = (
  projection: PropertyViolationProjection
): string => {
  const before =
    projection.before.missing + projection.before.type + projection.before.value;
  const after = projection.after.missing + projection.after.type + projection.after.value;
  if (before === after) {
    return `${after} ${after === 1 ? "violation" : "violations"} before and after.`;
  }
  return `Violations ${before} -> ${after}.`;
};

export const normalizeConfigStatusResponse = (payload: unknown): ConfigStatusResult => {
  const record = asRecord(payload) || {};
  return {
    ok: readBoolean(record.ok),
    ...normalizePropertyConfigStatus(record),
  };
};

import { normalizeParamValue } from "./filters";
import {
  asRecord,
  normalizeManagementResult,
  normalizeStringArray,
  readBoolean,
  readCount,
} from "./api-internal";
import { normalizePropertyConfigStatus } from "./api-properties";
import { normalizePropertyConfigDiff } from "./api-properties-mutation";
import type {
  SettingKind,
  SettingSummary,
  SettingValue,
  SettingsRequestBody,
  SettingsResult,
  TemplateDirStatus,
  TemplateSummary,
  TemplatesRequestBody,
  TemplatesResult,
} from "./api-types";

export const SETTINGS_ENDPOINT = "/api/settings";
export const SETTINGS_PREVIEW_ENDPOINT = "/api/settings/preview";
export const SETTINGS_APPLY_ENDPOINT = "/api/settings/apply";

export const TEMPLATES_ENDPOINT = "/api/templates";
export const TEMPLATES_PREVIEW_ENDPOINT = "/api/templates/preview";
export const TEMPLATES_APPLY_ENDPOINT = "/api/templates/apply";

const settingKinds: readonly SettingKind[] = ["string", "bool", "int", "string_list"];

// normalizeSettingKind falls back to "string" rather than dropping the setting:
// an unknown kind is a server this build does not fully understand, and showing
// its value as text is more useful than hiding a key that exists.
export const normalizeSettingKind = (input: unknown): SettingKind => {
  const kind = normalizeParamValue(input);
  return settingKinds.find((candidate) => candidate === kind) || "string";
};

export const normalizeSettingValue = (input: unknown, kind: SettingKind): SettingValue => {
  const record = asRecord(input) || {};
  return {
    kind,
    // The text of a string setting is content, not a parameter, so it keeps its
    // surrounding whitespace.
    text: typeof record.text === "string" ? record.text : "",
    number: readCount(record.number),
    flag: readBoolean(record.flag),
    items: normalizeStringArray(record.items),
  };
};

const normalizeSettingSummary = (input: unknown): SettingSummary | null => {
  const record = asRecord(input);
  if (!record) {
    return null;
  }
  const key = normalizeParamValue(record.key);
  if (key === "") {
    return null;
  }
  const kind = normalizeSettingKind(record.kind);
  return {
    key,
    kind,
    summary: normalizeParamValue(record.summary),
    assigned: readBoolean(record.assigned),
    layer: normalizeParamValue(record.layer),
    shadowed: readBoolean(record.shadowed),
    value: normalizeSettingValue(record.value, kind),
  };
};

export const normalizeSettingSummaries = (input: unknown): SettingSummary[] => {
  if (!Array.isArray(input)) {
    return [];
  }
  return input.flatMap((item) => {
    const summary = normalizeSettingSummary(item);
    return summary ? [summary] : [];
  });
};

export const normalizeSettingsResponse = (payload: unknown): SettingsResult => {
  const base = normalizeManagementResult(payload);
  const record = asRecord(payload) || {};
  return {
    ok: base.ok,
    code: base.ok ? undefined : base.code,
    message: base.message,
    generation: readCount(record.generation),
    action: normalizeParamValue(record.action),
    key: normalizeParamValue(record.key),
    settings: normalizeSettingSummaries(record.settings),
    config_diff: normalizePropertyConfigDiff(record.config_diff),
    preview_id: normalizeParamValue(record.preview_id),
    expires_at: normalizeParamValue(record.expires_at),
    journal_id: normalizeParamValue(record.journal_id),
    config: normalizePropertyConfigStatus(record.config),
  };
};

const normalizeTemplateSummary = (input: unknown): TemplateSummary | null => {
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
    rel_path: normalizeParamValue(record.rel_path),
    size: readCount(record.size),
    revision: normalizeParamValue(record.revision),
    // Template bytes are the document being edited: trimming them here would
    // silently drop the trailing newline every save round-trips.
    content: typeof record.content === "string" ? record.content : "",
  };
};

export const normalizeTemplateSummaries = (input: unknown): TemplateSummary[] => {
  if (!Array.isArray(input)) {
    return [];
  }
  return input.flatMap((item) => {
    const summary = normalizeTemplateSummary(item);
    return summary ? [summary] : [];
  });
};

const normalizeTemplateDirStatus = (input: unknown): TemplateDirStatus => {
  const record = asRecord(input) || {};
  return {
    excluded_from_notes: readBoolean(record.excluded_from_notes),
    portable: readBoolean(record.portable),
  };
};

export const normalizeTemplatesResponse = (payload: unknown): TemplatesResult => {
  const base = normalizeManagementResult(payload);
  const record = asRecord(payload) || {};
  return {
    ok: base.ok,
    code: base.ok ? undefined : base.code,
    message: base.message,
    generation: readCount(record.generation),
    action: normalizeParamValue(record.action),
    name: normalizeParamValue(record.name),
    rel_path: normalizeParamValue(record.rel_path),
    dir: normalizeParamValue(record.dir),
    extension: normalizeParamValue(record.extension),
    templates: normalizeTemplateSummaries(record.templates),
    created: readBoolean(record.created),
    removed: readBoolean(record.removed),
    revision: normalizeParamValue(record.revision),
    diff: normalizePropertyConfigDiff(record.diff),
    preview_id: normalizeParamValue(record.preview_id),
    expires_at: normalizeParamValue(record.expires_at),
    journal_id: normalizeParamValue(record.journal_id),
    config: normalizePropertyConfigStatus(record.config),
    templating: normalizeTemplateDirStatus(record.templating),
  };
};

// buildConfigRequestInit renders one JSON mutation request for either family.
// The CSRF token travels in the header *and* the body, as the property and
// saved-view surfaces already do: the server reads the header first and falls
// back to the body field.
export const buildConfigRequestInit = (
  token: string,
  body: SettingsRequestBody | TemplatesRequestBody
): RequestInit => ({
  method: "POST",
  headers: {
    Accept: "application/json",
    "Content-Type": "application/json",
    "X-BN-CSRF": token,
  },
  body: JSON.stringify(body),
});

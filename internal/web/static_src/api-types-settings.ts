// Portable settings and template files transport ------------------------------

import type { PropertyConfigDiffLine, PropertyConfigStatus } from "./api-types";

// SettingKind is the declared type of one editable setting. It is carried on
// every value rather than inferred from which field is populated, so "the empty
// string" and "false" stay distinguishable across the wire.
export type SettingKind = "string" | "bool" | "int" | "string_list";

export type SettingValue = {
  kind: SettingKind;
  text: string;
  number: number;
  flag: boolean;
  items: string[];
};

// SettingSummary is one editable portable setting as it stands.
//
// `assigned` separates "the file says this" from "this is the default", and
// `layer` names the file that assigned it. A portable key parked in the
// machine-local overlay is `shadowed`: it still decides what the vault does, so
// writing the portable file would change nothing a person could see, and the
// server refuses that edit rather than performing it.
export type SettingSummary = {
  key: string;
  kind: SettingKind;
  summary: string;
  assigned: boolean;
  layer: string;
  shadowed: boolean;
  value: SettingValue;
};

export type SettingOp = "set" | "unset";

export type SettingMutationRequest = {
  token: string;
  op: SettingOp;
  key: string;
  value?: SettingValue;
};

export type SettingsApplyRequest = {
  preview_id: string;
  operation_id?: string;
};

export type SettingsRequestBody = SettingMutationRequest | SettingsApplyRequest;

// SettingsResult is the one shape all three settings routes answer with: a list
// fills only `settings`, a preview adds `preview_id`/`expires_at`, an apply adds
// `journal_id`.
export type SettingsResult = {
  ok: boolean;
  code?: string;
  message?: string;
  generation: number;
  action: string;
  key: string;
  settings: SettingSummary[];
  config_diff: PropertyConfigDiffLine[];
  preview_id: string;
  expires_at: string;
  journal_id: string;
  config: PropertyConfigStatus;
};

// TemplateSummary is one file in the configured templates directory. `content`
// is filled only by a single-template read, so listing never carries every
// template's bytes.
export type TemplateSummary = {
  name: string;
  rel_path: string;
  size: number;
  revision: string;
  content: string;
};

// TemplateDirStatus is the pair of facts that makes this surface trustworthy: a
// template stays out of every note query, and it still travels to an enrolled
// peer. Both are derived from the configured directory, never assumed.
export type TemplateDirStatus = {
  excluded_from_notes: boolean;
  portable: boolean;
};

export type TemplateOp = "save" | "delete";

// TemplateMutationRequest carries the revision the edit was planned against.
// Empty means "this file does not exist", which is how a create says so; a
// delete must carry one, because deleting bytes nobody has read is the one
// template action no preview could describe.
export type TemplateMutationRequest = {
  token: string;
  op: TemplateOp;
  name: string;
  content: string;
  revision: string;
};

export type TemplatesApplyRequest = {
  preview_id: string;
};

export type TemplatesRequestBody = TemplateMutationRequest | TemplatesApplyRequest;

export type TemplatesResult = {
  ok: boolean;
  code?: string;
  message?: string;
  generation: number;
  action: string;
  name: string;
  rel_path: string;
  dir: string;
  extension: string;
  templates: TemplateSummary[];
  created: boolean;
  removed: boolean;
  revision: string;
  diff: PropertyConfigDiffLine[];
  preview_id: string;
  expires_at: string;
  journal_id: string;
  config: PropertyConfigStatus;
  templating: TemplateDirStatus;
};

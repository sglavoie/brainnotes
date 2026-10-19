// Property mutation transport ------------------------------------------------

import type { NotePreview, PropertyConfigStatus } from "./api-types";

// PropertyScalarKind is a kind a single value can carry. It excludes `list`,
// which only ever describes a container.
export type PropertyScalarKind =
  | "string"
  | "wikilink"
  | "date"
  | "datetime"
  | "int"
  | "float"
  | "bool";

export type PropertyKind = PropertyScalarKind | "list";

// PropertyTypedValue is the request/response spelling of a value. A wikilink
// text is the bare target with no brackets, heading, or alias. Null and
// unsupported values have no spelling and cannot be expressed here.
export type PropertyTypedValue = {
  kind: PropertyKind;
  text: string;
  items?: PropertyTypedValue[];
};

export type PropertyMutationAction =
  | "set"
  | "unset"
  | "replace_value"
  | "rename_property"
  | "repair";

export type PropertyValueOp = "replace" | "list_add";

export type PropertySchemaOp = "upsert" | "delete";

// PropertyBlocker is one reason a plan cannot proceed, or one issue the plan
// deliberately leaves alone. `fatal` is derived on the client from the kind:
// every kind except `repair_skip` suppresses the receipt server-side.
export type PropertyBlocker = {
  rel_path: string;
  property: string;
  kind: string;
  detail: string;
  fatal: boolean;
};

export type PropertyConfigDiffLine = {
  kind: string;
  line: number;
  text: string;
};

export type PropertyViolationTotals = {
  missing: number;
  type: number;
  value: number;
  notes: number;
};

export type PropertyViolationProjection = {
  before: PropertyViolationTotals;
  after: PropertyViolationTotals;
};

export type PropertyMutationResult = {
  ok: boolean;
  code?: string;
  message?: string;
  generation: number;
  action: string;
  property: string;
  new_name: string;
  selected: number;
  affected: number;
  previews: NotePreview[];
  blockers: PropertyBlocker[];
  config_diff: PropertyConfigDiffLine[];
  projected_violations: PropertyViolationProjection;
  preview_id: string;
  expires_at: string;
  journal_id: string;
  config: PropertyConfigStatus;
};

export type PropertyMutationRequest = {
  action: PropertyMutationAction;
  property: string;
  new_name?: string;
  notes: string[];
  value?: PropertyTypedValue;
  match?: PropertyTypedValue;
};

export type PropertyValueEditRequest = {
  operation_id?: string;
  note: string;
  property: string;
  op: PropertyValueOp;
  expected_hash: string;
  value: PropertyTypedValue;
};

export type PropertySchemaDefinitionInput = {
  type: PropertyKind;
  items?: PropertyScalarKind;
  required: boolean;
  allowed: string[];
};

export type PropertySchemaRequest = {
  op: PropertySchemaOp;
  property: string;
  definition?: PropertySchemaDefinitionInput;
};

export type PropertyApplyRequest = {
  preview_id: string;
  operation_id?: string;
};

// PropertyPendingPreview is an issued receipt held by the browser between the
// preview response and the explicit apply the user confirms.
export type PropertyPendingPreview = {
  kind: "notes" | "schema";
  previewID: string;
  expiresAt: string;
  result: PropertyMutationResult;
};

export type ConfigStatusResult = PropertyConfigStatus & {
  ok: boolean;
};

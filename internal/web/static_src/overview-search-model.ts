import {
  type BulkAction,
  type BulkFailure,
  type BulkPreviewManagementResult,
  type SearchResult,
} from "./api";

export type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;
export type BulkStatusKind = "" | "is-success" | "is-error" | "is-loading";
export type BulkActionFields = Partial<Record<"folder" | "property" | "op" | "value", string>>;
export type BulkPreviewState = {
  action: BulkAction;
  fingerprint: string;
  previewID: string;
  expiresAt?: string;
  result: BulkPreviewManagementResult;
};

export type OverviewSearchDeps = {
  fetch?: FetchLike;
  confirm?: (message: string) => boolean;
};

export type OverviewSearchResultOptions = {
  selected?: boolean;
  onSelectionChange?: (result: SearchResult, selected: boolean) => void;
};

export const overviewSearchEmptyMessage = (query: string): string =>
  query ? `No notes match "${query}".` : "No notes match the active filters.";

export const bulkApplyFallbackMessage = (action: BulkAction): string => {
  switch (action) {
    case "trash":
      return "Moved selected notes to Trash.";
    case "move":
      return "Moved selected notes.";
    case "frontmatter_set":
    case "frontmatter_unset":
      return "Updated selected notes.";
    default:
      return "Applied selected notes.";
  }
};

export const previewHeading = (action: BulkAction): string => {
  switch (action) {
    case "trash":
      return "Trash preview";
    case "move":
      return "Move preview";
    case "frontmatter_set":
      return "Update property preview";
    case "frontmatter_unset":
      return "Remove property preview";
    default:
      return "Bulk preview";
  }
};

export const bulkActionLabel = (
  action: BulkAction,
  phase: "preview" | "apply"
): string => {
  switch (action) {
    case "trash":
      return phase === "preview" ? "Preview Trash" : "Apply Trash";
    case "move":
      return phase === "preview" ? "Preview Move" : "Apply Move";
    case "frontmatter_set":
      return phase === "preview" ? "Preview update" : "Update property";
    case "frontmatter_unset":
      return phase === "preview" ? "Preview removal" : "Remove property";
    default:
      return phase === "preview" ? "Preview" : "Apply";
  }
};

export const normalizeBulkFrontmatterOp = (
  value: string | undefined
): "=" | "+=" | "-=" | undefined => {
  if (value === "+=" || value === "-=" || value === "=") {
    return value;
  }
  return undefined;
};

export const normalizeBulkFields = (fields: BulkActionFields): BulkActionFields => {
  const out: BulkActionFields = {};
  if (typeof fields.folder === "string") {
    out.folder = fields.folder.trim();
  }
  if (typeof fields.property === "string") {
    out.property = fields.property.trim();
  }
  if (typeof fields.value === "string") {
    out.value = fields.value;
  }
  const op = normalizeBulkFrontmatterOp(fields.op);
  if (op) {
    out.op = op;
  }
  return out;
};

export const previewFingerprint = (
  action: BulkAction,
  fields: BulkActionFields,
  keys: string[]
): string =>
  JSON.stringify({
    action,
    keys,
    ...normalizeBulkFields(fields),
  });

export const readMoveFields = (form: HTMLFormElement | null): BulkActionFields => {
  if (!form) {
    return {};
  }
  const formData = new FormData(form);
  return {
    folder: String(formData.get("folder") || "").trim(),
  };
};

export const readSetFields = (form: HTMLFormElement | null): BulkActionFields => {
  if (!form) {
    return {};
  }
  const formData = new FormData(form);
  return {
    property: String(formData.get("property") || "").trim(),
    op: String(formData.get("op") || "="),
    value: String(formData.get("value") || ""),
  };
};

export const readUnsetFields = (form: HTMLFormElement | null): BulkActionFields => {
  if (!form) {
    return {};
  }
  const formData = new FormData(form);
  return {
    property: String(formData.get("property") || "").trim(),
  };
};

export const bulkStatusMessage = (
  result: {
    ok?: boolean;
    message?: string;
    failures?: BulkFailure[];
  },
  fallback: string
): string => {
  const message = result && result.ok && result.message ? result.message : fallback;
  const failures = Array.isArray(result.failures) ? result.failures : [];
  if (failures.length === 0) {
    return message;
  }
  const summary = failures.slice(0, 3).map((failure) => {
    const key = failure.key || failure.rel_path || "note";
    return `${key}: ${failure.message || failure.code || "failed"}`;
  });
  const suffix = failures.length > summary.length
    ? `; ${failures.length - summary.length} more`
    : "";
  return `${message} Failures: ${summary.join("; ")}${suffix}.`;
};

import { normalizeParamValue } from "./filters";
import {
  asRecord,
  normalizeManagementResult,
  normalizeStringArray,
  readBoolean,
  readCount,
} from "./api-internal";
import type {
  PropertiesResult,
  PropertyConfigStatus,
  PropertyDefinition,
  PropertyDetailQuery,
  PropertyDetailResult,
  PropertyIssue,
  PropertyPagination,
  PropertyProposal,
  PropertyRow,
  PropertyRowFilter,
  PropertyRowFilterStatus,
  PropertySummary,
  PropertyTotals,
  PropertyValue,
  PropertyValueCount,
  PropertyViolationCounts,
} from "./api-types";

export const PROPERTY_ROW_FILTERS: readonly PropertyRowFilterStatus[] = [
  "all",
  "violations",
  "missing",
  "unsupported",
] as const;

export const normalizePropertyRowFilterStatus = (value: unknown): PropertyRowFilterStatus => {
  const normalized = normalizeParamValue(value);
  return PROPERTY_ROW_FILTERS.includes(normalized as PropertyRowFilterStatus)
    ? (normalized as PropertyRowFilterStatus)
    : "all";
};

// normalizePropertyValue keeps an unsupported value label-only: the raw scalar
// is dropped so no consumer can mistake a display label for a writable value.
export const normalizePropertyValue = (input: unknown): PropertyValue => {
  const record = asRecord(input);
  if (!record) {
    return { kind: "unsupported", text: "", items: [], supported: false };
  }
  const supported = readBoolean(record.supported);
  const items = Array.isArray(record.items)
    ? record.items.map((item) => normalizePropertyValue(item))
    : [];
  const value: PropertyValue = {
    kind: normalizeParamValue(record.kind) || "unsupported",
    text: typeof record.text === "string" ? record.text : normalizeParamValue(record.text),
    items,
    supported,
  };
  if (supported && record.value !== undefined && record.value !== null) {
    value.value = record.value;
  }
  return value;
};

export const normalizePropertyValueCounts = (input: unknown): PropertyValueCount[] => {
  if (!Array.isArray(input)) {
    return [];
  }
  return input.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    return [{ value: normalizePropertyValue(record.value), notes: readCount(record.notes) }];
  });
};

export const normalizePropertyIssues = (input: unknown): PropertyIssue[] => {
  if (!Array.isArray(input)) {
    return [];
  }
  return input.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    const kind = normalizeParamValue(record.kind);
    const detail = normalizeParamValue(record.detail);
    if (kind === "" && detail === "") {
      return [];
    }
    return [{ kind, detail }];
  });
};

export const normalizePropertyViolationCounts = (input: unknown): PropertyViolationCounts => {
  const record = asRecord(input) || {};
  return {
    missing: readCount(record.missing),
    type: readCount(record.type),
    value: readCount(record.value),
  };
};

export const normalizePropertyDefinition = (input: unknown): PropertyDefinition | undefined => {
  const record = asRecord(input);
  if (!record) {
    return undefined;
  }
  const type = normalizeParamValue(record.type);
  if (type === "") {
    return undefined;
  }
  return {
    type,
    items: normalizeParamValue(record.items) || undefined,
    required: readBoolean(record.required),
    allowed: normalizeStringArray(record.allowed),
    summary: normalizeParamValue(record.summary),
  };
};

export const normalizePropertyProposal = (input: unknown): PropertyProposal | undefined => {
  const record = asRecord(input);
  if (!record) {
    return undefined;
  }
  const property = normalizeParamValue(record.property);
  const type = normalizeParamValue(record.type);
  if (property === "" || type === "") {
    return undefined;
  }
  return {
    property,
    type,
    items: normalizeParamValue(record.items) || undefined,
    notes: readCount(record.notes),
    rationale: normalizeParamValue(record.rationale),
    toml: typeof record.toml === "string" ? record.toml : "",
  };
};

export const normalizePropertyProposals = (input: unknown): PropertyProposal[] => {
  if (!Array.isArray(input)) {
    return [];
  }
  return input.flatMap((item) => {
    const proposal = normalizePropertyProposal(item);
    return proposal ? [proposal] : [];
  });
};

export const normalizePropertySummary = (input: unknown): PropertySummary => {
  const record = asRecord(input) || {};
  return {
    name: normalizeParamValue(record.name),
    path: normalizeParamValue(record.path),
    configured: readBoolean(record.configured),
    definition: normalizePropertyDefinition(record.definition),
    note_count: readCount(record.note_count),
    total_notes: readCount(record.total_notes),
    coverage: readCount(record.coverage),
    missing_required: readCount(record.missing_required),
    observed_kinds: normalizeStringArray(record.observed_kinds),
    distinct_values: readCount(record.distinct_values),
    violation_notes: readCount(record.violation_notes),
    violations: readCount(record.violations),
    violation_counts: normalizePropertyViolationCounts(record.violation_counts),
    unsupported_values: readCount(record.unsupported_values),
    unsupported_notes: readCount(record.unsupported_notes),
    issues: readCount(record.issues),
    proposal: normalizePropertyProposal(record.proposal),
  };
};

export const normalizePropertySummaries = (input: unknown): PropertySummary[] => {
  if (!Array.isArray(input)) {
    return [];
  }
  return input.flatMap((item) => {
    const summary = normalizePropertySummary(item);
    return summary.name === "" ? [] : [summary];
  });
};

export const normalizePropertyRows = (input: unknown): PropertyRow[] => {
  if (!Array.isArray(input)) {
    return [];
  }
  return input.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    const relPath = normalizeParamValue(record.rel_path);
    if (relPath === "") {
      return [];
    }
    const present = readBoolean(record.present);
    const row: PropertyRow = {
      rel_path: relPath,
      title: normalizeParamValue(record.title),
      folder: normalizeParamValue(record.folder),
      path: normalizeParamValue(record.path),
      hash: normalizeParamValue(record.hash),
      present,
      issues: normalizePropertyIssues(record.issues),
      unsupported: readBoolean(record.unsupported),
    };
    if (present && record.value !== undefined && record.value !== null) {
      row.value = normalizePropertyValue(record.value);
    }
    return [row];
  });
};

export const normalizePropertyPagination = (input: unknown): PropertyPagination => {
  const record = asRecord(input) || {};
  return {
    limit: readCount(record.limit),
    offset: readCount(record.offset),
    total: readCount(record.total),
    returned: readCount(record.returned),
    has_more: readBoolean(record.has_more),
  };
};

export const normalizePropertyRowFilter = (input: unknown): PropertyRowFilter => {
  const record = asRecord(input) || {};
  return {
    status: normalizePropertyRowFilterStatus(record.status),
    value: typeof record.value === "string" && record.value !== "" ? record.value : undefined,
    q: normalizeParamValue(record.q) || undefined,
  };
};

export const normalizePropertyConfigStatus = (input: unknown): PropertyConfigStatus => {
  const record = asRecord(input) || {};
  return {
    generation: readCount(record.generation),
    source_hash: normalizeParamValue(record.source_hash),
    status: normalizeParamValue(record.status) || "ok",
    error: normalizeParamValue(record.error) || undefined,
    restart_required: normalizeStringArray(record.restart_required),
    writable: readBoolean(record.writable),
  };
};

export const normalizePropertyTotals = (input: unknown): PropertyTotals => {
  const record = asRecord(input) || {};
  return {
    properties: readCount(record.properties),
    configured: readCount(record.configured),
    unconfigured: readCount(record.unconfigured),
    notes: readCount(record.notes),
    violations: readCount(record.violations),
    violation_notes: readCount(record.violation_notes),
    missing_required: readCount(record.missing_required),
    unsupported_values: readCount(record.unsupported_values),
    proposals: readCount(record.proposals),
  };
};

export const normalizePropertiesResponse = (payload: unknown): PropertiesResult => {
  const base = normalizeManagementResult(payload);
  const record = asRecord(payload) || {};
  return {
    ok: base.ok,
    code: base.ok ? undefined : base.code,
    message: base.message,
    generation: readCount(record.generation),
    total_notes: readCount(record.total_notes),
    totals: normalizePropertyTotals(record.totals),
    properties: normalizePropertySummaries(record.properties),
    proposals: normalizePropertyProposals(record.proposals),
    config: normalizePropertyConfigStatus(record.config),
  };
};

export const normalizePropertyDetailResponse = (payload: unknown): PropertyDetailResult => {
  const base = normalizeManagementResult(payload);
  const record = asRecord(payload) || {};
  return {
    ok: base.ok,
    code: base.ok ? undefined : base.code,
    message: base.message,
    generation: readCount(record.generation),
    property: normalizePropertySummary(record.property),
    values: normalizePropertyValueCounts(record.values),
    rows: normalizePropertyRows(record.rows),
    pagination: normalizePropertyPagination(record.pagination),
    filter: normalizePropertyRowFilter(record.filter),
    config: normalizePropertyConfigStatus(record.config),
  };
};

export const buildPropertyDetailURL = (
  endpoint: string,
  query: PropertyDetailQuery
): string => {
  const params = new URLSearchParams();
  params.set("property", query.property);
  if (query.status && query.status !== "all") {
    params.set("filter", query.status);
  }
  if (query.value !== undefined && query.value !== "") {
    params.set("value", query.value);
  }
  if (query.q !== undefined && query.q.trim() !== "") {
    params.set("q", query.q.trim());
  }
  if (query.limit !== undefined && query.limit > 0) {
    params.set("limit", String(query.limit));
  }
  if (query.offset !== undefined && query.offset > 0) {
    params.set("offset", String(query.offset));
  }
  return `${endpoint}?${params.toString()}`;
};

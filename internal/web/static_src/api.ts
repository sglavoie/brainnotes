import { normalizeParamValue, notePathForKey, repeatedFilterParams } from "./filters";

export type RawRecord = Record<string, unknown>;

export type SearchResult = {
  key: string;
  path: string;
  title: string;
  folder: string;
  matched_fields: string[];
  issues: string[];
  snippet: string;
};

export type ApiFacetOption = {
  value: string;
  count: number;
};

export type ApiFacetIssueOption = ApiFacetOption & {
  param: string;
  label: string;
};

export type FacetsResponse = {
  tags: ApiFacetOption[];
  categories: ApiFacetOption[];
  status: ApiFacetOption[];
  types: ApiFacetOption[];
  folders: ApiFacetOption[];
  properties: ApiFacetOption[];
  issues: ApiFacetIssueOption[];
};

export type FacetGroup = {
  key?: keyof FacetsResponse;
  label: string;
  param?: string;
  repeat?: boolean;
  issue?: boolean;
};

export type FacetFilterOption = {
  param: string;
  value: string;
  label: string;
  count: number;
  groupLabel: string;
  repeat: boolean;
};

export type OpenNoteRequest = {
  key: string;
  token: string;
};

export type OpenNoteResult =
  | {
      ok: true;
    }
  | {
      ok: false;
      message: string;
    };

const asRecord = (value: unknown): RawRecord | null =>
  value && typeof value === "object" ? (value as RawRecord) : null;

const normalizeStringArray = (value: unknown): string[] =>
  Array.isArray(value)
    ? value.map((item) => normalizeParamValue(item)).filter((item) => item !== "")
    : [];

const readCount = (value: unknown): number => {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : 0;
};

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
        folder: normalizeParamValue(record.folder),
        matched_fields: normalizeStringArray(record.matched_fields),
        issues: normalizeStringArray(record.issues),
        snippet: normalizeParamValue(record.snippet),
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

export const facetGroups: FacetGroup[] = [
  { key: "tags", label: "Tags", param: "tag", repeat: true },
  { key: "categories", label: "Categories", param: "category", repeat: true },
  { key: "status", label: "Status", param: "status" },
  { key: "types", label: "Types", param: "type" },
  { key: "folders", label: "Folders", param: "folder" },
  { key: "properties", label: "Properties", param: "has" },
  { key: "issues", label: "Issues", issue: true },
];

export const labelForIssueParam = (param: string): string => {
  switch (param) {
    case "broken":
      return "Broken links";
    case "dead_end":
      return "Dead ends";
    case "orphan":
      return "Orphans";
    default:
      return param;
  }
};

export const fallbackGroupForParam = (param: string): FacetGroup => {
  for (const group of facetGroups) {
    if (group.issue) {
      continue;
    }
    if (group.param === param) {
      return group;
    }
  }
  if (param === "broken" || param === "dead_end" || param === "orphan") {
    return { label: "Issues", issue: true };
  }
  return { label: "Filters" };
};

export const fallbackFacetOption = (param: string, value: string): FacetFilterOption => {
  const group = fallbackGroupForParam(param);
  const label = group.issue ? labelForIssueParam(param) : value;
  return {
    param,
    value,
    label,
    count: 0,
    groupLabel: group.label,
    repeat: repeatedFilterParams.has(param),
  };
};

export const normalizeFacetOptions = (payload: unknown): FacetFilterOption[] => {
  const data = asRecord(payload) || {};
  const options: FacetFilterOption[] = [];
  for (const group of facetGroups) {
    const rawValues = group.key ? data[group.key] : undefined;
    const values: unknown[] = Array.isArray(rawValues) ? rawValues : [];
    for (const item of values) {
      const record = asRecord(item);
      if (!record) {
        continue;
      }
      const param = group.issue ? normalizeParamValue(record.param) : normalizeParamValue(group.param);
      const value = normalizeParamValue(record.value);
      if (param === "" || value === "") {
        continue;
      }
      options.push({
        param,
        value,
        label: group.issue ? normalizeParamValue(record.label) || labelForIssueParam(param) : value,
        count: readCount(record.count),
        groupLabel: group.label,
        repeat: Boolean(group.repeat),
      });
    }
  }
  return options.sort((left, right) => {
    if (left.groupLabel !== right.groupLabel) {
      return left.groupLabel.localeCompare(right.groupLabel);
    }
    if (right.count !== left.count) {
      return right.count - left.count;
    }
    return left.label.localeCompare(right.label);
  });
};

export const buildOpenNoteForm = (request: OpenNoteRequest): URLSearchParams => {
  const body = new URLSearchParams();
  body.set("key", request.key);
  body.set("token", request.token);
  return body;
};

export const normalizeOpenErrorMessage = (status: number, detail: unknown): string => {
  const message = normalizeParamValue(detail);
  return message || `Open failed (${status}).`;
};

export const normalizeOpenSuccess = (ok: boolean, status: number, detail: unknown): OpenNoteResult => {
  if (ok) {
    return { ok: true };
  }
  return {
    ok: false,
    message: normalizeOpenErrorMessage(status, detail),
  };
};

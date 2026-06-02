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

export type NoteKeyMutationRequest = {
  key: string;
  token: string;
};

export type CreateNoteRequest = NoteKeyMutationRequest & {
  target: string;
};

export type FrontmatterSetRequest = NoteKeyMutationRequest & {
  property: string;
  op: "=" | "+=" | "-=";
  value: string;
};

export type FrontmatterUnsetRequest = NoteKeyMutationRequest & {
  property: string;
};

export type BulkAction =
  | "trash"
  | "move"
  | "frontmatter_set"
  | "frontmatter_unset";

export type BulkNotesRequest = {
  action: BulkAction;
  keys: string[];
  token: string;
  folder?: string;
  property?: string;
  op?: "=" | "+=" | "-=";
  value?: string;
};

export type UndoRequest = {
  token: string;
  entryID?: string;
  force?: boolean;
  confirmForce?: boolean;
};

export type HistoryFile = {
  path: string;
  before: string;
  after: string;
};

export type HistoryEntry = {
  id: string;
  timestamp: string;
  command: string;
  file_count: number;
  files: HistoryFile[];
};

export type OpenNoteResult =
  | {
      ok: true;
    }
  | {
      ok: false;
      message: string;
    };

export type ManagementSuccessResult = {
  ok: true;
  message?: string;
  key?: string;
  path?: string;
  rel_path?: string;
  journal_id?: string;
};

export type ManagementErrorResult = {
  ok: false;
  code?: string;
  message?: string;
};

export type ManagementResult = ManagementSuccessResult | ManagementErrorResult;

export type BulkFailure = {
  key?: string;
  rel_path?: string;
  code?: string;
  message?: string;
};

export type BulkManagementResult = ManagementResult & {
  changed?: number;
  unchanged?: number;
  failed?: number;
  failures?: BulkFailure[];
};

export type HistoryResult = ManagementResult & {
  entries?: HistoryEntry[];
};

export type UndoChange = {
  path: string;
  current_state: string;
  restored_state: string;
};

export type UndoConflict = {
  path: string;
  expected_state: string;
  current_state: string;
};

export type UndoResult = ManagementResult & {
  entry_id?: string;
  command?: string;
  timestamp?: string;
  changes?: UndoChange[];
  conflicts?: UndoConflict[];
  requires_force?: boolean;
};

export const managementMutationEventName = "bn:management:mutated";

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

export const buildNoteKeyMutationForm = (
  request: NoteKeyMutationRequest
): URLSearchParams => {
  const body = new URLSearchParams();
  body.set("key", request.key);
  body.set("token", request.token);
  return body;
};

export const buildCreateNoteForm = (request: CreateNoteRequest): URLSearchParams => {
  const body = buildNoteKeyMutationForm(request);
  body.set("target", request.target);
  return body;
};

export const buildFrontmatterSetForm = (
  request: FrontmatterSetRequest
): URLSearchParams => {
  const body = buildNoteKeyMutationForm(request);
  body.set("property", request.property);
  body.set("op", request.op);
  body.set("value", request.value);
  return body;
};

export const buildFrontmatterUnsetForm = (
  request: FrontmatterUnsetRequest
): URLSearchParams => {
  const body = buildNoteKeyMutationForm(request);
  body.set("property", request.property);
  return body;
};

export const normalizeBulkNoteKeys = (keys: unknown[]): string[] => {
  const out: string[] = [];
  const seen = new Set<string>();
  for (const rawKey of keys) {
    const key = normalizeParamValue(rawKey);
    if (key === "") {
      continue;
    }
    const folded = key.toLowerCase();
    if (seen.has(folded)) {
      continue;
    }
    seen.add(folded);
    out.push(key);
  }
  return out;
};

export const buildBulkNotesForm = (request: BulkNotesRequest): URLSearchParams => {
  const body = new URLSearchParams();
  body.set("action", request.action);
  body.set("token", request.token);
  for (const key of normalizeBulkNoteKeys(request.keys)) {
    body.append("keys[]", key);
  }
  if (request.folder !== undefined) {
    body.set("folder", request.folder);
  }
  if (request.property !== undefined) {
    body.set("property", request.property);
  }
  if (request.op !== undefined) {
    body.set("op", request.op);
  }
  if (request.value !== undefined) {
    body.set("value", request.value);
  }
  return body;
};

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

export const buildOpenNoteForm = (request: OpenNoteRequest): URLSearchParams =>
  buildNoteKeyMutationForm(request);

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

export const normalizeManagementErrorMessage = (status: number, detail: unknown): string => {
  const record = asRecord(detail);
  if (record) {
    const message = normalizeParamValue(record.message);
    if (message) {
      return message;
    }
  }

  const message = normalizeParamValue(detail);
  return message || `Request failed (${status}).`;
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

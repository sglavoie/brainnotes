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

// Cockpit / Daily Fixes ------------------------------------------------------

export type CockpitAction = {
  kind: string;
  label: string;
};

export type CockpitAffectedNote = {
  title: string;
  rel_path: string;
  folder?: string;
  location?: string;
};

export type CockpitTask = {
  id: string;
  signature: string;
  kind: string;
  priority: number;
  title: string;
  summary: string;
  detail: string;
  count: number;
  affected: CockpitAffectedNote[];
  actions: CockpitAction[];
  triage_only: boolean;
};

export type CockpitCounts = {
  active: number;
  done_hidden: number;
  snoozed_hidden: number;
  total_generated: number;
};

export type CockpitWarning = {
  message: string;
  path?: string;
};

export type CockpitQueueResult = {
  ok: boolean;
  code?: string;
  message?: string;
  tasks: CockpitTask[];
  counts: CockpitCounts;
  warnings: CockpitWarning[];
};

export type CockpitItemAction = "mark_done" | "done" | "snooze" | "clear";

export type CockpitSnooze = "tomorrow" | "week";

export type CockpitItemMutationRequest = {
  id: string;
  action: CockpitItemAction;
  token: string;
  snooze?: CockpitSnooze;
};

export type FixAction = "schema_repair" | "relink" | "create_stub";

export type FixMutationRequest = {
  id: string;
  action: FixAction;
  token: string;
  previewID?: string;
};

export type NotePreviewText = {
  present: boolean;
  text: string;
};

export type NotePreviewChange = {
  kind: string;
  key?: string;
  line?: number;
  old: NotePreviewText;
  new: NotePreviewText;
};

export type NotePreview = {
  rel_path: string;
  changes: NotePreviewChange[];
};

export type FixSkip = {
  rel_path?: string;
  target?: string;
  reason: string;
};

export type FixPreviewResult = {
  ok: boolean;
  code?: string;
  message?: string;
  action: string;
  task_id: string;
  previews: NotePreview[];
  skipped: FixSkip[];
  preview_id: string;
  expires_at: string;
};

export type FixApplyResult = {
  ok: boolean;
  code?: string;
  message?: string;
  action: string;
  task_id: string;
  changed: string[];
  created: string[];
  skipped: FixSkip[];
  journal_id?: string;
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
  previewID?: string;
};

export type BulkPreviewRequest = BulkNotesRequest;

export type BulkApplyRequest = BulkNotesRequest & {
  previewID: string;
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

export type BulkManagementItem = {
  key?: string;
  path?: string;
  rel_path?: string;
  changed: boolean;
};

export type BulkManagementResult = ManagementResult & {
  changed?: number;
  unchanged?: number;
  failed?: number;
  results?: BulkManagementItem[];
  failures?: BulkFailure[];
};

export type BulkManagementPath = {
  key: string;
  path: string;
  rel_path: string;
};

export type BulkAffectedPath = {
  role: string;
  key: string;
  path: string;
  rel_path: string;
  exists: boolean;
};

export type BulkPreviewValue = {
  present: boolean;
  value?: unknown;
};

export type BulkFrontmatterChange = {
  key: string;
  before: BulkPreviewValue;
  after: BulkPreviewValue;
  changed: boolean;
};

export type BulkMovePreview = {
  rewrite_count: number;
  rewrite_file_count: number;
  body_rewrite_count: number;
  frontmatter_rewrite_count: number;
};

export type BulkPreviewRow = {
  key: string;
  path: string;
  rel_path: string;
  source: BulkManagementPath;
  destination?: BulkManagementPath;
  changed: boolean;
  move?: BulkMovePreview;
  frontmatter: BulkFrontmatterChange[];
  affected: BulkAffectedPath[];
};

export type BulkPreviewManagementResult = ManagementResult & {
  action?: BulkAction;
  changed?: number;
  unchanged?: number;
  failed?: number;
  preview_id?: string;
  expires_at?: string;
  results?: BulkPreviewRow[];
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

const readBoolean = (value: unknown): boolean => {
  if (typeof value === "boolean") {
    return value;
  }
  const normalized = normalizeParamValue(value).toLowerCase();
  return normalized === "true" || normalized === "1";
};

const normalizeBulkAction = (value: unknown): BulkAction | undefined => {
  switch (normalizeParamValue(value)) {
    case "trash":
    case "move":
    case "frontmatter_set":
    case "frontmatter_unset":
      return normalizeParamValue(value) as BulkAction;
    default:
      return undefined;
  }
};

const normalizeBulkManagementPath = (value: unknown): BulkManagementPath => {
  const record = asRecord(value) || {};
  return {
    key: normalizeParamValue(record.key),
    path: normalizeParamValue(record.path),
    rel_path: normalizeParamValue(record.rel_path),
  };
};

const normalizeBulkManagementItem = (value: unknown): BulkManagementItem | null => {
  const record = asRecord(value);
  if (!record) {
    return null;
  }
  return {
    key: normalizeParamValue(record.key) || undefined,
    path: normalizeParamValue(record.path) || undefined,
    rel_path: normalizeParamValue(record.rel_path) || undefined,
    changed: readBoolean(record.changed),
  };
};

export const normalizeBulkFailures = (value: unknown): BulkFailure[] => {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    const key = normalizeParamValue(record.key);
    const relPath = normalizeParamValue(record.rel_path);
    const code = normalizeParamValue(record.code);
    const message = normalizeParamValue(record.message);
    if (key === "" && relPath === "" && code === "" && message === "") {
      return [];
    }
    return [{
      key: key || undefined,
      rel_path: relPath || undefined,
      code: code || undefined,
      message: message || undefined,
    }];
  });
};

const normalizeBulkAffectedPaths = (value: unknown): BulkAffectedPath[] => {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    return [{
      role: normalizeParamValue(record.role),
      key: normalizeParamValue(record.key),
      path: normalizeParamValue(record.path),
      rel_path: normalizeParamValue(record.rel_path),
      exists: readBoolean(record.exists),
    }];
  });
};

export const normalizeBulkPreviewValue = (value: unknown): BulkPreviewValue => {
  const record = asRecord(value);
  if (!record) {
    return { present: false };
  }
  if (!readBoolean(record.present)) {
    return { present: false };
  }
  return {
    present: true,
    value: record.value,
  };
};

export const normalizeBulkFrontmatterChanges = (value: unknown): BulkFrontmatterChange[] => {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    const key = normalizeParamValue(record.key);
    if (key === "") {
      return [];
    }
    return [{
      key,
      before: normalizeBulkPreviewValue(record.before),
      after: normalizeBulkPreviewValue(record.after),
      changed: readBoolean(record.changed),
    }];
  });
};

const normalizeBulkMovePreview = (value: unknown): BulkMovePreview | undefined => {
  const record = asRecord(value);
  if (!record) {
    return undefined;
  }
  return {
    rewrite_count: readCount(record.rewrite_count),
    rewrite_file_count: readCount(record.rewrite_file_count),
    body_rewrite_count: readCount(record.body_rewrite_count),
    frontmatter_rewrite_count: readCount(record.frontmatter_rewrite_count),
  };
};

export const normalizeExpiryString = (value: unknown): string => {
  const raw = normalizeParamValue(value);
  if (raw === "") {
    return "";
  }
  const parsed = new Date(raw);
  return Number.isNaN(parsed.getTime()) ? "" : parsed.toISOString();
};

export const normalizeBulkPreviewRows = (value: unknown): BulkPreviewRow[] => {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.flatMap((item) => {
    const record = asRecord(item);
    if (!record) {
      return [];
    }
    const source = normalizeBulkManagementPath(record.source);
    const destinationRecord = asRecord(record.destination);
    return [{
      key: normalizeParamValue(record.key),
      path: normalizeParamValue(record.path),
      rel_path: normalizeParamValue(record.rel_path),
      source,
      destination: destinationRecord ? normalizeBulkManagementPath(destinationRecord) : undefined,
      changed: readBoolean(record.changed),
      move: normalizeBulkMovePreview(record.move),
      frontmatter: normalizeBulkFrontmatterChanges(record.frontmatter),
      affected: normalizeBulkAffectedPaths(record.affected),
    }];
  });
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

const buildBulkRequestForm = (request: BulkNotesRequest): URLSearchParams => {
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
  if (request.previewID !== undefined && request.previewID.trim() !== "") {
    body.set("preview_id", request.previewID.trim());
  }
  return body;
};

export const buildBulkNotesForm = (request: BulkNotesRequest): URLSearchParams =>
  buildBulkRequestForm(request);

export const buildBulkPreviewForm = (request: BulkPreviewRequest): URLSearchParams =>
  buildBulkRequestForm(request);

export const buildBulkApplyForm = (request: BulkApplyRequest): URLSearchParams =>
  buildBulkRequestForm(request);

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

const normalizeManagementResult = (payload: unknown): ManagementResult => {
  const record = asRecord(payload) || {};
  return readBoolean(record.ok)
    ? {
        ok: true,
        message: normalizeParamValue(record.message) || undefined,
        key: normalizeParamValue(record.key) || undefined,
        path: normalizeParamValue(record.path) || undefined,
        rel_path: normalizeParamValue(record.rel_path) || undefined,
        journal_id: normalizeParamValue(record.journal_id) || undefined,
      }
    : {
        ok: false,
        code: normalizeParamValue(record.code) || undefined,
        message: normalizeParamValue(record.message) || undefined,
      };
};

export const normalizeBulkManagementResult = (payload: unknown): BulkManagementResult => {
  const base = normalizeManagementResult(payload);
  const record = asRecord(payload) || {};
  return {
    ...base,
    changed: readCount(record.changed),
    unchanged: readCount(record.unchanged),
    failed: readCount(record.failed),
    results: Array.isArray(record.results)
      ? record.results.flatMap((item) => {
          const normalized = normalizeBulkManagementItem(item);
          return normalized ? [normalized] : [];
        })
      : [],
    failures: normalizeBulkFailures(record.failures),
  };
};

export const normalizeBulkPreviewManagementResult = (
  payload: unknown
): BulkPreviewManagementResult => {
  const base = normalizeManagementResult(payload);
  const record = asRecord(payload) || {};
  return {
    ...base,
    action: normalizeBulkAction(record.action),
    changed: readCount(record.changed),
    unchanged: readCount(record.unchanged),
    failed: readCount(record.failed),
    preview_id: normalizeParamValue(record.preview_id) || undefined,
    expires_at: normalizeExpiryString(record.expires_at) || undefined,
    results: normalizeBulkPreviewRows(record.results),
    failures: normalizeBulkFailures(record.failures),
  };
};

export const normalizeManagementErrorMessage = (status: number, detail: unknown): string => {
  const result = normalizeManagementResult(detail);
  if (!result.ok && result.message) {
    return result.message;
  }

  const message = asRecord(detail) ? "" : normalizeParamValue(detail);
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

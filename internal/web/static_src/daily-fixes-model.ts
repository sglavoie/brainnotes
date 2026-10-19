import type {
  CockpitAffectedNote,
  CockpitCounts,
  CockpitItemAction,
  CockpitSnooze,
  CockpitTask,
  FixAction,
} from "./api";

export type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

export type DailyFixesDeps = {
  fetch?: FetchLike;
};

export type DailyFixesConfig = {
  queueEndpoint: string;
  itemEndpoint: string;
  previewEndpoint: string;
  applyEndpoint: string;
  openEndpoint: string;
  csrfToken: string;
};

export type FixesFetchRequest = {
  url: string;
  init: RequestInit;
};

export type PreviewState = {
  action: FixAction;
  previewID: string;
};

export const FIX_ACTIONS = new Set<string>(["schema_repair", "relink", "create_stub"]);
export const IMMEDIATE_ITEM_ACTIONS = new Set<string>(["exempt_dead_end", "clean_long_tail"]);

export const readDailyFixesConfig = (root: HTMLElement): DailyFixesConfig => ({
  queueEndpoint: root.getAttribute("data-fixes-endpoint") || "/api/cockpit",
  itemEndpoint: root.getAttribute("data-fixes-item-endpoint") || "/api/cockpit/item",
  previewEndpoint: root.getAttribute("data-fixes-preview-endpoint") || "/api/fixes/preview",
  applyEndpoint: root.getAttribute("data-fixes-apply-endpoint") || "/api/fixes/apply",
  openEndpoint: root.getAttribute("data-fixes-open-endpoint") || "/api/open",
  csrfToken: root.getAttribute("data-fixes-csrf") || "",
});

// fixOpenKeyFromRelPath derives the route key `/api/open` expects from a task's
// affected note rel_path. It mirrors the server's managementRouteKeyForRelPath:
// drop a trailing `.md`, lower-case, and reject traversal or empty segments.
export const fixOpenKeyFromRelPath = (relPath: string): string => {
  const trimmed = (relPath || "").trim().replace(/\\/g, "/");
  if (trimmed === "") {
    return "";
  }
  const withoutExt = trimmed.replace(/\.md$/i, "");
  const segments: string[] = [];
  for (const segment of withoutExt.split("/")) {
    if (segment === "" || segment === "." || segment === "..") {
      return "";
    }
    segments.push(segment.toLowerCase());
  }
  return segments.join("/");
};

const fixKindLabels: ReadonlyMap<string, string> = new Map([
  ["frontmatter_error", "Frontmatter"],
  ["schema_violation", "Schema"],
  ["broken_target", "Broken target"],
  ["duplicate_title", "Duplicate title"],
  ["orphan_note", "Orphan"],
  ["dead_end_note", "Dead end"],
  ["long_tail_value", "Long-tail value"],
  ["sensitive_content", "Sensitive content"],
  ["missing_review_date", "Missing review date"],
  ["stale_status", "Stale status"],
  ["wip_limit", "WIP limit"],
  ["hub_needs_synthesis", "Hub needs synthesis"],
  ["capture_backlog", "Capture backlog"],
  ["ai_daily_review", "AI daily review"],
]);

export const fixKindLabel = (kind: string): string => fixKindLabels.get(kind) ?? "Fix";

export const fixTaskLabel = (task: CockpitTask): string =>
  task.title || task.summary || task.id || "Fix";

export const affectedNoteLabel = (note: CockpitAffectedNote): string =>
  note.title || note.rel_path || "Affected note";

export const fixCountsStatusMessage = (counts: CockpitCounts): string => {
  if (counts.active === 1) {
    return "1 active fix.";
  }
  if (counts.active > 1) {
    return `${counts.active} active fixes.`;
  }
  return "No active fixes.";
};

export const normalizeItemAction = (value: string): CockpitItemAction | "" => {
  switch (value) {
    case "mark_done":
    case "done":
    case "snooze":
    case "clear":
    case "exempt_dead_end":
    case "clean_long_tail":
      return value;
    default:
      return "";
  }
};

export const normalizeSnooze = (value: string): CockpitSnooze | undefined => {
  switch (value) {
    case "tomorrow":
    case "week":
      return value;
    default:
      return undefined;
  }
};

export const normalizeFixAction = (value: string): FixAction | "" => {
  switch (value) {
    case "schema_repair":
    case "relink":
    case "create_stub":
      return value;
    default:
      return "";
  }
};

export const itemActionLoadingMessage = (
  action: CockpitItemAction,
  snooze?: CockpitSnooze
): string => {
  if (action === "snooze") {
    return snooze === "week"
      ? "Snoozing fix for a week…"
      : "Snoozing fix until tomorrow…";
  }
  if (action === "clear") {
    return "Clearing fix state…";
  }
  if (action === "exempt_dead_end") {
    return "Marking note as an intentional dead end…";
  }
  if (action === "clean_long_tail") {
    return "Removing property from affected notes…";
  }
  return "Marking fix done…";
};

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

export const fixKindLabel = (kind: string): string => {
  switch (kind) {
    case "frontmatter_error":
      return "Frontmatter";
    case "schema_violation":
      return "Schema";
    case "broken_target":
      return "Broken target";
    case "duplicate_title":
      return "Duplicate title";
    case "orphan_note":
      return "Orphan";
    case "dead_end_note":
      return "Dead end";
    case "long_tail_value":
      return "Long-tail value";
    case "sensitive_content":
      return "Sensitive content";
    case "missing_review_date":
      return "Missing review date";
    case "stale_status":
      return "Stale status";
    case "wip_limit":
      return "WIP limit";
    case "hub_needs_synthesis":
      return "Hub needs synthesis";
    case "ai_daily_review":
      return "AI daily review";
    default:
      return "Fix";
  }
};

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
      ? "Snoozing fix for a week..."
      : "Snoozing fix until tomorrow...";
  }
  if (action === "clear") {
    return "Clearing fix state...";
  }
  if (action === "exempt_dead_end") {
    return "Marking note as an intentional dead end...";
  }
  return "Marking fix done...";
};

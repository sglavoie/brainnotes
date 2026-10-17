import { queryShapeParamKeys, sharedFilterParamKeys, normalizeParamValue } from "./filters";
import type {
  PropertyConfigDiffLine,
  SavedViewDefinition,
  SavedViewSummary,
  SavedViewsResult,
} from "./api-types";

export type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

export type SavedViewsDeps = {
  fetch?: FetchLike;
};

/**
 * The pages that hydrate a query bar and therefore apply an expression from the
 * URL. A saved view opens on the current page when it is one of them, so
 * following a view from the graph keeps you on the graph; from anywhere else it
 * opens the Library, which is where a filtered result list lives.
 */
export const savedViewsQueryPages = ["/library", "/graph"] as const;

export const savedViewsNavBasePath = (pathname: unknown): string => {
  const normalized = normalizeParamValue(pathname).replace(/\/+$/, "");
  return savedViewsQueryPages.find((page) => page === normalized) || "/library";
};

/**
 * Renders a stored cap as its URL value. Anything that is not a positive whole
 * number — 0, a negative, a non-number crossing the wire — is "no cap" and
 * produces the empty string, which savedViewParams drops.
 */
export const savedViewLimitParam = (limit: unknown): string => {
  const value = Number(limit);
  if (!Number.isFinite(value) || !Number.isInteger(value) || value <= 0) {
    return "";
  }
  return String(value);
};

/**
 * The URL parameters one view applies.
 *
 * A view is a complete query shape, so a link carries only its own `q`, `expr`,
 * `sort`, `relative_to` and `limit` and no chips: following a view lands you on
 * exactly what was saved rather than on the view intersected with whatever was
 * already selected.
 *
 * `limit` is part of that shape. Omitting it was the whole defect this replaces:
 * a view saved as "the ten most recently modified" opened showing every match,
 * so the nav reported a view as enabled while running something wider than its
 * stored definition. A limit of 0 means "no cap" and is left out of the link.
 */
export const savedViewParams = (view: SavedViewSummary): URLSearchParams => {
  const params = new URLSearchParams();
  const values: Record<(typeof queryShapeParamKeys)[number], string> = {
    expr: view.search && view.query === '""' ? "" : normalizeParamValue(view.query),
    sort: normalizeParamValue(view.sort),
    relative_to: normalizeParamValue(view.relative_to),
    limit: savedViewLimitParam(view.limit),
  };
  for (const key of queryShapeParamKeys) {
    if (values[key] !== "") {
      params.set(key, values[key]);
    }
  }
  if (view.search) params.set("q", view.search);
  return params;
};

export const savedViewHref = (view: SavedViewSummary, basePath: string): string => {
  const query = savedViewParams(view).toString();
  return query ? `${basePath}?${query}` : basePath;
};

/**
 * The hover text for one nav item. A disabled view shows why it is disabled —
 * the session 11 contract that a rotted view is never silently dropped — and an
 * enabled one shows what it will do.
 */
export const savedViewTitle = (view: SavedViewSummary): string => {
  if (!view.enabled) {
    return view.detail || `${view.name} no longer compiles.`;
  }
  const parts = [view.description, view.normalized || view.query].filter(
    (part) => normalizeParamValue(part) !== ""
  );
  return parts.join(" — ");
};

export type SavedViewSaveInput = {
  filterParams?: string;
  name: string;
  description: string;
  query: string;
  sort: string;
  relativeTo: string;
  limit: number;
};

/** Preserve the complete URL selection; the server compiles facets into the
 * shared query language before issuing a preview receipt. */
export const savedViewInputFromParams = (
  params: URLSearchParams,
  fields: { name: string; description: string }
): SavedViewSaveInput => ({
  name: normalizeParamValue(fields.name),
  description: normalizeParamValue(fields.description),
  query: normalizeParamValue(params.get("expr")),
  filterParams: savedSearchParams(params),
  sort: normalizeParamValue(params.get("sort")),
  relativeTo: normalizeParamValue(params.get("relative_to")),
  limit: savedSearchLimit(params),
});

export const savedViewDefinition = (input: SavedViewSaveInput): SavedViewDefinition => ({
  query: input.query,
  ...(input.filterParams ? { filter_params: input.filterParams } : {}),
  sort: input.sort,
  relative_to: input.relativeTo,
  limit: input.limit,
  description: input.description,
});

/**
 * Rejects a save the server would refuse anyway, so the obvious mistakes cost
 * no round trip. Everything subtler than these two — the name grammar, whether
 * the expression still compiles — stays the server's call.
 */
export const savedViewSaveError = (input: SavedViewSaveInput): string => {
  if (input.name === "") {
    return "Name the view before saving it.";
  }
  if (input.query === "" && !input.filterParams) {
    return "Choose a text search, filter, or query expression before saving a view.";
  }
  return "";
};

// SavedViewPendingSave is an issued receipt held between the preview response
// and the explicit confirmation an overwrite needs.
export type SavedViewPendingSave = {
  name: string;
  previewID: string;
  expiresAt: string;
  created: boolean;
  diff: PropertyConfigDiffLine[];
  result: SavedViewsResult;
};

export const savedViewPendingSave = (
  name: string,
  result: SavedViewsResult
): SavedViewPendingSave => ({
  name,
  previewID: result.preview_id,
  expiresAt: result.expires_at,
  created: result.created,
  diff: result.config_diff,
  result,
});

/**
 * The record a create leaves behind once its write has landed: the diff that
 * was already applied, kept so the auto-chained create still *shows* what it
 * wrote instead of discarding it.
 *
 * It deliberately carries no `previewID`. The receipt the server issued was
 * consumed by the apply that produced this, so there is nothing left to
 * re-apply, and the type makes that structural rather than a rule the UI has to
 * remember: nothing downstream can reach a preview ID through it.
 */
export type SavedViewCreateReceipt = {
  name: string;
  diff: PropertyConfigDiffLine[];
};

export const savedViewCreateReceipt = (
  pending: SavedViewPendingSave
): SavedViewCreateReceipt => ({
  name: pending.name,
  diff: pending.diff,
});

export const savedViewOverwriteMessage = (name: string): string =>
  `"${name}" already exists. Review the diff, then confirm the overwrite.`;

export const savedViewSavedMessage = (name: string, created: boolean): string =>
  created ? `Saved view "${name}".` : `Updated view "${name}".`;

export const savedViewNoChangeMessage = (name: string): string =>
  `"${name}" is already saved exactly like this.`;

/**
 * Renders a config diff as the unified text a confirmation shows.
 *
 * `config.PatchLineKind` is spelled as the marker itself (`"+"` / `"-"`), which
 * is what crosses the wire; the word forms are accepted too so the renderer
 * cannot silently degrade to an unmarked block if the spelling ever changes.
 */
export const savedViewDiffText = (diff: PropertyConfigDiffLine[]): string =>
  diff
    .map((line) => {
      const marker = line.kind === "-" || line.kind === "remove" ? "-" : "+";
      return `${marker} ${line.text}`;
    })
    .join("\n");

const savedSearchParams = (params: URLSearchParams): string => {
  const selected = new URLSearchParams();
  for (const key of ["q", "expr", ...sharedFilterParamKeys]) {
    for (const value of params.getAll(key)) {
      if (value.trim() || ["orphan", "broken", "dead_end"].includes(key)) selected.append(key, value);
    }
  }
  return selected.toString();
};

const savedSearchLimit = (params: URLSearchParams): number => {
  const requested = Number(savedViewLimitParam(params.get("limit")) || 0);
  return window.location.pathname === "/library" ? Math.min(requested || 50, 50) : requested;
};

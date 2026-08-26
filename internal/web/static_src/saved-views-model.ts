import { queryShapeParamKeys, normalizeParamValue } from "./filters";
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
 * The URL parameters one view applies.
 *
 * A view is a complete query shape, so a link carries only its own `expr`,
 * `sort` and `relative_to` and no chips: following a view lands you on exactly
 * what was saved rather than on the view intersected with whatever was already
 * selected.
 */
export const savedViewParams = (view: SavedViewSummary): URLSearchParams => {
  const params = new URLSearchParams();
  const values: Record<(typeof queryShapeParamKeys)[number], string> = {
    expr: normalizeParamValue(view.query),
    sort: normalizeParamValue(view.sort),
    relative_to: normalizeParamValue(view.relative_to),
  };
  for (const key of queryShapeParamKeys) {
    if (values[key] !== "") {
      params.set(key, values[key]);
    }
  }
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
  name: string;
  description: string;
  query: string;
  sort: string;
  relativeTo: string;
};

/**
 * Reads the query shape a save would store from a URL. `expr` is the view's
 * query; `sort` and `relative_to` ride along so a view can carry an ordering
 * even where the page cannot yet produce one.
 */
export const savedViewInputFromParams = (
  params: URLSearchParams,
  fields: { name: string; description: string }
): SavedViewSaveInput => ({
  name: normalizeParamValue(fields.name),
  description: normalizeParamValue(fields.description),
  query: normalizeParamValue(params.get("expr")),
  sort: normalizeParamValue(params.get("sort")),
  relativeTo: normalizeParamValue(params.get("relative_to")),
});

export const savedViewDefinition = (input: SavedViewSaveInput): SavedViewDefinition => ({
  query: input.query,
  sort: input.sort,
  relative_to: input.relativeTo,
  limit: 0,
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
  if (input.query === "") {
    return "Write a query expression before saving it as a view.";
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

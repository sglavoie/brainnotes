export const filterChangeEventName = "bn:filterschange";
export const browserQueryChangeEventName = "bn:querychange";

export type FiltersChangeDetail = {
  mode: string;
  params: URLSearchParams;
};

export type FiltersChangeEvent = CustomEvent<FiltersChangeDetail>;

declare global {
  interface WindowEventMap {
    [filterChangeEventName]: FiltersChangeEvent;
  }
}

export const sharedFilterParamKeys = [
  "tag",
  "category",
  "status",
  "type",
  "folder",
  "has",
  "missing",
  "orphan",
  "broken",
  "dead_end",
  "age",
  "created",
  "min_backlinks",
  "max_backlinks",
  "min_links",
  "max_links",
] as const;

export type SharedFilterParamKey = (typeof sharedFilterParamKeys)[number];

/**
 * Parameters that shape a query without being facet chips.
 *
 * `limit` is one of them: a saved view persists a cap, and a link that dropped
 * it would open the view showing more rows than the view was saved as. It rides
 * along here rather than in the chip vocabulary for the same reason `sort` does
 * — it shapes a result set without selecting one.
 *
 * They are deliberately **not** in `sharedFilterParamKeys`: that tuple is the
 * chip vocabulary, and `facets.ts` deletes every key in it before writing the
 * current chips back to the URL — an entry here would be erased by the next
 * chip toggle. They are single-valued, they ride along in every filtered API
 * URL, and `updateBrowserQuery` preserves them, so a Library or Graph link
 * stays shareable with its expression and ordering intact.
 */
export const queryShapeParamKeys = ["expr", "sort", "relative_to", "limit"] as const;

export type QueryShapeParamKey = (typeof queryShapeParamKeys)[number];

/** The URL parameter carrying the query expression. */
export const queryExprParam: QueryShapeParamKey = "expr";

export const repeatedFilterParams = new Set<string>(["tag", "category"]);

export const normalizeParamValue = (value: unknown): string => String(value || "").trim();

export const notePathForKey = (key: unknown): string => {
  if (!key) {
    return "";
  }
  const segments = String(key).split("/");
  const escaped = segments.map((segment) => encodeURIComponent(segment));
  return `/n/${escaped.join("/")}`;
};

export const readPageQuery = (): string => {
  const params = new URLSearchParams(window.location.search);
  return normalizeParamValue(params.get("q"));
};

export const hasActiveSharedFilters = (params?: URLSearchParams): boolean => {
  const source = params || new URLSearchParams(window.location.search);
  return sharedFilterParamKeys.some((key) =>
    source.getAll(key).some((value) => normalizeParamValue(value) !== "")
  );
};

/**
 * Reads one query-shape parameter, defaulting to the current browser URL.
 */
export const readQueryShapeParam = (
  key: QueryShapeParamKey,
  params?: URLSearchParams
): string =>
  normalizeParamValue((params || new URLSearchParams(window.location.search)).get(key));

export const readExprParam = (params?: URLSearchParams): string =>
  readQueryShapeParam(queryExprParam, params);

/**
 * Writes one query-shape parameter to the browser URL and reports whether the
 * URL changed. A blank value removes the parameter rather than writing an empty
 * one, so a cleared expression leaves a clean link.
 */
export const syncQueryShapeParam = (key: QueryShapeParamKey, value: unknown): boolean => {
  const params = new URLSearchParams(window.location.search);
  const normalized = normalizeParamValue(value);
  if (normalizeParamValue(params.get(key)) === normalized) {
    return false;
  }
  if (normalized === "") {
    params.delete(key);
  } else {
    params.set(key, normalized);
  }
  updateBrowserQuery(params);
  return true;
};

/**
 * Writes the expression to the browser URL and reports whether it changed.
 *
 * Only a server-confirmed expression is ever published, so a shared URL cannot
 * carry a query that does not compile.
 */
export const syncExprParam = (expr: unknown): boolean =>
  syncQueryShapeParam(queryExprParam, expr);

/**
 * Reports whether anything other than the free-text query narrows the results.
 *
 * `sort`, `relative_to`, and `limit` are excluded on purpose: they order and
 * cap a result set, they do not select one, so an ordering or a cap alone must
 * not make an empty search box fetch the whole vault. `expr` is included because the server treats it as
 * a structural filter exactly like a chip.
 */
export const hasActiveFilters = (params?: URLSearchParams): boolean => {
  const source = params || new URLSearchParams(window.location.search);
  return hasActiveSharedFilters(source) || readExprParam(source) !== "";
};

export const copyQueryShapeParams = (
  fromParams: URLSearchParams,
  toParams: URLSearchParams
): void => {
  for (const key of queryShapeParamKeys) {
    const normalized = normalizeParamValue(fromParams.get(key));
    if (normalized !== "") {
      toParams.set(key, normalized);
    }
  }
};

/**
 * Copies the whole URL-carried filter state: the facet chips first, then the
 * query-shape parameters. Both halves live here rather than in the callers so
 * `buildFilteredAPIURL` — and through it `buildSearchAPIURL` and the graph —
 * cannot disagree about what a filtered request carries.
 */
export const copySharedFilterParams = (
  fromParams: URLSearchParams,
  toParams: URLSearchParams
): void => {
  for (const key of sharedFilterParamKeys) {
    for (const value of fromParams.getAll(key)) {
      const normalized = normalizeParamValue(value);
      if (normalized !== "") {
        toParams.append(key, normalized);
      }
    }
  }
  copyQueryShapeParams(fromParams, toParams);
};

export const updateBrowserQuery = (params: URLSearchParams): void => {
  if (!window.history || typeof window.history.replaceState !== "function") {
    return;
  }
  const query = params.toString();
  const nextURL = `${window.location.pathname}${query ? `?${query}` : ""}${window.location.hash}`;
  const currentURL = `${window.location.pathname}${window.location.search}${window.location.hash}`;
  if (nextURL !== currentURL) {
    window.history.replaceState(null, "", nextURL);
    window.dispatchEvent(new Event(browserQueryChangeEventName));
  }
};

export const syncPageQueryParam = (query: unknown): void => {
  const params = new URLSearchParams(window.location.search);
  const normalized = normalizeParamValue(query);
  if (normalized === "") {
    params.delete("q");
  } else {
    params.set("q", normalized);
  }
  updateBrowserQuery(params);
};

export const buildFilteredAPIURL = (path: string, query: unknown): string => {
  const params = new URLSearchParams();
  const normalizedQuery = normalizeParamValue(query);
  if (normalizedQuery !== "") {
    params.set("q", normalizedQuery);
  }
  copySharedFilterParams(new URLSearchParams(window.location.search), params);
  const queryString = params.toString();
  return queryString ? `${path}?${queryString}` : path;
};

const buildSearchURL = (path: string, query: unknown, includeSharedFilters: boolean): string => {
  if (includeSharedFilters) {
    return buildFilteredAPIURL(path, query);
  }
  const params = new URLSearchParams();
  const normalized = normalizeParamValue(query);
  if (normalized !== "") {
    params.set("q", normalized);
  }
  const queryString = params.toString();
  return queryString ? `${path}?${queryString}` : path;
};

export const buildSearchAPIURL = (query: unknown, includeSharedFilters: boolean): string =>
  buildSearchURL("/api/search", query, includeSharedFilters);

export const buildLibrarySearchURL = (query: unknown, includeSharedFilters: boolean): string =>
  buildSearchURL("/library", query, includeSharedFilters);

export const dispatchFiltersChange = (target: Window, detail: FiltersChangeDetail): void => {
  target.dispatchEvent(new CustomEvent(filterChangeEventName, { detail }));
};

export const shouldHandleFilterChangeForMode = (
  event: FiltersChangeEvent,
  mode: string
): boolean => !event.detail.mode || event.detail.mode === mode;

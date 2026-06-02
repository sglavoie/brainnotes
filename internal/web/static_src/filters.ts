export const filterChangeEventName = "bn:filterschange";

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

export const buildSearchAPIURL = (query: unknown, includeSharedFilters: boolean): string => {
  if (includeSharedFilters) {
    return buildFilteredAPIURL("/api/search", query);
  }
  const params = new URLSearchParams();
  const normalized = normalizeParamValue(query);
  if (normalized !== "") {
    params.set("q", normalized);
  }
  const queryString = params.toString();
  return queryString ? `/api/search?${queryString}` : "/api/search";
};

export const dispatchFiltersChange = (target: Window, detail: FiltersChangeDetail): void => {
  target.dispatchEvent(new CustomEvent(filterChangeEventName, { detail }));
};

export const shouldHandleFilterChangeForMode = (
  event: FiltersChangeEvent,
  mode: string
): boolean => !event.detail.mode || event.detail.mode === mode;

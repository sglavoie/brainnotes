import type { SavedViewSummary } from "./api-types";
import { hasActiveSharedFilters, normalizeParamValue, queryShapeParamKeys } from "./filters";
import { savedViewParams, savedViewsQueryPages } from "./saved-views-model";

// Compare the stored spelling and the server's canonical spelling, without
// attempting to interpret the query language again in the browser.
export const savedViewIsCurrent = (view: SavedViewSummary, url: URL): boolean => {
  if (!view.enabled || !savedViewsQueryPages.some((page) => page === url.pathname.replace(/\/+$/, ""))) {
    return false;
  }
  const params = url.searchParams;
  if (normalizeParamValue(params.get("q")) || hasActiveSharedFilters(params)) return false;
  const expected = savedViewParams(view);
  return queryShapeParamKeys.every((key) => {
    const actual = normalizeParamValue(params.get(key));
    if (key === "expr") {
      return actual !== "" && (actual === expected.get(key) || actual === normalizeParamValue(view.normalized));
    }
    if (key === "limit" && (actual === "" || actual === "0")) return !expected.has(key);
    return actual === (expected.get(key) || "");
  });
};

export const syncCurrentSavedView = (container: HTMLElement, views: SavedViewSummary[]): void => {
  const url = new URL(window.location.href);
  for (const link of container.querySelectorAll<HTMLAnchorElement>("a[data-saved-view]")) {
    const view = views.find((view) => view.name === link.dataset.savedView);
    if (view && savedViewIsCurrent(view, url)) link.setAttribute("aria-current", "true");
    else link.removeAttribute("aria-current");
  }
};

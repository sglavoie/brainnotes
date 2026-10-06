import { buildHistoryQuery, type HistoryQuery } from "./api";
import type { HistoryWorkspaceElements } from "./history-workspace-elements";

const filterValues = (params: URLSearchParams, key: string): string[] =>
  [...new Set(params.getAll(key).map((value) => value.trim()).filter(Boolean))];

const urlOffset = (value: string | null): number => {
  const offset = Number(value);
  return Number.isSafeInteger(offset) && offset >= 0 ? offset : 0;
};

export const restoreHistoryURL = (elements: HistoryWorkspaceElements): HistoryQuery => {
  const params = new URLSearchParams(window.location.search);
  const select = elements.pageSizeSelect;
  const requested = params.get("limit") || String(elements.pageSize);
  if (select) {
    const supported = [...select.options].some((option) =>
      option.value === requested && Number(option.value) <= elements.maxPageSize);
    select.value = supported ? requested : String(elements.pageSize);
  }
  if (elements.path) elements.path.value = params.get("path") || "";
  if (elements.pathMatch) elements.pathMatch.value = params.get("path_match") === "exact" ? "exact" : "contains";
  const origins = filterValues(params, "origin");
  const classes = filterValues(params, "class");
  restoreChecks(elements.originFilters, origins);
  restoreChecks(elements.classFilters, classes);
  return { origins, classes, offset: urlOffset(params.get("offset")) };
};

const restoreChecks = (root: HTMLElement | null, values: string[]): void => {
  for (const input of root?.querySelectorAll<HTMLInputElement>('input[type="checkbox"]') || []) {
    input.checked = values.includes(input.value);
  }
};

export const writeHistoryURL = (query: HistoryQuery, mode: "push" | "replace"): void => {
  const url = new URL(window.location.href);
  for (const key of ["limit", "offset", "origin", "class", "path", "path_match"]) url.searchParams.delete(key);
  for (const [key, value] of buildHistoryQuery(query)) url.searchParams.append(key, value);
  if (url.href === window.location.href) return;
  if (mode === "push") window.history.pushState(null, "", url);
  else window.history.replaceState(window.history.state, "", url);
};

import { queryElement } from "./dom";

/**
 * Every element and every endpoint the /history workspace reads, found once.
 *
 * The endpoints and the page sizes are read from the markup rather than
 * compiled in, for the same reason the filter vocabulary is: the server decides
 * what the maximum page is and what the routes are called, and a second copy
 * here would be a second opinion that can be wrong.
 */
export type HistoryWorkspaceElements = {
  root: HTMLElement;
  listEndpoint: string;
  previewEndpoint: string;
  undoEndpoint: string;
  token: string;
  pageSize: number;
  maxPageSize: number;

  filters: HTMLFormElement | null;
  path: HTMLInputElement | null;
  pathMatch: HTMLSelectElement | null;
  originFilters: HTMLElement | null;
  classFilters: HTMLElement | null;
  pageSizeSelect: HTMLSelectElement | null;
  refresh: HTMLButtonElement | null;
  clearFilters: HTMLButtonElement | null;

  status: HTMLElement;
  warnings: HTMLElement;
  list: HTMLElement;
  pager: HTMLElement | null;
  previous: HTMLButtonElement | null;
  range: HTMLElement | null;
  next: HTMLButtonElement | null;

  restore: HTMLElement | null;
  restoreSummary: HTMLElement | null;
  restoreFacts: HTMLElement | null;
  restoreChanges: HTMLElement | null;
  restoreConflicts: HTMLElement | null;
  restoreConflictList: HTMLElement | null;
  restoreConsent: HTMLInputElement | null;
  restoreConfirm: HTMLButtonElement | null;
  restoreCancel: HTMLButtonElement | null;
};

const readSize = (root: HTMLElement, name: string, fallback: number): number => {
  const raw = Number.parseInt(root.getAttribute(name) || "", 10);
  return Number.isFinite(raw) && raw > 0 ? raw : fallback;
};

export const findHistoryWorkspaceElements = (
  doc: Document
): HistoryWorkspaceElements | null => {
  const root = queryElement(doc, "[data-history-workspace]", HTMLElement);
  if (!root) {
    return null;
  }
  const status = queryElement(root, "[data-history-status]", HTMLElement);
  const warnings = queryElement(root, "[data-history-warnings]", HTMLElement);
  const list = queryElement(root, "[data-history-list]", HTMLElement);
  if (!status || !warnings || !list) {
    return null;
  }
  const pageSize = readSize(root, "data-history-page-size", 20);
  return {
    root,
    listEndpoint: root.getAttribute("data-history-endpoint") || "",
    previewEndpoint: root.getAttribute("data-history-preview-endpoint") || "",
    undoEndpoint: root.getAttribute("data-history-undo-endpoint") || "",
    token: root.getAttribute("data-history-csrf") || "",
    pageSize,
    maxPageSize: readSize(root, "data-history-max-page-size", pageSize),

    filters: queryElement(root, "[data-history-filters]", HTMLFormElement),
    path: queryElement(root, "[data-history-path]", HTMLInputElement),
    pathMatch: queryElement(root, "[data-history-path-match]", HTMLSelectElement),
    originFilters: queryElement(root, "[data-history-origin-filters]", HTMLElement),
    classFilters: queryElement(root, "[data-history-class-filters]", HTMLElement),
    pageSizeSelect: queryElement(root, "[data-history-page-size-select]", HTMLSelectElement),
    refresh: queryElement(root, "[data-history-refresh]", HTMLButtonElement),
    clearFilters: queryElement(root, "[data-history-clear-filters]", HTMLButtonElement),

    status,
    warnings,
    list,
    pager: queryElement(root, "[data-history-pager]", HTMLElement),
    previous: queryElement(root, "[data-history-previous]", HTMLButtonElement),
    range: queryElement(root, "[data-history-range]", HTMLElement),
    next: queryElement(root, "[data-history-next]", HTMLButtonElement),

    restore: queryElement(root, "[data-history-restore]", HTMLElement),
    restoreSummary: queryElement(root, "[data-history-restore-summary]", HTMLElement),
    restoreFacts: queryElement(root, "[data-history-restore-facts]", HTMLElement),
    restoreChanges: queryElement(root, "[data-history-restore-changes]", HTMLElement),
    restoreConflicts: queryElement(root, "[data-history-restore-conflicts]", HTMLElement),
    restoreConflictList: queryElement(root, "[data-history-restore-conflict-list]", HTMLElement),
    restoreConsent: queryElement(root, "[data-history-restore-consent]", HTMLInputElement),
    restoreConfirm: queryElement(root, "[data-history-restore-confirm]", HTMLButtonElement),
    restoreCancel: queryElement(root, "[data-history-restore-cancel]", HTMLButtonElement),
  };
};

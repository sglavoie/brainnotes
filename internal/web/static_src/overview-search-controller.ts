import { normalizeBulkNoteKeys, type BulkAction } from "./api";
import { normalizeParamValue, readPageQuery } from "./filters";
import { bulkActionLabel } from "./overview-search-model";

export const overviewSearchQuery = (input: HTMLInputElement | null): string =>
  input instanceof HTMLInputElement ? normalizeParamValue(input.value) : readPageQuery();

export const selectedOverviewKeys = (selectedKeys: Map<string, string>): string[] =>
  normalizeBulkNoteKeys(Array.from(selectedKeys.keys()));

export const setOverviewBusy = (root: HTMLElement, busy: boolean): void => {
  root.setAttribute("aria-busy", busy ? "true" : "false");
};

export const syncOverviewActionButton = (
  button: HTMLButtonElement | null,
  action: BulkAction,
  applyReady: boolean
): void => {
  if (!button) {
    return;
  }
  const phase = applyReady ? "apply" : "preview";
  button.textContent = bulkActionLabel(action, phase);
  button.setAttribute("data-bulk-phase", phase);
};

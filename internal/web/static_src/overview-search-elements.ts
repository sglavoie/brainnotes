import { getElementByID, queryElement } from "./dom";

export type OverviewSearchElements = {
  root: HTMLElement;
  input: HTMLInputElement | null;
  statusEl: HTMLElement;
  listEl: HTMLElement;
  bulkRoot: HTMLElement | null;
  selectedCountEl: HTMLElement | null;
  bulkStatusEl: HTMLElement | null;
  previewPanel: HTMLElement | null;
  previewTitleEl: HTMLElement | null;
  previewMessageEl: HTMLElement | null;
  previewCountsEl: HTMLElement | null;
  previewResultsEl: HTMLElement | null;
  previewFailuresEl: HTMLElement | null;
  previewApplyButton: HTMLButtonElement | null;
  previewClearButton: HTMLButtonElement | null;
  csrfToken: string;
  trashButton: HTMLButtonElement | null;
  moveForm: HTMLFormElement | null;
  moveButton: HTMLButtonElement | null;
  setForm: HTMLFormElement | null;
  setButton: HTMLButtonElement | null;
  unsetForm: HTMLFormElement | null;
  unsetButton: HTMLButtonElement | null;
};

/**
 * Resolves every DOM node the overview search panel needs, or null when the
 * panel (or one of its two required children) is absent from the page.
 */
export const resolveOverviewSearchElements = (): OverviewSearchElements | null => {
  const root = queryElement(document, "[data-overview-search-results]", HTMLElement);
  if (!root) {
    return null;
  }

  const input = getElementByID("search-input", HTMLInputElement);
  const statusEl = queryElement(root, "[data-overview-search-status]", HTMLElement);
  const listEl = queryElement(root, "[data-overview-search-list]", HTMLElement);
  if (!statusEl || !listEl) {
    return null;
  }

  const bulkRoot = queryElement(root, "[data-overview-bulk]", HTMLElement);
  const previewPanel = bulkRoot
    ? queryElement(bulkRoot, "[data-overview-bulk-preview]", HTMLElement)
    : null;
  const moveForm = bulkRoot
    ? queryElement(bulkRoot, "[data-bulk-move]", HTMLFormElement)
    : null;
  const setForm = bulkRoot
    ? queryElement(bulkRoot, "[data-bulk-frontmatter-set]", HTMLFormElement)
    : null;
  const unsetForm = bulkRoot
    ? queryElement(bulkRoot, "[data-bulk-frontmatter-unset]", HTMLFormElement)
    : null;

  return {
    root,
    input,
    statusEl,
    listEl,
    bulkRoot,
    selectedCountEl: bulkRoot
      ? queryElement(bulkRoot, "[data-overview-selected-count]", HTMLElement)
      : null,
    bulkStatusEl: bulkRoot
      ? queryElement(bulkRoot, "[data-overview-bulk-status]", HTMLElement)
      : null,
    previewPanel,
    previewTitleEl: previewPanel
      ? queryElement(previewPanel, "[data-overview-bulk-preview-title]", HTMLElement)
      : null,
    previewMessageEl: previewPanel
      ? queryElement(previewPanel, "[data-overview-bulk-preview-message]", HTMLElement)
      : null,
    previewCountsEl: previewPanel
      ? queryElement(previewPanel, "[data-overview-bulk-preview-counts]", HTMLElement)
      : null,
    previewResultsEl: previewPanel
      ? queryElement(previewPanel, "[data-overview-bulk-preview-results]", HTMLElement)
      : null,
    previewFailuresEl: previewPanel
      ? queryElement(previewPanel, "[data-overview-bulk-preview-failures]", HTMLElement)
      : null,
    previewApplyButton: previewPanel
      ? queryElement(previewPanel, "[data-overview-bulk-preview-apply]", HTMLButtonElement)
      : null,
    previewClearButton: previewPanel
      ? queryElement(previewPanel, "[data-overview-bulk-preview-clear]", HTMLButtonElement)
      : null,
    csrfToken: bulkRoot?.getAttribute("data-overview-csrf") || "",
    trashButton: bulkRoot
      ? queryElement(bulkRoot, "[data-bulk-trash]", HTMLButtonElement)
      : null,
    moveForm,
    moveButton: moveForm
      ? queryElement(moveForm, "button[type=\"submit\"]", HTMLButtonElement)
      : null,
    setForm,
    setButton: setForm
      ? queryElement(setForm, "button[type=\"submit\"]", HTMLButtonElement)
      : null,
    unsetForm,
    unsetButton: unsetForm
      ? queryElement(unsetForm, "button[type=\"submit\"]", HTMLButtonElement)
      : null,
  };
};

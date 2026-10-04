import { getElementByID, queryElement } from "./dom";

export type OverviewSearchElements = {
  root: HTMLElement;
  input: HTMLInputElement | null;
  statusEl: HTMLElement;
  listEl: HTMLElement;
  bulkRoot: HTMLElement | null;
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
  const previewPanel = optionalElement(bulkRoot, "[data-overview-bulk-preview]", HTMLElement);
  const moveForm = optionalElement(bulkRoot, "[data-bulk-move]", HTMLFormElement);
  const setForm = optionalElement(bulkRoot, "[data-bulk-frontmatter-set]", HTMLFormElement);
  const unsetForm = optionalElement(bulkRoot, "[data-bulk-frontmatter-unset]", HTMLFormElement);

  return {
    root,
    input,
    statusEl,
    listEl,
    bulkRoot,
    bulkStatusEl: optionalElement(bulkRoot, "[data-overview-bulk-status]", HTMLElement),
    previewPanel,
    previewTitleEl: optionalElement(previewPanel, "[data-overview-bulk-preview-title]", HTMLElement),
    previewMessageEl: optionalElement(previewPanel, "[data-overview-bulk-preview-message]", HTMLElement),
    previewCountsEl: optionalElement(previewPanel, "[data-overview-bulk-preview-counts]", HTMLElement),
    previewResultsEl: optionalElement(previewPanel, "[data-overview-bulk-preview-results]", HTMLElement),
    previewFailuresEl: optionalElement(previewPanel, "[data-overview-bulk-preview-failures]", HTMLElement),
    previewApplyButton: optionalElement(previewPanel, "[data-overview-bulk-preview-apply]", HTMLButtonElement),
    previewClearButton: optionalElement(previewPanel, "[data-overview-bulk-preview-clear]", HTMLButtonElement),
    csrfToken: bulkRoot?.getAttribute("data-overview-csrf") || "",
    trashButton: optionalElement(bulkRoot, "[data-bulk-trash]", HTMLButtonElement),
    moveForm,
    moveButton: optionalElement(moveForm, "button[type=\"submit\"]", HTMLButtonElement),
    setForm,
    setButton: optionalElement(setForm, "button[type=\"submit\"]", HTMLButtonElement),
    unsetForm,
    unsetButton: optionalElement(unsetForm, "button[type=\"submit\"]", HTMLButtonElement),
  };
};

const optionalElement = <T extends Element>(
  root: ParentNode | null, selector: string, constructor: { new (): T }
): T | null => root ? queryElement(root, selector, constructor) : null;

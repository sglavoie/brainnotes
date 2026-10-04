import { queryAllElements, queryElement } from "./dom";

export const overviewSelectionInputs = (list: HTMLElement): HTMLInputElement[] =>
  queryAllElements(list, ".overview-result-select input", HTMLInputElement);

const syncCopySelection = (root: HTMLElement, inputs: HTMLInputElement[], busy: boolean): void => {
  const button = root.querySelector<HTMLButtonElement>("[data-overview-copy-wikilinks]");
  if (!button) return;
  // The server supplies exact-case targets. Never reconstruct them from folded
  // keys or display titles; emit only selected, displayed rows in display order.
  const text = inputs.filter((input) => input.checked && input.dataset.wikilink)
    .map((input) => `[[${input.dataset.wikilink}]]`).join("\n");
  if (button.dataset.copyText !== text || busy) {
    const controls = button.closest("[data-copy-controls]");
    controls?.querySelector("[data-copy-fallback]")?.remove();
    const status = controls?.querySelector("[data-copy-status]");
    if (status) status.textContent = "";
  }
  button.dataset.copyText = busy ? "" : text;
  button.disabled = busy || !text;
};

export const syncOverviewSelectionControls = (
  root: HTMLElement,
  list: HTMLElement,
  selectedCount: number,
  busy: boolean
): void => {
  const inputs = overviewSelectionInputs(list);
  syncCopySelection(root, inputs, busy);
  const controls = queryElement(root, "[data-overview-selection]", HTMLElement);
  if (controls) {
    controls.hidden = inputs.length === 0;
  }
  const select = queryElement(root, "[data-overview-select-displayed]", HTMLButtonElement);
  const clear = queryElement(root, "[data-overview-clear-selection]", HTMLButtonElement);
  if (select) {
    select.disabled = busy || inputs.length === 0 || inputs.every((input) => input.checked);
  }
  if (clear) {
    clear.disabled = busy || selectedCount === 0;
  }
  for (const input of inputs) {
    input.disabled = busy;
  }
};

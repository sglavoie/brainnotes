import { queryAllElements, queryElement } from "./dom";

export const overviewSelectionInputs = (list: HTMLElement): HTMLInputElement[] =>
  queryAllElements(list, ".overview-result-select input", HTMLInputElement);

export const syncOverviewSelectionControls = (
  root: HTMLElement,
  list: HTMLElement,
  selectedCount: number,
  busy: boolean
): void => {
  const inputs = overviewSelectionInputs(list);
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

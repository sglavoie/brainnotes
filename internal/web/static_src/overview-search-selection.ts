import { queryAllElements, queryElement } from "./dom";

export const overviewSelectionInputs = (list: HTMLElement): HTMLInputElement[] =>
  queryAllElements(list, ".overview-result-select input", HTMLInputElement);

export const bindOverviewRangeSelection = (list: HTMLElement): (() => void) => {
  let anchor: HTMLInputElement | null = null;
  list.addEventListener("click", (event) => {
    const input = event.target;
    if (!(input instanceof HTMLInputElement) || input.disabled ||
      !input.matches(".overview-result-select input")) return;
    const inputs = overviewSelectionInputs(list);
    const start = inputs.findIndex((candidate) => candidate === anchor);
    const end = inputs.indexOf(input);
    if (event.shiftKey && start >= 0) {
      // A checkbox click has already toggled its checked state. Apply that
      // state through the ordinary change handlers, including preview clearing.
      for (const sibling of inputs.slice(Math.min(start, end), Math.max(start, end) + 1)) {
        if (sibling.disabled || sibling === input || sibling.checked === input.checked) continue;
        sibling.checked = input.checked;
        sibling.dispatchEvent(new Event("change", { bubbles: true }));
      }
    }
    anchor = input;
  });
  return () => { anchor = null; };
};

const syncCopySelection = (root: HTMLElement, inputs: HTMLInputElement[], busy: boolean, selected?: ReadonlyMap<string, string>): void => {
  const button = root.querySelector<HTMLButtonElement>("[data-overview-copy-wikilinks]");
  if (!button) return;
  const text = selectedWikilinks(inputs, selected);
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
  busy: boolean,
  selected?: ReadonlyMap<string, string>
): void => {
  const inputs = overviewSelectionInputs(list);
  syncSelectionCount(root, inputs, selectedCount);
  syncCopySelection(root, inputs, busy, selected);
  const controls = queryElement(root, "[data-overview-selection]", HTMLElement);
  if (controls) {
    controls.hidden = inputs.length === 0 && selectedCount === 0;
    controls.dataset.hasSelection = String(selectedCount > 0);
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

const selectedWikilinks = (inputs: HTMLInputElement[], selected?: ReadonlyMap<string, string>): string => {
  // Keep exact targets supplied by the server, including selections on other pages.
  const targets = selected ? [...selected.values()].filter(Boolean)
    : inputs.filter((input) => input.checked && input.dataset.wikilink).map((input) => input.dataset.wikilink!);
  return targets.map((target) => `[[${target}]]`).join("\n");
};

const syncSelectionCount = (root: HTMLElement, inputs: HTMLInputElement[], selectedCount: number): void => {
  const count = root.querySelector<HTMLElement>("[data-overview-selected-count]");
  const elsewhere = selectedCount - inputs.filter((input) => input.checked).length;
  if (count) count.textContent = `${selectedCount} selected${elsewhere > 0 ? ` (${elsewhere} on other pages)` : ""}`;
};

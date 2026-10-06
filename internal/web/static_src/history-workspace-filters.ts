import type { HistoryOption, HistoryQuery } from "./api";
import { queryAllElements } from "./dom";
import type { HistoryWorkspaceElements } from "./history-workspace-elements";

/**
 * The filter vocabulary is rendered from what the last reply listed, never from
 * a list kept here. The server refuses a value it does not recognize, so a
 * checkbox this page invented would be a control that only ever produces an
 * error — and a name this page translated itself could disagree with the name
 * on the rows below it.
 */
export const renderHistoryFilterGroup = (
  container: HTMLElement | null,
  name: string,
  options: HistoryOption[],
  selected: string[]
): void => {
  if (!container) {
    return;
  }
  const legend = container.querySelector("legend");
  container.textContent = "";
  if (legend) {
    container.appendChild(legend);
  }
  for (const option of options) {
    container.appendChild(filterCheckbox(name, option, selected.includes(option.value)));
  }
};

const filterCheckbox = (name: string, option: HistoryOption, checked: boolean): HTMLLabelElement => {
  const label = document.createElement("label");
  label.className = "history-filter-option";

  const input = document.createElement("input");
  input.type = "checkbox";
  input.name = name;
  input.value = option.value;
  input.checked = checked;
  input.setAttribute("data-history-filter", name);

  const text = document.createElement("span");
  text.textContent = option.label || option.value;

  label.appendChild(input);
  label.appendChild(text);
  return label;
};

export const readHistoryFilterGroup = (container: HTMLElement | null): string[] => {
  if (!container) {
    return [];
  }
  return queryAllElements(container, "input[type=checkbox]", HTMLInputElement)
    .filter((input) => input.checked)
    .map((input) => input.value);
};

export const clearHistoryFilterGroup = (container: HTMLElement | null): void => {
  if (!container) {
    return;
  }
  for (const input of queryAllElements(container, "input[type=checkbox]", HTMLInputElement)) {
    input.checked = false;
  }
};

/**
 * readHistoryPageSize keeps the chosen page inside the bound the server
 * published. Asking for more is a refusal rather than a truncation, so a select
 * that had drifted past the maximum would break the page rather than show less.
 */
export const readHistoryPageSize = (elements: HistoryWorkspaceElements): number => {
  const raw = Number.parseInt(elements.pageSizeSelect?.value || "", 10);
  if (!Number.isFinite(raw) || raw < 1) {
    return elements.pageSize;
  }
  return Math.min(raw, elements.maxPageSize);
};

export const readHistoryQuery = (
  elements: HistoryWorkspaceElements,
  offset: number
): HistoryQuery => ({
  limit: readHistoryPageSize(elements),
  offset: Math.max(0, offset),
  origins: readHistoryFilterGroup(elements.originFilters),
  classes: readHistoryFilterGroup(elements.classFilters),
  path: elements.path?.value || "",
  pathMatch: elements.pathMatch?.value === "exact" ? "exact" : "contains",
});

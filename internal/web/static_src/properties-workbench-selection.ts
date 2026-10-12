import type { PropertyRow, PropertySummary } from "./api";
import { queryAllElements, queryElement } from "./dom";
import { selectionLabel } from "./properties-workbench-model";

export type WorkbenchState = {
  selected: Set<string>;
  summary: PropertySummary | null;
  rows: PropertyRow[];
  matchValue: { kind: string; text: string } | null;
  busy: boolean;
};

export const createWorkbenchState = (): WorkbenchState => ({
  selected: new Set<string>(),
  summary: null,
  rows: [],
  matchValue: null,
  busy: false,
});

export type SelectionView = {
  rowElements: () => HTMLElement[];
  renderSelection: () => void;
  renderActions: () => void;
  setBusy: (next: boolean) => void;
  toggleSelection: (relPath: string, force?: boolean) => void;
  focusRow: (index: number) => void;
  updateRowText: (relPath: string, text: string) => void;
};

// createSelectionView paints the selection and the enabled state of the bulk
// actions onto the page; the state it reads lives in `state` so the workbench
// and its event handlers share one copy.
export const createSelectionView = (
  root: HTMLElement,
  rowsEl: HTMLElement,
  state: WorkbenchState
): SelectionView => {
  const countEl = queryElement(root, "[data-property-selection-count]", HTMLElement);
  const clearSelectionEl = queryElement(
    root,
    "[data-property-clear-selection]",
    HTMLButtonElement
  );

  const rowElements = (): HTMLElement[] =>
    queryAllElements(rowsEl, "[data-property-row]", HTMLElement);

  const renderActions = (): void => {
    for (const button of queryAllElements(root, "[data-property-action]", HTMLButtonElement)) {
      const action = button.getAttribute("data-property-action") || "";
      const needsMatch = action === "replace_value";
      button.disabled =
        state.busy || state.selected.size === 0 || (needsMatch && state.matchValue === null);
    }
  };

  const renderSelection = (): void => {
    if (countEl) {
      countEl.textContent = selectionLabel(state.selected.size);
    }
    if (clearSelectionEl) {
      clearSelectionEl.disabled = state.selected.size === 0;
    }
    for (const element of rowElements()) {
      const relPath = element.getAttribute("data-property-row") || "";
      element.classList.toggle("is-selected", state.selected.has(relPath));
    }
    for (const box of queryAllElements(rowsEl, "[data-property-row-select]", HTMLInputElement)) {
      box.checked = state.selected.has(box.getAttribute("data-property-row-select") || "");
    }
    renderActions();
  };

  const setBusy = (next: boolean): void => {
    state.busy = next;
    root.setAttribute("aria-busy", next ? "true" : "false");
    renderActions();
  };

  const toggleSelection = (relPath: string, force?: boolean): void => {
    const next = force === undefined ? !state.selected.has(relPath) : force;
    if (next) {
      state.selected.add(relPath);
    } else {
      state.selected.delete(relPath);
    }
    renderSelection();
  };

  const focusRow = (index: number): void => {
    const elements = rowElements();
    if (elements.length === 0) {
      return;
    }
    const clamped = Math.max(0, Math.min(index, elements.length - 1));
    elements.forEach((element, position) => {
      element.tabIndex = position === clamped ? 0 : -1;
    });
    elements[clamped]?.focus();
  };

  // updateRowText repaints the edited row from what was just written, so the
  // value on screen is correct before the refetch that restates the totals.
  const updateRowText = (relPath: string, text: string): void => {
    const element = rowElements().find(
      (row) => row.getAttribute("data-property-row") === relPath
    );
    const value = element ? queryElement(element, ".properties-row-value", HTMLElement) : null;
    if (!value) {
      return;
    }
    value.textContent = text;
    value.classList.remove("is-missing");
  };

  return {
    rowElements,
    renderSelection,
    renderActions,
    setBusy,
    toggleSelection,
    focusRow,
    updateRowText,
  };
};

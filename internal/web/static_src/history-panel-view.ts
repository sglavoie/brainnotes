import type { HistoryEntry, UndoConflict } from "./api";
import { queryAllElements, queryElement } from "./dom";
import { setStatusText, type StatusKind } from "./status";

export const formatHistoryTimestamp = (value: string): string => {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString(undefined, {
    dateStyle: "short",
    timeStyle: "short",
  });
};

export const historyFileCountLabel = (count: number): string =>
  count === 1 ? "1 path" : `${count} paths`;

export const historyConflictSummary = (conflicts: UndoConflict[]): string => {
  if (conflicts.length === 0) {
    return "Undo has conflicts.";
  }
  const sample = conflicts.slice(0, 3).map((conflict) => {
    const path = conflict.path || "path";
    const expected = conflict.expected_state || "expected";
    const current = conflict.current_state || "current";
    return `${path}: expected ${expected}, current ${current}`;
  });
  const suffix = conflicts.length > sample.length
    ? `; ${conflicts.length - sample.length} more`
    : "";
  return `Undo has conflicts: ${sample.join("; ")}${suffix}.`;
};

/**
 * historyForceConsentMessage names what forcing would replace.
 *
 * It is built from the *preview*, not from the conflict reply, and that is the
 * whole reason the panel previews before it forces: the conflict reply says which
 * paths refused, while the preview says which paths the restore writes and which
 * of those hold newer work. Asking somebody to confirm the first is asking them
 * to agree to something narrower than what happens.
 */
export const historyForceConsentMessage = (paths: string[], changeCount: number): string => {
  const sample = paths.slice(0, 3).join(", ");
  const more = paths.length > 3 ? `, and ${paths.length - 3} more` : "";
  const replaced = paths.length === 0
    ? "no newer work"
    : `newer work in ${sample}${more}`;
  return `Restore ${historyFileCountLabel(changeCount)} and replace ${replaced}?`;
};

export type HistoryPanelElements = {
  root: HTMLElement;
  list: HTMLElement;
  status: HTMLElement;
  refresh: HTMLButtonElement | null;
  undoLatest: HTMLButtonElement | null;
  conflictBox: HTMLElement | null;
  conflictSummary: HTMLElement | null;
  force: HTMLButtonElement | null;
};

export const findHistoryPanelElements = (doc: Document): HistoryPanelElements | null => {
  const root = queryElement(doc, "[data-history-panel]", HTMLElement);
  if (!root) {
    return null;
  }
  const list = queryElement(root, "[data-history-list]", HTMLElement);
  const status = queryElement(root, "[data-history-status]", HTMLElement);
  if (!list || !status) {
    return null;
  }
  return {
    root,
    list,
    status,
    refresh: queryElement(root, "[data-history-refresh]", HTMLButtonElement),
    undoLatest: queryElement(root, "[data-history-undo-latest]", HTMLButtonElement),
    conflictBox: queryElement(root, "[data-history-conflict]", HTMLElement),
    conflictSummary: queryElement(root, "[data-history-conflict-summary]", HTMLElement),
    force: queryElement(root, "[data-history-force]", HTMLButtonElement),
  };
};

export const setHistoryPanelStatus = (
  elements: HistoryPanelElements,
  message: string,
  kind: StatusKind = ""
): void => {
  setStatusText(elements.status, message, kind);
};

export const setHistoryPanelBusy = (
  elements: HistoryPanelElements,
  busy: boolean,
  entryCount: number,
  forceReady: boolean
): void => {
  elements.root.setAttribute("aria-busy", busy ? "true" : "false");
  if (elements.refresh) {
    elements.refresh.disabled = busy;
  }
  if (elements.undoLatest) {
    elements.undoLatest.disabled = busy || entryCount === 0;
  }
  for (const button of queryAllElements(elements.list, "[data-history-undo-entry]", HTMLButtonElement)) {
    button.disabled = busy;
  }
  if (elements.force) {
    elements.force.disabled = busy || !forceReady;
  }
};

const historyPaths = (paths: string[]): HTMLUListElement => {
  const list = document.createElement("ul");
  list.className = "history-paths";
  list.setAttribute("aria-label", "Affected paths");
  for (const path of paths) {
    const item = document.createElement("li");
    item.textContent = path;
    list.append(item);
  }
  return list;
};

export const appendHistoryPaths = (main: HTMLElement, entry: HistoryEntry): void => {
  const paths = entry.files.map((file) => file.path);
  if (!paths.length) return;
  main.append(historyPaths(paths.slice(0, 3)));
  if (paths.length <= 3) return;
  const details = document.createElement("details");
  details.className = "history-more-paths";
  const summary = document.createElement("summary");
  const remaining = paths.length - 3;
  summary.textContent = `${remaining} more ${remaining === 1 ? "path" : "paths"}`;
  details.append(summary, historyPaths(paths.slice(3)));
  main.append(details);
};

const historyEntryRow = (entry: HistoryEntry, onUndo: (entryID: string) => void): HTMLLIElement => {
  const item = document.createElement("li");
  item.className = "history-item";

  const main = document.createElement("div");
  main.className = "history-item-main";

  const command = document.createElement("span");
  command.className = "history-command";
  command.textContent = entry.command || entry.id;
  main.appendChild(command);

  const meta = document.createElement("span");
  meta.className = "history-meta";
  const time = formatHistoryTimestamp(entry.timestamp);
  const count = historyFileCountLabel(entry.file_count || entry.files.length);
  meta.textContent = time ? `${time} - ${count}` : count;
  main.appendChild(meta);
  appendHistoryPaths(main, entry);

  const undoButton = document.createElement("button");
  undoButton.className = "history-button history-danger";
  undoButton.type = "button";
  undoButton.setAttribute("data-history-undo-entry", entry.id);
  undoButton.setAttribute("aria-label", `Undo ${entry.command || "change"}`);
  undoButton.textContent = "Undo";
  undoButton.addEventListener("click", () => {
    onUndo(entry.id);
  });

  item.appendChild(main);
  item.appendChild(undoButton);
  return item;
};

export const renderHistoryPanelEntries = (
  elements: HistoryPanelElements,
  entries: HistoryEntry[],
  onUndo: (entryID: string) => void
): void => {
  elements.list.textContent = "";
  for (const entry of entries) {
    elements.list.appendChild(historyEntryRow(entry, onUndo));
  }
};

export const showHistoryPanelConflict = (
  elements: HistoryPanelElements,
  conflicts: UndoConflict[],
  forceReady: boolean
): void => {
  if (elements.conflictSummary) {
    elements.conflictSummary.textContent = historyConflictSummary(conflicts);
  }
  if (elements.conflictBox) {
    elements.conflictBox.hidden = false;
  }
  if (elements.force) {
    elements.force.disabled = !forceReady;
  }
};

export const hideHistoryPanelConflict = (elements: HistoryPanelElements): void => {
  if (elements.conflictBox) {
    elements.conflictBox.hidden = true;
  }
  if (elements.conflictSummary) {
    elements.conflictSummary.textContent = "";
  }
  if (elements.force) {
    elements.force.disabled = true;
  }
};

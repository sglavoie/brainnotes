import { renderTextComparison } from "./text-comparison";
import type { HistoryPreviewChange, HistoryPreviewResult, UndoConflict } from "./api";
import { formatHistoryTimestamp } from "./history-panel-view";
import type { HistoryWorkspaceElements } from "./history-workspace-elements";

/**
 * historyChangeDurability says what restoring one path means afterwards.
 *
 * Recorded and travels are separate questions and are reported separately: a
 * restored setting is recorded in this vault's content history and stays on
 * this machine, and somebody recovering it on the strength of "it is in the
 * history" would be expecting it on their other replica too.
 */
export const historyChangeDurability = (change: HistoryPreviewChange): string => {
  if (!change.recorded) {
    return "not recorded in the content history";
  }
  return change.travels
    ? "recorded here, and travels to your other replica"
    : "recorded here only, and stays on this machine";
};

const appendFact = (list: HTMLElement, term: string, detail: string): void => {
  const dt = document.createElement("dt");
  dt.textContent = term;
  const dd = document.createElement("dd");
  dd.textContent = detail;
  list.appendChild(dt);
  list.appendChild(dd);
};

const renderRestoreFacts = (
  container: HTMLElement | null,
  preview: HistoryPreviewResult
): void => {
  if (!container) {
    return;
  }
  container.textContent = "";
  appendFact(container, "Came from", preview.origin_label || preview.origin || "unknown");
  appendFact(container, "Recorded command", preview.command || "(none recorded)");
  appendFact(container, "Recorded at", formatHistoryTimestamp(preview.timestamp || ""));
  appendFact(container, "Entry", preview.entry_id || "");
};

const renderRestoreChanges = (
  container: HTMLElement | null,
  changes: HistoryPreviewChange[]
): void => {
  if (!container) {
    return;
  }
  container.textContent = "";
  for (const change of changes) {
    const item = document.createElement("li");
    item.className = change.conflict ? "history-change is-conflict" : "history-change";
    item.setAttribute("data-history-change", change.path);

    const path = document.createElement("code");
    path.textContent = change.path;
    item.appendChild(path);

    const detail = document.createElement("p");
    detail.className = "history-change-detail";
    detail.textContent =
      `${restoreEffect(change)}; ${historyChangeDurability(change)}.`;
    item.appendChild(detail);

    if (change.conflict) {
      const flag = document.createElement("p");
      flag.className = "history-change-conflict";
      flag.textContent = "This path holds newer work that restoring would replace.";
      item.appendChild(flag);
    }
    appendRestoreComparison(item, change);
    container.appendChild(item);
  }
};

const restoreEffect = (change: HistoryPreviewChange): string => {
  if (change.restored_state === "missing") return "Remove the created file";
  return change.current_state === "missing" ? "Bring back the removed file" : "Restore the earlier version";
};

const appendRestoreComparison = (item: HTMLElement, change: HistoryPreviewChange): void => {
  const host = item.ownerDocument.createElement("div");
  if (change.comparison) {
    renderTextComparison(host, change.comparison.current, change.comparison.restored, change.class === "note" ? "Current note" : "Current file", "After restoring");
  } else {
    host.className = "comparison-unavailable";
    host.textContent = change.comparison_unavailable || "Text comparison is unavailable.";
  }
  item.append(host);
};

const renderRestoreConflicts = (
  elements: HistoryWorkspaceElements,
  conflicts: UndoConflict[]
): void => {
  if (elements.restoreConflictList) {
    elements.restoreConflictList.textContent = "";
    for (const conflict of conflicts) {
      const item = document.createElement("li");
      item.setAttribute("data-history-conflict-path", conflict.path);
      item.textContent =
        `${conflict.path}: the history holds ${conflict.expected_state}, this vault now holds ${conflict.current_state}`;
      elements.restoreConflictList.appendChild(item);
    }
  }
  if (elements.restoreConflicts) {
    elements.restoreConflicts.hidden = conflicts.length === 0;
  }
  if (elements.restoreConsent) {
    elements.restoreConsent.checked = false;
  }
};

/**
 * renderHistoryRestorePreview shows what the server said this restore would do,
 * and nothing this page worked out for itself.
 *
 * The Restore button is disabled while a conflict is listed and the consent box
 * is unticked, which is the page's half of the same rule the server enforces
 * with the preview receipt: nothing replaces newer work without somebody having
 * been shown the newer work by name.
 */
export const renderHistoryRestorePreview = (
  elements: HistoryWorkspaceElements,
  preview: HistoryPreviewResult
): void => {
  const conflicts = preview.conflicts || [];
  if (elements.restoreSummary) {
    elements.restoreSummary.textContent = preview.message || "";
    elements.restoreSummary.classList.toggle("is-warning", conflicts.length > 0);
  }
  renderRestoreFacts(elements.restoreFacts, preview);
  renderRestoreChanges(elements.restoreChanges, preview.changes || []);
  renderRestoreConflicts(elements, conflicts);
  if (elements.restore) {
    elements.restore.hidden = false;
    elements.restore.setAttribute("aria-busy", "false");
  }
  syncHistoryRestoreConsent(elements, conflicts.length > 0);
};

export const syncHistoryRestoreConsent = (
  elements: HistoryWorkspaceElements,
  requiresConsent: boolean
): void => {
  if (!elements.restoreConfirm) {
    return;
  }
  elements.restoreConfirm.disabled = requiresConsent && !elements.restoreConsent?.checked;
  elements.restoreConfirm.textContent = requiresConsent
    ? "Restore and replace newer work"
    : "Restore";
};

export const hideHistoryRestorePanel = (elements: HistoryWorkspaceElements): void => {
  if (elements.restore) {
    elements.restore.hidden = true;
    elements.restore.setAttribute("aria-busy", "false");
  }
  if (elements.restoreConflicts) {
    elements.restoreConflicts.hidden = true;
  }
  if (elements.restoreConsent) {
    elements.restoreConsent.checked = false;
  }
  if (elements.restoreConfirm) {
    elements.restoreConfirm.disabled = false;
    elements.restoreConfirm.textContent = "Restore";
  }
};

export const showHistoryPreviewPending = (elements: HistoryWorkspaceElements): void => {
  for (const container of [elements.restoreFacts, elements.restoreChanges, elements.restoreConflictList]) {
    if (container) container.textContent = "";
  }
  if (elements.restoreSummary) {
    elements.restoreSummary.textContent = "Checking what restoring this would do...";
    elements.restoreSummary.classList.remove("is-warning");
  }
  if (elements.restoreConfirm) elements.restoreConfirm.disabled = true;
  if (elements.restore) {
    elements.restore.hidden = false;
    elements.restore.setAttribute("aria-busy", "true");
  }
};

export const focusHistoryPreview = (elements: HistoryWorkspaceElements): void => {
  elements.restore?.querySelector<HTMLElement>("[data-history-restore-heading]")?.focus();
};

import {
  buildUndoForm,
  managementMutationEventName,
  normalizeHistoryEntries,
  normalizeManagementErrorMessage,
  type HistoryEntry,
  type UndoConflict,
  type UndoResult,
} from "./api";
import { queryAllElements, queryElement } from "./dom";
import { setStatusText, type StatusKind } from "./status";

type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;
type ConfirmLike = (message: string) => boolean;

export type HistoryPanelDeps = {
  fetch?: FetchLike;
  confirm?: ConfirmLike;
};

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

export const initHistoryPanel = (deps: HistoryPanelDeps = {}): void => {
  const root = queryElement(document, "[data-history-panel]", HTMLElement);
  if (!root) {
    return;
  }

  const listEl = queryElement(root, "[data-history-list]", HTMLElement);
  const statusEl = queryElement(root, "[data-history-status]", HTMLElement);
  const refreshButton = queryElement(root, "[data-history-refresh]", HTMLButtonElement);
  const undoLatestButton = queryElement(root, "[data-history-undo-latest]", HTMLButtonElement);
  const conflictBox = queryElement(root, "[data-history-conflict]", HTMLElement);
  const conflictSummaryEl = queryElement(root, "[data-history-conflict-summary]", HTMLElement);
  const forceButton = queryElement(root, "[data-history-force]", HTMLButtonElement);
  if (!listEl || !statusEl) {
    return;
  }

  const csrfToken = root.getAttribute("data-history-csrf") || "";
  const fetcher = deps.fetch || fetch;
  const confirmAction = deps.confirm || ((message: string): boolean => window.confirm(message));
  let entries: HistoryEntry[] = [];
  let pendingConflict: UndoResult | null = null;
  let busy = false;

  const setBusy = (next: boolean): void => {
    busy = next;
    root.setAttribute("aria-busy", next ? "true" : "false");
    if (refreshButton) {
      refreshButton.disabled = next;
    }
    if (undoLatestButton) {
      undoLatestButton.disabled = next || entries.length === 0;
    }
    for (const button of queryAllElements(listEl, "[data-history-undo-entry]", HTMLButtonElement)) {
      button.disabled = next;
    }
    if (forceButton) {
      forceButton.disabled = next || !pendingConflict?.entry_id;
    }
  };

  const setStatus = (message: string, kind: StatusKind = ""): void => {
    setStatusText(statusEl, message, kind);
  };

  const hideConflict = (): void => {
    pendingConflict = null;
    if (conflictBox) {
      conflictBox.hidden = true;
    }
    if (conflictSummaryEl) {
      conflictSummaryEl.textContent = "";
    }
    if (forceButton) {
      forceButton.disabled = true;
    }
  };

  const showConflict = (result: UndoResult): void => {
    pendingConflict = result;
    const conflicts = Array.isArray(result.conflicts) ? result.conflicts : [];
    if (conflictSummaryEl) {
      conflictSummaryEl.textContent = historyConflictSummary(conflicts);
    }
    if (conflictBox) {
      conflictBox.hidden = false;
    }
    if (forceButton) {
      forceButton.disabled = !result.entry_id;
    }
  };

  const renderEntries = (): void => {
    listEl.textContent = "";
    for (const entry of entries) {
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

      const undoButton = document.createElement("button");
      undoButton.className = "history-button history-danger";
      undoButton.type = "button";
      undoButton.setAttribute("data-history-undo-entry", entry.id);
      undoButton.setAttribute("aria-label", `Undo ${entry.command || "change"}`);
      undoButton.textContent = "Undo";
      undoButton.addEventListener("click", () => {
        void runUndo(entry.id, false);
      });

      item.appendChild(main);
      item.appendChild(undoButton);
      listEl.appendChild(item);
    }
    setBusy(busy);
  };

  const loadHistory = async (successMessage = ""): Promise<void> => {
    setBusy(true);
    setStatus("Loading history...", "is-loading");
    try {
      const resp = await fetcher("/api/history", {
        headers: { Accept: "application/json" },
      });
      const payload = await readHistoryPanelPayload(resp);
      if (!resp.ok) {
        setStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
        return;
      }
      entries = normalizeHistoryEntries(payload);
      renderEntries();
      if (entries.length === 0) {
        setStatus("No recent changes.", "is-empty");
      } else {
        setStatus(successMessage || `${entries.length} recent changes.`, successMessage ? "is-success" : "");
      }
    } catch {
      setStatus("History unavailable.", "is-error");
    } finally {
      setBusy(false);
    }
  };

  const runUndo = async (entryID: string, force: boolean): Promise<void> => {
    if (csrfToken.trim() === "") {
      setStatus("Undo unavailable.", "is-error");
      return;
    }
    if (!force && !confirmAction("Undo this change?")) {
      setStatus("Undo canceled.");
      return;
    }
    if (force && !confirmAction("Force undo and overwrite conflicting paths?")) {
      setStatus("Force undo canceled.");
      return;
    }

    setBusy(true);
    setStatus(force ? "Forcing undo..." : "Undoing change...", "is-loading");
    try {
      const body = buildUndoForm({
        token: csrfToken,
        entryID,
        force,
        confirmForce: force,
      });
      const resp = await fetcher("/api/undo", {
        method: "POST",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
          "X-BN-CSRF": csrfToken,
        },
        body: body.toString(),
      });
      const payload = await readHistoryPanelPayload(resp);
      const result = payload as UndoResult;
      if (resp.status === 409 && result?.requires_force) {
        showConflict(result);
        setStatus("Undo has conflicts.", "is-error");
        return;
      }
      if (!resp.ok) {
        setStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
        return;
      }
      hideConflict();
      const message = result && result.ok && result.message ? result.message : "Undo restored paths.";
      await loadHistory(message);
    } catch {
      setStatus("Undo failed.", "is-error");
    } finally {
      setBusy(false);
    }
  };

  refreshButton?.addEventListener("click", () => {
    hideConflict();
    void loadHistory();
  });
  undoLatestButton?.addEventListener("click", () => {
    void runUndo("", false);
  });
  forceButton?.addEventListener("click", () => {
    const entryID = pendingConflict?.entry_id || "";
    if (entryID === "") {
      setStatus("No conflict selected.", "is-error");
      return;
    }
    void runUndo(entryID, true);
  });
  window.addEventListener(managementMutationEventName, () => {
    hideConflict();
    void loadHistory("History refreshed.");
  });

  hideConflict();
  void loadHistory();
};

const readHistoryPanelPayload = async (resp: Response): Promise<unknown> => {
  const contentType = resp.headers?.get("Content-Type") || "";
  try {
    if (contentType.includes("application/json")) {
      return await resp.json();
    }
    return await resp.text();
  } catch {
    return "";
  }
};

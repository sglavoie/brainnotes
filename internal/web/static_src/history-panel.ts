import { confirmUndo, undoDescription } from "./history-confirm";
import {
  HISTORY_ENDPOINT,
  HISTORY_PREVIEW_ENDPOINT,
  UNDO_ENDPOINT,
  managementMutationEventName,
  type HistoryEntry,
  type HistoryPreviewResult,
} from "./api";
import {
  fetchHistoryPage,
  previewHistoryRestore,
  restoreHistoryEntry,
  type HistoryFetch,
} from "./history-requests";
import {
  findHistoryPanelElements,
  hideHistoryPanelConflict,
  renderHistoryPanelEntries,
  setHistoryPanelBusy,
  setHistoryPanelStatus,
  showHistoryPanelConflict,
  type HistoryPanelElements,
} from "./history-panel-view";
import { focusFallback, focusIsLost, queryAllElements } from "./dom";
import type { StatusKind } from "./status";
import { newWriteOperationID, offerWriteOutcomeCheck } from "./write-outcome-check";

export {
  formatHistoryTimestamp,
  historyConflictSummary,
  historyFileCountLabel,
  historyForceConsentMessage,
} from "./history-panel-view";

type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;
type ConfirmLike = (message: string) => boolean;

export type HistoryPanelDeps = {
  fetch?: FetchLike;
  confirm?: ConfirmLike;
};

/**
 * The recent-changes panel that sits on /library, /properties and /settings.
 *
 * Its plain undo is the narrow one: the server refuses it outright if any path
 * has moved on, so it cannot replace newer work. Forcing is the only way past
 * that, and this panel reaches it the same way the /history workspace does — by
 * asking the preview route what the restore would overwrite, showing that in the
 * confirmation, and sending the receipt back with the force.
 */
export const initHistoryPanel = (deps: HistoryPanelDeps = {}): void => {
  const elements = findHistoryPanelElements(document);
  if (!elements) {
    return;
  }
  const controller = createHistoryPanelController(elements, deps);
  controller.bind();
  void controller.load();
};

/** HistoryPanelIO is everything the two restore flows below need from the panel:
 * a way to talk to the server and a way to say what is happening. */
type HistoryPanelIO = {
  elements: HistoryPanelElements;
  fetcher: HistoryFetch;
  token: string;
  confirm: (preview: HistoryPreviewResult) => Promise<boolean>;
  setBusy: (busy: boolean) => void;
  setStatus: (message: string, kind?: StatusKind) => void;
};

type RestoreOutcome =
  | { kind: "restored"; message: string }
  // entryID is the entry the *server* resolved, not the one that was asked for.
  // They differ for "undo the latest change", which is posted with no identifier
  // and can only be forced against the identifier the refusal answers with.
  | { kind: "conflict"; entryID: string }
  // The reply was lost: the restore may have landed, and only the journal can say.
  | { kind: "uncertain"; operationID: string }
  | { kind: "halted" };

// A confirmed restore changed notes other panels show, so it is announced like
// any other write; beforeAnnounce lets this panel's own reload keep the news.
const offerUndoOutcomeCheck = (io: HistoryPanelIO, operationID: string, beforeAnnounce: () => void): void => {
  offerWriteOutcomeCheck(io.elements.status, operationID, io.fetcher, "Undo", () => {
    beforeAnnounce();
    window.dispatchEvent(new CustomEvent(managementMutationEventName));
  });
};

type HistoryPanelState = {
  entryCount: () => number;
  hasConflict: () => boolean;
  returnFocus: () => Element | null;
};

const createHistoryPanelIO = (
  elements: HistoryPanelElements,
  deps: HistoryPanelDeps,
  state: HistoryPanelState
): HistoryPanelIO => {
  const baseFetch = deps.fetch || fetch;
  return {
    elements,
    fetcher: (input, init) => baseFetch(input, init),
    token: elements.root.getAttribute("data-history-csrf") || "",
    confirm: deps.confirm
      ? (preview) => Promise.resolve(deps.confirm!(undoDescription(preview)))
      : (preview) => confirmUndo(preview, document, state.returnFocus()),
    setBusy: (busy: boolean): void => {
      setHistoryPanelBusy(elements, busy, state.entryCount(), state.hasConflict());
    },
    setStatus: (message: string, kind: StatusKind = ""): void => {
      setHistoryPanelStatus(elements, message, kind);
    },
  };
};

// Disabling or rerendering the focused button drops focus to <body>. Put it
// back on the same control when it still exists, else on the status line.
const recoverHistoryFocus = (elements: HistoryPanelElements, returnFocus: Element | null): void => {
  if (!focusIsLost(document)) return;
  const target = returnFocus instanceof HTMLButtonElement ? returnFocus : null;
  const entryID = target?.getAttribute("data-history-undo-entry");
  const match = entryID
    ? queryAllElements(elements.list, "[data-history-undo-entry]", HTMLButtonElement)
      .find((button) => button.getAttribute("data-history-undo-entry") === entryID)
    : target;
  if (match?.isConnected && !match.disabled) match.focus();
  else focusFallback(elements.status);
};

const showHistoryEntries = (io: HistoryPanelIO, count: number, successMessage: string): void => {
  if (count === 0) {
    io.setStatus("No recent changes.", "is-empty");
    return;
  }
  io.setStatus(successMessage || `${count} recent changes.`, successMessage ? "is-success" : "");
};

type HistoryPanelActions = {
  refresh: () => void;
  undoLatest: () => void;
  force: () => void;
  mutated: () => void;
};

const bindHistoryPanel = (elements: HistoryPanelElements, actions: HistoryPanelActions): void => {
  elements.refresh?.addEventListener("click", actions.refresh);
  elements.undoLatest?.addEventListener("click", actions.undoLatest);
  elements.force?.addEventListener("click", actions.force);
  window.addEventListener(managementMutationEventName, actions.mutated);
};

// HistoryReloadState is what decides whether, and how, a reload renders.
type HistoryReloadState = {
  // The message the next mutation-event reload shows instead of the generic one.
  message: string;
  // Only the newest load may render: an older answer landing late would put a
  // stale list back on screen.
  generation: number;
  // A mutation event during an undo waits for it (pending): a reload started
  // mid-undo would re-enable the buttons while the undo is still in flight.
  restoring: boolean;
  pending: boolean;
  // Set when a reload follows this panel's own undo, whose button the rerender
  // or the busy state took focus from.
  focusAfterLoad: boolean;
};

const createHistoryPanelController = (
  elements: HistoryPanelElements,
  deps: HistoryPanelDeps
): { bind: () => void; load: (successMessage?: string) => Promise<void> } => {
  let entries: HistoryEntry[] = [];
  let conflictEntryID = "";
  let returnFocus: Element | null = null;
  const reload: HistoryReloadState = { message: "", generation: 0, restoring: false, pending: false, focusAfterLoad: false };

  const io = createHistoryPanelIO(elements, deps, {
    entryCount: () => entries.length,
    hasConflict: () => conflictEntryID !== "",
    returnFocus: () => returnFocus,
  });

  const hideConflict = (): void => {
    conflictEntryID = "";
    hideHistoryPanelConflict(elements);
  };

  const load = async (successMessage = ""): Promise<void> => {
    const request = ++reload.generation;
    io.setBusy(true);
    io.setStatus("Loading history…", "is-loading");
    const outcome = await fetchHistoryPage(io.fetcher, HISTORY_ENDPOINT, {});
    if (request !== reload.generation) return;
    if (outcome.ok) {
      entries = outcome.result.entries || [];
      renderHistoryPanelEntries(elements, entries, (entryID) => void restore(entryID, plainRestore));
    }
    io.setBusy(false);
    if (outcome.ok) showHistoryEntries(io, entries.length, successMessage);
    else io.setStatus(outcome.message, "is-error");
    if (reload.focusAfterLoad) recoverHistoryFocus(elements, returnFocus);
    reload.focusAfterLoad = false;
  };

  const refreshAfterMutation = (): void => {
    reload.pending = reload.restoring;
    if (reload.restoring) return;
    hideConflict();
    void load(reload.message || "History refreshed.");
    reload.message = "";
  };

  // A confirmed undo changed notes that other panels show, so it is announced
  // like any other write; this panel's own reload is the listener's.
  const settleRestore = (outcome: RestoreOutcome): void => {
    reload.restoring = false;
    if (outcome.kind === "conflict") {
      conflictEntryID = outcome.entryID;
      io.setBusy(false);
    }
    if (outcome.kind === "restored") {
      reload.message = outcome.message;
      reload.focusAfterLoad = true;
      window.dispatchEvent(new CustomEvent(managementMutationEventName));
    } else {
      recoverHistoryFocus(elements, returnFocus);
      if (reload.pending) refreshAfterMutation();
    }
    if (outcome.kind === "uncertain") offerUndoOutcomeCheck(io, outcome.operationID, () => { reload.message = "Undo confirmed."; });
  };

  const restore = async (
    entryID: string,
    run: (io: HistoryPanelIO, entryID: string) => Promise<RestoreOutcome>
  ): Promise<void> => {
    if (io.token.trim() === "") {
      io.setStatus("Undo unavailable.", "is-error");
      return;
    }
    returnFocus = document.activeElement;
    reload.restoring = true;
    let outcome: RestoreOutcome = { kind: "halted" };
    try {
      outcome = await run(io, entryID);
    } finally {
      settleRestore(outcome);
    }
  };

  const bind = (): void => {
    hideConflict();
    bindHistoryPanel(elements, {
      refresh: () => {
        hideConflict();
        void load();
      },
      undoLatest: () => void restore("", plainRestore),
      force: () => {
        if (conflictEntryID === "") io.setStatus("No conflict selected.", "is-error");
        else void restore(conflictEntryID, forcedRestore);
      },
      mutated: refreshAfterMutation,
    });
  };

  return { bind, load };
};

/** plainRestore is the undo that refuses rather than overwrite. A refusal is
 * reported with the paths the server named, and is the point at which forcing
 * becomes available — not a fault to stop on. */
const plainRestore = async (io: HistoryPanelIO, entryID: string): Promise<RestoreOutcome> => {
  io.setBusy(true);
  io.setStatus("Checking what this would undo…", "is-loading");
  try {
    const preview = await previewHistoryRestore(io.fetcher, HISTORY_PREVIEW_ENDPOINT, io.token, entryID);
    if (!preview.ok) {
      io.setStatus(preview.message, "is-error");
      return { kind: "halted" };
    }
    const result = preview.result;
    if (!result.entry_id || !result.preview_id) {
      io.setStatus("Couldn't identify the change to undo. No change was submitted.", "is-error");
      return { kind: "halted" };
    }
    if (result.requires_force) {
      showHistoryPanelConflict(io.elements, result.conflicts || [], true);
      io.setStatus("This undo would replace newer work. Review it before forcing undo.", "is-error");
      return { kind: "conflict", entryID: result.entry_id };
    }
    if (!await io.confirm(result)) {
      io.setStatus("Undo canceled.");
      return { kind: "halted" };
    }
    io.setStatus("Undoing change…", "is-loading");
    const operationID = newWriteOperationID();
    const outcome = await restoreHistoryEntry(io.fetcher, UNDO_ENDPOINT, {
      token: io.token, entryID: result.entry_id, previewID: result.preview_id, operationID,
    });
    if (outcome.ok) return { kind: "restored", message: outcome.result.message || "Undo restored paths." };
    io.setStatus(outcome.message, "is-error");
    return outcome.uncertain ? { kind: "uncertain", operationID } : { kind: "halted" };
  } finally {
    io.setBusy(false);
  }
};

/** forcedRestore previews first, every time. The receipt it takes is what the
 * server requires before it will replace newer work, and the confirmation is
 * worded from that preview rather than from the earlier refusal. */
const forcedRestore = async (io: HistoryPanelIO, entryID: string): Promise<RestoreOutcome> => {
  io.setBusy(true);
  io.setStatus("Checking what this would replace…", "is-loading");
  const preview = await previewHistoryRestore(io.fetcher, HISTORY_PREVIEW_ENDPOINT, io.token, entryID);
  io.setBusy(false);
  if (!preview.ok) {
    io.setStatus(preview.message, "is-error");
    return { kind: "halted" };
  }
  showHistoryPanelConflict(io.elements, preview.result.conflicts || [], true);
  if (!await io.confirm(preview.result)) {
    io.setStatus("Force undo canceled.");
    return { kind: "halted" };
  }
  io.setBusy(true);
  io.setStatus("Forcing undo…", "is-loading");
  const operationID = newWriteOperationID();
  const outcome = await restoreHistoryEntry(io.fetcher, UNDO_ENDPOINT, {
    token: io.token,
    entryID: preview.result.entry_id || entryID,
    force: true,
    confirmForce: true,
    previewID: preview.result.preview_id,
    operationID,
  });
  io.setBusy(false);
  if (!outcome.ok) {
    io.setStatus(outcome.message, "is-error");
    return outcome.uncertain ? { kind: "uncertain", operationID } : { kind: "halted" };
  }
  return { kind: "restored", message: outcome.result.message || "Undo restored paths." };
};

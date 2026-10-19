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

const createHistoryPanelController = (
  elements: HistoryPanelElements,
  deps: HistoryPanelDeps
): { bind: () => void; load: (successMessage?: string) => Promise<void> } => {
  const baseFetch = deps.fetch || fetch;
  let entries: HistoryEntry[] = [];
  let conflictEntryID = "";
  let returnFocus: Element | null = null;
  // The message the next mutation-event reload shows instead of the generic one.
  let refreshMessage = "";

  const io: HistoryPanelIO = {
    elements,
    fetcher: (input, init) => baseFetch(input, init),
    token: elements.root.getAttribute("data-history-csrf") || "",
    confirm: deps.confirm
      ? (preview) => Promise.resolve(deps.confirm!(undoDescription(preview)))
      : (preview) => confirmUndo(preview, document, returnFocus),
    setBusy: (busy: boolean): void => {
      setHistoryPanelBusy(elements, busy, entries.length, conflictEntryID !== "");
    },
    setStatus: (message: string, kind: StatusKind = ""): void => {
      setHistoryPanelStatus(elements, message, kind);
    },
  };

  const hideConflict = (): void => {
    conflictEntryID = "";
    hideHistoryPanelConflict(elements);
  };

  const load = async (successMessage = ""): Promise<void> => {
    io.setBusy(true);
    io.setStatus("Loading history…", "is-loading");
    const outcome = await fetchHistoryPage(io.fetcher, HISTORY_ENDPOINT, {});
    if (!outcome.ok) {
      io.setBusy(false);
      io.setStatus(outcome.message, "is-error");
      return;
    }
    entries = outcome.result.entries || [];
    renderHistoryPanelEntries(elements, entries, (entryID) => void restore(entryID, plainRestore));
    io.setBusy(false);
    if (entries.length === 0) {
      io.setStatus("No recent changes.", "is-empty");
      return;
    }
    io.setStatus(
      successMessage || `${entries.length} recent changes.`,
      successMessage ? "is-success" : ""
    );
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
    const outcome = await run(io, entryID);
    if (outcome.kind === "conflict") {
      conflictEntryID = outcome.entryID;
      io.setBusy(false);
      return;
    }
    if (outcome.kind === "restored") {
      hideConflict();
      await load(outcome.message);
    }
    if (outcome.kind === "uncertain") offerUndoOutcomeCheck(io, outcome.operationID, () => { refreshMessage = "Undo confirmed."; });
  };

  const bind = (): void => {
    elements.refresh?.addEventListener("click", () => {
      hideConflict();
      void load();
    });
    elements.undoLatest?.addEventListener("click", () => {
      void restore("", plainRestore);
    });
    elements.force?.addEventListener("click", () => {
      if (conflictEntryID === "") {
        io.setStatus("No conflict selected.", "is-error");
        return;
      }
      void restore(conflictEntryID, forcedRestore);
    });
    window.addEventListener(managementMutationEventName, () => {
      hideConflict();
      void load(refreshMessage || "History refreshed.");
      refreshMessage = "";
    });
    hideConflict();
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

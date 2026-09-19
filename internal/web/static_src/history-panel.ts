import {
  HISTORY_ENDPOINT,
  HISTORY_PREVIEW_ENDPOINT,
  UNDO_ENDPOINT,
  managementMutationEventName,
  type HistoryEntry,
  type HistoryPreviewResult,
} from "./api";
import {
  conflicted,
  fetchHistoryPage,
  previewHistoryRestore,
  restoreHistoryEntry,
  type HistoryFetch,
} from "./history-requests";
import {
  findHistoryPanelElements,
  hideHistoryPanelConflict,
  historyForceConsentMessage,
  renderHistoryPanelEntries,
  setHistoryPanelBusy,
  setHistoryPanelStatus,
  showHistoryPanelConflict,
  type HistoryPanelElements,
} from "./history-panel-view";
import type { StatusKind } from "./status";

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
  confirm: ConfirmLike;
  setBusy: (busy: boolean) => void;
  setStatus: (message: string, kind?: StatusKind) => void;
};

type RestoreOutcome =
  | { kind: "restored"; message: string }
  // entryID is the entry the *server* resolved, not the one that was asked for.
  // They differ for "undo the latest change", which is posted with no identifier
  // and can only be forced against the identifier the refusal answers with.
  | { kind: "conflict"; entryID: string }
  | { kind: "halted" };

const createHistoryPanelController = (
  elements: HistoryPanelElements,
  deps: HistoryPanelDeps
): { bind: () => void; load: (successMessage?: string) => Promise<void> } => {
  const baseFetch = deps.fetch || fetch;
  let entries: HistoryEntry[] = [];
  let conflictEntryID = "";

  const io: HistoryPanelIO = {
    elements,
    fetcher: (input, init) => baseFetch(input, init),
    token: elements.root.getAttribute("data-history-csrf") || "",
    confirm: deps.confirm || ((message: string): boolean => window.confirm(message)),
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
    io.setStatus("Loading history...", "is-loading");
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
    const outcome = await run(io, entryID);
    if (outcome.kind === "conflict") {
      conflictEntryID = outcome.entryID;
      return;
    }
    if (outcome.kind === "restored") {
      hideConflict();
      await load(outcome.message);
    }
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
      void load("History refreshed.");
    });
    hideConflict();
  };

  return { bind, load };
};

/** plainRestore is the undo that refuses rather than overwrite. A refusal is
 * reported with the paths the server named, and is the point at which forcing
 * becomes available — not a fault to stop on. */
const plainRestore = async (io: HistoryPanelIO, entryID: string): Promise<RestoreOutcome> => {
  if (!io.confirm("Undo this change?")) {
    io.setStatus("Undo canceled.");
    return { kind: "halted" };
  }
  io.setBusy(true);
  io.setStatus("Undoing change...", "is-loading");
  const outcome = await restoreHistoryEntry(io.fetcher, UNDO_ENDPOINT, { token: io.token, entryID });
  io.setBusy(false);
  if (outcome.ok) {
    return { kind: "restored", message: outcome.result.message || "Undo restored paths." };
  }
  if (!conflicted(outcome)) {
    io.setStatus(outcome.message, "is-error");
    return { kind: "halted" };
  }
  // The refusal is what names the entry when none was sent. Forcing is offered
  // only once there is an identifier to force *against*: re-posting "the latest"
  // with force would resolve it a second time, against a journal that may have
  // gained an entry since the refusal — and force is exactly the flag that would
  // then overwrite without asking.
  const resolved = outcome.entryID || entryID;
  showHistoryPanelConflict(io.elements, outcome.conflicts, resolved !== "");
  io.setStatus(outcome.message, "is-error");
  return { kind: "conflict", entryID: resolved };
};

/** forcedRestore previews first, every time. The receipt it takes is what the
 * server requires before it will replace newer work, and the confirmation is
 * worded from that preview rather than from the earlier refusal. */
const forcedRestore = async (io: HistoryPanelIO, entryID: string): Promise<RestoreOutcome> => {
  io.setBusy(true);
  io.setStatus("Checking what this would replace...", "is-loading");
  const preview = await previewHistoryRestore(io.fetcher, HISTORY_PREVIEW_ENDPOINT, io.token, entryID);
  io.setBusy(false);
  if (!preview.ok) {
    io.setStatus(preview.message, "is-error");
    return { kind: "halted" };
  }
  showHistoryPanelConflict(io.elements, preview.result.conflicts || [], true);
  if (!io.confirm(forceConsentMessage(preview.result))) {
    io.setStatus("Force undo canceled.");
    return { kind: "halted" };
  }
  io.setBusy(true);
  io.setStatus("Forcing undo...", "is-loading");
  const outcome = await restoreHistoryEntry(io.fetcher, UNDO_ENDPOINT, {
    token: io.token,
    entryID: preview.result.entry_id || entryID,
    force: true,
    confirmForce: true,
    previewID: preview.result.preview_id,
  });
  io.setBusy(false);
  if (!outcome.ok) {
    io.setStatus(outcome.message, "is-error");
    return { kind: "halted" };
  }
  return { kind: "restored", message: outcome.result.message || "Undo restored paths." };
};

const forceConsentMessage = (preview: HistoryPreviewResult): string =>
  historyForceConsentMessage(
    (preview.conflicts || []).map((conflict) => conflict.path).filter((path) => path !== ""),
    (preview.changes || []).length
  );

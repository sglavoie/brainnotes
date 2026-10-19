import type { HistoryPreviewResult, HistoryQuery, HistoryResult } from "./api";
import {
  fetchHistoryPage,
  previewHistoryRestore,
  restoreHistoryEntry,
  stalePreview,
  type HistoryFetch,
} from "./history-requests";
import {
  findHistoryWorkspaceElements,
  type HistoryWorkspaceElements,
} from "./history-workspace-elements";
import {
  clearHistoryFilterGroup,
  summarizeHistoryFilters,
  readHistoryQuery,
  renderHistoryFilterGroup,
} from "./history-workspace-filters";
import {
  renderHistoryEntryList,
  renderHistoryWarnings,
  updateHistoryPager,
  renderHistoryEmptyStatus,
} from "./history-workspace-list";
import {
  hideHistoryRestorePanel,
  renderHistoryRestorePreview,
  syncHistoryRestoreConsent,
  showHistoryPreviewPending,
  focusHistoryPreview,
} from "./history-workspace-restore";
import { setStatusText, type StatusKind } from "./status";
import { restoreHistoryURL, writeHistoryURL } from "./history-workspace-url";

type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

export type HistoryWorkspaceDeps = {
  fetch?: FetchLike;
};

type WorkspaceState = {
  offset: number;
  previewIssue: number;
  previewTrigger: HTMLButtonElement | null;
  restoring: boolean;
  preview: HistoryPreviewResult | null;
  vocabularyRendered: boolean;
  urlFilters: HistoryQuery | null;
  /**
   * issued counts the page requests this workspace has started. A reply is
   * rendered only if it belongs to the newest one.
   *
   * Debouncing the path box narrows the race but does not close it: every filter
   * checkbox, both pager buttons and Refresh start a request immediately, and
   * nothing cancels the one already in flight. Two requests are therefore
   * ordinary, and responses can arrive in either order — so without this, a slow
   * first answer landing after a fast second one would repaint the list, the
   * pager and the offset with the *older* view while the controls still describe
   * the newer one. On this page that is not a cosmetic mismatch: the offset it
   * would restore is the one a subsequent Next pages on from.
   */
  issued: number;
  /**
   * success holds the outcome of a write until a page render puts it on screen.
   *
   * Kept on the state rather than passed as an argument because the render that
   * was meant to show it can be superseded: the reload a restore starts is in
   * flight for as long as any other, and a Refresh or a filter click landing
   * inside that window drops it. Every other message this page shows is a
   * restatement of what is already visible; this one is the only report of
   * something that happened once, so it waits for whichever render arrives.
   */
  success: string;
};

type WorkspaceContext = {
  elements: HistoryWorkspaceElements;
  fetcher: HistoryFetch;
  state: WorkspaceState;
  setStatus: (message: string, kind?: StatusKind) => void;
};

/**
 * The /history workspace: the whole recorded history of this vault, filtered
 * and paged, with restoring routed through a preview every time.
 *
 * The twenty-entry panel on the other pages answers "what did I just do"; this
 * page answers "where did that go", which is a different question and needs the
 * origins, the paths and the pages that question implies.
 */
export const initHistoryWorkspace = (deps: HistoryWorkspaceDeps = {}): void => {
  const elements = findHistoryWorkspaceElements(document);
  if (!elements) {
    return;
  }
  const baseFetch = deps.fetch || fetch;
  const ctx: WorkspaceContext = {
    elements,
    fetcher: (input, init) => baseFetch(input, init),
    state: { offset: 0, previewIssue: 0, previewTrigger: null, restoring: false, preview: null, vocabularyRendered: false, urlFilters: null, issued: 0, success: "" },
    setStatus: (message: string, kind: StatusKind = ""): void => {
      setStatusText(elements.status, message, kind);
    },
  };
  hydrateHistoryURL(ctx);
  hideHistoryRestorePanel(elements);
  bindHistoryWorkspace(ctx);
  void loadHistoryPage(ctx);
};

const bindHistoryWorkspace = (ctx: WorkspaceContext): void => {
  const { elements, state } = ctx;
  let pending = 0;
  const reload = (offset: number): void => {
    window.clearTimeout(pending);
    state.offset = Math.max(0, offset);
    writeHistoryURL(workspaceQuery(ctx), "push");
    void loadHistoryPage(ctx);
  };

  elements.filters?.addEventListener("submit", (event) => {
    event.preventDefault();
    reload(0);
  });
  elements.filters?.addEventListener("change", (event) => {
    // Path input already schedules a reload. Its blur-time change would remove
    // the empty-state action between pointerdown and click.
    if (event.target === elements.path) return;
    reload(0);
  });
  bindHistoryNavigation(ctx, () => window.clearTimeout(pending), () => {
    pending = window.setTimeout(() => reload(0), 300);
  });
  elements.refresh?.addEventListener("click", () => {
    reload(state.offset);
  });
  elements.clearFilters?.addEventListener("click", () => {
    state.urlFilters = null;
    if (elements.path) {
      elements.path.value = "";
    }
    if (elements.pathMatch) elements.pathMatch.value = "contains";
    if (elements.pageSizeSelect) elements.pageSizeSelect.value = String(elements.pageSize);
    clearHistoryFilterGroup(elements.originFilters);
    clearHistoryFilterGroup(elements.classFilters);
    reload(0);
  });
  elements.previous?.addEventListener("click", () => {
    reload(state.offset - pageStep(ctx));
  });
  elements.next?.addEventListener("click", () => {
    reload(state.offset + pageStep(ctx));
  });
  elements.restoreConsent?.addEventListener("change", () => {
    syncHistoryRestoreConsent(elements, (state.preview?.conflicts || []).length > 0);
  });
  elements.restoreConfirm?.addEventListener("click", () => {
    void applyHistoryRestore(ctx);
  });
  elements.restoreCancel?.addEventListener("click", () => {
    dismissHistoryPreview(ctx, true);
    ctx.setStatus("Restore canceled. Nothing was changed.");
  });
};

const bindHistoryNavigation = (ctx: WorkspaceContext, cancel: () => void, schedule: () => void): void => {
  ctx.elements.path?.addEventListener("input", () => {
    cancel();
    ctx.state.issued += 1;
    dismissHistoryPreview(ctx);
    schedule();
  });
  window.addEventListener("popstate", () => {
    if (!ctx.elements.root.isConnected) return;
    cancel();
    hydrateHistoryURL(ctx);
    dismissHistoryPreview(ctx);
    void loadHistoryPage(ctx);
  });
};

const hydrateHistoryURL = (ctx: WorkspaceContext): void => {
  const query = restoreHistoryURL(ctx.elements);
  ctx.state.offset = query.offset || 0;
  ctx.state.urlFilters = query;
};

const workspaceQuery = (ctx: WorkspaceContext): HistoryQuery => {
  const query = readHistoryQuery(ctx.elements, ctx.state.offset);
  // The first request needs the URL selections before the server has supplied
  // the vocabulary from which we can render checkboxes.
  if (!ctx.state.vocabularyRendered && ctx.state.urlFilters) {
    query.origins = ctx.state.urlFilters.origins;
    query.classes = ctx.state.urlFilters.classes;
  }
  return query;
};

const pageStep = (ctx: WorkspaceContext): number => {
  const limit = readHistoryQuery(ctx.elements, 0).limit;
  return limit && limit > 0 ? limit : ctx.elements.pageSize;
};

const loadHistoryPage = async (ctx: WorkspaceContext, success = ""): Promise<void> => {
  const { elements, state } = ctx;
  if (success !== "") {
    state.success = success;
  }
  dismissHistoryPreview(ctx);
  const issue = ++state.issued;
  elements.root.setAttribute("aria-busy", "true");
  ctx.setStatus("Loading history…", "is-loading");
  const query = workspaceQuery(ctx);
  writeHistoryURL(query, "replace");
  summarizeHistoryFilters(elements, query);
  const outcome = await fetchHistoryPage(ctx.fetcher, elements.listEndpoint, query);
  // Superseded: a newer request is the one the page is waiting on, so this reply
  // is dropped entirely — including the busy flag and the status line, which
  // describe that newer request now.
  if (issue !== state.issued) {
    return;
  }
  elements.root.setAttribute("aria-busy", "false");
  if (!outcome.ok) {
    // The pending write outcome is deliberately kept: this render failed, so it
    // never reported it, and the next one still has to.
    ctx.setStatus(outcome.message, "is-error");
    return;
  }
  const reporting = state.success;
  state.success = "";
  renderHistoryPage(ctx, outcome.result, query, reporting);
};

/** Everything a reply that survived the supersession check paints, separated
 * from the decisions about whether to paint it at all. */
const renderHistoryPage = (
  ctx: WorkspaceContext,
  result: HistoryResult,
  query: HistoryQuery,
  reporting: string
): void => {
  const { elements, state } = ctx;
  const entries = result.entries || [];
  // Taken from the reply because the reply is what the rest of this render
  // describes: the server answers with the window it actually read, and the
  // pager, the range label and the next Previous/Next click all have to agree
  // with that rather than with what was asked for. Walking off the end is not
  // what this prevents — has_more is; a page past the end comes back empty with
  // Next disabled, and this keeps the offset saying where that empty page was.
  state.offset = result.offset || 0;
  writeHistoryURL({ ...query, offset: state.offset }, "replace");
  renderVocabularyOnce(ctx, result, query.origins || [], query.classes || []);
  summarizeHistoryFilters(elements, query);
  renderHistoryWarnings(elements.warnings, result.warnings || [], result.warning_count || 0);
  renderHistoryEntryList(elements.list, entries, (entryID, trigger) => void openHistoryPreview(ctx, entryID, trigger));
  updateHistoryPager(elements, result, entries.length);
  reportPageStatus(ctx, result, entries.length, reporting, query);
};

/** The filter vocabulary is rendered once. Re-rendering it on every page would
 * replace the control somebody is part-way through using. */
const renderVocabularyOnce = (
  ctx: WorkspaceContext,
  result: HistoryResult,
  origins: string[],
  classes: string[]
): void => {
  if (ctx.state.vocabularyRendered) {
    return;
  }
  renderHistoryFilterGroup(ctx.elements.originFilters, "origin", result.origins || [], origins);
  renderHistoryFilterGroup(ctx.elements.classFilters, "class", result.classes || [], classes);
  ctx.state.vocabularyRendered = true;
};

const reportPageStatus = (
  ctx: WorkspaceContext,
  result: HistoryResult,
  shown: number,
  success: string,
  query: HistoryQuery
): void => {
  if (success !== "") {
    ctx.setStatus(success, "is-success");
    return;
  }
  const total = result.total || 0;
  if (total === 0) {
    renderHistoryEmptyStatus(ctx.elements, query, result.warning_count || 0);
    return;
  }
  ctx.setStatus(`Showing ${shown} of ${total} recorded changes.`);
};

// Invalidate the receipt and any pending response together: neither a later
// response nor an old confirm control may revive a dismissed selection.
const dismissHistoryPreview = (ctx: WorkspaceContext, returnFocus = false): void => {
  const trigger = ctx.state.previewTrigger;
  ctx.state.previewIssue += 1;
  ctx.state.preview = null;
  ctx.state.previewTrigger = null;
  hideHistoryRestorePanel(ctx.elements);
  if (returnFocus) {
    (trigger?.isConnected ? trigger : ctx.elements.refresh)?.focus();
  }
};

const openHistoryPreview = async (
  ctx: WorkspaceContext, entryID: string, trigger: HTMLButtonElement
): Promise<void> => {
  const { elements, state } = ctx;
  if (state.restoring) return;
  if (elements.token.trim() === "") {
    ctx.setStatus("This server does not accept restores.", "is-error");
    return;
  }
  dismissHistoryPreview(ctx);
  const issue = state.previewIssue;
  state.previewTrigger = trigger;
  showHistoryPreviewPending(elements);
  focusHistoryPreview(elements);
  ctx.setStatus("Checking what restoring this would do…", "is-loading");
  const outcome = await previewHistoryRestore(ctx.fetcher, elements.previewEndpoint, elements.token, entryID);
  if (issue !== state.previewIssue || !elements.root.isConnected) return;
  if (!outcome.ok) {
    dismissHistoryPreview(ctx, true);
    ctx.setStatus(outcome.message, "is-error");
    return;
  }
  state.preview = outcome.result;
  renderHistoryRestorePreview(elements, outcome.result);
  ctx.setStatus(
    (outcome.result.conflicts || []).length > 0
      ? "This restore would replace newer work. Read what it lists before confirming."
      : "Previewed. Nothing has changed yet.",
    (outcome.result.conflicts || []).length > 0 ? "is-warning" : ""
  );
};

const applyHistoryRestore = async (ctx: WorkspaceContext): Promise<void> => {
  const { elements, state } = ctx;
  if (state.restoring) return;
  const preview = state.preview;
  if (!preview) {
    ctx.setStatus("Preview a change before restoring it.", "is-error");
    return;
  }
  const forced = (preview.conflicts || []).length > 0;
  if (forced && !elements.restoreConsent?.checked) {
    ctx.setStatus("Confirm you understand what this replaces before restoring.", "is-error");
    return;
  }
  // Consume the local receipt before awaiting the write, preventing repeat clicks.
  state.restoring = true;
  dismissHistoryPreview(ctx, true);
  ctx.setStatus("Restoring…", "is-loading");
  const outcome = await restoreHistoryEntry(ctx.fetcher, elements.undoEndpoint, {
    token: elements.token,
    entryID: preview.entry_id,
    force: forced,
    confirmForce: forced,
    previewID: preview.preview_id,
  });
  state.restoring = false;
  state.offset = 0;
  if (!outcome.ok) {
    // Re-read on a refusal too. A restore error is not proof that nothing
    // happened: a snapshot failure means the files are already back and only the
    // safety commit is missing, and a rollback failure means content moved and
    // could not be put back — both of which the journal now has an entry for. A
    // list left showing the state before the attempt would be describing a vault
    // that no longer exists, right when somebody needs to see what it became.
    await loadHistoryPage(ctx);
    ctx.setStatus(restoreFailureMessage(outcome.message, stalePreview(outcome)), "is-error");
    return;
  }
  await loadHistoryPage(ctx, outcome.result.message || "Restored.");
};

const restoreFailureMessage = (message: string, stale: boolean): string =>
  stale ? `${message} Preview it again to see what changed.` : message;

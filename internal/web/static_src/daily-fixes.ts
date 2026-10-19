import { managementMutationEventName, type CockpitQueueResult } from "./api";
import {
  dispatchFixesClick,
  loadQueue as loadFixesQueue,
  type DailyFixesContext,
} from "./daily-fixes-actions";
import {
  readDailyFixesConfig,
  type DailyFixesDeps,
  type PreviewState,
} from "./daily-fixes-model";
import { clearPreviewEl } from "./daily-fixes-preview";
import {
  renderCounts,
  renderGroups,
  renderWarnings,
  type FixesCountElements,
} from "./daily-fixes-render";
import { queryAllElements, queryElement } from "./dom";
import { setStatusText, type StatusKind } from "./status";
import { offerWriteOutcomeCheck } from "./write-outcome-check";

export {
  fixCountsStatusMessage,
  fixKindLabel,
  fixOpenKeyFromRelPath,
  readDailyFixesConfig,
  type DailyFixesConfig,
  type DailyFixesDeps,
  type FixesFetchRequest,
} from "./daily-fixes-model";
export {
  buildFixesApplyFetchRequest,
  buildFixesItemFetchRequest,
  buildFixesPreviewFetchRequest,
} from "./daily-fixes-requests";
export { type DailyFixesContext } from "./daily-fixes-actions";

// readFixesCountElements collects the four header counters. They are looked up
// together and only ever handed to renderCounts as a unit, so they are read as
// one rather than as four more lines of element wiring in the panel.
const readFixesCountElements = (root: HTMLElement): FixesCountElements => ({
  active: queryElement(root, "[data-fixes-count-active]", HTMLElement),
  doneHidden: queryElement(root, "[data-fixes-count-done-hidden]", HTMLElement),
  snoozedHidden: queryElement(root, "[data-fixes-count-snoozed-hidden]", HTMLElement),
  totalGenerated: queryElement(root, "[data-fixes-count-total-generated]", HTMLElement),
});

// initDailyFixes owns the panel: its elements, its mutable state, and the
// wiring. Every queue action itself lives in daily-fixes-actions and receives
// this context, so each one is a named function rather than a closure inside a
// single very long arrow.
// A confirmed fix changed notes other panels show, so it is announced the way a
// fix that answered is; this queue reloads with the confirmation.
const offerFixOutcomeCheck = (ctx: DailyFixesContext, errorEl: HTMLElement | null, operationID: string): void => {
  if (!errorEl) return;
  offerWriteOutcomeCheck(errorEl, operationID, ctx.fetcher, "Fix", () => {
    ctx.suppressNextMutation();
    window.dispatchEvent(new CustomEvent(managementMutationEventName));
    void ctx.loadQueue("Fix confirmed.");
  });
};

export const initDailyFixes = (deps: DailyFixesDeps = {}): void => {
  const root = queryElement(document, "[data-fixes-panel]", HTMLElement);
  if (!root) {
    return;
  }

  const statusEl = queryElement(root, "[data-fixes-status]", HTMLElement);
  const errorEl = queryElement(root, "[data-fixes-error]", HTMLElement);
  const staleEl = queryElement(root, "[data-fixes-stale]", HTMLElement);
  const emptyEl = queryElement(root, "[data-fixes-empty]", HTMLElement);
  const warningListEl = queryElement(root, "[data-fixes-warnings]", HTMLElement);
  const listEl = queryElement(root, "[data-fixes-list]", HTMLElement);
  if (!statusEl || !listEl) {
    return;
  }

  const countEls = readFixesCountElements(root);
  const previews = new Map<string, PreviewState>();
  let busy = false;
  let ignoreNextMutation = false;

  const setStatus = (message: string, kind: StatusKind = ""): void => {
    setStatusText(statusEl, message, kind);
  };

  const setError = (message: string): void => {
    if (!errorEl) {
      return;
    }
    errorEl.textContent = message;
    errorEl.hidden = message === "";
  };

  const setBusy = (next: boolean): void => {
    busy = next;
    root.setAttribute("aria-busy", next ? "true" : "false");
    for (const button of queryAllElements(root, "[data-fixes-button]", HTMLButtonElement)) {
      button.disabled = next;
    }
  };

  const renderResult = (result: CockpitQueueResult): void => {
    previews.clear();
    renderCounts(countEls, result.counts);
    renderWarnings(warningListEl, result);
    renderGroups(listEl, result.tasks);
    const hasTasks = result.tasks.length > 0;
    listEl.hidden = !hasTasks;
    if (emptyEl) {
      emptyEl.hidden = hasTasks;
    }
    setBusy(busy);
  };

  const ctx: DailyFixesContext = {
    config: readDailyFixesConfig(root),
    fetcher: deps.fetch || ((input, init) => window.fetch(input, init)),
    listEl,
    previews,
    setBusy,
    setStatus,
    setError,
    showError: (message: string): void => {
      setError(message);
      setStatus("");
    },
    setQueueStale: (stale: boolean): void => {
      if (staleEl) staleEl.hidden = !stale;
    },
    renderResult,
    clearPreview: (id: string): void => {
      previews.delete(id);
      clearPreviewEl(listEl, id);
    },
    loadQueue: (successMessage = ""): Promise<void> => loadFixesQueue(ctx, successMessage),
    suppressNextMutation: (): void => {
      ignoreNextMutation = true;
    },
    offerOutcomeCheck: (operationID: string): void => offerFixOutcomeCheck(ctx, errorEl, operationID),
  };

  root.addEventListener("click", (event) => {
    dispatchFixesClick(ctx, root, event.target);
  });

  window.addEventListener(managementMutationEventName, () => {
    if (ignoreNextMutation) {
      ignoreNextMutation = false;
      return;
    }
    void ctx.loadQueue("Fixes refreshed.");
  });

  void ctx.loadQueue();
};

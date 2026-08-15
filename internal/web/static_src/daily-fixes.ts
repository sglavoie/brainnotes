import {
  buildOpenNoteForm,
  managementMutationEventName,
  normalizeCockpitQueueResponse,
  normalizeFixApplyResponse,
  normalizeFixPreviewResponse,
  normalizeManagementErrorMessage,
  normalizeOpenErrorMessage,
  type CockpitQueueResult,
} from "./api";
import {
  fixCountsStatusMessage,
  itemActionLoadingMessage,
  normalizeFixAction,
  normalizeItemAction,
  normalizeSnooze,
  readDailyFixesConfig,
  type DailyFixesDeps,
  type PreviewState,
} from "./daily-fixes-model";
import { clearPreviewEl, renderPreview } from "./daily-fixes-preview";
import {
  buildFixesApplyFetchRequest,
  buildFixesItemFetchRequest,
  buildFixesPreviewFetchRequest,
  jsonPostRequest,
  readResponsePayload,
} from "./daily-fixes-requests";
import { renderCounts, renderGroups, renderWarnings } from "./daily-fixes-render";
import { queryAllElements, queryElement } from "./dom";
import { setStatusText, type StatusKind } from "./status";

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

export const initDailyFixes = (deps: DailyFixesDeps = {}): void => {
  const root = queryElement(document, "[data-fixes-panel]", HTMLElement);
  if (!root) {
    return;
  }

  const statusEl = queryElement(root, "[data-fixes-status]", HTMLElement);
  const loadingEl = queryElement(root, "[data-fixes-loading]", HTMLElement);
  const errorEl = queryElement(root, "[data-fixes-error]", HTMLElement);
  const emptyEl = queryElement(root, "[data-fixes-empty]", HTMLElement);
  const warningListEl = queryElement(root, "[data-fixes-warnings]", HTMLElement);
  const listEl = queryElement(root, "[data-fixes-list]", HTMLElement);
  if (!statusEl || !listEl) {
    return;
  }

  const countEls = {
    active: queryElement(root, "[data-fixes-count-active]", HTMLElement),
    doneHidden: queryElement(root, "[data-fixes-count-done-hidden]", HTMLElement),
    snoozedHidden: queryElement(root, "[data-fixes-count-snoozed-hidden]", HTMLElement),
    totalGenerated: queryElement(root, "[data-fixes-count-total-generated]", HTMLElement),
  };
  const config = readDailyFixesConfig(root);
  const fetcher = deps.fetch || fetch;
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

  const showError = (message: string): void => {
    setError(message);
    setStatus(message, "is-error");
  };

  const setLoading = (loading: boolean): void => {
    if (loadingEl) {
      loadingEl.hidden = !loading;
    }
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

  const applyQueueResult = (
    payload: unknown,
    successMessage = "",
    successKind: StatusKind = ""
  ): boolean => {
    const result = normalizeCockpitQueueResponse(payload);
    if (!result.ok) {
      showError(result.message || "Cockpit request failed.");
      return false;
    }
    setError("");
    renderResult(result);
    const message = successMessage || fixCountsStatusMessage(result.counts);
    const kind = successKind || (result.counts.active === 0 ? "is-empty" : "");
    setStatus(message, kind);
    return true;
  };

  const loadQueue = async (successMessage = ""): Promise<void> => {
    setBusy(true);
    setLoading(true);
    setStatus("Loading fixes queue...", "is-loading");
    try {
      const resp = await fetcher(config.queueEndpoint, {
        headers: { Accept: "application/json" },
      });
      const payload = await readResponsePayload(resp);
      if (!resp.ok) {
        showError(normalizeManagementErrorMessage(resp.status, payload));
        return;
      }
      applyQueueResult(payload, successMessage, successMessage ? "is-success" : "");
    } catch {
      showError("Fixes queue unavailable.");
    } finally {
      setLoading(false);
      setBusy(false);
    }
  };

  const clearPreview = (id: string): void => {
    previews.delete(id);
    clearPreviewEl(listEl, id);
  };

  const runItemAction = async (button: HTMLButtonElement): Promise<void> => {
    const id = button.getAttribute("data-fixes-id") || "";
    const action = normalizeItemAction(button.getAttribute("data-fixes-item-action") || "");
    const snooze = normalizeSnooze(button.getAttribute("data-fixes-snooze") || "");
    if (!id || !action || !config.csrfToken) {
      showError("Fix action unavailable.");
      return;
    }
    if (action === "snooze" && !snooze) {
      showError("Fix snooze unavailable.");
      return;
    }

    setBusy(true);
    setStatus(itemActionLoadingMessage(action, snooze), "is-loading");
    try {
      const request = buildFixesItemFetchRequest(
        config.itemEndpoint,
        config.csrfToken,
        id,
        action,
        snooze
      );
      const resp = await fetcher(request.url, request.init);
      const payload = await readResponsePayload(resp);
      if (!resp.ok) {
        showError(normalizeManagementErrorMessage(resp.status, payload));
        return;
      }
      const result = normalizeCockpitQueueResponse(payload);
      if (action === "exempt_dead_end" || action === "clean_long_tail") {
        ignoreNextMutation = true;
        window.dispatchEvent(new CustomEvent(managementMutationEventName, { detail: payload }));
      }
      applyQueueResult(payload, result.message || "Fix updated.", "is-success");
    } catch {
      showError("Fix action failed.");
    } finally {
      setBusy(false);
    }
  };

  const runPreview = async (button: HTMLButtonElement): Promise<void> => {
    const id = button.getAttribute("data-fixes-id") || "";
    const action = normalizeFixAction(button.getAttribute("data-fixes-fix-action") || "");
    if (!id || !action || !config.csrfToken) {
      showError("Fix preview unavailable.");
      return;
    }

    setBusy(true);
    setStatus("Previewing fix...", "is-loading");
    try {
      const request = buildFixesPreviewFetchRequest(
        config.previewEndpoint,
        config.csrfToken,
        id,
        action
      );
      const resp = await fetcher(request.url, request.init);
      const payload = await readResponsePayload(resp);
      if (!resp.ok) {
        showError(normalizeManagementErrorMessage(resp.status, payload));
        return;
      }
      const result = normalizeFixPreviewResponse(payload);
      if (!result.ok || !result.preview_id) {
        showError(result.message || "Fix preview failed.");
        return;
      }
      previews.set(id, { action, previewID: result.preview_id });
      renderPreview(listEl, id, action, result.previews, result.message || "Preview ready.");
      setError("");
      setStatus(result.message || "Preview ready.", "is-success");
    } catch {
      showError("Fix preview failed.");
    } finally {
      setBusy(false);
    }
  };

  const runApply = async (button: HTMLButtonElement): Promise<void> => {
    const id = button.getAttribute("data-fixes-id") || "";
    const state = previews.get(id);
    if (!id || !state || !config.csrfToken) {
      showError("Preview the fix before applying it.");
      return;
    }

    setBusy(true);
    setStatus("Applying fix...", "is-loading");
    try {
      const request = buildFixesApplyFetchRequest(
        config.applyEndpoint,
        config.csrfToken,
        id,
        state.action,
        state.previewID
      );
      const resp = await fetcher(request.url, request.init);
      const payload = await readResponsePayload(resp);
      if (!resp.ok) {
        clearPreview(id);
        showError(normalizeManagementErrorMessage(resp.status, payload));
        await loadQueue();
        return;
      }
      const result = normalizeFixApplyResponse(payload);
      if (!result.ok) {
        clearPreview(id);
        showError(result.message || "Fix apply failed.");
        await loadQueue();
        return;
      }
      previews.delete(id);
      ignoreNextMutation = true;
      window.dispatchEvent(new CustomEvent(managementMutationEventName, { detail: result }));
      await loadQueue(result.message || "Fix applied.");
    } catch {
      clearPreview(id);
      showError("Fix apply failed.");
    } finally {
      setBusy(false);
    }
  };

  const runOpen = async (button: HTMLButtonElement): Promise<void> => {
    const key = button.getAttribute("data-fixes-open-key") || "";
    if (!key || !config.csrfToken) {
      showError("Open action unavailable.");
      return;
    }
    setBusy(true);
    setStatus("Opening editor...", "is-loading");
    try {
      const body = buildOpenNoteForm({ key, token: config.csrfToken });
      const request = jsonPostRequest(config.openEndpoint, config.csrfToken, body.toString());
      const resp = await fetcher(request.url, request.init);
      const payload = await readResponsePayload(resp);
      if (!resp.ok) {
        showError(normalizeOpenErrorMessage(resp.status, payload));
        return;
      }
      setError("");
      setStatus("Opened in editor. Refresh after saving changes.", "is-success");
    } catch {
      showError("Open failed.");
    } finally {
      setBusy(false);
    }
  };

  root.addEventListener("click", (event) => {
    const target = event.target;
    if (!(target instanceof Element)) {
      return;
    }
    const fix = target.closest<HTMLButtonElement>("[data-fixes-fix-action]");
    if (fix && root.contains(fix)) {
      void runPreview(fix);
      return;
    }
    const applyBtn = target.closest<HTMLButtonElement>("[data-fixes-preview-apply]");
    if (applyBtn && root.contains(applyBtn)) {
      void runApply(applyBtn);
      return;
    }
    const cancelBtn = target.closest<HTMLButtonElement>("[data-fixes-preview-cancel]");
    if (cancelBtn && root.contains(cancelBtn)) {
      clearPreview(cancelBtn.getAttribute("data-fixes-id") || "");
      return;
    }
    const itemBtn = target.closest<HTMLButtonElement>("[data-fixes-item-action]");
    if (itemBtn && root.contains(itemBtn)) {
      void runItemAction(itemBtn);
      return;
    }
    const openBtn = target.closest<HTMLButtonElement>("[data-fixes-open-key]");
    if (openBtn && root.contains(openBtn)) {
      void runOpen(openBtn);
    }
  });

  window.addEventListener(managementMutationEventName, () => {
    if (ignoreNextMutation) {
      ignoreNextMutation = false;
      return;
    }
    void loadQueue("Fixes refreshed.");
  });

  void loadQueue();
};

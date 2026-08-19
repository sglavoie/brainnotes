import {
  managementMutationEventName,
  normalizeBulkManagementResult,
  normalizeBulkNoteKeys,
  normalizeBulkPreviewManagementResult,
  normalizeManagementErrorMessage,
  normalizeSearchResults,
  type BulkAction,
  type BulkPreviewManagementResult,
  type SearchResult,
} from "./api";
import { queryAllElements } from "./dom";
import {
  buildSearchAPIURL,
  filterChangeEventName,
  hasActiveSharedFilters,
  readPageQuery,
  shouldHandleFilterChangeForMode,
  syncPageQueryParam,
} from "./filters";
import { resolveOverviewSearchElements } from "./overview-search-elements";
import {
  bulkApplyFallbackMessage,
  bulkStatusMessage,
  overviewSearchEmptyMessage,
  previewFingerprint,
  readMoveFields,
  readSetFields,
  readUnsetFields,
  type BulkActionFields,
  type BulkPreviewState,
  type BulkStatusKind,
  type OverviewSearchDeps,
} from "./overview-search-model";
import {
  overviewSearchQuery,
  selectedOverviewKeys,
  setOverviewBusy,
  syncOverviewActionButton,
} from "./overview-search-controller";
import {
  renderOverviewSearchResult,
  renderPreviewPanel,
  syncPreviewAttributes,
} from "./overview-search-render";
import { postBulkApply, postBulkPreview } from "./overview-search-requests";
import { setStatusText, type StatusKind } from "./status";

export {
  overviewSearchEmptyMessage,
  type OverviewSearchDeps,
  type OverviewSearchResultOptions,
} from "./overview-search-model";
export { renderOverviewSearchResult } from "./overview-search-render";

export const initOverviewSearchResults = (deps: OverviewSearchDeps = {}): void => {
  const els = resolveOverviewSearchElements();
  if (!els) {
    return;
  }

  const {
    root,
    input,
    statusEl,
    listEl,
    bulkRoot,
    selectedCountEl,
    bulkStatusEl,
    previewApplyButton,
    previewClearButton,
    csrfToken,
    trashButton,
    moveForm,
    moveButton,
    setForm,
    setButton,
    unsetForm,
    unsetButton,
  } = els;
  const fetcher = deps.fetch || fetch;
  const selectedKeys = new Map<string, string>();
  let bulkBusy = false;
  let previewState: BulkPreviewState | null = null;
  let debounceToken = 0;
  let ignoreNextManagementMutationClear = false;
  let requestToken = 0;

  const setStatus = (message: string, kind: StatusKind = ""): void => {
    setStatusText(statusEl, message, kind, ["is-loading", "is-empty", "is-error"]);
  };

  const setBulkStatus = (message: string, kind: BulkStatusKind = ""): void => {
    setStatusText(bulkStatusEl, message || " ", kind, [
      "is-success",
      "is-error",
      "is-loading",
    ]);
  };

  const previewMatches = (
    action: BulkAction,
    fields: BulkActionFields,
    keys = selectedOverviewKeys(selectedKeys)
  ): boolean =>
    Boolean(
      previewState &&
      previewState.previewID &&
      previewState.fingerprint === previewFingerprint(action, fields, keys)
    );

  const readFieldsForAction = (action: BulkAction): BulkActionFields => {
    switch (action) {
      case "move":
        return readMoveFields(moveForm);
      case "frontmatter_set":
        return readSetFields(setForm);
      case "frontmatter_unset":
        return readUnsetFields(unsetForm);
      default:
        return {};
    }
  };

  const updateBulkControls = (): void => {
    if (!bulkRoot) {
      return;
    }
    const count = selectedKeys.size;
    bulkRoot.hidden = count === 0;
    if (selectedCountEl) {
      selectedCountEl.textContent = count === 1 ? "1 selected" : `${count} selected`;
    }
    for (const control of queryAllElements(bulkRoot, "button, input, select", HTMLElement)) {
      if (
        control instanceof HTMLButtonElement ||
        control instanceof HTMLInputElement ||
        control instanceof HTMLSelectElement
      ) {
        control.disabled = bulkBusy || count === 0;
      }
    }
    syncOverviewActionButton(trashButton, "trash", previewMatches("trash", {}));
    syncOverviewActionButton(moveButton, "move", previewMatches("move", readMoveFields(moveForm)));
    syncOverviewActionButton(setButton, "frontmatter_set", previewMatches("frontmatter_set", readSetFields(setForm)));
    syncOverviewActionButton(unsetButton, "frontmatter_unset", previewMatches("frontmatter_unset", readUnsetFields(unsetForm)));
    if (previewApplyButton) {
      previewApplyButton.disabled = bulkBusy || count === 0 || previewState === null;
    }
    if (previewClearButton) {
      previewClearButton.disabled = bulkBusy || previewState === null;
    }
    renderPreviewPanel(els, previewState);
  };

  const clearPreviewState = ({ clearStatus = false }: { clearStatus?: boolean } = {}): void => {
    const hadPreview = previewState !== null;
    previewState = null;
    syncPreviewAttributes(bulkRoot, previewState);
    updateBulkControls();
    if (clearStatus && hadPreview) {
      setBulkStatus("");
    }
  };

  const setPreviewState = (
    action: BulkAction,
    fields: BulkActionFields,
    result: BulkPreviewManagementResult
  ): void => {
    previewState = {
      action,
      fingerprint: previewFingerprint(action, fields, selectedOverviewKeys(selectedKeys)),
      previewID: result.preview_id || "",
      expiresAt: result.expires_at,
      result,
    };
    syncPreviewAttributes(bulkRoot, previewState);
    updateBulkControls();
  };

  const setBulkBusy = (busy: boolean): void => {
    bulkBusy = busy;
    updateBulkControls();
  };

  const setSelected = (result: SearchResult, selected: boolean): void => {
    const key = normalizeBulkNoteKeys([result.key])[0] || "";
    if (key === "") {
      return;
    }
    clearPreviewState({ clearStatus: true });
    if (selected) {
      selectedKeys.set(key, result.title || key);
    } else {
      selectedKeys.delete(key);
    }
    updateBulkControls();
  };

  const pruneSelectionToRenderedKeys = (visibleKeys: Set<string>): void => {
    let changed = false;
    for (const key of selectedKeys.keys()) {
      if (!visibleKeys.has(key)) {
        selectedKeys.delete(key);
        changed = true;
      }
    }
    if (changed) {
      clearPreviewState({ clearStatus: true });
    }
    updateBulkControls();
  };

  const clearResults = (message: string, kind: StatusKind): void => {
    listEl.textContent = "";
    listEl.hidden = true;
    selectedKeys.clear();
    clearPreviewState({ clearStatus: true });
    setStatus(message, kind);
  };

  const renderResults = (payload: unknown, query: string): void => {
    const results = normalizeSearchResults(payload);
    listEl.textContent = "";

    let renderedCount = 0;
    const visibleKeys = new Set<string>();
    for (const result of results) {
      const key = normalizeBulkNoteKeys([result.key])[0] || "";
      if (renderOverviewSearchResult(listEl, result, {
        selected: key !== "" && selectedKeys.has(key),
        onSelectionChange: setSelected,
      })) {
        renderedCount += 1;
        if (key !== "") {
          visibleKeys.add(key);
        }
      }
    }
    pruneSelectionToRenderedKeys(visibleKeys);

    if (renderedCount === 0) {
      clearResults(overviewSearchEmptyMessage(query), "is-empty");
      return;
    }

    listEl.hidden = false;
    const noun = renderedCount === 1 ? "note" : "notes";
    const scope = query ? ` for "${query}"` : "";
    setStatus(`${renderedCount} matching ${noun}${scope}.`, "");
  };

  const scheduleSearch = (delay: number): void => {
    const query = overviewSearchQuery(input);
    window.clearTimeout(debounceToken);
    syncPageQueryParam(query);
    clearPreviewState({ clearStatus: true });

    if (query === "" && !hasActiveSharedFilters()) {
      requestToken += 1;
      setOverviewBusy(root, false);
      clearResults("No query or filters selected.", "is-empty");
      return;
    }

    const localToken = ++requestToken;
    setOverviewBusy(root, true);
    setStatus("Loading matching notes...", "is-loading");
    debounceToken = window.setTimeout(async () => {
      try {
        const resp = await fetcher(buildSearchAPIURL(query, true), {
          headers: { Accept: "application/json" },
        });
        if (!resp.ok) {
          throw new Error("search request failed");
        }
        const payload = await resp.json();
        if (localToken !== requestToken) {
          return;
        }
        renderResults(payload, query);
      } catch {
        if (localToken === requestToken) {
          clearResults("Search unavailable.", "is-error");
        }
      } finally {
        if (localToken === requestToken) {
          setOverviewBusy(root, false);
        }
      }
    }, delay);
  };

  if (input instanceof HTMLInputElement) {
    input.value = readPageQuery();
    input.addEventListener("input", () => {
      scheduleSearch(150);
    });
  }

  window.addEventListener(filterChangeEventName, (event) => {
    if (!shouldHandleFilterChangeForMode(event, "search")) {
      return;
    }
    scheduleSearch(0);
  });

  window.addEventListener(managementMutationEventName, () => {
    if (ignoreNextManagementMutationClear) {
      ignoreNextManagementMutationClear = false;
      return;
    }
    clearPreviewState({ clearStatus: true });
  });

  const runBulkPreview = async (
    action: BulkAction,
    fields: BulkActionFields,
    fallbackMessage: string
  ): Promise<void> => {
    const keys = selectedOverviewKeys(selectedKeys);
    if (!bulkRoot || csrfToken.trim() === "") {
      setBulkStatus("Bulk action unavailable.", "is-error");
      return;
    }
    if (keys.length === 0) {
      setBulkStatus("Select one or more notes.", "is-error");
      return;
    }

    setBulkBusy(true);
    setBulkStatus("Previewing bulk action...", "is-loading");
    try {
      const { resp, payload } = await postBulkPreview(
        fetcher,
        csrfToken,
        action,
        keys,
        fields
      );
      if (!resp.ok) {
        clearPreviewState();
        setBulkStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
        return;
      }
      const result = normalizeBulkPreviewManagementResult(payload);
      if (!result.ok || !result.preview_id) {
        clearPreviewState();
        setBulkStatus(result.message || "Bulk preview failed.", "is-error");
        return;
      }
      setPreviewState(action, fields, result);
      const failures = Array.isArray(result.failures) ? result.failures : [];
      setBulkStatus(
        bulkStatusMessage(result, fallbackMessage),
        failures.length > 0 ? "is-error" : "is-success"
      );
    } catch {
      clearPreviewState();
      setBulkStatus("Bulk preview failed.", "is-error");
    } finally {
      setBulkBusy(false);
    }
  };

  const runBulkApply = async (
    action: BulkAction,
    fields: BulkActionFields,
    fallbackMessage: string
  ): Promise<void> => {
    const keys = selectedOverviewKeys(selectedKeys);
    if (!bulkRoot || csrfToken.trim() === "") {
      setBulkStatus("Bulk action unavailable.", "is-error");
      return;
    }
    if (keys.length === 0) {
      setBulkStatus("Select one or more notes.", "is-error");
      return;
    }
    if (!previewState || !previewMatches(action, fields, keys)) {
      clearPreviewState();
      setBulkStatus("Preview the bulk action before applying it.", "is-error");
      return;
    }

    setBulkBusy(true);
    setBulkStatus("Applying bulk action...", "is-loading");
    try {
      const { resp, payload } = await postBulkApply(
        fetcher,
        csrfToken,
        action,
        keys,
        fields,
        previewState.previewID
      );
      if (!resp.ok) {
        clearPreviewState();
        setBulkStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
        return;
      }
      const result = normalizeBulkManagementResult(payload);
      const failures = Array.isArray(result.failures) ? result.failures : [];
      setBulkStatus(
        bulkStatusMessage(result, fallbackMessage),
        failures.length > 0 ? "is-error" : "is-success"
      );
      clearPreviewState();
      selectedKeys.clear();
      updateBulkControls();
      ignoreNextManagementMutationClear = true;
      window.dispatchEvent(new CustomEvent(managementMutationEventName, {
        detail: result,
      }));
      scheduleSearch(0);
    } catch {
      clearPreviewState();
      setBulkStatus("Bulk action failed.", "is-error");
    } finally {
      setBulkBusy(false);
    }
  };

  const runBulkAction = async (
    action: BulkAction,
    fields: BulkActionFields,
    fallbackMessage: string
  ): Promise<void> => {
    if (previewMatches(action, fields)) {
      await runBulkApply(action, fields, fallbackMessage);
      return;
    }
    await runBulkPreview(action, fields, fallbackMessage);
  };

  trashButton?.addEventListener("click", () => {
    void runBulkAction("trash", {}, bulkApplyFallbackMessage("trash"));
  });

  moveForm?.addEventListener("submit", (event) => {
    event.preventDefault();
    void runBulkAction("move", readMoveFields(moveForm), bulkApplyFallbackMessage("move"));
  });

  setForm?.addEventListener("submit", (event) => {
    event.preventDefault();
    const fields = readSetFields(setForm);
    if (!fields.property) {
      setBulkStatus("Property is required.", "is-error");
      return;
    }
    void runBulkAction("frontmatter_set", fields, bulkApplyFallbackMessage("frontmatter_set"));
  });

  unsetForm?.addEventListener("submit", (event) => {
    event.preventDefault();
    const fields = readUnsetFields(unsetForm);
    if (!fields.property) {
      setBulkStatus("Property is required.", "is-error");
      return;
    }
    void runBulkAction("frontmatter_unset", fields, bulkApplyFallbackMessage("frontmatter_unset"));
  });

  previewApplyButton?.addEventListener("click", () => {
    if (!previewState) {
      setBulkStatus("Preview the bulk action before applying it.", "is-error");
      return;
    }
    void runBulkApply(
      previewState.action,
      readFieldsForAction(previewState.action),
      bulkApplyFallbackMessage(previewState.action)
    );
  });

  previewClearButton?.addEventListener("click", () => {
    clearPreviewState({ clearStatus: true });
  });

  const bindPreviewInvalidation = (form: HTMLFormElement | null): void => {
    if (!form) {
      return;
    }
    form.addEventListener("input", () => {
      clearPreviewState({ clearStatus: true });
    });
    form.addEventListener("change", () => {
      clearPreviewState({ clearStatus: true });
    });
  };

  bindPreviewInvalidation(moveForm);
  bindPreviewInvalidation(setForm);
  bindPreviewInvalidation(unsetForm);

  updateBulkControls();
  scheduleSearch(0);
};

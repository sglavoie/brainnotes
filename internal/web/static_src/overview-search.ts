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
  hasActiveFilters,
  readPageQuery,
  shouldHandleFilterChangeForMode,
  syncPageQueryParam,
} from "./filters";
import { resolveOverviewSearchElements, type OverviewSearchElements } from "./overview-search-elements";
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
import { readSearchCounts, searchSummary, type SearchCounts } from "./search-summary";
import { overviewSelectionInputs, syncOverviewSelectionControls } from "./overview-search-selection";

export {
  overviewSearchEmptyMessage,
  type OverviewSearchDeps,
  type OverviewSearchResultOptions,
} from "./overview-search-model";
export { renderOverviewSearchResult } from "./overview-search-render";

class OverviewSearch {
  private readonly fetcher: typeof fetch;
  private readonly selectedKeys = new Map<string, string>();
  private bulkBusy = false;
  private previewState: BulkPreviewState | null = null;
  private debounceToken = 0;
  private ignoreNextManagementMutationClear = false;
  private requestToken = 0;

  constructor(private readonly els: OverviewSearchElements, deps: OverviewSearchDeps) {
    this.fetcher = deps.fetch || ((input, init) => fetch(input, init));
    this.bindSearchEvents();
    this.bindBulkEvents();
    this.bindSelectionEvents();
    this.bindPreviewInvalidation(els.moveForm);
    this.bindPreviewInvalidation(els.setForm);
    this.bindPreviewInvalidation(els.unsetForm);
    this.updateBulkControls();
    this.scheduleSearch(0);
  }

  private setStatus = (message: string, kind: StatusKind = ""): void => {
    setStatusText(this.els.statusEl, message, kind, ["is-loading", "is-empty", "is-error"]);
  };

  private setBulkStatus = (message: string, kind: BulkStatusKind = ""): void => {
    setStatusText(this.els.bulkStatusEl, message || " ", kind, [
      "is-success",
      "is-error",
      "is-loading",
    ]);
  };

  private previewMatches = (
    action: BulkAction,
    fields: BulkActionFields,
    keys = selectedOverviewKeys(this.selectedKeys)
  ): boolean =>
    Boolean(
      this.previewState &&
      this.previewState.previewID &&
      this.previewState.fingerprint === previewFingerprint(action, fields, keys)
    );

  private readFieldsForAction = (action: BulkAction): BulkActionFields => {
    switch (action) {
      case "move":
        return readMoveFields(this.els.moveForm);
      case "frontmatter_set":
        return readSetFields(this.els.setForm);
      case "frontmatter_unset":
        return readUnsetFields(this.els.unsetForm);
      default:
        return {};
    }
  };

  private disableBulkControls = (root: HTMLElement, disabled: boolean): void => {
    for (const control of queryAllElements(root, "button, input, select", HTMLElement)) {
      if (
        control instanceof HTMLButtonElement ||
        control instanceof HTMLInputElement ||
        control instanceof HTMLSelectElement
      ) {
        control.disabled = disabled;
      }
    }
  };

  private selectionBusy = (): boolean =>
    this.bulkBusy || this.els.root.getAttribute("aria-busy") === "true";

  private updateBulkControls = (): void => {
    syncOverviewSelectionControls(this.els.root, this.els.listEl, this.selectedKeys.size,
      this.selectionBusy());
    if (!this.els.bulkRoot) {
      return;
    }
    const count = this.selectedKeys.size;
    this.els.bulkRoot.hidden = count === 0;
    if (this.els.selectedCountEl) {
      this.els.selectedCountEl.textContent = count === 1 ? "1 selected" : `${count} selected`;
    }
    this.disableBulkControls(this.els.bulkRoot, this.bulkBusy || count === 0);
    syncOverviewActionButton(this.els.trashButton, "trash", this.previewMatches("trash", {}));
    syncOverviewActionButton(this.els.moveButton, "move", this.previewMatches("move", readMoveFields(this.els.moveForm)));
    syncOverviewActionButton(this.els.setButton, "frontmatter_set", this.previewMatches("frontmatter_set", readSetFields(this.els.setForm)));
    syncOverviewActionButton(this.els.unsetButton, "frontmatter_unset", this.previewMatches("frontmatter_unset", readUnsetFields(this.els.unsetForm)));
    if (this.els.previewApplyButton) {
      this.els.previewApplyButton.disabled = this.bulkBusy || count === 0 || this.previewState === null;
    }
    if (this.els.previewClearButton) {
      this.els.previewClearButton.disabled = this.bulkBusy || this.previewState === null;
    }
    renderPreviewPanel(this.els, this.previewState);
  };

  private clearPreviewState = ({ clearStatus = false }: { clearStatus?: boolean } = {}): void => {
    const hadPreview = this.previewState !== null;
    this.previewState = null;
    syncPreviewAttributes(this.els.bulkRoot, this.previewState);
    this.updateBulkControls();
    if (clearStatus && hadPreview) {
      this.setBulkStatus("");
    }
  };

  private setPreviewState = (
    action: BulkAction,
    fields: BulkActionFields,
    result: BulkPreviewManagementResult
  ): void => {
    this.previewState = {
      action,
      fingerprint: previewFingerprint(action, fields, selectedOverviewKeys(this.selectedKeys)),
      previewID: result.preview_id || "",
      expiresAt: result.expires_at,
      result,
    };
    syncPreviewAttributes(this.els.bulkRoot, this.previewState);
    this.updateBulkControls();
  };

  private setBulkBusy = (busy: boolean): void => {
    this.bulkBusy = busy;
    this.updateBulkControls();
  };

  private setSelected = (result: SearchResult, selected: boolean): void => {
    const key = normalizeBulkNoteKeys([result.key])[0] || "";
    if (key === "") {
      return;
    }
    this.clearPreviewState({ clearStatus: true });
    if (selected) {
      this.selectedKeys.set(key, result.title || key);
    } else {
      this.selectedKeys.delete(key);
    }
    this.updateBulkControls();
  };

  private selectDisplayed = (selected: boolean): void => {
    if (this.selectionBusy()) {
      return;
    }
    this.selectedKeys.clear();
    for (const input of overviewSelectionInputs(this.els.listEl)) {
      input.checked = selected;
      if (selected) {
        this.selectedKeys.set(input.value, input.value);
      }
    }
    this.clearPreviewState({ clearStatus: true });
  };

  private bindSelectionEvents = (): void => {
    this.els.root.querySelector("[data-overview-select-displayed]")?.addEventListener("click", () => {
      this.selectDisplayed(true);
    });
    this.els.root.querySelector("[data-overview-clear-selection]")?.addEventListener("click", () => {
      this.selectDisplayed(false);
    });
  };

  private pruneSelectionToRenderedKeys = (visibleKeys: Set<string>): void => {
    let changed = false;
    for (const key of this.selectedKeys.keys()) {
      if (!visibleKeys.has(key)) {
        this.selectedKeys.delete(key);
        changed = true;
      }
    }
    if (changed) {
      this.clearPreviewState({ clearStatus: true });
    }
    this.updateBulkControls();
  };

  private clearResults = (message: string, kind: StatusKind): void => {
    this.els.listEl.textContent = "";
    this.els.listEl.hidden = true;
    this.selectedKeys.clear();
    this.clearPreviewState({ clearStatus: true });
    this.setStatus(message, kind);
  };

  private renderResults = (payload: unknown, query: string, counts: SearchCounts | null): void => {
    const results = normalizeSearchResults(payload);
    this.els.listEl.textContent = "";

    let renderedCount = 0;
    const visibleKeys = new Set<string>();
    for (const result of results) {
      const key = normalizeBulkNoteKeys([result.key])[0] || "";
      if (renderOverviewSearchResult(this.els.listEl, result, {
        selected: key !== "" && this.selectedKeys.has(key),
        onSelectionChange: this.setSelected,
      })) {
        renderedCount += 1;
        if (key !== "") {
          visibleKeys.add(key);
        }
      }
    }
    this.pruneSelectionToRenderedKeys(visibleKeys);

    if (renderedCount === 0) {
      this.clearResults(overviewSearchEmptyMessage(query), "is-empty");
      return;
    }

    this.els.listEl.hidden = false;
    this.setStatus(searchSummary(renderedCount, query, counts), "");
  };

  private scheduleSearch = (delay: number): void => {
    const query = overviewSearchQuery(this.els.input);
    window.clearTimeout(this.debounceToken);
    syncPageQueryParam(query);
    this.clearPreviewState({ clearStatus: true });

    if (query === "" && !hasActiveFilters()) {
      this.requestToken += 1;
      setOverviewBusy(this.els.root, false);
      this.clearResults("No query or filters selected.", "is-empty");
      return;
    }

    const localToken = ++this.requestToken;
    setOverviewBusy(this.els.root, true);
    this.updateBulkControls();
    this.setStatus("Loading matching notes...", "is-loading");
    this.debounceToken = window.setTimeout(async () => {
      try {
        const resp = await this.fetcher(buildSearchAPIURL(query, true), {
          headers: { Accept: "application/json" },
        });
        if (!resp.ok) {
          throw new Error("search request failed");
        }
        const payload = await resp.json();
        if (localToken !== this.requestToken) {
          return;
        }
        this.renderResults(payload, query, readSearchCounts(resp));
      } catch {
        if (localToken === this.requestToken) {
          this.clearResults("Search unavailable.", "is-error");
        }
      } finally {
        if (localToken === this.requestToken) {
          setOverviewBusy(this.els.root, false);
          this.updateBulkControls();
        }
      }
    }, delay);
  };

  private canRunBulk = (keys: string[]): boolean => {
    if (!this.els.bulkRoot || this.els.csrfToken.trim() === "") {
      this.setBulkStatus("Bulk action unavailable.", "is-error");
      return false;
    }
    if (keys.length === 0) {
      this.setBulkStatus("Select one or more notes.", "is-error");
      return false;
    }
    return true;
  };

  private runBulkPreview = async (
    action: BulkAction,
    fields: BulkActionFields,
    fallbackMessage: string
  ): Promise<void> => {
    const keys = selectedOverviewKeys(this.selectedKeys);
    if (!this.canRunBulk(keys)) {
      return;
    }

    this.setBulkBusy(true);
    this.setBulkStatus("Previewing bulk action...", "is-loading");
    try {
      const { resp, payload } = await postBulkPreview(
        this.fetcher,
        this.els.csrfToken,
        action,
        keys,
        fields
      );
      if (!resp.ok) {
        this.clearPreviewState();
        this.setBulkStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
        return;
      }
      const result = normalizeBulkPreviewManagementResult(payload);
      if (!result.ok || !result.preview_id) {
        this.clearPreviewState();
        this.setBulkStatus(result.message || "Bulk preview failed.", "is-error");
        return;
      }
      this.setPreviewState(action, fields, result);
      const failures = Array.isArray(result.failures) ? result.failures : [];
      this.setBulkStatus(
        bulkStatusMessage(result, fallbackMessage),
        failures.length > 0 ? "is-error" : "is-success"
      );
    } catch {
      this.clearPreviewState();
      this.setBulkStatus("Bulk preview failed.", "is-error");
    } finally {
      this.setBulkBusy(false);
    }
  };

  private runBulkApply = async (
    action: BulkAction,
    fields: BulkActionFields,
    fallbackMessage: string
  ): Promise<void> => {
    const keys = selectedOverviewKeys(this.selectedKeys);
    if (!this.canRunBulk(keys)) {
      return;
    }
    if (!this.previewState || !this.previewMatches(action, fields, keys)) {
      this.clearPreviewState();
      this.setBulkStatus("Preview the bulk action before applying it.", "is-error");
      return;
    }

    this.setBulkBusy(true);
    this.setBulkStatus("Applying bulk action...", "is-loading");
    try {
      const { resp, payload } = await postBulkApply(
        this.fetcher,
        this.els.csrfToken,
        action,
        keys,
        fields,
        this.previewState.previewID
      );
      if (!resp.ok) {
        this.clearPreviewState();
        this.setBulkStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
        return;
      }
      const result = normalizeBulkManagementResult(payload);
      const failures = Array.isArray(result.failures) ? result.failures : [];
      this.setBulkStatus(
        bulkStatusMessage(result, fallbackMessage),
        failures.length > 0 ? "is-error" : "is-success"
      );
      this.clearPreviewState();
      this.selectedKeys.clear();
      this.updateBulkControls();
      this.ignoreNextManagementMutationClear = true;
      window.dispatchEvent(new CustomEvent(managementMutationEventName, {
        detail: result,
      }));
      this.scheduleSearch(0);
    } catch {
      this.clearPreviewState();
      this.setBulkStatus("Bulk action failed.", "is-error");
    } finally {
      this.setBulkBusy(false);
    }
  };

  private runBulkAction = async (
    action: BulkAction,
    fields: BulkActionFields,
    fallbackMessage: string
  ): Promise<void> => {
    if (this.previewMatches(action, fields)) {
      await this.runBulkApply(action, fields, fallbackMessage);
      return;
    }
    await this.runBulkPreview(action, fields, fallbackMessage);
  };

  private bindPreviewInvalidation = (form: HTMLFormElement | null): void => {
    if (!form) {
      return;
    }
    form.addEventListener("input", () => {
      this.clearPreviewState({ clearStatus: true });
    });
    form.addEventListener("change", () => {
      this.clearPreviewState({ clearStatus: true });
    });
  };

  private bindSearchEvents = (): void => {
    if (this.els.input instanceof HTMLInputElement) {
      this.els.input.value = readPageQuery();
      this.els.input.addEventListener("input", () => {
        this.scheduleSearch(150);
      });
    }

    window.addEventListener(filterChangeEventName, (event) => {
      if (!shouldHandleFilterChangeForMode(event, "search")) {
        return;
      }
      this.scheduleSearch(0);
    });

    window.addEventListener(managementMutationEventName, () => {
      if (this.ignoreNextManagementMutationClear) {
        this.ignoreNextManagementMutationClear = false;
        return;
      }
      this.clearPreviewState({ clearStatus: true });
    });

  };

  private bindBulkEvents = (): void => {
    this.els.trashButton?.addEventListener("click", () => {
      void this.runBulkAction("trash", {}, bulkApplyFallbackMessage("trash"));
    });

    this.els.moveForm?.addEventListener("submit", (event) => {
      event.preventDefault();
      void this.runBulkAction("move", readMoveFields(this.els.moveForm), bulkApplyFallbackMessage("move"));
    });

    this.els.setForm?.addEventListener("submit", (event) => {
      event.preventDefault();
      const fields = readSetFields(this.els.setForm);
      if (!fields.property) {
        this.setBulkStatus("Property is required.", "is-error");
        return;
      }
      void this.runBulkAction("frontmatter_set", fields, bulkApplyFallbackMessage("frontmatter_set"));
    });

    this.els.unsetForm?.addEventListener("submit", (event) => {
      event.preventDefault();
      const fields = readUnsetFields(this.els.unsetForm);
      if (!fields.property) {
        this.setBulkStatus("Property is required.", "is-error");
        return;
      }
      void this.runBulkAction("frontmatter_unset", fields, bulkApplyFallbackMessage("frontmatter_unset"));
    });

    this.els.previewApplyButton?.addEventListener("click", () => {
      if (!this.previewState) {
        this.setBulkStatus("Preview the bulk action before applying it.", "is-error");
        return;
      }
      void this.runBulkApply(
        this.previewState.action,
        this.readFieldsForAction(this.previewState.action),
        bulkApplyFallbackMessage(this.previewState.action)
      );
    });

    this.els.previewClearButton?.addEventListener("click", () => {
      this.clearPreviewState({ clearStatus: true });
    });

  };
}

export const initOverviewSearchResults = (deps: OverviewSearchDeps = {}): void => {
  const els = resolveOverviewSearchElements();
  if (els) {
    new OverviewSearch(els, deps);
  }
};

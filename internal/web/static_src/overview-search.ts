import { loadFailureMessage } from "./request-outcome";
import {
  managementMutationEventName,
  normalizeBulkNoteKeys,
  normalizeSearchResults,
  type SearchResult,
} from "./api";
import { queryElement } from "./dom";
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
  overviewSearchEmptyMessage,
  type OverviewSearchDeps,
} from "./overview-search-model";
import {
  overviewSearchQuery,
  setOverviewBusy,
} from "./overview-search-controller";
import {
  renderOverviewSearchResult,
} from "./overview-search-render";
import { setStatusText, type StatusKind } from "./status";
import { readSearchCounts, searchSummary, type SearchCounts } from "./search-summary";
import { bindOverviewRangeSelection, overviewSelectionInputs } from "./overview-search-selection";
import { LibraryPagination } from "./library-pagination";
import { bindLibraryPosition } from "./library-position";
import { OverviewSearchBulk } from "./overview-search-bulk";
import { renderOverviewEmptyHelp } from "./overview-search-empty";
import { LibrarySelectionStore } from "./library-selection-store";
import { bindSearchInput } from "./search-input";

export {
  overviewSearchEmptyMessage,
  type OverviewSearchDeps,
  type OverviewSearchResultOptions,
} from "./overview-search-model";
export { renderOverviewSearchResult } from "./overview-search-render";

class OverviewSearch extends OverviewSearchBulk {
  private readonly resetRangeSelection: () => void;
  private readonly restorePosition: () => void;
  private readonly pagination: LibraryPagination;
  private debounceToken = 0;
  private requestToken = 0;
  private loadingToken = 0;
  private activeRequest: AbortController | null = null;
  private settledStatus: { message: string; kind: StatusKind } | null = null;
  private readonly selectionStore: LibrarySelectionStore;
  private readonly resultOrder = new Map<string, number>();

  constructor(els: OverviewSearchElements, deps: OverviewSearchDeps) {
    super(els, deps);
    this.selectionStore = new LibrarySelectionStore(els.root.dataset.selectionVault || "");
    this.resetRangeSelection = bindOverviewRangeSelection(els.listEl);
    this.restorePosition = bindLibraryPosition(els.listEl);
    this.pagination = new LibraryPagination(els.root, () => this.scheduleSearch(0), () => this.bulkBusy);
    this.bindSearchEvents();
    this.bindBulkEvents();
    this.bindSelectionEvents();
    this.bindPreviewInvalidation(els.moveForm);
    this.bindPreviewInvalidation(els.setForm);
    this.bindPreviewInvalidation(els.unsetForm);
    this.updateBulkControls();
    this.scheduleSearch(0);
  }

  protected selectionChanged(): void { this.selectionStore?.save(this.selectedKeys); }

  // The aborted request's finally sees a newer token and skips its cleanup,
  // so canceling owns resetting the busy state (IME composition pauses here).
  private cancelSearch = (): void => {
    window.clearTimeout(this.debounceToken);
    window.clearTimeout(this.loadingToken);
    this.activeRequest?.abort();
    this.activeRequest = null;
    this.requestToken += 1;
    delete this.els.listEl.dataset.updating;
    setOverviewBusy(this.els.root, false);
    this.pagination.busy(false);
    this.updateBulkControls();
    if (this.els.statusEl.classList.contains("is-loading") && this.settledStatus) {
      this.setStatus(this.settledStatus.message, this.settledStatus.kind);
    }
  };

  private setStatus = (message: string, kind: StatusKind = ""): void => {
    if (kind !== "is-loading") this.settledStatus = { message, kind };
    const emptyHelp = this.els.root.querySelector<HTMLElement>("[data-overview-empty-help]");
    if (emptyHelp) emptyHelp.hidden = true;
    setStatusText(this.els.statusEl, message, kind, ["is-loading", "is-empty", "is-error"]);
    const retry = queryElement(this.els.root, "[data-overview-search-retry]", HTMLButtonElement);
    if (retry) {
      retry.disabled = kind === "is-loading";
      retry.hidden = kind !== "is-error" && !(kind === "is-loading" && !retry.hidden);
      if (retry.hidden && document.activeElement === retry) {
        this.els.input?.focus();
      }
    }
  };


  private setSelected = (result: SearchResult, selected: boolean): void => {
    const key = normalizeBulkNoteKeys([result.key])[0] || "";
    if (key === "") {
      return;
    }
    this.clearPreviewState({ clearStatus: true });
    if (selected) {
      this.selectedKeys.set(key, result.wikilink || "");
    } else {
      this.selectedKeys.delete(key);
    }
    this.orderSelection();
    this.updateBulkControls();
  };

  private selectDisplayed = (selected: boolean): void => {
    if (this.selectionBusy()) {
      return;
    }
    this.resetRangeSelection();
    if (!selected) this.selectedKeys.clear();
    for (const input of overviewSelectionInputs(this.els.listEl)) {
      input.checked = selected;
      if (selected) {
        this.selectedKeys.set(input.value, input.dataset.wikilink || "");
      }
    }
    this.orderSelection();
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

  private orderSelection = (): void => {
    const ordered = [...this.selectedKeys].sort(([a], [b]) =>
      (this.resultOrder.get(a) ?? 0) - (this.resultOrder.get(b) ?? 0));
    this.selectedKeys.clear();
    for (const [key, target] of ordered) this.selectedKeys.set(key, target);
  };

  private clearResults = (message: string, kind: StatusKind): void => {
    this.els.listEl.textContent = "";
    this.els.listEl.hidden = true;
    this.clearPreviewState({ clearStatus: true });
    this.setStatus(message, kind);
  };

  private renderResults = (payload: unknown, query: string, counts: SearchCounts | null): void => {
    const results = normalizeSearchResults(payload);
    this.els.listEl.textContent = "";

    let renderedCount = 0;
    for (const [index, result] of results.entries()) {
      const key = normalizeBulkNoteKeys([result.key])[0] || "";
      this.resultOrder.set(key, this.pagination.offset + index);
      if (renderOverviewSearchResult(this.els.listEl, result, {
        selected: key !== "" && this.selectedKeys.has(key),
        onSelectionChange: this.setSelected,
      })) {
        renderedCount += 1;
      }
    }
    this.orderSelection();
    this.updateBulkControls();
    this.pagination.render(counts, renderedCount);

    if (renderedCount === 0) {
      this.clearResults(this.pagination.offset ? "No results on this page. Use Previous results to return to the matches." : overviewSearchEmptyMessage(query), "is-empty");
      if (this.pagination.offset === 0) renderOverviewEmptyHelp(this.els.root);
      this.restorePosition();
      return;
    }

    this.els.listEl.hidden = false;
    this.setStatus(searchSummary(renderedCount, query, counts, this.pagination.offset), "");
    this.restorePosition();
  };

  protected scheduleSearch = (delay: number): void => {
    this.resetRangeSelection();
    const query = overviewSearchQuery(this.els.input);
    this.cancelSearch();
    syncPageQueryParam(query);
    syncLibraryActivity(query !== "" || hasActiveFilters());
    const apiURL = buildSearchAPIURL(query, true);
    this.selectionStore.prepare(apiURL);
    const request = this.pagination.prepare(apiURL);
    if (request.changed) { this.selectedKeys.clear(); this.resultOrder.clear(); }
    this.clearPreviewState({ clearStatus: true });

    if (query === "" && !hasActiveFilters()) {
      this.requestToken += 1;
      setOverviewBusy(this.els.root, false);
      this.pagination.render(null, 0);
      this.clearResults("No query or filters selected.", "is-empty");
      return;
    }

    const localToken = ++this.requestToken;
    const emptyHelp = this.els.root.querySelector<HTMLElement>("[data-overview-empty-help]");
    if (emptyHelp) emptyHelp.hidden = true;
    const controller = new AbortController();
    this.activeRequest = controller;
    setOverviewBusy(this.els.root, true);
    this.pagination.busy(true);
    this.updateBulkControls();
    this.els.listEl.dataset.updating = "true";
    this.loadingToken = window.setTimeout(() => this.setStatus(
      this.els.listEl.hidden ? "Loading matching notes…" : "Updating results… Previous matches are shown.", "is-loading"
    ), 350);
    this.debounceToken = window.setTimeout(async () => {
      try {
        const resp = await this.fetcher(request.url, {
          headers: { Accept: "application/json" },
          signal: controller.signal,
        });
        if (!resp.ok) {
          throw new Error("search request failed");
        }
        const payload = await resp.json();
        if (localToken !== this.requestToken) return;
        const restored = await this.selectionStore.restore(this.fetcher, controller.signal);
        if (localToken !== this.requestToken) {
          return;
        }
        if (restored) {
          this.selectedKeys.clear();
          for (const [key, target] of restored.targets) this.selectedKeys.set(key, target);
          for (const [key, position] of restored.positions) this.resultOrder.set(key, position);
        }
        this.selectionStore.restored();
        this.renderResults(payload, query, readSearchCounts(resp));
      } catch {
        if (localToken === this.requestToken) {
          window.clearTimeout(this.loadingToken);
          delete this.els.listEl.dataset.updating;
          this.pagination.failed();
          this.clearResults(loadFailureMessage("matching notes", "Retry search"), "is-error");
        }
      } finally {
        if (localToken === this.requestToken) {
          window.clearTimeout(this.loadingToken);
          delete this.els.listEl.dataset.updating;
          setOverviewBusy(this.els.root, false);
          this.updateBulkControls();
          this.pagination.busy(false);
        }
      }
    }, delay);
  };

  private bindSearchEvents = (): void => {
    this.els.root.querySelector("[data-overview-search-retry]")?.addEventListener("click", () => this.scheduleSearch(0));
    if (this.els.input instanceof HTMLInputElement) {
      this.els.input.value = readPageQuery();
      bindSearchInput(this.els.input, () => this.scheduleSearch(150), this.cancelSearch);
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
}

export const initOverviewSearchResults = (deps: OverviewSearchDeps = {}): void => {
  const els = resolveOverviewSearchElements();
  if (els) {
    new OverviewSearch(els, deps);
  }
};

// Only a transition between browsing and searching changes the disclosure.
// Opening activity during a search remains the reader's choice while typing.
const syncLibraryActivity = (searching: boolean): void => {
  const search = document.querySelector<HTMLElement>("[data-library-search]");
  if (search) search.dataset.searching = String(searching);
  const activity = document.querySelector<HTMLDetailsElement>("[data-library-activity]");
  if (!activity || activity.dataset.searching === String(searching)) return;
  activity.dataset.searching = String(searching);
  activity.open = !searching;
};

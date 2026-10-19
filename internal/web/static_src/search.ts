import { normalizeSearchResults, searchResultPath, type SearchResult } from "./api";
import { getElementByID, queryElement } from "./dom";
import { bindSearchShortcut } from "./search-shortcut";
import { libraryResultPath } from "./note-navigation";
import {
  buildSearchAPIURL,
  buildLibrarySearchURL,
  filterChangeEventName,
  hasActiveFilters,
  readPageQuery,
  shouldHandleFilterChangeForMode,
  syncPageQueryParam,
} from "./filters";

export type SearchNavigationDirection = "next" | "previous";

export type SearchResultOptionHandlers = {
  activate: (index: number) => void;
};

export const searchResultTitle = (result: SearchResult): string =>
  result.title || result.key || "Untitled";

export const searchResultFolderLabel = (result: SearchResult): string =>
  result.folder ? `Folder: ${result.folder}` : "Folder: /";

export const nextSearchActiveIndex = (
  activeIndex: number,
  resultCount: number,
  direction: SearchNavigationDirection
): number => {
  if (resultCount <= 0) {
    return -1;
  }
  if (direction === "next") {
    return activeIndex < resultCount - 1 ? activeIndex + 1 : 0;
  }
  return activeIndex > 0 ? activeIndex - 1 : resultCount - 1;
};

export const renderSearchResultOption = (
  doc: Document,
  result: SearchResult,
  index: number,
  handlers: SearchResultOptionHandlers
): HTMLLIElement => {
  const item = doc.createElement("li");
  const link = doc.createElement("a");
  link.href = libraryResultPath(searchResultPath(result));
  link.id = `search-option-${index}`;
  link.className = "search-result";
  link.setAttribute("role", "option");
  link.setAttribute("aria-selected", "false");
  link.setAttribute("data-search-index", String(index));

  const title = doc.createElement("span");
  title.className = "search-result-title";
  title.textContent = searchResultTitle(result);
  link.appendChild(title);

  const folder = doc.createElement("span");
  folder.className = "search-result-folder";
  folder.textContent = searchResultFolderLabel(result);
  link.appendChild(folder);

  if (result.snippet) {
    const snippet = doc.createElement("p");
    snippet.className = "search-result-snippet";
    snippet.innerHTML = result.snippet;
    link.appendChild(snippet);
  }

  link.addEventListener("mouseenter", () => {
    handlers.activate(index);
  });

  item.appendChild(link);
  return item;
};

// Matches the Library's delayed loading status: quick answers never flash it.
export const searchingFeedbackDelay = 350;

class HeaderSearch {
  private debounceToken = 0;
  private loadingToken = 0;
  private requestToken = 0;
  private activeRequest: AbortController | null = null;
  private results: SearchResult[] = [];
  private activeIndex = -1;
  private readonly retryButton: HTMLButtonElement | null;
  private readonly feedbackMessage: HTMLElement | null;

  constructor(
    private readonly input: HTMLInputElement,
    private readonly resultsEl: HTMLElement,
    private readonly feedbackEl: HTMLElement | null,
    private readonly libraryLink: HTMLAnchorElement | null,
    private readonly syncSearchURL: boolean
  ) {
    this.feedbackMessage = feedbackEl?.querySelector<HTMLElement>("[data-search-feedback-message]") || feedbackEl;
    this.retryButton = feedbackEl?.querySelector<HTMLButtonElement>("[data-search-retry]") || null;
    this.retryButton?.addEventListener("click", () => {
      this.input.focus();
      this.scheduleSearch(0);
    });
    this.retryButton?.addEventListener("keydown", (event) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      this.input.focus();
      this.dismiss();
    });
  }

  hasActiveSearchFilters(): boolean {
    return this.syncSearchURL && hasActiveFilters();
  }

  hasResults(): boolean {
    return this.results.length > 0;
  }

  setActive(nextIndex: number): void {
    this.activeIndex = nextIndex;
    for (const option of this.resultsEl.querySelectorAll("[data-search-index]")) {
      const active = Number(option.getAttribute("data-search-index")) === nextIndex;
      option.classList.toggle("is-active", active);
      option.setAttribute("aria-selected", active ? "true" : "false");
      if (active) {
        this.input.setAttribute("aria-activedescendant", option.id);
      }
    }
    if (nextIndex < 0) {
      this.input.removeAttribute("aria-activedescendant");
    }
  }

  private setKeyboardHint(visible: boolean): void {
    const hint = this.input.closest("[data-search-box]")?.querySelector<HTMLElement>("[data-search-keyboard-hint]");
    if (hint) hint.hidden = !visible;
  }

  clearResults(): void {
    this.results = [];
    this.resultsEl.textContent = "";
    this.resultsEl.hidden = true;
    this.setKeyboardHint(false);
    this.input.setAttribute("aria-expanded", "false");
    this.setActive(-1);
    if (this.feedbackEl) {
      if (this.feedbackMessage) this.feedbackMessage.textContent = "";
      this.feedbackEl.hidden = true;
    }
    if (this.retryButton) this.retryButton.hidden = true;
  }

  showFeedback(message: string): void {
    this.clearResults();
    if (this.feedbackEl) {
      if (this.feedbackMessage) this.feedbackMessage.textContent = message;
      this.feedbackEl.hidden = false;
    }
  }

  showFailure(): void {
    this.showFeedback("Search unavailable. Try again.");
    if (this.retryButton) this.retryButton.hidden = false;
  }

  private cancelPending(): void {
    window.clearTimeout(this.debounceToken);
    window.clearTimeout(this.loadingToken);
    this.activeRequest?.abort();
    this.activeRequest = null;
    this.resultsEl.removeAttribute("aria-busy");
  }

  dismiss(): void {
    this.cancelPending();
    this.requestToken += 1;
    this.clearResults();
    if (this.libraryLink) {
      this.libraryLink.hidden = true;
    }
  }

  updateLibraryLink(query: string): void {
    if (this.libraryLink) {
      this.libraryLink.href = buildLibrarySearchURL(query, this.syncSearchURL);
      this.libraryLink.hidden = query === "" && !this.hasActiveSearchFilters();
    }
  }

  navigateToResult(index: number): void {
    this.resultsEl.querySelector<HTMLAnchorElement>(`[data-search-index="${index}"]`)?.click();
  }

  keepActiveVisible(): void {
    const option = this.resultsEl.querySelector<HTMLElement>(`[data-search-index="${this.activeIndex}"]`);
    if (!option) {
      return;
    }
    const viewport = this.resultsEl.getBoundingClientRect();
    const row = option.getBoundingClientRect();
    const top = viewport.top + this.resultsEl.clientTop;
    const bottom = top + this.resultsEl.clientHeight;
    // Scroll only the popup, leaving the page and input focus in place.
    if (row.top < top) {
      this.resultsEl.scrollTop += row.top - top;
    } else if (row.bottom > bottom) {
      this.resultsEl.scrollTop += Math.min(row.top - top, row.bottom - bottom);
    }
  }

  renderResults(results: SearchResult[]): void {
    this.results = results;
    if (results.length === 0) {
      this.showFeedback(this.input.value.trim() ? "No matching notes." : "No notes match the active filters.");
      return;
    }
    this.resultsEl.textContent = "";
    if (this.feedbackEl) {
      if (this.feedbackMessage) this.feedbackMessage.textContent = "";
      this.feedbackEl.hidden = true;
    }
    if (this.retryButton) this.retryButton.hidden = true;
    results.forEach((result, index) => {
      this.resultsEl.appendChild(renderSearchResultOption(document, result, index, {
        activate: (nextIndex) => this.setActive(nextIndex),
      }));
    });
    this.resultsEl.hidden = false;
    this.setKeyboardHint(true);
    this.input.setAttribute("aria-expanded", "true");
    this.setActive(-1);
  }

  scheduleSearch(delay: number): void {
    const query = this.input.value.trim();
    this.cancelPending();
    if (this.syncSearchURL) {
      syncPageQueryParam(query);
    }
    const localToken = ++this.requestToken;
    this.updateLibraryLink(query);
    if (query === "" && !this.hasActiveSearchFilters()) {
      this.clearResults();
      return;
    }
    // Keep the current results (and the highlighted option) while the next
    // search is pending; announce progress only when nothing is displayed.
    if (this.hasResults()) this.resultsEl.setAttribute("aria-busy", "true");
    else this.clearResults();
    this.loadingToken = window.setTimeout(() => {
      if (localToken === this.requestToken && !this.hasResults()) this.showFeedback("Searching…");
    }, searchingFeedbackDelay);
    const controller = new AbortController();
    this.activeRequest = controller;
    this.debounceToken = window.setTimeout(() => {
      void this.fetchResults(query, localToken, controller.signal);
    }, delay);
  }

  private settle(localToken: number): boolean {
    if (localToken !== this.requestToken) return false;
    window.clearTimeout(this.loadingToken);
    this.activeRequest = null;
    this.resultsEl.removeAttribute("aria-busy");
    return true;
  }

  private async fetchResults(query: string, localToken: number, signal: AbortSignal): Promise<void> {
    try {
      const resp = await fetch(buildSearchAPIURL(query, this.syncSearchURL), {
        headers: { Accept: "application/json" },
        signal,
      });
      if (localToken !== this.requestToken) {
        return;
      }
      if (!resp.ok) {
        if (this.settle(localToken)) this.showFailure();
        return;
      }
      const payload = await resp.json();
      if (this.settle(localToken)) {
        this.renderResults(normalizeSearchResults(payload));
      }
    } catch {
      if (this.settle(localToken)) {
        this.showFailure();
      }
    }
  }

  openLibrary(event: KeyboardEvent): boolean {
    if (event.key !== "Enter" || this.activeIndex >= 0 || !this.libraryLink || this.libraryLink.hidden) {
      return false;
    }
    event.preventDefault();
    this.libraryLink.click();
    return true;
  }

  onKeyDown(event: KeyboardEvent): void {
    if (event.isComposing) {
      return;
    }
    if (event.key === "Escape") {
      event.preventDefault();
      this.dismiss();
      return;
    }
    if (this.openLibrary(event) || !this.hasResults()) {
      return;
    }
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      this.setActive(nextSearchActiveIndex(
        this.activeIndex,
        this.results.length,
        event.key === "ArrowDown" ? "next" : "previous"
      ));
      this.keepActiveVisible();
    } else if (event.key === "Enter" && this.activeIndex >= 0) {
      event.preventDefault();
      this.navigateToResult(this.activeIndex);
    }
  }
}

export const initSearch = (): void => {
  const searchBox = queryElement(document, "[data-search-box]", HTMLElement);
  const input = getElementByID("search-input", HTMLInputElement);
  if (input && searchBox?.hasAttribute("data-library-search-input")) {
    bindSearchShortcut(input);
    input.addEventListener("keydown", (event) => {
      if (event.key !== "Enter" || event.isComposing) return;
      event.preventDefault();
      const results = document.getElementById("overview-results-heading");
      results?.setAttribute("tabindex", "-1");
      results?.focus();
    });
    return;
  }
  const resultsEl = getElementByID("search-results", HTMLElement);
  if (!searchBox || !input || !resultsEl) {
    return;
  }
  const feedbackEl = queryElement(searchBox, "[data-search-feedback]", HTMLElement);
  const libraryLink = queryElement(searchBox, "[data-search-library]", HTMLAnchorElement);
  const syncSearchURL = Boolean(
    queryElement(document, '[data-facet-controls][data-facet-mode="search"]', HTMLElement)
  );
  const search = new HeaderSearch(input, resultsEl, feedbackEl, libraryLink, syncSearchURL);
  bindSearchShortcut(input);

  if (syncSearchURL) {
    input.value = readPageQuery();
    // The Library loads its own results. Keep header suggestions closed until
    // search is focused so a restored URL cannot cover the Save as view button.
    search.updateLibraryLink(input.value.trim());
    window.addEventListener(filterChangeEventName, (event) => {
      if (shouldHandleFilterChangeForMode(event, "search")) {
        search.dismiss();
        search.updateLibraryLink(input.value.trim());
        if (document.activeElement === input) {
          search.scheduleSearch(0);
        }
      }
    });
  }

  input.addEventListener("input", () => search.scheduleSearch(150));
  input.addEventListener("focus", () => {
    if (input.value.trim() !== "" || search.hasActiveSearchFilters()) {
      search.scheduleSearch(0);
    }
  });
  input.addEventListener("keydown", (event) => search.onKeyDown(event));
  searchBox.addEventListener("mousedown", (event) => {
    if (event.button !== 0 || !(event.target instanceof Element)) return;
    if (!event.target.closest("a[href], button")) return;
    // Safari can blur the input without focusing a tapped link. Keep focus
    // here so focusout cannot remove the target before its native click runs.
    event.preventDefault();
  });
  searchBox.addEventListener("focusout", (event) => {
    if (!(event.relatedTarget instanceof Node) || !searchBox.contains(event.relatedTarget)) {
      search.dismiss();
    }
  });
  document.addEventListener("click", (event) => {
    const target = event.target;
    if (target instanceof Node && !searchBox.contains(target)) {
      search.dismiss();
    }
  });
};

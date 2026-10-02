import { normalizeSearchResults, searchResultPath, type SearchResult } from "./api";
import { getElementByID, queryElement } from "./dom";
import { bindSearchShortcut } from "./search-shortcut";
import {
  buildSearchAPIURL,
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
  link.href = searchResultPath(result);
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

class HeaderSearch {
  private debounceToken = 0;
  private requestToken = 0;
  private results: SearchResult[] = [];
  private activeIndex = -1;

  constructor(
    private readonly input: HTMLInputElement,
    private readonly resultsEl: HTMLElement,
    private readonly feedbackEl: HTMLElement | null,
    private readonly syncSearchURL: boolean
  ) {}

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

  clearResults(): void {
    this.results = [];
    this.resultsEl.textContent = "";
    this.resultsEl.hidden = true;
    this.input.setAttribute("aria-expanded", "false");
    this.setActive(-1);
    if (this.feedbackEl) {
      this.feedbackEl.textContent = "";
      this.feedbackEl.hidden = true;
    }
  }

  showFeedback(message: string): void {
    this.clearResults();
    if (this.feedbackEl) {
      this.feedbackEl.textContent = message;
      this.feedbackEl.hidden = false;
    }
  }

  dismiss(): void {
    window.clearTimeout(this.debounceToken);
    this.requestToken += 1;
    this.clearResults();
  }

  navigateToResult(index: number): void {
    this.resultsEl.querySelector<HTMLAnchorElement>(`[data-search-index="${index}"]`)?.click();
  }

  renderResults(results: SearchResult[]): void {
    this.results = results;
    if (results.length === 0) {
      this.showFeedback(this.input.value.trim() ? "No matching notes." : "No notes match the active filters.");
      return;
    }
    this.resultsEl.textContent = "";
    if (this.feedbackEl) {
      this.feedbackEl.textContent = "";
      this.feedbackEl.hidden = true;
    }
    results.forEach((result, index) => {
      this.resultsEl.appendChild(renderSearchResultOption(document, result, index, {
        activate: (nextIndex) => this.setActive(nextIndex),
      }));
    });
    this.resultsEl.hidden = false;
    this.input.setAttribute("aria-expanded", "true");
    this.setActive(-1);
  }

  scheduleSearch(delay: number): void {
    const query = this.input.value.trim();
    window.clearTimeout(this.debounceToken);
    if (this.syncSearchURL) {
      syncPageQueryParam(query);
    }
    const localToken = ++this.requestToken;
    if (query === "" && !this.hasActiveSearchFilters()) {
      this.clearResults();
      return;
    }
    this.showFeedback("Searching...");
    this.debounceToken = window.setTimeout(() => {
      void this.fetchResults(query, localToken);
    }, delay);
  }

  private async fetchResults(query: string, localToken: number): Promise<void> {
    try {
      const resp = await fetch(buildSearchAPIURL(query, this.syncSearchURL), {
        headers: { Accept: "application/json" },
      });
      if (localToken !== this.requestToken) {
        return;
      }
      if (!resp.ok) {
        this.showFeedback("Search unavailable. Try again.");
        return;
      }
      const payload = await resp.json();
      if (localToken === this.requestToken) {
        this.renderResults(normalizeSearchResults(payload));
      }
    } catch {
      if (localToken === this.requestToken) {
        this.showFeedback("Search unavailable. Try again.");
      }
    }
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
    if (!this.hasResults()) {
      return;
    }
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      this.setActive(nextSearchActiveIndex(
        this.activeIndex,
        this.results.length,
        event.key === "ArrowDown" ? "next" : "previous"
      ));
    } else if (event.key === "Enter" && this.activeIndex >= 0) {
      event.preventDefault();
      this.navigateToResult(this.activeIndex);
    }
  }
}

export const initSearch = (): void => {
  const searchBox = queryElement(document, "[data-search-box]", HTMLElement);
  const input = getElementByID("search-input", HTMLInputElement);
  const resultsEl = getElementByID("search-results", HTMLElement);
  if (!searchBox || !input || !resultsEl) {
    return;
  }
  const feedbackEl = queryElement(searchBox, "[data-search-feedback]", HTMLElement);
  const syncSearchURL = Boolean(
    queryElement(document, '[data-facet-controls][data-facet-mode="search"]', HTMLElement)
  );
  const search = new HeaderSearch(input, resultsEl, feedbackEl, syncSearchURL);
  bindSearchShortcut(input);

  if (syncSearchURL) {
    input.value = readPageQuery();
    if (input.value.trim() !== "" || search.hasActiveSearchFilters()) {
      search.scheduleSearch(0);
    }
    window.addEventListener(filterChangeEventName, (event) => {
      if (shouldHandleFilterChangeForMode(event, "search")) {
        search.scheduleSearch(0);
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

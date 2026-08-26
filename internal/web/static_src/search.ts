import { normalizeSearchResults, searchResultPath, type SearchResult } from "./api";
import { getElementByID, queryElement } from "./dom";
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
  choose: (index: number) => void;
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
  const button = doc.createElement("button");
  button.type = "button";
  button.id = `search-option-${index}`;
  button.className = "search-result";
  button.setAttribute("role", "option");
  button.setAttribute("aria-selected", "false");
  button.setAttribute("data-search-index", String(index));

  const title = doc.createElement("span");
  title.className = "search-result-title";
  title.textContent = searchResultTitle(result);
  button.appendChild(title);

  const folder = doc.createElement("span");
  folder.className = "search-result-folder";
  folder.textContent = searchResultFolderLabel(result);
  button.appendChild(folder);

  if (result.snippet) {
    const snippet = doc.createElement("p");
    snippet.className = "search-result-snippet";
    snippet.innerHTML = result.snippet;
    button.appendChild(snippet);
  }

  button.addEventListener("mouseenter", () => {
    handlers.activate(index);
  });
  button.addEventListener("click", () => {
    handlers.choose(index);
  });

  item.appendChild(button);
  return item;
};

export const initSearch = (): void => {
  const searchBox = queryElement(document, "[data-search-box]", HTMLElement);
  const input = getElementByID("search-input", HTMLInputElement);
  const resultsEl = getElementByID("search-results", HTMLElement);
  if (!searchBox || !input || !resultsEl) {
    return;
  }

  const syncSearchURL = Boolean(
    queryElement(document, '[data-facet-controls][data-facet-mode="search"]', HTMLElement)
  );
  let debounceToken = 0;
  let requestToken = 0;
  let results: SearchResult[] = [];
  let activeIndex = -1;

  const setExpanded = (expanded: boolean): void => {
    input.setAttribute("aria-expanded", expanded ? "true" : "false");
  };

  const setActive = (nextIndex: number): void => {
    activeIndex = nextIndex;
    const options = resultsEl.querySelectorAll("[data-search-index]");
    for (const option of options) {
      const index = Number(option.getAttribute("data-search-index"));
      const active = index === activeIndex;
      option.classList.toggle("is-active", active);
      option.setAttribute("aria-selected", active ? "true" : "false");
      if (active) {
        input.setAttribute("aria-activedescendant", option.id);
      }
    }
    if (activeIndex < 0) {
      input.removeAttribute("aria-activedescendant");
    }
  };

  const clearResults = (): void => {
    results = [];
    resultsEl.textContent = "";
    resultsEl.hidden = true;
    setExpanded(false);
    setActive(-1);
  };

  const hasActiveSearchFilters = (): boolean => syncSearchURL && hasActiveFilters();

  const navigateToResult = (index: number): void => {
    if (index < 0 || index >= results.length) {
      return;
    }
    const result = results[index];
    const path = searchResultPath(result);
    if (!path) {
      return;
    }
    window.location.assign(path);
  };

  const renderResults = (): void => {
    resultsEl.textContent = "";
    if (results.length === 0) {
      clearResults();
      return;
    }

    results.forEach((result, index) => {
      resultsEl.appendChild(
        renderSearchResultOption(document, result, index, {
          activate: setActive,
          choose: navigateToResult,
        })
      );
    });

    resultsEl.hidden = false;
    setExpanded(true);
    setActive(-1);
  };

  const scheduleSearch = (delay: number): void => {
    const query = input.value.trim();
    window.clearTimeout(debounceToken);
    if (syncSearchURL) {
      syncPageQueryParam(query);
    }

    if (query === "" && !hasActiveSearchFilters()) {
      requestToken += 1;
      clearResults();
      return;
    }

    const localToken = ++requestToken;
    debounceToken = window.setTimeout(async () => {
      try {
        const resp = await fetch(buildSearchAPIURL(query, syncSearchURL), {
          headers: { Accept: "application/json" },
        });
        if (!resp.ok || localToken !== requestToken) {
          return;
        }
        const payload = await resp.json();
        if (localToken !== requestToken) {
          return;
        }
        results = normalizeSearchResults(payload);
        renderResults();
      } catch {
        if (localToken === requestToken) {
          clearResults();
        }
      }
    }, delay);
  };

  if (syncSearchURL) {
    input.value = readPageQuery();
    if (input.value.trim() !== "" || hasActiveSearchFilters()) {
      scheduleSearch(0);
    }
    window.addEventListener(filterChangeEventName, (event) => {
      if (!shouldHandleFilterChangeForMode(event, "search")) {
        return;
      }
      scheduleSearch(0);
    });
  }

  input.addEventListener("input", () => {
    scheduleSearch(150);
  });

  input.addEventListener("keydown", (event) => {
    if (results.length === 0) {
      if (event.key === "Escape") {
        clearResults();
      }
      return;
    }

    if (event.key === "ArrowDown") {
      event.preventDefault();
      setActive(nextSearchActiveIndex(activeIndex, results.length, "next"));
      return;
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      setActive(nextSearchActiveIndex(activeIndex, results.length, "previous"));
      return;
    }
    if (event.key === "Enter" && activeIndex >= 0) {
      event.preventDefault();
      navigateToResult(activeIndex);
      return;
    }
    if (event.key === "Escape") {
      event.preventDefault();
      clearResults();
    }
  });

  document.addEventListener("click", (event) => {
    const target = event.target;
    if (target instanceof Node && !searchBox.contains(target)) {
      clearResults();
    }
  });
};

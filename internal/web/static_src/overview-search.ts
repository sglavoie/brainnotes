import { normalizeSearchResults, searchResultPath, type SearchResult } from "./api";
import { getElementByID, queryElement } from "./dom";
import {
  buildSearchAPIURL,
  filterChangeEventName,
  hasActiveSharedFilters,
  normalizeParamValue,
  readPageQuery,
  shouldHandleFilterChangeForMode,
  syncPageQueryParam,
} from "./filters";
import { setStatusText, type StatusKind } from "./status";

export const overviewSearchEmptyMessage = (query: string): string =>
  query ? `No notes match "${query}".` : "No notes match the active filters.";

export const renderOverviewSearchResult = (
  listEl: HTMLElement,
  result: SearchResult
): boolean => {
  const path = searchResultPath(result);
  if (!path) {
    return false;
  }

  const doc = listEl.ownerDocument;
  const item = doc.createElement("li");
  item.className = "overview-result-item";

  const link = doc.createElement("a");
  link.className = "overview-result-link";
  link.setAttribute("href", path);

  const title = doc.createElement("span");
  title.className = "overview-result-title";
  title.textContent = result.title || result.key || "Untitled";
  link.appendChild(title);

  const meta = doc.createElement("span");
  meta.className = "overview-result-meta";

  const folder = doc.createElement("span");
  folder.textContent = result.folder ? `Folder: ${result.folder}` : "Folder: /";
  meta.appendChild(folder);

  if (result.matched_fields.length > 0) {
    const fields = doc.createElement("span");
    fields.className = "overview-result-badge";
    fields.textContent = `Matched: ${result.matched_fields.join(", ")}`;
    meta.appendChild(fields);
  }

  for (const issue of result.issues) {
    const badge = doc.createElement("span");
    badge.className = "overview-result-badge is-issue";
    badge.textContent = issue.replace(/_/g, " ");
    meta.appendChild(badge);
  }
  link.appendChild(meta);

  if (result.snippet) {
    const snippet = doc.createElement("p");
    snippet.className = "overview-result-snippet";
    snippet.innerHTML = result.snippet;
    link.appendChild(snippet);
  }

  item.appendChild(link);
  listEl.appendChild(item);
  return true;
};

export const initOverviewSearchResults = (): void => {
  const root = queryElement(document, "[data-overview-search-results]", HTMLElement);
  if (!root) {
    return;
  }

  const input = getElementByID("search-input", HTMLInputElement);
  const statusEl = queryElement(root, "[data-overview-search-status]", HTMLElement);
  const listEl = queryElement(root, "[data-overview-search-list]", HTMLElement);
  if (!statusEl || !listEl) {
    return;
  }

  let debounceToken = 0;
  let requestToken = 0;

  const currentQuery = (): string =>
    input instanceof HTMLInputElement ? normalizeParamValue(input.value) : readPageQuery();

  const setBusy = (busy: boolean): void => {
    root.setAttribute("aria-busy", busy ? "true" : "false");
  };

  const setStatus = (message: string, kind: StatusKind = ""): void => {
    setStatusText(statusEl, message, kind, ["is-loading", "is-empty", "is-error"]);
  };

  const clearResults = (message: string, kind: StatusKind): void => {
    listEl.textContent = "";
    listEl.hidden = true;
    setStatus(message, kind);
  };

  const renderResults = (payload: unknown, query: string): void => {
    const results = normalizeSearchResults(payload);
    listEl.textContent = "";

    let renderedCount = 0;
    for (const result of results) {
      if (renderOverviewSearchResult(listEl, result)) {
        renderedCount += 1;
      }
    }

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
    const query = currentQuery();
    window.clearTimeout(debounceToken);
    syncPageQueryParam(query);

    if (query === "" && !hasActiveSharedFilters()) {
      requestToken += 1;
      setBusy(false);
      clearResults("No query or filters selected.", "is-empty");
      return;
    }

    const localToken = ++requestToken;
    setBusy(true);
    setStatus("Loading matching notes...", "is-loading");
    debounceToken = window.setTimeout(async () => {
      try {
        const resp = await fetch(buildSearchAPIURL(query, true), {
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
          setBusy(false);
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

  scheduleSearch(0);
};

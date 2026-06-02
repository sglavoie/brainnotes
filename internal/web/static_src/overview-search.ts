import {
  buildBulkNotesForm,
  managementMutationEventName,
  normalizeBulkNoteKeys,
  normalizeManagementErrorMessage,
  normalizeSearchResults,
  searchResultPath,
  type BulkAction,
  type BulkManagementResult,
  type SearchResult,
} from "./api";
import { getElementByID, queryAllElements, queryElement } from "./dom";
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

type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;
type BulkStatusKind = "" | "is-success" | "is-error" | "is-loading";

export type OverviewSearchDeps = {
  fetch?: FetchLike;
  confirm?: (message: string) => boolean;
};

export type OverviewSearchResultOptions = {
  selected?: boolean;
  onSelectionChange?: (result: SearchResult, selected: boolean) => void;
};

export const overviewSearchEmptyMessage = (query: string): string =>
  query ? `No notes match "${query}".` : "No notes match the active filters.";

export const renderOverviewSearchResult = (
  listEl: HTMLElement,
  result: SearchResult,
  options: OverviewSearchResultOptions = {}
): boolean => {
  const path = searchResultPath(result);
  if (!path) {
    return false;
  }

  const doc = listEl.ownerDocument;
  const item = doc.createElement("li");
  item.className = "overview-result-item";
  if (result.key) {
    item.setAttribute("data-overview-result-key", result.key);
  }

  const row = doc.createElement("div");
  row.className = "overview-result-row";

  if (result.key) {
    row.classList.add("has-selection");
    const selectWrap = doc.createElement("label");
    selectWrap.className = "overview-result-select";
    const checkbox = doc.createElement("input");
    checkbox.type = "checkbox";
    checkbox.value = result.key;
    checkbox.checked = Boolean(options.selected);
    checkbox.setAttribute(
      "aria-label",
      `Select ${result.title || result.key || "note"}`
    );
    checkbox.addEventListener("change", () => {
      if (options.onSelectionChange) {
        options.onSelectionChange(result, checkbox.checked);
      }
    });
    selectWrap.appendChild(checkbox);
    row.appendChild(selectWrap);
  }

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

  row.appendChild(link);
  item.appendChild(row);
  listEl.appendChild(item);
  return true;
};

export const initOverviewSearchResults = (deps: OverviewSearchDeps = {}): void => {
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

  const bulkRoot = queryElement(root, "[data-overview-bulk]", HTMLElement);
  const selectedCountEl = bulkRoot
    ? queryElement(bulkRoot, "[data-overview-selected-count]", HTMLElement)
    : null;
  const bulkStatusEl = bulkRoot
    ? queryElement(bulkRoot, "[data-overview-bulk-status]", HTMLElement)
    : null;
  const csrfToken = bulkRoot?.getAttribute("data-overview-csrf") || "";
  const fetcher = deps.fetch || fetch;
  const confirmAction = deps.confirm || ((message: string): boolean => window.confirm(message));
  const selectedKeys = new Map<string, string>();
  let bulkBusy = false;
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

  const setBulkStatus = (message: string, kind: BulkStatusKind = ""): void => {
    setStatusText(bulkStatusEl, message || " ", kind, [
      "is-success",
      "is-error",
      "is-loading",
    ]);
  };

  const selectedKeyList = (): string[] =>
    normalizeBulkNoteKeys(Array.from(selectedKeys.keys()));

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
    if (selected) {
      selectedKeys.set(key, result.title || key);
    } else {
      selectedKeys.delete(key);
    }
    updateBulkControls();
  };

  const pruneSelectionToRenderedKeys = (visibleKeys: Set<string>): void => {
    for (const key of selectedKeys.keys()) {
      if (!visibleKeys.has(key)) {
        selectedKeys.delete(key);
      }
    }
    updateBulkControls();
  };

  const clearResults = (message: string, kind: StatusKind): void => {
    listEl.textContent = "";
    listEl.hidden = true;
    selectedKeys.clear();
    updateBulkControls();
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

  const runBulkAction = async (
    action: BulkAction,
    fields: Partial<Record<"folder" | "property" | "op" | "value", string>>,
    fallbackMessage: string,
    confirmMessage = ""
  ): Promise<void> => {
    const keys = selectedKeyList();
    if (!bulkRoot || csrfToken.trim() === "") {
      setBulkStatus("Bulk action unavailable.", "is-error");
      return;
    }
    if (keys.length === 0) {
      setBulkStatus("Select one or more notes.", "is-error");
      return;
    }
    if (confirmMessage && !confirmAction(confirmMessage)) {
      setBulkStatus("Bulk action canceled.");
      return;
    }

    setBulkBusy(true);
    setBulkStatus("Applying bulk action...", "is-loading");
    try {
      const body = buildBulkNotesForm({
        action,
        keys,
        token: csrfToken,
        folder: fields.folder,
        property: fields.property,
        op: normalizeBulkFrontmatterOp(fields.op),
        value: fields.value,
      });
      const resp = await fetcher("/api/notes/bulk", {
        method: "POST",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
          "X-BN-CSRF": csrfToken,
        },
        body: body.toString(),
      });
      const payload = await readBulkResponsePayload(resp);
      if (!resp.ok) {
        setBulkStatus(normalizeManagementErrorMessage(resp.status, payload), "is-error");
        return;
      }
      const result = payload as BulkManagementResult;
      const failures = Array.isArray(result.failures) ? result.failures : [];
      setBulkStatus(
        bulkStatusMessage(result, fallbackMessage),
        failures.length > 0 ? "is-error" : "is-success"
      );
      window.dispatchEvent(new CustomEvent(managementMutationEventName, {
        detail: result,
      }));
      selectedKeys.clear();
      scheduleSearch(0);
    } catch {
      setBulkStatus("Bulk action failed.", "is-error");
    } finally {
      setBulkBusy(false);
    }
  };

  const trashButton = bulkRoot
    ? queryElement(bulkRoot, "[data-bulk-trash]", HTMLButtonElement)
    : null;
  trashButton?.addEventListener("click", () => {
    const count = selectedKeys.size;
    void runBulkAction(
      "trash",
      {},
      "Moved selected notes to Trash.",
      `Move ${count === 1 ? "1 selected note" : `${count} selected notes`} to Trash?`
    );
  });

  const moveForm = bulkRoot
    ? queryElement(bulkRoot, "[data-bulk-move]", HTMLFormElement)
    : null;
  moveForm?.addEventListener("submit", (event) => {
    event.preventDefault();
    const formData = new FormData(moveForm);
    void runBulkAction("move", {
      folder: String(formData.get("folder") || ""),
    }, "Moved selected notes.");
  });

  const setForm = bulkRoot
    ? queryElement(bulkRoot, "[data-bulk-frontmatter-set]", HTMLFormElement)
    : null;
  setForm?.addEventListener("submit", (event) => {
    event.preventDefault();
    const formData = new FormData(setForm);
    const property = String(formData.get("property") || "").trim();
    if (property === "") {
      setBulkStatus("Property is required.", "is-error");
      return;
    }
    void runBulkAction("frontmatter_set", {
      property,
      op: String(formData.get("op") || "="),
      value: String(formData.get("value") || ""),
    }, "Updated selected notes.");
  });

  const unsetForm = bulkRoot
    ? queryElement(bulkRoot, "[data-bulk-frontmatter-unset]", HTMLFormElement)
    : null;
  unsetForm?.addEventListener("submit", (event) => {
    event.preventDefault();
    const formData = new FormData(unsetForm);
    const property = String(formData.get("property") || "").trim();
    if (property === "") {
      setBulkStatus("Property is required.", "is-error");
      return;
    }
    void runBulkAction("frontmatter_unset", { property }, "Updated selected notes.");
  });

  updateBulkControls();
  scheduleSearch(0);
};

const normalizeBulkFrontmatterOp = (
  value: string | undefined
): "=" | "+=" | "-=" | undefined => {
  if (value === "+=" || value === "-=" || value === "=") {
    return value;
  }
  return undefined;
};

const readBulkResponsePayload = async (resp: Response): Promise<unknown> => {
  const contentType = resp.headers?.get("Content-Type") || "";
  try {
    if (contentType.includes("application/json")) {
      return await resp.json();
    }
    return await resp.text();
  } catch {
    return "";
  }
};

const bulkStatusMessage = (
  result: BulkManagementResult,
  fallback: string
): string => {
  const message = result && result.ok && result.message ? result.message : fallback;
  const failures = Array.isArray(result.failures) ? result.failures : [];
  if (failures.length === 0) {
    return message;
  }
  const summary = failures.slice(0, 3).map((failure) => {
    const key = failure.key || failure.rel_path || "note";
    return `${key}: ${failure.message || failure.code || "failed"}`;
  });
  const suffix = failures.length > summary.length
    ? `; ${failures.length - summary.length} more`
    : "";
  return `${message} Failures: ${summary.join("; ")}${suffix}.`;
};

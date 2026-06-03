import {
  buildBulkApplyForm,
  buildBulkPreviewForm,
  managementMutationEventName,
  type BulkFrontmatterChange,
  normalizeBulkManagementResult,
  normalizeBulkNoteKeys,
  normalizeBulkPreviewManagementResult,
  normalizeManagementErrorMessage,
  normalizeSearchResults,
  searchResultPath,
  type BulkAction,
  type BulkFailure,
  type BulkPreviewManagementResult,
  type BulkPreviewRow,
  type BulkPreviewValue,
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
type BulkActionFields = Partial<Record<"folder" | "property" | "op" | "value", string>>;
type BulkPreviewState = {
  action: BulkAction;
  fingerprint: string;
  previewID: string;
  expiresAt?: string;
  result: BulkPreviewManagementResult;
};

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

const bulkApplyFallbackMessage = (action: BulkAction): string => {
  switch (action) {
    case "trash":
      return "Moved selected notes to Trash.";
    case "move":
      return "Moved selected notes.";
    case "frontmatter_set":
    case "frontmatter_unset":
      return "Updated selected notes.";
    default:
      return "Applied selected notes.";
  }
};

const previewHeading = (action: BulkAction): string => {
  switch (action) {
    case "trash":
      return "Trash preview";
    case "move":
      return "Move preview";
    case "frontmatter_set":
      return "Frontmatter set preview";
    case "frontmatter_unset":
      return "Frontmatter unset preview";
    default:
      return "Bulk preview";
  }
};

const formatPreviewValue = (value: BulkPreviewValue): string => {
  if (!value.present) {
    return "(absent)";
  }
  if (typeof value.value === "string") {
    return JSON.stringify(value.value);
  }
  if (
    typeof value.value === "number" ||
    typeof value.value === "boolean" ||
    value.value === null
  ) {
    return String(value.value);
  }
  try {
    return JSON.stringify(value.value);
  } catch {
    return String(value.value);
  }
};

const renderPreviewFrontmatterItem = (
  listEl: HTMLElement,
  change: BulkFrontmatterChange
): void => {
  const doc = listEl.ownerDocument;
  const item = doc.createElement("li");
  item.className = "overview-bulk-preview-frontmatter-item";
  if (!change.changed) {
    item.classList.add("is-unchanged");
  }
  item.textContent = `${change.key}: ${formatPreviewValue(change.before)} -> ${formatPreviewValue(change.after)}`;
  listEl.appendChild(item);
};

const renderPreviewResultRow = (listEl: HTMLElement, result: BulkPreviewRow): void => {
  const doc = listEl.ownerDocument;
  const item = doc.createElement("li");
  item.className = "overview-bulk-preview-result";
  if (!result.changed) {
    item.classList.add("is-unchanged");
  }

  const header = doc.createElement("div");
  header.className = "overview-bulk-preview-result-header";

  const path = doc.createElement("p");
  path.className = "overview-bulk-preview-path";
  const sourcePath = result.source.rel_path || result.rel_path || result.key;
  const destinationPath = result.destination?.rel_path || result.rel_path;
  path.textContent = destinationPath && destinationPath !== sourcePath
    ? `${sourcePath} -> ${destinationPath}`
    : sourcePath;
  header.appendChild(path);

  const status = doc.createElement("span");
  status.className = "overview-bulk-preview-result-state";
  status.textContent = result.changed ? "Will change" : "No change";
  header.appendChild(status);
  item.appendChild(header);

  if (result.move) {
    const move = doc.createElement("p");
    move.className = "overview-bulk-preview-detail";
    move.textContent = `${result.move.rewrite_count} rewrites across ${result.move.rewrite_file_count} files (body ${result.move.body_rewrite_count}, frontmatter ${result.move.frontmatter_rewrite_count})`;
    item.appendChild(move);
  }

  if (result.frontmatter.length > 0) {
    const frontmatterList = doc.createElement("ul");
    frontmatterList.className = "overview-bulk-preview-frontmatter";
    for (const change of result.frontmatter) {
      renderPreviewFrontmatterItem(frontmatterList, change);
    }
    item.appendChild(frontmatterList);
  }

  listEl.appendChild(item);
};

const renderPreviewFailureItem = (listEl: HTMLElement, failure: BulkFailure): void => {
  const doc = listEl.ownerDocument;
  const item = doc.createElement("li");
  item.className = "overview-bulk-preview-failure";
  const label = failure.rel_path || failure.key || "note";
  item.textContent = `${label}: ${failure.message || failure.code || "failed"}`;
  listEl.appendChild(item);
};

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
  const previewPanel = bulkRoot
    ? queryElement(bulkRoot, "[data-overview-bulk-preview]", HTMLElement)
    : null;
  const previewTitleEl = previewPanel
    ? queryElement(previewPanel, "[data-overview-bulk-preview-title]", HTMLElement)
    : null;
  const previewMessageEl = previewPanel
    ? queryElement(previewPanel, "[data-overview-bulk-preview-message]", HTMLElement)
    : null;
  const previewCountsEl = previewPanel
    ? queryElement(previewPanel, "[data-overview-bulk-preview-counts]", HTMLElement)
    : null;
  const previewResultsEl = previewPanel
    ? queryElement(previewPanel, "[data-overview-bulk-preview-results]", HTMLElement)
    : null;
  const previewFailuresEl = previewPanel
    ? queryElement(previewPanel, "[data-overview-bulk-preview-failures]", HTMLElement)
    : null;
  const previewApplyButton = previewPanel
    ? queryElement(previewPanel, "[data-overview-bulk-preview-apply]", HTMLButtonElement)
    : null;
  const previewClearButton = previewPanel
    ? queryElement(previewPanel, "[data-overview-bulk-preview-clear]", HTMLButtonElement)
    : null;
  const csrfToken = bulkRoot?.getAttribute("data-overview-csrf") || "";
  const fetcher = deps.fetch || fetch;
  const trashButton = bulkRoot
    ? queryElement(bulkRoot, "[data-bulk-trash]", HTMLButtonElement)
    : null;
  const moveForm = bulkRoot
    ? queryElement(bulkRoot, "[data-bulk-move]", HTMLFormElement)
    : null;
  const moveButton = moveForm
    ? queryElement(moveForm, "button[type=\"submit\"]", HTMLButtonElement)
    : null;
  const setForm = bulkRoot
    ? queryElement(bulkRoot, "[data-bulk-frontmatter-set]", HTMLFormElement)
    : null;
  const setButton = setForm
    ? queryElement(setForm, "button[type=\"submit\"]", HTMLButtonElement)
    : null;
  const unsetForm = bulkRoot
    ? queryElement(bulkRoot, "[data-bulk-frontmatter-unset]", HTMLFormElement)
    : null;
  const unsetButton = unsetForm
    ? queryElement(unsetForm, "button[type=\"submit\"]", HTMLButtonElement)
    : null;
  const selectedKeys = new Map<string, string>();
  let bulkBusy = false;
  let previewState: BulkPreviewState | null = null;
  let debounceToken = 0;
  let ignoreNextManagementMutationClear = false;
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

  const readMoveFields = (): BulkActionFields => {
    if (!moveForm) {
      return {};
    }
    const formData = new FormData(moveForm);
    return {
      folder: String(formData.get("folder") || "").trim(),
    };
  };

  const readSetFields = (): BulkActionFields => {
    if (!setForm) {
      return {};
    }
    const formData = new FormData(setForm);
    return {
      property: String(formData.get("property") || "").trim(),
      op: String(formData.get("op") || "="),
      value: String(formData.get("value") || ""),
    };
  };

  const readUnsetFields = (): BulkActionFields => {
    if (!unsetForm) {
      return {};
    }
    const formData = new FormData(unsetForm);
    return {
      property: String(formData.get("property") || "").trim(),
    };
  };

  const normalizeBulkFields = (fields: BulkActionFields): BulkActionFields => {
    const out: BulkActionFields = {};
    if (typeof fields.folder === "string") {
      out.folder = fields.folder.trim();
    }
    if (typeof fields.property === "string") {
      out.property = fields.property.trim();
    }
    if (typeof fields.value === "string") {
      out.value = fields.value;
    }
    const op = normalizeBulkFrontmatterOp(fields.op);
    if (op) {
      out.op = op;
    }
    return out;
  };

  const previewFingerprint = (
    action: BulkAction,
    fields: BulkActionFields,
    keys = selectedKeyList()
  ): string =>
    JSON.stringify({
      action,
      keys,
      ...normalizeBulkFields(fields),
    });

  const previewMatches = (
    action: BulkAction,
    fields: BulkActionFields,
    keys = selectedKeyList()
  ): boolean =>
    Boolean(
      previewState &&
      previewState.previewID &&
      previewState.fingerprint === previewFingerprint(action, fields, keys)
    );

  const syncPreviewAttributes = (): void => {
    if (!bulkRoot) {
      return;
    }
    if (!previewState) {
      bulkRoot.removeAttribute("data-overview-preview-ready");
      bulkRoot.removeAttribute("data-overview-preview-action");
      bulkRoot.removeAttribute("data-overview-preview-id");
      bulkRoot.removeAttribute("data-overview-preview-expires-at");
      return;
    }
    bulkRoot.setAttribute("data-overview-preview-ready", "true");
    bulkRoot.setAttribute("data-overview-preview-action", previewState.action);
    bulkRoot.setAttribute("data-overview-preview-id", previewState.previewID);
    if (previewState.expiresAt) {
      bulkRoot.setAttribute("data-overview-preview-expires-at", previewState.expiresAt);
    } else {
      bulkRoot.removeAttribute("data-overview-preview-expires-at");
    }
  };

  const readFieldsForAction = (action: BulkAction): BulkActionFields => {
    switch (action) {
      case "move":
        return readMoveFields();
      case "frontmatter_set":
        return readSetFields();
      case "frontmatter_unset":
        return readUnsetFields();
      default:
        return {};
    }
  };

  const renderPreviewCounts = (result: BulkPreviewManagementResult): void => {
    if (!previewCountsEl) {
      return;
    }
    const doc = previewCountsEl.ownerDocument;
    previewCountsEl.textContent = "";
    const counts: Array<[string, number]> = [
      ["Changed", result.changed || 0],
      ["Unchanged", result.unchanged || 0],
      ["Failed", result.failed || 0],
    ];
    for (const [label, value] of counts) {
      const row = doc.createElement("div");
      row.className = "overview-bulk-preview-count";
      const term = doc.createElement("dt");
      term.textContent = label;
      const detail = doc.createElement("dd");
      detail.textContent = String(value);
      row.append(term, detail);
      previewCountsEl.appendChild(row);
    }
  };

  const renderPreviewPanel = (): void => {
    if (!previewPanel) {
      return;
    }
    if (!previewState) {
      previewPanel.hidden = true;
      if (previewCountsEl) {
        previewCountsEl.textContent = "";
      }
      if (previewResultsEl) {
        previewResultsEl.textContent = "";
      }
      if (previewFailuresEl) {
        previewFailuresEl.textContent = "";
      }
      if (previewApplyButton) {
        previewApplyButton.textContent = "Apply";
      }
      return;
    }

    previewPanel.hidden = false;
    if (previewTitleEl) {
      previewTitleEl.textContent = previewHeading(previewState.action);
    }
    if (previewMessageEl) {
      previewMessageEl.textContent = previewState.result.message || "Preview ready.";
    }
    renderPreviewCounts(previewState.result);

    if (previewResultsEl) {
      previewResultsEl.textContent = "";
      for (const result of previewState.result.results || []) {
        renderPreviewResultRow(previewResultsEl, result);
      }
    }

    if (previewFailuresEl) {
      previewFailuresEl.textContent = "";
      const failures = previewState.result.failures || [];
      previewFailuresEl.hidden = failures.length === 0;
      for (const failure of failures) {
        renderPreviewFailureItem(previewFailuresEl, failure);
      }
    }

    if (previewApplyButton) {
      previewApplyButton.textContent = bulkActionLabel(previewState.action, "apply");
    }
  };

  const bulkActionLabel = (
    action: BulkAction,
    phase: "preview" | "apply"
  ): string => {
    switch (action) {
      case "trash":
        return phase === "preview" ? "Preview Trash" : "Apply Trash";
      case "move":
        return phase === "preview" ? "Preview Move" : "Apply Move";
      case "frontmatter_set":
        return phase === "preview" ? "Preview Set" : "Apply Set";
      case "frontmatter_unset":
        return phase === "preview" ? "Preview Unset" : "Apply Unset";
      default:
        return phase === "preview" ? "Preview" : "Apply";
    }
  };

  const syncActionButton = (
    button: HTMLButtonElement | null,
    action: BulkAction,
    fields: BulkActionFields
  ): void => {
    if (!button) {
      return;
    }
    const phase = previewMatches(action, fields) ? "apply" : "preview";
    button.textContent = bulkActionLabel(action, phase);
    button.setAttribute("data-bulk-phase", phase);
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
    syncActionButton(trashButton, "trash", {});
    syncActionButton(moveButton, "move", readMoveFields());
    syncActionButton(setButton, "frontmatter_set", readSetFields());
    syncActionButton(unsetButton, "frontmatter_unset", readUnsetFields());
    if (previewApplyButton) {
      previewApplyButton.disabled = bulkBusy || count === 0 || previewState === null;
    }
    if (previewClearButton) {
      previewClearButton.disabled = bulkBusy || previewState === null;
    }
    renderPreviewPanel();
  };

  const clearPreviewState = ({ clearStatus = false }: { clearStatus?: boolean } = {}): void => {
    const hadPreview = previewState !== null;
    previewState = null;
    syncPreviewAttributes();
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
      fingerprint: previewFingerprint(action, fields),
      previewID: result.preview_id || "",
      expiresAt: result.expires_at,
      result,
    };
    syncPreviewAttributes();
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
    const query = currentQuery();
    window.clearTimeout(debounceToken);
    syncPageQueryParam(query);
    clearPreviewState({ clearStatus: true });

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
    const keys = selectedKeyList();
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
      const body = buildBulkPreviewForm({
        action,
        keys,
        token: csrfToken,
        folder: fields.folder,
        property: fields.property,
        op: normalizeBulkFrontmatterOp(fields.op),
        value: fields.value,
      });
      const resp = await fetcher("/api/notes/bulk/preview", {
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
    const keys = selectedKeyList();
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
      const body = buildBulkApplyForm({
        action,
        keys,
        token: csrfToken,
        folder: fields.folder,
        property: fields.property,
        op: normalizeBulkFrontmatterOp(fields.op),
        value: fields.value,
        previewID: previewState.previewID,
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
    void runBulkAction("move", readMoveFields(), bulkApplyFallbackMessage("move"));
  });

  setForm?.addEventListener("submit", (event) => {
    event.preventDefault();
    const fields = readSetFields();
    if (!fields.property) {
      setBulkStatus("Property is required.", "is-error");
      return;
    }
    void runBulkAction("frontmatter_set", fields, bulkApplyFallbackMessage("frontmatter_set"));
  });

  unsetForm?.addEventListener("submit", (event) => {
    event.preventDefault();
    const fields = readUnsetFields();
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
  result: {
    ok?: boolean;
    message?: string;
    failures?: BulkFailure[];
  },
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

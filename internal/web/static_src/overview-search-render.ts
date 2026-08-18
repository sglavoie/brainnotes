import {
  type BulkFrontmatterChange,
  buildNotePinRemoveFetchRequest,
  buildNotePinSetFetchRequest,
  normalizeManagementErrorMessage,
  readNotePinResponsePayload,
  searchResultPath,
  type BulkFailure,
  type BulkPreviewManagementResult,
  type BulkPreviewRow,
  type BulkPreviewValue,
  type SearchResult,
} from "./api";
import { queryElement } from "./dom";
import {
  bulkActionLabel,
  previewHeading,
  type BulkPreviewState,
  type OverviewSearchResultOptions,
} from "./overview-search-model";

/** The subset of overview search elements the preview renderers touch. */
export type OverviewPreviewElements = {
  bulkRoot: HTMLElement | null;
  previewPanel: HTMLElement | null;
  previewTitleEl: HTMLElement | null;
  previewMessageEl: HTMLElement | null;
  previewCountsEl: HTMLElement | null;
  previewResultsEl: HTMLElement | null;
  previewFailuresEl: HTMLElement | null;
  previewApplyButton: HTMLButtonElement | null;
};

export const formatPreviewValue = (value: BulkPreviewValue): string => {
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

export const renderPreviewFrontmatterItem = (
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

export const renderPreviewResultRow = (listEl: HTMLElement, result: BulkPreviewRow): void => {
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

export const renderPreviewFailureItem = (listEl: HTMLElement, failure: BulkFailure): void => {
  const doc = listEl.ownerDocument;
  const item = doc.createElement("li");
  item.className = "overview-bulk-preview-failure";
  const label = failure.rel_path || failure.key || "note";
  item.textContent = `${label}: ${failure.message || failure.code || "failed"}`;
  listEl.appendChild(item);
};

export const renderPreviewCounts = (
  previewCountsEl: HTMLElement | null,
  result: BulkPreviewManagementResult
): void => {
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

export const renderPreviewPanel = (
  els: OverviewPreviewElements,
  previewState: BulkPreviewState | null
): void => {
  const {
    previewPanel,
    previewTitleEl,
    previewMessageEl,
    previewCountsEl,
    previewResultsEl,
    previewFailuresEl,
    previewApplyButton,
  } = els;
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
  renderPreviewCounts(previewCountsEl, previewState.result);

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

export const syncPreviewAttributes = (
  bulkRoot: HTMLElement | null,
  previewState: BulkPreviewState | null
): void => {
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

// The token is read at click time rather than captured at render time because
// the bulk panel that carries it is rendered by the page, not by this module.
const overviewCSRFToken = (doc: Document): string =>
  queryElement(doc, "[data-overview-csrf]", HTMLElement)?.getAttribute("data-overview-csrf") || "";

// A Library row is a plain <li>, so a real button is valid here — unlike the
// search typeahead, whose whole row is a button[role=option].
export const renderOverviewResultPinButton = (
  doc: Document,
  result: SearchResult
): HTMLButtonElement => {
  const button = doc.createElement("button");
  button.type = "button";
  button.className = "pin-button overview-result-pin";
  button.setAttribute("data-pin-button", "");

  const applyState = (pinned: boolean): void => {
    const label = pinned ? "Unpin" : "Pin";
    button.setAttribute("aria-pressed", pinned ? "true" : "false");
    button.textContent = label;
    button.setAttribute("aria-label", `${label} ${result.title || result.key}`);
  };
  applyState(Boolean(result.pinned));

  button.addEventListener("click", async () => {
    const pinned = button.getAttribute("aria-pressed") === "true";
    const token = overviewCSRFToken(doc);
    if (!token) {
      button.title = "Pin action unavailable.";
      return;
    }

    button.disabled = true;
    button.title = "";
    applyState(!pinned);
    try {
      const request = pinned
        ? buildNotePinRemoveFetchRequest({ key: result.key, token })
        : buildNotePinSetFetchRequest({ key: result.key, token });
      const resp = await fetch(request.url, request.init);
      if (!resp.ok) {
        const payload = await readNotePinResponsePayload(resp);
        button.title = normalizeManagementErrorMessage(resp.status, payload);
        applyState(pinned);
      }
    } catch {
      button.title = pinned ? "Unpin failed." : "Pin failed.";
      applyState(pinned);
    } finally {
      button.disabled = false;
    }
  });

  return button;
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
  if (result.key) {
    row.appendChild(renderOverviewResultPinButton(doc, result));
  }
  item.appendChild(row);
  listEl.appendChild(item);
  return true;
};

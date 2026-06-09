import {
  buildOpenNoteForm,
  buildReviewItemForm,
  managementMutationEventName,
  normalizeManagementErrorMessage,
  normalizeOpenErrorMessage,
  normalizeReviewAPIResponse,
  type ReviewAffectedNote,
  type ReviewAPIResult,
  type ReviewCounts,
  type ReviewItem,
  type ReviewItemAction,
  type ReviewSnooze,
} from "./api";
import { queryAllElements, queryElement } from "./dom";
import { setStatusText, type StatusKind } from "./status";

type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

export type ReviewPanelDeps = {
  fetch?: FetchLike;
};

export type ReviewPanelConfig = {
  reviewEndpoint: string;
  reviewItemEndpoint: string;
  csrfToken: string;
};

export type ReviewFetchRequest = {
  url: string;
  init: RequestInit;
};

export const readReviewPanelConfig = (root: HTMLElement): ReviewPanelConfig => ({
  reviewEndpoint: root.getAttribute("data-review-endpoint") || "/api/review",
  reviewItemEndpoint: root.getAttribute("data-review-item-endpoint") || "/api/review/item",
  csrfToken: root.getAttribute("data-review-csrf") || "",
});

export const reviewRouteKeyFromPath = (path: string): string => {
  const cleanPath = path.trim().split(/[?#]/, 1)[0];
  const prefix = "/n/";
  if (!cleanPath.startsWith(prefix)) {
    return "";
  }
  const encodedKey = cleanPath.slice(prefix.length);
  if (encodedKey === "") {
    return "";
  }
  const decodedSegments: string[] = [];
  for (const segment of encodedKey.split("/")) {
    if (segment === "") {
      return "";
    }
    let decoded = "";
    try {
      decoded = decodeURIComponent(segment);
    } catch {
      return "";
    }
    if (
      decoded === "" ||
      decoded === "." ||
      decoded === ".." ||
      decoded.includes("/")
    ) {
      return "";
    }
    decodedSegments.push(decoded);
  }
  return decodedSegments.join("/");
};

export const buildReviewItemFetchRequest = (
  endpoint: string,
  csrfToken: string,
  id: string,
  action: ReviewItemAction,
  snooze?: ReviewSnooze
): ReviewFetchRequest => {
  const body = buildReviewItemForm({
    id,
    action,
    token: csrfToken,
    snooze,
  });
  return {
    url: endpoint,
    init: {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
        "X-BN-CSRF": csrfToken,
      },
      body: body.toString(),
    },
  };
};

export const buildReviewOpenFetchRequest = (
  key: string,
  csrfToken: string
): ReviewFetchRequest => {
  const body = buildOpenNoteForm({ key, token: csrfToken });
  return {
    url: "/api/open",
    init: {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
        "X-BN-CSRF": csrfToken,
      },
      body: body.toString(),
    },
  };
};

export const reviewCountsStatusMessage = (counts: ReviewCounts): string => {
  if (counts.active === 1) {
    return "1 active review item.";
  }
  if (counts.active > 1) {
    return `${counts.active} active review items.`;
  }
  return "No active review items.";
};

const reviewKindLabel = (kind: string): string => {
  switch (kind) {
    case "frontmatter_error":
      return "Frontmatter";
    case "schema_violation":
      return "Schema";
    case "broken_target":
      return "Broken target";
    case "duplicate_title":
      return "Duplicate title";
    case "orphan_note":
      return "Orphan";
    case "dead_end_note":
      return "Dead end";
    default:
      return "Review";
  }
};

const reviewItemLabel = (item: ReviewItem): string =>
  item.title || item.summary || item.id || "Review item";

const affectedNoteLabel = (note: ReviewAffectedNote): string =>
  note.title || note.rel_path || note.path || "Affected note";

const safeLocalPath = (path: string | undefined): string => {
  const value = (path || "").trim();
  return value.startsWith("/") ? value : "";
};

const normalizeReviewAction = (value: string): ReviewItemAction | "" => {
  switch (value) {
    case "done":
    case "snooze":
    case "clear":
      return value;
    default:
      return "";
  }
};

const normalizeReviewSnooze = (value: string): ReviewSnooze | undefined => {
  switch (value) {
    case "tomorrow":
    case "week":
      return value;
    default:
      return undefined;
  }
};

const reviewActionLoadingMessage = (
  action: ReviewItemAction,
  snooze?: ReviewSnooze
): string => {
  if (action === "done") {
    return "Marking review item done...";
  }
  if (action === "snooze") {
    return snooze === "week"
      ? "Snoozing review item for a week..."
      : "Snoozing review item until tomorrow...";
  }
  return "Updating review item...";
};

export const initReviewPanel = (deps: ReviewPanelDeps = {}): void => {
  const root = queryElement(document, "[data-review-panel]", HTMLElement);
  if (!root) {
    return;
  }

  const statusEl = queryElement(root, "[data-review-status]", HTMLElement);
  const loadingEl = queryElement(root, "[data-review-loading]", HTMLElement);
  const errorEl = queryElement(root, "[data-review-error]", HTMLElement);
  const emptyEl = queryElement(root, "[data-review-empty]", HTMLElement);
  const warningListEl = queryElement(root, "[data-review-warnings]", HTMLElement);
  const listEl = queryElement(root, "[data-review-list]", HTMLElement);
  if (!statusEl || !listEl) {
    return;
  }

  const countEls = {
    active: queryElement(root, "[data-review-count-active]", HTMLElement),
    doneHidden: queryElement(root, "[data-review-count-done-hidden]", HTMLElement),
    snoozedHidden: queryElement(root, "[data-review-count-snoozed-hidden]", HTMLElement),
    totalGenerated: queryElement(root, "[data-review-count-total-generated]", HTMLElement),
  };
  const config = readReviewPanelConfig(root);
  const fetcher = deps.fetch || fetch;
  let busy = false;

  const setStatus = (message: string, kind: StatusKind = ""): void => {
    setStatusText(statusEl, message, kind);
  };

  const setError = (message: string): void => {
    if (!errorEl) {
      return;
    }
    errorEl.textContent = message;
    errorEl.hidden = message === "";
  };

  const showError = (message: string): void => {
    setError(message);
    setStatus(message, "is-error");
  };

  const setLoading = (loading: boolean): void => {
    if (loadingEl) {
      loadingEl.hidden = !loading;
    }
  };

  const setBusy = (next: boolean): void => {
    busy = next;
    root.setAttribute("aria-busy", next ? "true" : "false");
    for (const button of queryAllElements(
      root,
      "[data-review-action], [data-review-open-key]",
      HTMLButtonElement
    )) {
      button.disabled = next;
    }
  };

  const renderCounts = (counts: ReviewCounts): void => {
    if (countEls.active) {
      countEls.active.textContent = String(counts.active);
    }
    if (countEls.doneHidden) {
      countEls.doneHidden.textContent = String(counts.done_hidden);
    }
    if (countEls.snoozedHidden) {
      countEls.snoozedHidden.textContent = String(counts.snoozed_hidden);
    }
    if (countEls.totalGenerated) {
      countEls.totalGenerated.textContent = String(counts.total_generated);
    }
  };

  const renderWarnings = (result: ReviewAPIResult): void => {
    if (!warningListEl) {
      return;
    }
    warningListEl.textContent = "";
    warningListEl.hidden = result.warnings.length === 0;
    for (const warning of result.warnings) {
      const item = warningListEl.ownerDocument.createElement("li");
      item.setAttribute("data-review-warning", "");
      if (warning.code) {
        item.setAttribute("data-review-warning-code", warning.code);
      }
      item.textContent = warning.message || warning.code || "Review warning.";
      warningListEl.appendChild(item);
    }
  };

  const renderAffectedNote = (
    doc: Document,
    list: HTMLElement,
    note: ReviewAffectedNote
  ): void => {
    const item = doc.createElement("li");
    item.className = "review-affected-note";

    const label = affectedNoteLabel(note);
    const href = safeLocalPath(note.path);
    if (href) {
      const link = doc.createElement("a");
      link.href = href;
      link.textContent = label;
      item.appendChild(link);
    } else {
      const text = doc.createElement("span");
      text.textContent = label;
      item.appendChild(text);
    }

    const folderHref = safeLocalPath(note.folder_path);
    if (note.folder && folderHref) {
      const folder = doc.createElement("a");
      folder.className = "diagnostic-folder";
      folder.href = folderHref;
      folder.textContent = note.folder;
      item.appendChild(folder);
    }
    if (note.location) {
      const location = doc.createElement("span");
      location.className = "diagnostic-location";
      location.textContent = note.location;
      item.appendChild(location);
    }

    const openKey = reviewRouteKeyFromPath(note.path || "");
    if (openKey) {
      const openButton = doc.createElement("button");
      openButton.className = "review-action review-open-editor";
      openButton.type = "button";
      openButton.setAttribute("data-review-open-key", openKey);
      openButton.setAttribute("aria-label", `Open ${label} in editor`);
      openButton.textContent = "Open in editor";
      item.appendChild(openButton);
    }

    list.appendChild(item);
  };

  const renderReviewItem = (item: ReviewItem): HTMLLIElement => {
    const doc = listEl.ownerDocument;
    const row = doc.createElement("li");
    row.className = "review-item";
    row.setAttribute("data-review-item", "");
    row.setAttribute("data-review-id", item.id);
    row.setAttribute("data-review-kind", item.kind);
    row.setAttribute("data-review-signature", item.signature);

    const main = doc.createElement("div");
    main.className = "review-item-main";

    const kind = doc.createElement("p");
    kind.className = "review-item-kind";
    kind.textContent = reviewKindLabel(item.kind);
    main.appendChild(kind);

    const title = doc.createElement("h3");
    title.className = "review-item-title";
    title.textContent = reviewItemLabel(item);
    main.appendChild(title);

    if (item.summary) {
      const summary = doc.createElement("p");
      summary.className = "review-item-summary";
      summary.textContent = item.summary;
      main.appendChild(summary);
    }

    if (item.detail || item.affected.length > 0) {
      const details = doc.createElement("details");
      details.className = "review-item-details";

      const detailsSummary = doc.createElement("summary");
      detailsSummary.textContent = "View details";
      details.appendChild(detailsSummary);

      if (item.detail) {
        const detail = doc.createElement("p");
        detail.className = "review-item-detail";
        detail.textContent = item.detail;
        details.appendChild(detail);
      }

      if (item.affected.length > 0) {
        const affectedList = doc.createElement("ul");
        affectedList.className = "review-affected-list";
        affectedList.setAttribute("aria-label", "Affected notes");
        for (const affected of item.affected) {
          renderAffectedNote(doc, affectedList, affected);
        }
        details.appendChild(affectedList);
      }

      main.appendChild(details);
    }

    const actions = doc.createElement("div");
    actions.className = "review-item-actions";
    actions.setAttribute("data-review-item-actions", "");
    actions.appendChild(renderActionButton(doc, item.id, "done", "Done"));
    actions.appendChild(renderActionButton(
      doc,
      item.id,
      "snooze",
      "Snooze tomorrow",
      "tomorrow"
    ));
    actions.appendChild(renderActionButton(
      doc,
      item.id,
      "snooze",
      "Snooze next week",
      "week"
    ));

    row.append(main, actions);
    return row;
  };

  const renderResult = (result: ReviewAPIResult): void => {
    renderCounts(result.counts);
    renderWarnings(result);
    listEl.textContent = "";
    for (const item of result.items) {
      listEl.appendChild(renderReviewItem(item));
    }
    const hasItems = result.items.length > 0;
    listEl.hidden = !hasItems;
    if (emptyEl) {
      emptyEl.hidden = hasItems;
    }
    setBusy(busy);
  };

  const applyResult = (
    payload: unknown,
    successMessage = "",
    successKind: StatusKind = ""
  ): boolean => {
    const result = normalizeReviewAPIResponse(payload);
    if (!result.ok) {
      showError(result.message || "Review request failed.");
      return false;
    }
    setError("");
    renderResult(result);
    const message = successMessage || reviewCountsStatusMessage(result.counts);
    const kind = successKind || (result.counts.active === 0 ? "is-empty" : "");
    setStatus(message, kind);
    return true;
  };

  const loadReview = async (successMessage = ""): Promise<void> => {
    setBusy(true);
    setLoading(true);
    setStatus("Loading review queue...", "is-loading");
    try {
      const resp = await fetcher(config.reviewEndpoint, {
        headers: { Accept: "application/json" },
      });
      const payload = await readReviewResponsePayload(resp);
      if (!resp.ok) {
        showError(normalizeManagementErrorMessage(resp.status, payload));
        return;
      }
      applyResult(payload, successMessage, successMessage ? "is-success" : "");
    } catch {
      showError("Review unavailable.");
    } finally {
      setLoading(false);
      setBusy(false);
    }
  };

  const runItemAction = async (button: HTMLButtonElement): Promise<void> => {
    const id = button.getAttribute("data-review-action-id") || "";
    const action = normalizeReviewAction(button.getAttribute("data-review-action") || "");
    const snooze = normalizeReviewSnooze(button.getAttribute("data-review-snooze") || "");
    if (!id || !action || !config.csrfToken) {
      showError("Review action unavailable.");
      return;
    }
    if (action === "snooze" && !snooze) {
      showError("Review snooze unavailable.");
      return;
    }

    setBusy(true);
    setStatus(reviewActionLoadingMessage(action, snooze), "is-loading");
    try {
      const request = buildReviewItemFetchRequest(
        config.reviewItemEndpoint,
        config.csrfToken,
        id,
        action,
        snooze
      );
      const resp = await fetcher(request.url, request.init);
      const payload = await readReviewResponsePayload(resp);
      if (!resp.ok) {
        showError(normalizeManagementErrorMessage(resp.status, payload));
        return;
      }
      const result = normalizeReviewAPIResponse(payload);
      applyResult(payload, result.message || "Review item updated.", "is-success");
    } catch {
      showError("Review action failed.");
    } finally {
      setBusy(false);
    }
  };

  const runOpenAction = async (button: HTMLButtonElement): Promise<void> => {
    const key = button.getAttribute("data-review-open-key") || "";
    if (!key || !config.csrfToken) {
      showError("Open action unavailable.");
      return;
    }

    setBusy(true);
    setStatus("Opening editor...", "is-loading");
    try {
      const request = buildReviewOpenFetchRequest(key, config.csrfToken);
      const resp = await fetcher(request.url, request.init);
      const payload = await readReviewResponsePayload(resp);
      if (!resp.ok) {
        showError(normalizeOpenErrorMessage(resp.status, payload));
        return;
      }
      setError("");
      setStatus("Opened in editor. Refresh after saving changes.", "is-success");
    } catch {
      showError("Open failed.");
    } finally {
      setBusy(false);
    }
  };

  root.addEventListener("click", (event) => {
    const target = event.target;
    if (!(target instanceof Element)) {
      return;
    }
    const actionButton = target.closest<HTMLButtonElement>("[data-review-action]");
    if (actionButton && root.contains(actionButton)) {
      void runItemAction(actionButton);
      return;
    }
    const openButton = target.closest<HTMLButtonElement>("[data-review-open-key]");
    if (openButton && root.contains(openButton)) {
      void runOpenAction(openButton);
    }
  });

  window.addEventListener(managementMutationEventName, () => {
    void loadReview("Review refreshed.");
  });

  void loadReview();
};

const renderActionButton = (
  doc: Document,
  id: string,
  action: ReviewItemAction,
  label: string,
  snooze?: ReviewSnooze
): HTMLButtonElement => {
  const button = doc.createElement("button");
  button.className = "review-action";
  button.type = "button";
  button.setAttribute("data-review-action", action);
  button.setAttribute("data-review-action-id", id);
  if (snooze) {
    button.setAttribute("data-review-snooze", snooze);
  }
  button.textContent = label;
  return button;
};

const readReviewResponsePayload = async (resp: Response): Promise<unknown> => {
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

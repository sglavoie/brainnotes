import {
  buildCockpitItemForm,
  buildFixApplyForm,
  buildFixPreviewForm,
  buildOpenNoteForm,
  managementMutationEventName,
  normalizeCockpitQueueResponse,
  normalizeFixApplyResponse,
  normalizeFixPreviewResponse,
  normalizeManagementErrorMessage,
  normalizeOpenErrorMessage,
  type CockpitAffectedNote,
  type CockpitCounts,
  type CockpitItemAction,
  type CockpitQueueResult,
  type CockpitSnooze,
  type CockpitTask,
  type FixAction,
  type NotePreview,
  type NotePreviewChange,
  type NotePreviewText,
} from "./api";
import { queryAllElements, queryElement } from "./dom";
import { notePathForKey } from "./filters";
import { setStatusText, type StatusKind } from "./status";

type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

export type DailyFixesDeps = {
  fetch?: FetchLike;
};

export type DailyFixesConfig = {
  queueEndpoint: string;
  itemEndpoint: string;
  previewEndpoint: string;
  applyEndpoint: string;
  openEndpoint: string;
  csrfToken: string;
};

export type FixesFetchRequest = {
  url: string;
  init: RequestInit;
};

const FIX_ACTIONS = new Set<string>(["schema_repair", "relink", "create_stub"]);

export const readDailyFixesConfig = (root: HTMLElement): DailyFixesConfig => ({
  queueEndpoint: root.getAttribute("data-fixes-endpoint") || "/api/cockpit",
  itemEndpoint: root.getAttribute("data-fixes-item-endpoint") || "/api/cockpit/item",
  previewEndpoint: root.getAttribute("data-fixes-preview-endpoint") || "/api/fixes/preview",
  applyEndpoint: root.getAttribute("data-fixes-apply-endpoint") || "/api/fixes/apply",
  openEndpoint: root.getAttribute("data-fixes-open-endpoint") || "/api/open",
  csrfToken: root.getAttribute("data-fixes-csrf") || "",
});

// fixOpenKeyFromRelPath derives the route key `/api/open` expects from a task's
// affected note rel_path. It mirrors the server's managementRouteKeyForRelPath:
// drop a trailing `.md`, lower-case, and reject traversal or empty segments.
export const fixOpenKeyFromRelPath = (relPath: string): string => {
  const trimmed = (relPath || "").trim().replace(/\\/g, "/");
  if (trimmed === "") {
    return "";
  }
  const withoutExt = trimmed.replace(/\.md$/i, "");
  const segments: string[] = [];
  for (const segment of withoutExt.split("/")) {
    if (segment === "" || segment === "." || segment === "..") {
      return "";
    }
    segments.push(segment.toLowerCase());
  }
  return segments.join("/");
};

export const fixKindLabel = (kind: string): string => {
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
    case "long_tail_value":
      return "Long-tail value";
    default:
      return "Fix";
  }
};

const fixTaskLabel = (task: CockpitTask): string =>
  task.title || task.summary || task.id || "Fix";

const affectedNoteLabel = (note: CockpitAffectedNote): string =>
  note.title || note.rel_path || "Affected note";

export const fixCountsStatusMessage = (counts: CockpitCounts): string => {
  if (counts.active === 1) {
    return "1 active fix.";
  }
  if (counts.active > 1) {
    return `${counts.active} active fixes.`;
  }
  return "No active fixes.";
};

const normalizeItemAction = (value: string): CockpitItemAction | "" => {
  switch (value) {
    case "mark_done":
    case "done":
    case "snooze":
    case "clear":
      return value;
    default:
      return "";
  }
};

const normalizeSnooze = (value: string): CockpitSnooze | undefined => {
  switch (value) {
    case "tomorrow":
    case "week":
      return value;
    default:
      return undefined;
  }
};

const normalizeFixAction = (value: string): FixAction | "" => {
  switch (value) {
    case "schema_repair":
    case "relink":
    case "create_stub":
      return value;
    default:
      return "";
  }
};

const itemActionLoadingMessage = (
  action: CockpitItemAction,
  snooze?: CockpitSnooze
): string => {
  if (action === "snooze") {
    return snooze === "week"
      ? "Snoozing fix for a week..."
      : "Snoozing fix until tomorrow...";
  }
  if (action === "clear") {
    return "Clearing fix state...";
  }
  return "Marking fix done...";
};

export const buildFixesItemFetchRequest = (
  endpoint: string,
  csrfToken: string,
  id: string,
  action: CockpitItemAction,
  snooze?: CockpitSnooze
): FixesFetchRequest => {
  const body = buildCockpitItemForm({ id, action, token: csrfToken, snooze });
  return jsonPostRequest(endpoint, csrfToken, body.toString());
};

export const buildFixesPreviewFetchRequest = (
  endpoint: string,
  csrfToken: string,
  id: string,
  action: FixAction
): FixesFetchRequest => {
  const body = buildFixPreviewForm({ id, action, token: csrfToken });
  return jsonPostRequest(endpoint, csrfToken, body.toString());
};

export const buildFixesApplyFetchRequest = (
  endpoint: string,
  csrfToken: string,
  id: string,
  action: FixAction,
  previewID: string
): FixesFetchRequest => {
  const body = buildFixApplyForm({ id, action, token: csrfToken, previewID });
  return jsonPostRequest(endpoint, csrfToken, body.toString());
};

const jsonPostRequest = (
  url: string,
  csrfToken: string,
  body: string
): FixesFetchRequest => ({
  url,
  init: {
    method: "POST",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
      "X-BN-CSRF": csrfToken,
    },
    body,
  },
});

const previewChangeText = (value: NotePreviewText): string =>
  value.present ? (value.text === "" ? "(empty)" : value.text) : "(absent)";

const previewChangeLine = (change: NotePreviewChange): string => {
  const rendered = `${previewChangeText(change.old)} -> ${previewChangeText(change.new)}`;
  switch (change.kind) {
    case "frontmatter":
      return `frontmatter[${change.key || ""}]: ${rendered}`;
    case "body":
      return `body:${change.line || 0}: ${rendered}`;
    case "path":
      return `path: ${rendered}`;
    default:
      return rendered;
  }
};

type PreviewState = {
  action: FixAction;
  previewID: string;
};

export const initDailyFixes = (deps: DailyFixesDeps = {}): void => {
  const root = queryElement(document, "[data-fixes-panel]", HTMLElement);
  if (!root) {
    return;
  }

  const statusEl = queryElement(root, "[data-fixes-status]", HTMLElement);
  const loadingEl = queryElement(root, "[data-fixes-loading]", HTMLElement);
  const errorEl = queryElement(root, "[data-fixes-error]", HTMLElement);
  const emptyEl = queryElement(root, "[data-fixes-empty]", HTMLElement);
  const warningListEl = queryElement(root, "[data-fixes-warnings]", HTMLElement);
  const listEl = queryElement(root, "[data-fixes-list]", HTMLElement);
  if (!statusEl || !listEl) {
    return;
  }

  const countEls = {
    active: queryElement(root, "[data-fixes-count-active]", HTMLElement),
    doneHidden: queryElement(root, "[data-fixes-count-done-hidden]", HTMLElement),
    snoozedHidden: queryElement(root, "[data-fixes-count-snoozed-hidden]", HTMLElement),
    totalGenerated: queryElement(root, "[data-fixes-count-total-generated]", HTMLElement),
  };
  const config = readDailyFixesConfig(root);
  const fetcher = deps.fetch || fetch;
  const previews = new Map<string, PreviewState>();
  let busy = false;
  let ignoreNextMutation = false;

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
    for (const button of queryAllElements(root, "[data-fixes-button]", HTMLButtonElement)) {
      button.disabled = next;
    }
  };

  const renderCounts = (counts: CockpitCounts): void => {
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

  const renderWarnings = (result: CockpitQueueResult): void => {
    if (!warningListEl) {
      return;
    }
    warningListEl.textContent = "";
    warningListEl.hidden = result.warnings.length === 0;
    for (const warning of result.warnings) {
      const item = warningListEl.ownerDocument.createElement("li");
      item.setAttribute("data-fixes-warning", "");
      item.textContent = warning.message || "Cockpit warning.";
      warningListEl.appendChild(item);
    }
  };

  const openButton = (
    doc: Document,
    note: CockpitAffectedNote
  ): HTMLButtonElement | null => {
    const key = fixOpenKeyFromRelPath(note.rel_path);
    if (key === "") {
      return null;
    }
    const button = doc.createElement("button");
    button.className = "review-action review-open-editor";
    button.type = "button";
    button.setAttribute("data-fixes-button", "");
    button.setAttribute("data-fixes-open-key", key);
    button.setAttribute("aria-label", `Open ${affectedNoteLabel(note)} in editor`);
    button.textContent = "Open in editor";
    return button;
  };

  const renderAffectedNote = (
    doc: Document,
    list: HTMLElement,
    note: CockpitAffectedNote
  ): void => {
    const item = doc.createElement("li");
    item.className = "review-affected-note";

    const label = affectedNoteLabel(note);
    const key = fixOpenKeyFromRelPath(note.rel_path);
    if (key !== "") {
      const link = doc.createElement("a");
      link.href = notePathForKey(key);
      link.textContent = label;
      item.appendChild(link);
    } else {
      const text = doc.createElement("span");
      text.textContent = label;
      item.appendChild(text);
    }

    if (note.folder) {
      const folder = doc.createElement("span");
      folder.className = "diagnostic-folder";
      folder.textContent = note.folder;
      item.appendChild(folder);
    }
    if (note.location) {
      const location = doc.createElement("span");
      location.className = "diagnostic-location";
      location.textContent = note.location;
      item.appendChild(location);
    }

    const open = openButton(doc, note);
    if (open) {
      item.appendChild(open);
    }

    list.appendChild(item);
  };

  const triageButton = (
    doc: Document,
    id: string,
    action: CockpitItemAction,
    label: string,
    snooze?: CockpitSnooze
  ): HTMLButtonElement => {
    const button = doc.createElement("button");
    button.className = "review-action";
    button.type = "button";
    button.setAttribute("data-fixes-button", "");
    button.setAttribute("data-fixes-item-action", action);
    button.setAttribute("data-fixes-id", id);
    if (snooze) {
      button.setAttribute("data-fixes-snooze", snooze);
    }
    button.textContent = label;
    return button;
  };

  const fixButton = (
    doc: Document,
    id: string,
    action: FixAction,
    label: string
  ): HTMLButtonElement => {
    const button = doc.createElement("button");
    button.className = "review-action fixes-fix-action";
    button.type = "button";
    button.setAttribute("data-fixes-button", "");
    button.setAttribute("data-fixes-fix-action", action);
    button.setAttribute("data-fixes-id", id);
    button.textContent = label;
    return button;
  };

  const renderTask = (task: CockpitTask): HTMLLIElement => {
    const doc = listEl.ownerDocument;
    const row = doc.createElement("li");
    row.className = "review-item fixes-item";
    row.setAttribute("data-fixes-task", "");
    row.setAttribute("data-fixes-id", task.id);
    row.setAttribute("data-fixes-kind", task.kind);
    row.setAttribute("data-fixes-signature", task.signature);

    const main = doc.createElement("div");
    main.className = "review-item-main";

    const kind = doc.createElement("p");
    kind.className = "review-item-kind";
    kind.textContent = fixKindLabel(task.kind);
    main.appendChild(kind);

    const title = doc.createElement("h3");
    title.className = "review-item-title";
    title.textContent = fixTaskLabel(task);
    main.appendChild(title);

    if (task.summary) {
      const summary = doc.createElement("p");
      summary.className = "review-item-summary";
      summary.textContent = task.summary;
      main.appendChild(summary);
    }

    if (task.detail || task.affected.length > 0) {
      const details = doc.createElement("details");
      details.className = "review-item-details";
      const detailsSummary = doc.createElement("summary");
      detailsSummary.textContent = "View details";
      details.appendChild(detailsSummary);

      if (task.detail) {
        const detail = doc.createElement("p");
        detail.className = "review-item-detail";
        detail.textContent = task.detail;
        details.appendChild(detail);
      }

      if (task.affected.length > 0) {
        const affectedList = doc.createElement("ul");
        affectedList.className = "review-affected-list";
        affectedList.setAttribute("aria-label", "Affected notes");
        for (const affected of task.affected) {
          renderAffectedNote(doc, affectedList, affected);
        }
        details.appendChild(affectedList);
      }
      main.appendChild(details);
    }

    const actions = doc.createElement("div");
    actions.className = "review-item-actions";
    actions.setAttribute("data-fixes-task-actions", "");
    if (!task.triage_only) {
      for (const action of task.actions) {
        if (FIX_ACTIONS.has(action.kind)) {
          actions.appendChild(fixButton(doc, task.id, action.kind as FixAction, action.label));
        }
      }
    }
    actions.appendChild(triageButton(doc, task.id, "mark_done", "Done"));
    actions.appendChild(triageButton(doc, task.id, "snooze", "Snooze tomorrow", "tomorrow"));
    actions.appendChild(triageButton(doc, task.id, "snooze", "Snooze next week", "week"));
    main.appendChild(actions);

    const preview = doc.createElement("div");
    preview.className = "fixes-preview";
    preview.setAttribute("data-fixes-preview", "");
    preview.hidden = true;
    main.appendChild(preview);

    row.appendChild(main);
    return row;
  };

  const renderGroups = (tasks: CockpitTask[]): void => {
    const doc = listEl.ownerDocument;
    listEl.textContent = "";
    const order: string[] = [];
    const groups = new Map<string, CockpitTask[]>();
    for (const task of tasks) {
      const bucket = groups.get(task.kind);
      if (bucket) {
        bucket.push(task);
      } else {
        order.push(task.kind);
        groups.set(task.kind, [task]);
      }
    }
    for (const kind of order) {
      const bucket = groups.get(kind) || [];
      const group = doc.createElement("li");
      group.className = "fixes-group";
      group.setAttribute("data-fixes-group", kind);

      const heading = doc.createElement("h3");
      heading.className = "fixes-group-heading";
      heading.textContent = `${fixKindLabel(kind)} (${bucket.length})`;
      group.appendChild(heading);

      const groupList = doc.createElement("ul");
      groupList.className = "fixes-group-list";
      for (const task of bucket) {
        groupList.appendChild(renderTask(task));
      }
      group.appendChild(groupList);
      listEl.appendChild(group);
    }
  };

  const renderResult = (result: CockpitQueueResult): void => {
    previews.clear();
    renderCounts(result.counts);
    renderWarnings(result);
    renderGroups(result.tasks);
    const hasTasks = result.tasks.length > 0;
    listEl.hidden = !hasTasks;
    if (emptyEl) {
      emptyEl.hidden = hasTasks;
    }
    setBusy(busy);
  };

  const applyQueueResult = (
    payload: unknown,
    successMessage = "",
    successKind: StatusKind = ""
  ): boolean => {
    const result = normalizeCockpitQueueResponse(payload);
    if (!result.ok) {
      showError(result.message || "Cockpit request failed.");
      return false;
    }
    setError("");
    renderResult(result);
    const message = successMessage || fixCountsStatusMessage(result.counts);
    const kind = successKind || (result.counts.active === 0 ? "is-empty" : "");
    setStatus(message, kind);
    return true;
  };

  const loadQueue = async (successMessage = ""): Promise<void> => {
    setBusy(true);
    setLoading(true);
    setStatus("Loading fixes queue...", "is-loading");
    try {
      const resp = await fetcher(config.queueEndpoint, {
        headers: { Accept: "application/json" },
      });
      const payload = await readResponsePayload(resp);
      if (!resp.ok) {
        showError(normalizeManagementErrorMessage(resp.status, payload));
        return;
      }
      applyQueueResult(payload, successMessage, successMessage ? "is-success" : "");
    } catch {
      showError("Fixes queue unavailable.");
    } finally {
      setLoading(false);
      setBusy(false);
    }
  };

  const findPreviewEl = (id: string): HTMLElement | null => {
    for (const task of queryAllElements(listEl, "[data-fixes-task]", HTMLElement)) {
      if (task.getAttribute("data-fixes-id") === id) {
        return queryElement(task, "[data-fixes-preview]", HTMLElement);
      }
    }
    return null;
  };

  const clearPreview = (id: string): void => {
    previews.delete(id);
    const el = findPreviewEl(id);
    if (el) {
      el.textContent = "";
      el.hidden = true;
    }
  };

  const renderPreview = (
    id: string,
    action: FixAction,
    previews_: NotePreview[],
    message: string
  ): void => {
    const el = findPreviewEl(id);
    if (!el) {
      return;
    }
    const doc = el.ownerDocument;
    el.textContent = "";
    el.hidden = false;

    const heading = doc.createElement("p");
    heading.className = "fixes-preview-message";
    heading.textContent = message || "Preview ready.";
    el.appendChild(heading);

    for (const preview of previews_) {
      const noteEl = doc.createElement("div");
      noteEl.className = "fixes-preview-note";
      const path = doc.createElement("p");
      path.className = "fixes-preview-path";
      path.textContent = preview.rel_path;
      noteEl.appendChild(path);
      const changeList = doc.createElement("ul");
      changeList.className = "fixes-preview-changes";
      for (const change of preview.changes) {
        const line = doc.createElement("li");
        line.textContent = previewChangeLine(change);
        changeList.appendChild(line);
      }
      noteEl.appendChild(changeList);
      el.appendChild(noteEl);
    }

    const controls = doc.createElement("div");
    controls.className = "fixes-preview-actions";
    const applyBtn = doc.createElement("button");
    applyBtn.className = "review-action fixes-preview-apply";
    applyBtn.type = "button";
    applyBtn.setAttribute("data-fixes-button", "");
    applyBtn.setAttribute("data-fixes-preview-apply", "");
    applyBtn.setAttribute("data-fixes-id", id);
    applyBtn.textContent = "Apply fix";
    const cancelBtn = doc.createElement("button");
    cancelBtn.className = "review-action fixes-preview-cancel";
    cancelBtn.type = "button";
    cancelBtn.setAttribute("data-fixes-button", "");
    cancelBtn.setAttribute("data-fixes-preview-cancel", "");
    cancelBtn.setAttribute("data-fixes-id", id);
    cancelBtn.textContent = "Cancel";
    controls.append(applyBtn, cancelBtn);
    el.appendChild(controls);
    void action;
  };

  const runItemAction = async (button: HTMLButtonElement): Promise<void> => {
    const id = button.getAttribute("data-fixes-id") || "";
    const action = normalizeItemAction(button.getAttribute("data-fixes-item-action") || "");
    const snooze = normalizeSnooze(button.getAttribute("data-fixes-snooze") || "");
    if (!id || !action || !config.csrfToken) {
      showError("Fix action unavailable.");
      return;
    }
    if (action === "snooze" && !snooze) {
      showError("Fix snooze unavailable.");
      return;
    }

    setBusy(true);
    setStatus(itemActionLoadingMessage(action, snooze), "is-loading");
    try {
      const request = buildFixesItemFetchRequest(
        config.itemEndpoint,
        config.csrfToken,
        id,
        action,
        snooze
      );
      const resp = await fetcher(request.url, request.init);
      const payload = await readResponsePayload(resp);
      if (!resp.ok) {
        showError(normalizeManagementErrorMessage(resp.status, payload));
        return;
      }
      const result = normalizeCockpitQueueResponse(payload);
      applyQueueResult(payload, result.message || "Fix updated.", "is-success");
    } catch {
      showError("Fix action failed.");
    } finally {
      setBusy(false);
    }
  };

  const runPreview = async (button: HTMLButtonElement): Promise<void> => {
    const id = button.getAttribute("data-fixes-id") || "";
    const action = normalizeFixAction(button.getAttribute("data-fixes-fix-action") || "");
    if (!id || !action || !config.csrfToken) {
      showError("Fix preview unavailable.");
      return;
    }

    setBusy(true);
    setStatus("Previewing fix...", "is-loading");
    try {
      const request = buildFixesPreviewFetchRequest(
        config.previewEndpoint,
        config.csrfToken,
        id,
        action
      );
      const resp = await fetcher(request.url, request.init);
      const payload = await readResponsePayload(resp);
      if (!resp.ok) {
        showError(normalizeManagementErrorMessage(resp.status, payload));
        return;
      }
      const result = normalizeFixPreviewResponse(payload);
      if (!result.ok || !result.preview_id) {
        showError(result.message || "Fix preview failed.");
        return;
      }
      previews.set(id, { action, previewID: result.preview_id });
      renderPreview(id, action, result.previews, result.message || "Preview ready.");
      setError("");
      setStatus(result.message || "Preview ready.", "is-success");
    } catch {
      showError("Fix preview failed.");
    } finally {
      setBusy(false);
    }
  };

  const runApply = async (button: HTMLButtonElement): Promise<void> => {
    const id = button.getAttribute("data-fixes-id") || "";
    const state = previews.get(id);
    if (!id || !state || !config.csrfToken) {
      showError("Preview the fix before applying it.");
      return;
    }

    setBusy(true);
    setStatus("Applying fix...", "is-loading");
    try {
      const request = buildFixesApplyFetchRequest(
        config.applyEndpoint,
        config.csrfToken,
        id,
        state.action,
        state.previewID
      );
      const resp = await fetcher(request.url, request.init);
      const payload = await readResponsePayload(resp);
      if (!resp.ok) {
        clearPreview(id);
        showError(normalizeManagementErrorMessage(resp.status, payload));
        await loadQueue();
        return;
      }
      const result = normalizeFixApplyResponse(payload);
      if (!result.ok) {
        clearPreview(id);
        showError(result.message || "Fix apply failed.");
        await loadQueue();
        return;
      }
      previews.delete(id);
      ignoreNextMutation = true;
      window.dispatchEvent(new CustomEvent(managementMutationEventName, { detail: result }));
      await loadQueue(result.message || "Fix applied.");
    } catch {
      clearPreview(id);
      showError("Fix apply failed.");
    } finally {
      setBusy(false);
    }
  };

  const runOpen = async (button: HTMLButtonElement): Promise<void> => {
    const key = button.getAttribute("data-fixes-open-key") || "";
    if (!key || !config.csrfToken) {
      showError("Open action unavailable.");
      return;
    }
    setBusy(true);
    setStatus("Opening editor...", "is-loading");
    try {
      const body = buildOpenNoteForm({ key, token: config.csrfToken });
      const request = jsonPostRequest(config.openEndpoint, config.csrfToken, body.toString());
      const resp = await fetcher(request.url, request.init);
      const payload = await readResponsePayload(resp);
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
    const fix = target.closest<HTMLButtonElement>("[data-fixes-fix-action]");
    if (fix && root.contains(fix)) {
      void runPreview(fix);
      return;
    }
    const applyBtn = target.closest<HTMLButtonElement>("[data-fixes-preview-apply]");
    if (applyBtn && root.contains(applyBtn)) {
      void runApply(applyBtn);
      return;
    }
    const cancelBtn = target.closest<HTMLButtonElement>("[data-fixes-preview-cancel]");
    if (cancelBtn && root.contains(cancelBtn)) {
      clearPreview(cancelBtn.getAttribute("data-fixes-id") || "");
      return;
    }
    const itemBtn = target.closest<HTMLButtonElement>("[data-fixes-item-action]");
    if (itemBtn && root.contains(itemBtn)) {
      void runItemAction(itemBtn);
      return;
    }
    const openBtn = target.closest<HTMLButtonElement>("[data-fixes-open-key]");
    if (openBtn && root.contains(openBtn)) {
      void runOpen(openBtn);
    }
  });

  window.addEventListener(managementMutationEventName, () => {
    if (ignoreNextMutation) {
      ignoreNextMutation = false;
      return;
    }
    void loadQueue("Fixes refreshed.");
  });

  void loadQueue();
};

const readResponsePayload = async (resp: Response): Promise<unknown> => {
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

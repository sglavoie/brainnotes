import type {
  CockpitAffectedNote,
  CockpitCounts,
  CockpitItemAction,
  CockpitQueueResult,
  CockpitSnooze,
  CockpitTask,
  FixAction,
} from "./api";
import {
  affectedNoteLabel,
  FIX_ACTIONS,
  fixKindLabel,
  fixOpenKeyFromRelPath,
  fixTaskLabel,
} from "./daily-fixes-model";
import { notePathForKey } from "./filters";

export type FixesCountElements = {
  active: HTMLElement | null;
  doneHidden: HTMLElement | null;
  snoozedHidden: HTMLElement | null;
  totalGenerated: HTMLElement | null;
};

export const renderCounts = (
  countEls: FixesCountElements,
  counts: CockpitCounts
): void => {
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

export const renderWarnings = (
  warningListEl: HTMLElement | null,
  result: CockpitQueueResult
): void => {
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

export const openButton = (
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

export const renderAffectedNote = (
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

export const triageButton = (
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

export const fixButton = (
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

export const renderTask = (doc: Document, task: CockpitTask): HTMLLIElement => {
  const row = doc.createElement("li");
  row.className = "review-item fixes-item";
  row.setAttribute("data-fixes-task", "");
  row.setAttribute("data-fixes-id", task.id);
  row.setAttribute("data-fixes-kind", task.kind);
  row.setAttribute("data-fixes-signature", task.signature);
  if (task.triage_only) {
    row.setAttribute("data-fixes-triage", "");
  }

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

export const renderGroups = (listEl: HTMLElement, tasks: CockpitTask[]): void => {
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
      groupList.appendChild(renderTask(doc, task));
    }
    group.appendChild(groupList);
    listEl.appendChild(group);
  }
};

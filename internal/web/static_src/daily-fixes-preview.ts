import type {
  FixAction,
  NotePreview,
  NotePreviewChange,
  NotePreviewText,
} from "./api";
import { queryAllElements, queryElement } from "./dom";

export const previewChangeText = (value: NotePreviewText): string =>
  value.present ? (value.text === "" ? "(empty)" : value.text) : "(absent)";

export const previewChangeLine = (change: NotePreviewChange): string => {
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

export const findPreviewEl = (listEl: HTMLElement, id: string): HTMLElement | null => {
  for (const task of queryAllElements(listEl, "[data-fixes-task]", HTMLElement)) {
    if (task.getAttribute("data-fixes-id") === id) {
      return queryElement(task, "[data-fixes-preview]", HTMLElement);
    }
  }
  return null;
};

export const renderPreview = (
  listEl: HTMLElement,
  id: string,
  action: FixAction,
  previews_: NotePreview[],
  message: string
): void => {
  const el = findPreviewEl(listEl, id);
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
  applyBtn.className = "bn-button review-action fixes-preview-apply";
  applyBtn.type = "button";
  applyBtn.setAttribute("data-fixes-button", "");
  applyBtn.setAttribute("data-fixes-preview-apply", "");
  applyBtn.setAttribute("data-fixes-id", id);
  applyBtn.textContent = "Apply fix";
  const cancelBtn = doc.createElement("button");
  cancelBtn.className = "bn-button review-action fixes-preview-cancel";
  cancelBtn.type = "button";
  cancelBtn.setAttribute("data-fixes-button", "");
  cancelBtn.setAttribute("data-fixes-preview-cancel", "");
  cancelBtn.setAttribute("data-fixes-id", id);
  cancelBtn.textContent = "Cancel";
  controls.append(applyBtn, cancelBtn);
  el.appendChild(controls);
  void action;
};

export const clearPreviewEl = (listEl: HTMLElement, id: string): void => {
  const el = findPreviewEl(listEl, id);
  if (el) {
    el.textContent = "";
    el.hidden = true;
  }
};

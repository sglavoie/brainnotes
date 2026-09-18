import type { AttachmentEntry, AttachmentTracker, UploadFile } from "./source-attachments";

// The attachment panel: the three ways a file gets in, and the list of what is
// waiting to be saved.
//
// Choosing, pasting and dropping are one code path with three doors, which is
// deliberate — a screenshot pasted from the clipboard and the same file dragged
// from a folder must produce the same row, the same progress and the same link,
// or two of the three doors are only tested by whoever happens to use them.
//
// Everything here is DOM. What an upload *means* is in source-attachments.ts,
// and this module never decides an outcome — it renders `entries()` and calls
// three methods.

export type AttachmentElements = {
  panel: HTMLElement | null;
  input: HTMLInputElement | null;
  choose: HTMLButtonElement | null;
  list: HTMLElement | null;
  // dropZone is the element a file may be dropped on. It is the editing surface
  // rather than the whole page: a drop anywhere else is somebody missing, and
  // swallowing it would stop the browser from doing the obvious thing with a
  // file dragged onto a link.
  dropZone: HTMLElement | null;
};

export type AttachmentPanelOptions = {
  elements: AttachmentElements;
  tracker: AttachmentTracker;
  doc: Document;
};

export const initAttachmentPanel = (options: AttachmentPanelOptions): (() => void) => {
  const { elements, tracker } = options;
  const render = (): void => renderAttachmentList(elements.list, tracker.entries(), options.doc);

  const unbinds = [
    bindChooser(elements, tracker),
    bindClipboard(elements.dropZone, tracker),
    bindDropZone(elements.dropZone, tracker),
    bindListActions(elements.list, tracker),
  ];
  render();

  return (): void => {
    for (const unbind of unbinds) {
      unbind();
    }
  };
};

export const renderAttachmentList = (
  list: HTMLElement | null,
  entries: readonly AttachmentEntry[],
  doc: Document
): void => {
  if (!list) {
    return;
  }
  list.replaceChildren(...entries.map((entry) => renderEntry(entry, doc)));
  // The list is hidden rather than empty when there is nothing staged, so the
  // heading and its explanation do not sit over a blank box for the whole of an
  // ordinary editing session.
  const container = list.closest("[data-source-attachments]");
  if (container instanceof HTMLElement) {
    container.hidden = entries.length === 0;
  }
};

const renderEntry = (entry: AttachmentEntry, doc: Document): HTMLElement => {
  const item = doc.createElement("li");
  item.className = `source-editor-attachment is-${entry.state}`;
  item.setAttribute("data-source-attachment", entry.id);
  item.setAttribute("data-source-attachment-state", entry.state);

  const name = doc.createElement("span");
  name.className = "source-editor-attachment-name";
  name.textContent = entry.relPath || entry.filename;
  item.append(name);

  const status = doc.createElement("span");
  status.className = "source-editor-attachment-status";
  status.textContent = entryStatusText(entry);
  item.append(status);

  if (entry.state === "uploading") {
    item.append(renderProgress(entry, doc));
  }
  item.append(renderActions(entry, doc));
  return item;
};

const renderProgress = (entry: AttachmentEntry, doc: Document): HTMLElement => {
  const progress = doc.createElement("progress");
  progress.className = "source-editor-attachment-progress";
  progress.max = Math.max(entry.size, 1);
  progress.value = Math.min(entry.loaded, Math.max(entry.size, 1));
  return progress;
};

const renderActions = (entry: AttachmentEntry, doc: Document): HTMLElement => {
  const actions = doc.createElement("span");
  actions.className = "source-editor-attachment-actions";
  if (entry.state === "failed" || entry.state === "canceled") {
    actions.append(actionButton(doc, "data-source-attachment-retry", entry.id, "Retry"));
  }
  actions.append(
    actionButton(
      doc,
      "data-source-attachment-cancel",
      entry.id,
      entry.state === "staged" ? "Remove" : "Cancel"
    )
  );
  return actions;
};

const actionButton = (
  doc: Document,
  marker: string,
  id: string,
  label: string
): HTMLButtonElement => {
  const button = doc.createElement("button");
  button.type = "button";
  button.className = "source-editor-attachment-action";
  button.setAttribute(marker, "");
  button.setAttribute("data-source-attachment-id", id);
  button.textContent = label;
  return button;
};

// entryStatusText says what is true of the bytes, not of the request. "Staged"
// is the one that carries the most weight: the file is on the server and is not
// in the vault, and a person who reads "uploaded" there and closes the tab has
// lost it.
const entryStatusText = (entry: AttachmentEntry): string => {
  switch (entry.state) {
    case "uploading":
      return `Uploading… ${percent(entry)}%`;
    case "staged":
      return "Staged — saved with the note";
    case "canceled":
      return entry.message || "Upload stopped.";
    default:
      return entry.message || "The upload failed.";
  }
};

const percent = (entry: AttachmentEntry): number =>
  entry.size > 0 ? Math.min(100, Math.floor((entry.loaded / entry.size) * 100)) : 0;

const bindChooser = (elements: AttachmentElements, tracker: AttachmentTracker): (() => void) => {
  const { input, choose } = elements;
  const onChoose = (): void => input?.click();
  const onChange = (): void => {
    tracker.add(filesOf(input?.files));
    // Cleared so that choosing the same file twice in a row still fires a
    // change event; without it the second attempt looks like nothing happened.
    if (input) {
      input.value = "";
    }
  };
  choose?.addEventListener("click", onChoose);
  input?.addEventListener("change", onChange);
  return (): void => {
    choose?.removeEventListener("click", onChoose);
    input?.removeEventListener("change", onChange);
  };
};

const bindClipboard = (zone: HTMLElement | null, tracker: AttachmentTracker): (() => void) => {
  const onPaste = (event: ClipboardEvent): void => {
    const files = filesOf(event.clipboardData?.files);
    if (files.length === 0) {
      // Text, and the editor's own paste. Untouched deliberately: intercepting
      // a paste that carries no file would put this module between a person and
      // every ordinary copy of a paragraph.
      return;
    }
    event.preventDefault();
    tracker.add(files);
  };
  zone?.addEventListener("paste", onPaste);
  return (): void => zone?.removeEventListener("paste", onPaste);
};

const bindDropZone = (zone: HTMLElement | null, tracker: AttachmentTracker): (() => void) => {
  const onDragOver = (event: DragEvent): void => {
    if (!carriesFiles(event)) {
      return;
    }
    // Both halves are required. Without preventDefault on dragover the drop
    // event never fires at all, and without it on drop the browser navigates
    // away from the editor to display the file — taking every unsaved word with
    // it, which is the exact loss this session exists to prevent.
    event.preventDefault();
    zone?.classList.add("is-drop-target");
  };
  const onDragLeave = (): void => zone?.classList.remove("is-drop-target");
  const onDrop = (event: DragEvent): void => {
    const files = filesOf(event.dataTransfer?.files);
    zone?.classList.remove("is-drop-target");
    if (files.length === 0) {
      return;
    }
    event.preventDefault();
    // Capture phase plus stopPropagation: CodeMirror installs its own drop
    // handler on the same element and would otherwise insert the file's name as
    // text beside the link this puts there.
    event.stopPropagation();
    tracker.add(files);
  };
  zone?.addEventListener("dragover", onDragOver, true);
  zone?.addEventListener("dragleave", onDragLeave, true);
  zone?.addEventListener("drop", onDrop, true);
  return (): void => {
    zone?.removeEventListener("dragover", onDragOver, true);
    zone?.removeEventListener("dragleave", onDragLeave, true);
    zone?.removeEventListener("drop", onDrop, true);
  };
};

const carriesFiles = (event: DragEvent): boolean =>
  Array.from(event.dataTransfer?.types ?? []).includes("Files");

// bindListActions is one delegated listener rather than a listener per button,
// because the list is re-rendered on every progress event and per-button
// handlers would be rebound dozens of times a second on a large upload.
const bindListActions = (list: HTMLElement | null, tracker: AttachmentTracker): (() => void) => {
  const onClick = (event: Event): void => {
    const target = event.target;
    if (!(target instanceof HTMLElement)) {
      return;
    }
    const id = target.getAttribute("data-source-attachment-id");
    if (id === null) {
      return;
    }
    if (target.hasAttribute("data-source-attachment-retry")) {
      tracker.retry(id);
    } else if (target.hasAttribute("data-source-attachment-cancel")) {
      tracker.cancel(id);
    }
  };
  list?.addEventListener("click", onClick);
  return (): void => list?.removeEventListener("click", onClick);
};

const filesOf = (files: FileList | null | undefined): UploadFile[] =>
  files ? Array.from(files) : [];

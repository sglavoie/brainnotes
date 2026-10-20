import {
  propertyPlanIsBlocked,
  propertyViolationDelta,
  readReceiptExpiryLabel,
  type PropertyBlocker,
  type PropertyMutationResult,
} from "./api";
import { previewChangeLine } from "./daily-fixes-preview";

export type PreviewDialogOptions = {
  title: string;
  summary: string;
  confirmLabel: string;
  result: PropertyMutationResult;
  onConfirm: () => void;
  onCancel?: () => void;
  // Where focus returns on close; defaults to the element focused at open.
  returnFocus?: Element | null;
};

export type PreviewDialog = {
  element: HTMLElement;
  open: (options: PreviewDialogOptions) => void;
  close: () => void;
  isOpen: () => boolean;
};

export type PreviewDialogDeps = {
  now?: () => Date;
};

export const blockerLabel = (blocker: PropertyBlocker): string => {
  const where = blocker.rel_path || blocker.property;
  const detail = blocker.detail || blocker.kind;
  return where === "" ? detail : `${where}: ${detail}`;
};

export const previewSummaryLabel = (result: PropertyMutationResult): string => {
  const files = result.previews.length;
  const noteScope =
    result.selected === 0
      ? ""
      : `${result.selected} ${result.selected === 1 ? "note" : "notes"} selected, `;
  const changeScope = `${files} ${files === 1 ? "file" : "files"} would change`;
  const configScope = result.config_diff.length > 0 ? ", plus brainnotes.toml" : "";
  return `${noteScope}${changeScope}${configScope}.`;
};

const appendHeading = (doc: Document, parent: HTMLElement, text: string): void => {
  const heading = doc.createElement("h3");
  heading.className = "properties-dialog-subheading";
  heading.textContent = text;
  parent.appendChild(heading);
};

const renderBlockers = (
  doc: Document,
  parent: HTMLElement,
  blockers: PropertyBlocker[],
  fatal: boolean
): void => {
  const shown = blockers.filter((blocker) => blocker.fatal === fatal);
  if (shown.length === 0) {
    return;
  }
  appendHeading(doc, parent, fatal ? "Blocked" : "Warnings");
  const list = doc.createElement("ul");
  list.className = fatal
    ? "properties-dialog-blockers"
    : "properties-dialog-warnings";
  list.setAttribute(fatal ? "data-property-preview-blockers" : "data-property-preview-warnings", "");
  for (const blocker of shown) {
    const item = doc.createElement("li");
    item.textContent = blockerLabel(blocker);
    list.appendChild(item);
  }
  parent.appendChild(list);
};

const renderConfigDiff = (
  doc: Document,
  parent: HTMLElement,
  result: PropertyMutationResult
): void => {
  if (result.config_diff.length === 0) {
    return;
  }
  appendHeading(doc, parent, "brainnotes.toml");
  const block = doc.createElement("pre");
  block.className = "properties-dialog-config-diff";
  block.setAttribute("data-property-preview-config", "");
  block.textContent = result.config_diff
    .map((line) => {
      const marker = line.kind === "add" ? "+" : line.kind === "remove" ? "-" : " ";
      return `${marker} ${line.text}`;
    })
    .join("\n");
  parent.appendChild(block);
};

const renderFiles = (
  doc: Document,
  parent: HTMLElement,
  result: PropertyMutationResult
): void => {
  if (result.previews.length === 0) {
    return;
  }
  appendHeading(doc, parent, "Affected notes");
  const list = doc.createElement("ul");
  list.className = "properties-dialog-files";
  list.setAttribute("data-property-preview-files", "");
  for (const preview of result.previews) {
    const item = doc.createElement("li");
    item.className = "properties-dialog-file";

    const path = doc.createElement("p");
    path.className = "properties-dialog-file-path";
    path.textContent = preview.rel_path;
    item.appendChild(path);

    const changes = doc.createElement("ul");
    changes.className = "properties-dialog-changes";
    for (const change of preview.changes) {
      const line = doc.createElement("li");
      line.textContent = previewChangeLine(change);
      changes.appendChild(line);
    }
    item.appendChild(changes);
    list.appendChild(item);
  }
  parent.appendChild(list);
};

const isOutside = (dialog: HTMLElement, event: MouseEvent): boolean => {
  const box = dialog.getBoundingClientRect();
  return event.clientX < box.left || event.clientX > box.right ||
    event.clientY < box.top || event.clientY > box.bottom;
};

type PreviewElements = {
  dialog: HTMLDialogElement;
  title: HTMLElement;
  summary: HTMLElement;
  body: HTMLElement;
  expiry: HTMLElement;
  confirm: HTMLButtonElement;
  cancel: HTMLButtonElement;
};

const appendElement = (
  doc: Document,
  parent: HTMLElement,
  tag: string,
  className: string,
  attributes: Record<string, string> = {}
): HTMLElement => {
  const element = doc.createElement(tag);
  element.className = className;
  for (const [name, value] of Object.entries(attributes)) {
    element.setAttribute(name, value);
  }
  parent.appendChild(element);
  return element;
};

const actionButton = (doc: Document, className: string, attribute: string): HTMLButtonElement => {
  const button = doc.createElement("button");
  button.type = "button";
  button.className = className;
  button.setAttribute(attribute, "");
  return button;
};

const buildPreviewElements = (doc: Document): PreviewElements => {
  const dialog = doc.createElement("dialog");
  dialog.className = "properties-dialog properties-preview-dialog";
  dialog.setAttribute("data-property-dialog", "");
  dialog.setAttribute("aria-labelledby", "property-dialog-title");
  dialog.setAttribute("aria-describedby", "property-dialog-summary");
  const title = appendElement(doc, dialog, "h2", "properties-dialog-title", { id: "property-dialog-title" });
  const summary = appendElement(doc, dialog, "p", "properties-dialog-summary", {
    id: "property-dialog-summary",
    "data-property-preview-summary": "",
  });
  const body = appendElement(doc, dialog, "div", "properties-dialog-body");
  const expiry = appendElement(doc, dialog, "p", "properties-dialog-expiry", {
    "data-property-preview-expiry": "",
  });
  const actions = appendElement(doc, dialog, "div", "properties-dialog-actions");
  const confirm = actionButton(doc, "properties-button properties-button-primary", "data-property-preview-confirm");
  const cancel = actionButton(doc, "properties-button", "data-property-preview-cancel");
  cancel.textContent = "Cancel";
  actions.append(confirm, cancel);
  return { dialog, title, summary, body, expiry, confirm, cancel };
};

const renderPreview = (
  doc: Document,
  els: PreviewElements,
  options: PreviewDialogOptions,
  now: Date
): void => {
  const { result } = options;
  const blocked = propertyPlanIsBlocked(result.blockers);
  els.title.textContent = options.title;
  els.summary.textContent = options.summary;
  els.body.textContent = "";
  renderBlockers(doc, els.body, result.blockers, true);
  renderBlockers(doc, els.body, result.blockers, false);
  renderConfigDiff(doc, els.body, result);
  renderFiles(doc, els.body, result);
  appendElement(doc, els.body, "p", "properties-dialog-projection", {
    "data-property-preview-projection": "",
  }).textContent = propertyViolationDelta(result.projected_violations);
  const expiryLabel = blocked
    ? "This plan cannot be applied until every blocker is resolved."
    : readReceiptExpiryLabel(result.expires_at, now);
  els.expiry.textContent = expiryLabel;
  els.expiry.hidden = expiryLabel === "";
  els.confirm.textContent = options.confirmLabel;
  els.confirm.disabled = blocked || result.preview_id === "";
};

// Escape, the browser's own close request, a press on the backdrop and Cancel
// all cancel; only Confirm confirms.
const bindDismissal = (dialog: HTMLDialogElement, cancel: HTMLButtonElement, dismiss: () => void, isOpen: () => boolean): void => {
  dialog.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && !event.isComposing) {
      event.preventDefault();
      event.stopPropagation();
      dismiss();
    }
  });
  dialog.addEventListener("cancel", (event) => {
    event.preventDefault();
    dismiss();
  });
  dialog.addEventListener("close", () => {
    if (isOpen() && !dialog.open) dismiss();
  });
  dialog.addEventListener("mousedown", (event) => {
    if (event.target === dialog && isOutside(dialog, event)) dismiss();
  });
  cancel.addEventListener("click", dismiss);
};

// createPreviewDialog builds one native modal <dialog> that every previewed
// action reuses. showModal() makes the rest of the page inert and keeps focus
// inside, as confirmAction does; this module restores focus to the opener.
export const createPreviewDialog = (
  doc: Document,
  host: HTMLElement,
  deps: PreviewDialogDeps = {}
): PreviewDialog => {
  const now = deps.now || (() => new Date());
  const els = buildPreviewElements(doc);
  const { dialog } = els;
  host.appendChild(dialog);

  let opened = false;
  let restoreFocus: HTMLElement | null = null;
  let handlers: PreviewDialogOptions | null = null;

  const close = (): void => {
    if (!opened) {
      return;
    }
    opened = false;
    handlers = null;
    if (dialog.open) {
      dialog.close();
    }
    if (restoreFocus?.isConnected) {
      restoreFocus.focus();
    }
    restoreFocus = null;
  };

  const cancelDialog = (): void => {
    const onCancel = handlers?.onCancel;
    close();
    onCancel?.();
  };

  bindDismissal(dialog, els.cancel, cancelDialog, () => opened);
  els.confirm.addEventListener("click", () => {
    const onConfirm = handlers?.onConfirm;
    close();
    onConfirm?.();
  });

  const open = (options: PreviewDialogOptions): void => {
    handlers = options;
    renderPreview(doc, els, options, now());
    // A caller that disabled its controls while previewing names the opener.
    const active = options.returnFocus ?? doc.activeElement;
    restoreFocus = active instanceof HTMLElement && active !== doc.body ? active : null;
    opened = true;
    if (!dialog.open) {
      dialog.showModal();
    }
    // Cancel first, like confirmAction: Enter alone never applies a change.
    els.cancel.focus();
  };

  return {
    element: dialog,
    open,
    close,
    isOpen: () => opened,
  };
};

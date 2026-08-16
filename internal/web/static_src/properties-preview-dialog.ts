import {
  propertyPlanIsBlocked,
  propertyViolationDelta,
  readReceiptExpiryLabel,
  type PropertyBlocker,
  type PropertyMutationResult,
} from "./api";
import { previewChangeLine } from "./daily-fixes-preview";
import { queryAllElements } from "./dom";

const FOCUSABLE_SELECTOR = [
  "a[href]",
  "button:not([disabled])",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  "[tabindex]:not([tabindex='-1'])",
].join(",");

export type PreviewDialogOptions = {
  title: string;
  summary: string;
  confirmLabel: string;
  result: PropertyMutationResult;
  onConfirm: () => void;
  onCancel?: () => void;
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

// createPreviewDialog builds one modal that every previewed action reuses. It
// owns its own focus trap and restores focus to whatever opened it, so a
// keyboard user is never stranded behind the dialog.
export const createPreviewDialog = (
  doc: Document,
  host: HTMLElement,
  deps: PreviewDialogDeps = {}
): PreviewDialog => {
  const now = deps.now || (() => new Date());

  const backdrop = doc.createElement("div");
  backdrop.className = "properties-dialog-backdrop";
  backdrop.setAttribute("data-property-dialog", "");
  backdrop.hidden = true;

  const dialog = doc.createElement("div");
  dialog.className = "properties-dialog";
  dialog.setAttribute("role", "dialog");
  dialog.setAttribute("aria-modal", "true");
  dialog.setAttribute("aria-labelledby", "property-dialog-title");
  dialog.setAttribute("aria-describedby", "property-dialog-summary");
  backdrop.appendChild(dialog);

  const title = doc.createElement("h2");
  title.className = "properties-dialog-title";
  title.id = "property-dialog-title";
  dialog.appendChild(title);

  const summary = doc.createElement("p");
  summary.className = "properties-dialog-summary";
  summary.id = "property-dialog-summary";
  summary.setAttribute("data-property-preview-summary", "");
  dialog.appendChild(summary);

  const body = doc.createElement("div");
  body.className = "properties-dialog-body";
  dialog.appendChild(body);

  const expiry = doc.createElement("p");
  expiry.className = "properties-dialog-expiry";
  expiry.setAttribute("data-property-preview-expiry", "");
  dialog.appendChild(expiry);

  const actions = doc.createElement("div");
  actions.className = "properties-dialog-actions";
  const confirm = doc.createElement("button");
  confirm.type = "button";
  confirm.className = "properties-button properties-button-primary";
  confirm.setAttribute("data-property-preview-confirm", "");
  const cancel = doc.createElement("button");
  cancel.type = "button";
  cancel.className = "properties-button";
  cancel.setAttribute("data-property-preview-cancel", "");
  cancel.textContent = "Cancel";
  actions.append(confirm, cancel);
  dialog.appendChild(actions);

  host.appendChild(backdrop);

  let opened = false;
  let restoreFocus: HTMLElement | null = null;
  let handlers: PreviewDialogOptions | null = null;

  const close = (): void => {
    if (!opened) {
      return;
    }
    opened = false;
    handlers = null;
    backdrop.hidden = true;
    restoreFocus?.focus();
    restoreFocus = null;
  };

  const cancelDialog = (): void => {
    const onCancel = handlers?.onCancel;
    close();
    onCancel?.();
  };

  const trapFocus = (event: KeyboardEvent): void => {
    const focusable = queryAllElements(dialog, FOCUSABLE_SELECTOR, HTMLElement).filter(
      (element) => !element.hasAttribute("hidden")
    );
    if (focusable.length === 0) {
      return;
    }
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    const active = doc.activeElement;
    if (event.shiftKey && (active === first || !dialog.contains(active))) {
      event.preventDefault();
      last?.focus();
      return;
    }
    if (!event.shiftKey && active === last) {
      event.preventDefault();
      first?.focus();
    }
  };

  dialog.addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      cancelDialog();
      return;
    }
    if (event.key === "Tab") {
      trapFocus(event);
    }
  });
  backdrop.addEventListener("mousedown", (event) => {
    if (event.target === backdrop) {
      cancelDialog();
    }
  });
  cancel.addEventListener("click", () => {
    cancelDialog();
  });
  confirm.addEventListener("click", () => {
    const onConfirm = handlers?.onConfirm;
    close();
    onConfirm?.();
  });

  const open = (options: PreviewDialogOptions): void => {
    handlers = options;
    const { result } = options;
    const blocked = propertyPlanIsBlocked(result.blockers);

    title.textContent = options.title;
    summary.textContent = options.summary;
    body.textContent = "";

    renderBlockers(doc, body, result.blockers, true);
    renderBlockers(doc, body, result.blockers, false);
    renderConfigDiff(doc, body, result);
    renderFiles(doc, body, result);

    const projection = doc.createElement("p");
    projection.className = "properties-dialog-projection";
    projection.setAttribute("data-property-preview-projection", "");
    projection.textContent = propertyViolationDelta(result.projected_violations);
    body.appendChild(projection);

    const expiryLabel = blocked
      ? "This plan cannot be applied until every blocker is resolved."
      : readReceiptExpiryLabel(result.expires_at, now());
    expiry.textContent = expiryLabel;
    expiry.hidden = expiryLabel === "";

    confirm.textContent = options.confirmLabel;
    confirm.disabled = blocked || result.preview_id === "";

    const active = doc.activeElement;
    restoreFocus = active instanceof HTMLElement ? active : null;
    backdrop.hidden = false;
    opened = true;
    if (confirm.disabled) {
      cancel.focus();
    } else {
      confirm.focus();
    }
  };

  return {
    element: backdrop,
    open,
    close,
    isOpen: () => opened,
  };
};

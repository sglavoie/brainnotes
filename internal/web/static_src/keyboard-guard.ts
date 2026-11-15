const editableSelector = 'input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="textbox"]';
const ownedSelector = '#graph-canvas, #graph-node-list, [data-source-editor]';
export const dialogSelector = 'dialog[open], [role="dialog"]:not([hidden]), [role="alertdialog"]:not([hidden])';

export const isEditableTarget = (target: EventTarget | null): boolean =>
  target instanceof Element && target.closest(editableSelector) !== null;

export const acceptsShortcutKey = (event: KeyboardEvent): boolean =>
  !event.ctrlKey && !event.metaKey && !event.altKey &&
  (!event.shiftKey || event.key === "?") &&
  !event.isComposing && !event.repeat && !event.defaultPrevented;

export const allowsSingleKey = (event: KeyboardEvent, doc: Document = document): boolean =>
  doc.documentElement.dataset.shortcuts !== "off" && acceptsShortcutKey(event) &&
  !isEditableTarget(event.target) && !doc.querySelector(dialogSelector) &&
  !(event.target instanceof Element && event.target.closest(ownedSelector));

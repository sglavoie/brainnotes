const editableSelector = 'input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="textbox"]';
const dialogSelector = 'dialog[open], [role="dialog"]:not([hidden]), [role="alertdialog"]:not([hidden])';

const isSearchShortcut = (event: KeyboardEvent): boolean =>
  event.key === "/" && !event.ctrlKey && !event.metaKey && !event.altKey &&
  !event.isComposing && !event.defaultPrevented && !event.repeat;

export const bindSearchShortcut = (input: HTMLInputElement): (() => void) => {
  const doc = input.ownerDocument;
  const onKeyDown = (event: KeyboardEvent): void => {
    if (!isSearchShortcut(event) || !input.isConnected || input.disabled || doc.querySelector(dialogSelector)) {
      return;
    }
    const target = event.target;
    if (target instanceof Element && target.closest(editableSelector)) {
      return;
    }
    event.preventDefault();
    input.focus();
  };
  doc.addEventListener("keydown", onKeyDown);
  return () => doc.removeEventListener("keydown", onKeyDown);
};

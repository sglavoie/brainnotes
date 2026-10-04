const editableSelector = 'input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="textbox"]';
const dialogSelector = 'dialog[open], [role="dialog"]:not([hidden]), [role="alertdialog"]:not([hidden])';

const isSearchShortcut = (event: KeyboardEvent): boolean =>
  event.key === "/" && !event.ctrlKey && !event.metaKey && !event.altKey &&
  !event.isComposing && !event.defaultPrevented && !event.repeat;

export const bindSearchShortcut = (input: HTMLInputElement): (() => void) => {
  const doc = input.ownerDocument;
  const scope = input.closest<HTMLElement>("[data-search-box]") || input;
  let returnTarget: HTMLElement | null = null;
  const returnFocus = (): void => {
    const target = returnTarget;
    returnTarget = null;
    if (target?.isConnected && !target.closest('[hidden], [inert], :disabled')) {
      target.focus({ preventScroll: true });
    }
  };
  const onFocusOut = (event: FocusEvent): void => {
    if (!(event.relatedTarget instanceof Node) || !scope.contains(event.relatedTarget)) {
      returnTarget = null;
    }
  };
  const onKeyDown = (event: KeyboardEvent): void => {
    // Search's own handler dismisses first, invalidating any pending response.
    if (isSearchEscape(event, scope)) {
      returnFocus();
      return;
    }
    if (!isSearchShortcut(event) || !input.isConnected || input.disabled || doc.querySelector(dialogSelector)) {
      return;
    }
    const target = event.target;
    if (target instanceof Element && target.closest(editableSelector)) {
      return;
    }
    event.preventDefault();
    returnTarget = doc.activeElement instanceof HTMLElement && doc.activeElement !== doc.body
      ? doc.activeElement : null;
    input.focus();
  };
  doc.addEventListener("keydown", onKeyDown);
  scope.addEventListener("focusout", onFocusOut);
  return () => {
    doc.removeEventListener("keydown", onKeyDown);
    scope.removeEventListener("focusout", onFocusOut);
    returnTarget = null;
  };
};

const isSearchEscape = (event: KeyboardEvent, scope: HTMLElement): boolean =>
  event.key === "Escape" && !event.ctrlKey && !event.metaKey && !event.altKey &&
  !event.shiftKey && !event.isComposing && event.target instanceof Node && scope.contains(event.target);

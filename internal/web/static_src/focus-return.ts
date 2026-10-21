// captureFocusReturn keeps focus where a person left it across an async
// operation that disables the focused control. Disabling a control can blur it
// onto <body>; the returned function puts focus back on `target` once the
// operation settles. Only interaction outside `target` and `owned` in the
// meantime — a focus move or a pointer press, including a click on plain text —
// cancels the return, so the page never pulls focus back from somewhere a
// person chose. Call the returned function after re-enabling the controls.
export const captureFocusReturn = (
  target: HTMLElement | null,
  owned: readonly Element[] = []
): (() => void) => {
  if (!target) {
    return () => {};
  }
  const doc = target.ownerDocument;
  const scopes = [target, ...owned];
  let movedAway = false;
  const observe = (event: Event): void => {
    const node = event.target;
    if (!(node instanceof Node) || !scopes.some((scope) => scope.contains(node))) movedAway = true;
  };
  doc.addEventListener("focusin", observe, true);
  doc.addEventListener("pointerdown", observe, true);
  return () => {
    doc.removeEventListener("focusin", observe, true);
    doc.removeEventListener("pointerdown", observe, true);
    if (!movedAway && target.isConnected) target.focus();
  };
};

// focusedWithin is the element holding focus when it sits inside `scope`, the
// usual `target` for captureFocusReturn.
export const focusedWithin = (scope: Element | null | undefined): HTMLElement | null => {
  const active = scope?.ownerDocument.activeElement;
  return scope && active instanceof HTMLElement && scope.contains(active) ? active : null;
};

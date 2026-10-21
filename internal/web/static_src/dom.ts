type ElementConstructor<T extends Element> = {
  new (): T;
};

export const narrowElement = <T extends Element>(
  element: Element | null,
  constructor: ElementConstructor<T>
): T | null => (element instanceof constructor ? element : null);

export const queryElement = <T extends Element>(
  root: ParentNode,
  selector: string,
  constructor: ElementConstructor<T>
): T | null => narrowElement(root.querySelector(selector), constructor);

export const queryAllElements = <T extends Element>(
  root: ParentNode,
  selector: string,
  constructor: ElementConstructor<T>
): T[] =>
  Array.from(root.querySelectorAll(selector)).flatMap((element) => {
    const narrowed = narrowElement(element, constructor);
    return narrowed ? [narrowed] : [];
  });

// focusIsLost reports whether focus has fallen to <body>, which happens when a
// rerender or a hide removes the control that had it.
export const focusIsLost = (doc: Document): boolean =>
  !doc.activeElement || doc.activeElement === doc.body;

// focusFallback gives focus to a non-interactive landmark, such as the status
// line that reports what just happened, making it focusable from script only.
export const focusFallback = (target: HTMLElement | null): void => {
  if (!target) return;
  if (target.tabIndex < 0 && !target.hasAttribute("tabindex")) target.tabIndex = -1;
  target.focus();
};

export const getElementByID = <T extends Element>(
  id: string,
  constructor: ElementConstructor<T>
): T | null => narrowElement(document.getElementById(id), constructor);

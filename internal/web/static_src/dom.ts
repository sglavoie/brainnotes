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

export const getElementByID = <T extends Element>(
  id: string,
  constructor: ElementConstructor<T>
): T | null => narrowElement(document.getElementById(id), constructor);

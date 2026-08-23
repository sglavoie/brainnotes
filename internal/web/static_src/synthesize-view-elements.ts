
import type { RelatedMode } from "./related-types";

export const suggestionID = (kind: "tab" | "panel", mode: RelatedMode): string =>
  `synthesize-${kind}-${mode}`;

export const element = <K extends keyof HTMLElementTagNameMap>(
  doc: Document,
  tag: K,
  className: string,
  attributes: Record<string, string> = {}
): HTMLElementTagNameMap[K] => {
  const node = doc.createElement(tag);
  if (className !== "") {
    node.className = className;
  }
  for (const [name, value] of Object.entries(attributes)) {
    node.setAttribute(name, value);
  }
  return node;
};

export const labelled = (
  doc: Document,
  id: string,
  text: string,
  control: HTMLElement
): HTMLElement => {
  const wrapper = element(doc, "div", "synthesize-field");
  const label = element(doc, "label", "synthesize-field-label");
  label.setAttribute("for", id);
  label.textContent = text;
  control.id = id;
  wrapper.append(label, control);
  return wrapper;
};

// A field error is announced by the control that owns it rather than by a live
// region, so a validation message reaches a screen reader when focus lands on
// the offending field instead of interrupting whatever it was reading.
export const labelledWithError = (
  doc: Document,
  id: string,
  text: string,
  control: HTMLElement,
  errorAttribute: string
): { wrapper: HTMLElement; error: HTMLElement } => {
  const wrapper = labelled(doc, id, text, control);
  const error = element(doc, "p", "synthesize-field-error", { [errorAttribute]: "" });
  error.id = `${id}-error`;
  error.hidden = true;
  control.setAttribute("aria-describedby", error.id);
  wrapper.appendChild(error);
  return { wrapper, error };
};

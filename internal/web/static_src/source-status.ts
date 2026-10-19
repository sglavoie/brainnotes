import { setStatusText, type StatusKind } from "./status";

export const setSourceStatus = (element: HTMLElement | null, message: string, kind: StatusKind = ""): void => {
  setStatusText(element, message, kind);
  // Detailed outcomes get the toolbar's full width; short save states stay
  // beside Save. This changes layout only, never the message or its meaning.
  if (element) {
    element.dataset.expanded = String(message.length > 25);
    element.dataset.idle = String(message === "Ready to edit.");
  }
};

import { setStatusText, type StatusKind } from "./status";

// idle marks a line that says nothing worth room on a narrow toolbar ("Ready to
// edit.", "No unsaved changes."). It is passed by the caller rather than read
// from the words, so rewording a message cannot change what is shown.
export const setSourceStatus = (
  element: HTMLElement | null,
  message: string,
  kind: StatusKind = "",
  idle = false
): void => {
  setStatusText(element, message, kind);
  // Detailed outcomes get the toolbar's full width; short save states stay
  // beside Save. This changes layout only, never the message or its meaning.
  if (element) {
    element.dataset.expanded = String(message.length > 25);
    element.dataset.idle = String(idle);
  }
};

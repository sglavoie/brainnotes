// Ctrl/⌘ + Enter submits a multi-line field. An input method uses Enter to
// commit the text it is composing, so that Enter never submits.
export const isSubmitChord = (event: KeyboardEvent): boolean =>
  event.key === "Enter" && (event.ctrlKey || event.metaKey) && !event.isComposing;

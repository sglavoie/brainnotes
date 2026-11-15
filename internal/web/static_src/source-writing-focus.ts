// Escape restores the ordinary controls without changing the draft or selection.
export const bindFocusExit = (panel: HTMLElement): (() => void) => {
  const doc = panel.ownerDocument;
  const onKey = (event: KeyboardEvent): void => {
    const layout = panel.querySelector<HTMLSelectElement>("[data-writing-layout]");
    if (event.key !== "Escape" || event.defaultPrevented || event.isComposing ||
        modified(event) ||
        layout?.value !== "focus" || doc.querySelector("dialog[open]")) return;
    layout.value = "write";
    layout.dispatchEvent(new Event("change"));
    event.preventDefault();
  };
  const exit = panel.querySelector<HTMLButtonElement>("[data-writing-exit]");
  const leave = (): void => {
    const layout = panel.querySelector<HTMLSelectElement>("[data-writing-layout]");
    if (!layout) return;
    layout.value = "write";
    layout.dispatchEvent(new Event("change"));
    panel.querySelector<HTMLElement>(".cm-content")?.focus({ preventScroll: true });
  };
  exit?.addEventListener("click", leave);
  doc.addEventListener("keydown", onKey);
  return () => { doc.removeEventListener("keydown", onKey); exit?.removeEventListener("click", leave); };
};

const modified = (event: KeyboardEvent): boolean => event.ctrlKey || event.metaKey || event.altKey || event.shiftKey;

export const applyFocusView = (panel: HTMLElement, focus: boolean): void => {
  panel.closest<HTMLElement>(".source-editor-main")?.classList.toggle("is-writing-focus", focus);
  const exit = panel.querySelector<HTMLButtonElement>("[data-writing-exit]");
  if (exit) exit.hidden = !focus;
  if (focus) {
    panel.querySelector<HTMLDetailsElement>(".source-editor-format")?.removeAttribute("open");
    panel.querySelector<HTMLElement>(".cm-content")?.focus({ preventScroll: true });
  }
};

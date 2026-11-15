import type { EditorState } from "./source-editor-controller";

// Elapsed time sits outside the live region to avoid repeated announcements. The state
// itself is announced once per transition and also describes the Save button.
export const createSaveState = (panel: HTMLElement, state: EditorState): { refresh(): void; dispose(): void } => {
  const label = panel.querySelector<HTMLElement>("[data-save-label]");
  const age = panel.querySelector<HTMLElement>("[data-save-age]");
  const save = panel.querySelector<HTMLButtonElement>("[data-source-save]");
  const refresh = (): void => {
    const dirty = state.surface !== null && state.surface.getValue() !== state.savedContent;
    const text = saveLabel(state, dirty);
    if (label && label.textContent !== text) label.textContent = text;
    if (save) save.setAttribute("aria-busy", String(state.saving));
    if (!age) return;
    const elapsed = text === "Saved" && state.savedAt !== undefined ? savedAge(state.savedAt) : "";
    if (age.textContent !== elapsed) age.textContent = elapsed;
    age.title = elapsed ? new Date(state.savedAt!).toLocaleString() : "";
  };
  const timer = window.setInterval(refresh, 15000);
  refresh();
  return { refresh, dispose: () => window.clearInterval(timer) };
};

const saveLabel = (state: EditorState, dirty: boolean): string => {
  if (state.saving) return "Saving…";
  if (state.pendingSave) return "Save unconfirmed";
  if (!state.surface) return "Not loaded";
  return dirty ? "Unsaved changes" : "Saved";
};

const savedAge = (at: number): string => {
  const minutes = Math.max(0, Math.floor((Date.now() - at) / 60000));
  if (minutes === 0) return " just now";
  if (minutes < 60) return ` ${minutes}m ago`;
  if (minutes < 1440) return ` ${Math.floor(minutes / 60)}h ago`;
  return ` ${Math.floor(minutes / 1440)}d ago`;
};

export const updateSaveButton = (save: HTMLButtonElement | null, disabled: boolean): void => {
  if (save && save.disabled !== disabled) save.disabled = disabled;
};

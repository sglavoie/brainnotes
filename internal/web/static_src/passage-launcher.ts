import { loadThoughtStyles } from "./thought-styles";
import { loadWorkspaceScript } from "./workspace-script";

// Ordinary reading pays only for this listener. Selection loads the composer.
export const initPassageThought = (body: HTMLElement): void => {
  if (!body.dataset.noteKey || document.documentElement.dataset.readOnly === "true") return;
  let loading = false;
  const load = (): void => {
    const selection = getSelection();
    if (loading || !selection || selection.isCollapsed || !body.contains(selection.anchorNode) || !body.contains(selection.focusNode)) return;
    loading = true;
    void loadThoughtStyles().then(() => loadWorkspaceScript("passage-thought"))
      .then(() => document.removeEventListener("selectionchange", load))
      .catch(() => { loading = false; });
  };
  document.addEventListener("selectionchange", load);
};

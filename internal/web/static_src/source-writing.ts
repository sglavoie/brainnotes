import type { SourcePanes } from "./source-editor-panes";

// Desktop presentation is independent of the narrow-screen Edit/Preview tabs.
export const initWritingPreferences = (panel: HTMLElement, panes: SourcePanes): void => {
  const container = panel.querySelector<HTMLElement>("[data-source-panes]");
  const layout = panel.querySelector<HTMLSelectElement>("[data-writing-layout]");
  const font = panel.querySelector<HTMLSelectElement>("[data-writing-font]");
  if (!container || !layout || !font) return;
  const preferences = layout.closest<HTMLElement>(".writing-preferences")!;
  const marker = document.createComment("writing preferences");
  preferences.before(marker);
  const format = panel.querySelector(".source-editor-commands");
  const desktop = window.matchMedia("(min-width: 961px)");
  const restore = (key: string, allowed: string[], fallback: string): string => {
    try { const value = localStorage.getItem(key); return value && allowed.includes(value) ? value : fallback; }
    catch { return fallback; }
  };
  layout.value = restore("bn.writing-layout", ["write", "split", "preview"], "write");
  font.value = restore("bn.writing-font", ["prose", "mono"], "prose");
  const arrange = (): void => {
    if (desktop.matches) marker.after(preferences);
    else format?.append(preferences);
  };
  const apply = (): void => {
    container.dataset.writingLayout = layout.value;
    panel.dataset.writingFont = font.value;
    if (desktop.matches) panes.select(layout.value === "preview" ? "preview" : "edit");
  };
  for (const control of [layout, font]) control.addEventListener("change", () => {
    apply();
    try { localStorage.setItem("bn.writing-layout", layout.value); localStorage.setItem("bn.writing-font", font.value); }
    catch { /* Preferences still apply to this editor. */ }
  });
  // Find and formatting must always reveal the editable surface.
  panes.onSelect((pane) => {
    if (desktop.matches && pane === "edit" && layout.value === "preview") {
      layout.value = "write"; container.dataset.writingLayout = "write";
    }
  });
  desktop.addEventListener("change", () => { arrange(); if (desktop.matches) apply(); else panes.select("edit"); });
  arrange();
  apply();
};

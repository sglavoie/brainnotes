import { rememberCurrentNote } from "./switcher-data";
import { openKeyboardTools } from "./keyboard-tools-loader";

export const initCommandSwitcher = (): void => {
  rememberCurrentNote();
  for (const hint of document.querySelectorAll("[data-switcher-key]")) {
    hint.textContent = /Mac|iPhone|iPad/.test(navigator.platform) ? "⌘K" : "Ctrl K";
    hint.closest("button")?.setAttribute("aria-label", `Search ${hint.textContent} — notes and actions`);
  }
  for (const trigger of document.querySelectorAll("[data-switcher-launch]")) trigger.addEventListener("click", () => void openKeyboardTools("switcher"));
  document.addEventListener("keydown", (event) => {
    if (event.key.toLowerCase() !== "k" || !(event.metaKey || event.ctrlKey) || event.altKey || event.shiftKey || event.isComposing || event.repeat) return;
    event.preventDefault();
    event.stopPropagation();
    void openKeyboardTools("switcher");
  }, { capture: true });
};

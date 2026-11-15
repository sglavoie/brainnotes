import { loadWorkspaceScript } from "./workspace-script";

export type KeyboardToolRequest = { action: "sheet" | "switcher"; origin: HTMLElement | null };
let pending = false;
let notice: HTMLElement | undefined;

const showFailure = (origin: HTMLElement | null): void => {
  notice = document.createElement("p");
  notice.setAttribute("role", "status");
  notice.textContent = "Keyboard tools unavailable. Use the same shortcut or button to retry.";
  (origin?.closest("header") || document.querySelector("main") || document.body).append(notice);
};

export const openKeyboardTools = async (action: KeyboardToolRequest["action"]): Promise<void> => {
  // Nonmodal previews own single keys, but must not block explicit tool requests.
  if (pending || document.querySelector("dialog[open]")) return;
  const origin = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  pending = true;
  notice?.remove();
  try {
    await loadWorkspaceScript("keyboard-tools");
    // A different dialog or a move into another field while loading owns focus.
    if (document.querySelector("dialog[open]") || document.activeElement !== origin) return;
    document.dispatchEvent(new CustomEvent<KeyboardToolRequest>("bn:keyboard-tools", { detail: { action, origin } }));
  } catch {
    showFailure(origin);
  } finally { pending = false; }
};

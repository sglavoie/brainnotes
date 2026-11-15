import { openKeyboardTools } from "./keyboard-tools-loader";
import { allowsSingleKey } from "./keyboard-guard";
import { initShortcutPreference } from "./keyboard-preference";

const destinations: Record<string, string> = { b: "/", l: "/library", t: "/threads", p: "/pinned", g: "/graph" };
const listSelector = ".overview-result-link, .pinned-note-title, [data-thread-link]";
export type ShortcutMatch = { action: string; prefix: number | null };

// Time and eligibility are inputs: no timer, DOM or parallel selection state.
export const matchShortcut = (key: string, now: number, prefix: number | null, allowed: boolean): ShortcutMatch => {
  if (!allowed) return { action: "", prefix: null };
  if (prefix !== null && now - prefix < 1000 && destinations[key]) {
    return { action: destinations[key], prefix: null };
  }
  if (key === "g") return { action: "", prefix: now };
  return { action: ["j", "k", "Enter", "o", "x", "e", "?"].includes(key) ? key : "", prefix: null };
};

const moveFocus = (links: HTMLElement[], index: number, key: string): void => {
  const start = key === "j" ? 0 : links.length - 1;
  const next = index < 0 ? start : Math.max(0, Math.min(links.length - 1, index + (key === "j" ? 1 : -1)));
  links[next].focus();
};

const toggleSelection = (link: HTMLElement): boolean => {
  const checkbox = link.closest("li")?.querySelector<HTMLInputElement>('input[type="checkbox"]:not(:disabled)');
  if (!checkbox) return false;
  checkbox.click();
  return true;
};

const listAction = (doc: Document, key: string): boolean => {
  const links = Array.from(doc.querySelectorAll<HTMLElement>(listSelector)).filter((link) => link.checkVisibility());
  const index = links.indexOf(doc.activeElement as HTMLElement);
  // Focused native controls keep their activation and typeahead keys.
  if (index < 0 && doc.activeElement?.closest("a, button, summary, input, select, textarea")) return false;
  if (!links.length) return false;
  if (key === "j" || key === "k") {
    moveFocus(links, index, key);
    return true;
  }
  if (index < 0) return false;
  if (key === "x") return toggleSelection(links[index]);
  links[index].click();
  return true;
};

const activate = (doc: Document, action: string, openSheet: () => void): boolean => {
  if (action === "?") { openSheet(); return true; }
  if (["j", "k", "Enter", "o", "x"].includes(action)) return listAction(doc, action);
  const selector = action === "e" ? "[data-source-editor-launch]" : `.desktop-workspace-nav a[href="${action}"]`;
  const control = doc.querySelector<HTMLElement>(selector);
  if (!control) return false;
  control.click();
  return true;
};

export const initKeyboardShortcuts = (doc: Document = document): void => {
  initShortcutPreference(doc);
  const openSheet = (): void => { void openKeyboardTools("sheet"); };
  doc.querySelectorAll("[data-shortcuts-open]").forEach((button) => button.addEventListener("click", openSheet));
  let prefix: number | null = null;
  const reset = (): void => { prefix = null; };
  doc.addEventListener("focusin", reset);
  doc.addEventListener("pointerdown", reset);
  doc.defaultView?.addEventListener("blur", reset);
  doc.addEventListener("keydown", (event) => {
    const match = matchShortcut(event.key, Date.now(), prefix, allowsSingleKey(event, doc));
    prefix = match.prefix;
    if (match.action && activate(doc, match.action, openSheet)) event.preventDefault();
  });
};
